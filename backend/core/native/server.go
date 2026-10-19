package native

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"github.com/miekg/dns"
)

const (
	// maxTCPConns bounds open TCP connections. It is its own limit, apart from
	// maxInFlight, so slow TCP clients cannot take the slots UDP queries need
	// (the dnsproxy backend shares them: open question Q4).
	maxTCPConns = 64

	// maxTCPConnsPerIP keeps one host from holding every one of them.
	maxTCPConnsPerIP = 8

	// tcpIdleTimeout closes a connection that sends nothing after its first
	// query; tcpReadTimeout bounds the wait for the first query and each message.
	tcpIdleTimeout = 10 * time.Second
	tcpReadTimeout = 5 * time.Second

	// maxTCPQueries is the most queries one connection may carry.
	maxTCPQueries = 100

	ioTimeout = 5 * time.Second

	// udpReadSize is the buffer for one incoming UDP message. Queries are small,
	// so it is generous.
	udpReadSize = 4096

	// shutdownTimeout bounds closing the listeners after the run context ends.
	shutdownTimeout = 5 * time.Second
)

// listenAddrs parses every listen entry; plain DNS needs UDP and TCP on each.
func listenAddrs(listen []string) ([]netip.AddrPort, error) {
	if len(listen) == 0 {
		return nil, errors.New("listen: at least one ip:port address is required")
	}
	out := make([]netip.AddrPort, 0, len(listen))
	var errs []error
	for i, entry := range listen {
		ap, err := netip.ParseAddrPort(entry)
		if err != nil {
			errs = append(errs, fmt.Errorf("listen[%d] %q is not ip:port: %w", i, entry, err))
			continue
		}
		out = append(out, ap)
	}
	return out, errors.Join(errs...)
}

// acceptMsg decides which messages reach the handler. Everything that is not a
// response and holds exactly one question is passed on, whatever its opcode: the
// engine answers NOTIMP for a non-QUERY opcode and records it, which the default
// accept function would do silently. A response is ignored, since answering one
// is how reflection loops start. A message with another number of questions or
// with extra records is ignored too: miekg would answer it FORMERR before the
// ACL, the rate limiter and the query log see it (dnsproxy does that; native
// stays silent, and no real client sends such a message).
func acceptMsg(dh dns.Header) dns.MsgAcceptAction {
	const qrBit = 1 << 15
	switch {
	case dh.Bits&qrBit != 0:
		return dns.MsgIgnore
	case dh.Qdcount != 1 || dh.Ancount > 1 || dh.Nscount > 1 || dh.Arcount > 2:
		return dns.MsgIgnore
	}
	return dns.MsgAccept
}

// wsaEMsgSize is the Windows error for a UDP datagram larger than the receive
// buffer (WSAEMSGSIZE). Elsewhere the kernel truncates silently.
const wsaEMsgSize = syscall.Errno(10040)

// readRetryPause keeps the serve loop from spinning on a read that keeps failing.
const readRetryPause = 10 * time.Millisecond

// tolerantReader wraps miekg's reader so that a failed UDP read does not stop
// the UDP server. miekg treats every read error that is not temporary as fatal
// and returns from the serve loop, which would let any host on the network
// switch DNS off with a single packet (on Windows an oversized datagram fails the
// read with WSAEMSGSIZE). Only a closed socket, which is how shutdown ends the
// loop, is left fatal.
type tolerantReader struct{ dns.Reader }

func (r tolerantReader) ReadUDP(conn *net.UDPConn, timeout time.Duration) ([]byte, *dns.SessionUDP, error) {
	m, s, err := r.Reader.ReadUDP(conn, timeout)
	if err == nil || errors.Is(err, net.ErrClosed) {
		return m, s, err
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return m, s, err
	}
	if !errors.Is(err, wsaEMsgSize) {
		time.Sleep(readRetryPause)
	}
	return nil, nil, temporaryError{err}
}

// temporaryError marks a read error the serve loop may skip.
type temporaryError struct{ error }

func (temporaryError) Temporary() bool { return true }
func (temporaryError) Timeout() bool   { return false }

// limitListener caps the connections a listener has open at once. Accept waits
// for a free slot, so surplus clients queue in the kernel backlog.
type limitListener struct {
	net.Listener
	slots     chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	perIP  int
	mu     sync.Mutex
	counts map[netip.Addr]int
}

// newLimitListener caps l at limit open connections, and at perIP from any one
// client address, so one host cannot take every slot.
func newLimitListener(l net.Listener, limit, perIP int) *limitListener {
	return &limitListener{
		Listener: l, slots: make(chan struct{}, limit), done: make(chan struct{}),
		perIP: perIP, counts: make(map[netip.Addr]int),
	}
}

// Accept implements net.Listener. A connection from an address that already has
// its share is closed at once and the wait for the next one goes on.
func (l *limitListener) Accept() (net.Conn, error) {
	for {
		select {
		case l.slots <- struct{}{}:
		case <-l.done:
			return nil, net.ErrClosed
		}
		conn, err := l.Listener.Accept()
		if err != nil {
			<-l.slots
			return nil, err
		}
		ip := remoteIP(conn)
		if !l.reserve(ip) {
			_ = conn.Close()
			<-l.slots
			continue
		}
		return &limitConn{Conn: conn, release: func() { l.unreserve(ip); <-l.slots }}, nil
	}
}

// remoteIP returns the peer address of conn without a port, IPv4-mapped IPv6
// unmapped. An address that cannot be parsed counts as the zero address.
func remoteIP(conn net.Conn) netip.Addr {
	ap, err := netip.ParseAddrPort(conn.RemoteAddr().String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

func (l *limitListener) reserve(ip netip.Addr) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts[ip] >= l.perIP {
		return false
	}
	l.counts[ip]++
	return true
}

func (l *limitListener) unreserve(ip netip.Addr) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts[ip]--; l.counts[ip] <= 0 {
		delete(l.counts, ip)
	}
}

// Close implements net.Listener and wakes an Accept that waits for a slot.
func (l *limitListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// limitConn gives its slot back exactly once, when it is closed.
type limitConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// socket is a bound pair of UDP and TCP sockets for one listen address.
type socket struct {
	udp net.PacketConn
	tcp *limitListener
}

// bind opens UDP and TCP sockets for every address. On any failure it closes
// what it opened and returns the error.
func bind(addrs []netip.AddrPort) ([]socket, error) {
	var socks []socket
	closeAll := func() {
		for _, s := range socks {
			_ = s.udp.Close()
			_ = s.tcp.Close()
		}
	}
	for _, ap := range addrs {
		udp, err := net.ListenPacket("udp", ap.String())
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("listening on udp %s: %w", ap, err)
		}
		tcp, err := net.Listen("tcp", ap.String())
		if err != nil {
			_ = udp.Close()
			closeAll()
			return nil, fmt.Errorf("listening on tcp %s: %w", ap, err)
		}
		socks = append(socks, socket{udp: udp, tcp: newLimitListener(tcp, maxTCPConns, maxTCPConnsPerIP)})
	}
	return socks, nil
}

// servers returns the miekg servers for the sockets, UDP first on every address.
func servers(socks []socket, h dns.Handler, started func()) []*dns.Server {
	var out []*dns.Server
	for _, s := range socks {
		out = append(out,
			&dns.Server{
				PacketConn: s.udp, Handler: h, UDPSize: udpReadSize,
				MsgAcceptFunc: acceptMsg, NotifyStartedFunc: started,
				DecorateReader: func(r dns.Reader) dns.Reader { return tolerantReader{r} },
			},
			&dns.Server{
				Listener: s.tcp, Handler: h,
				MsgAcceptFunc: acceptMsg, NotifyStartedFunc: started,
				ReadTimeout: tcpReadTimeout, WriteTimeout: ioTimeout,
				IdleTimeout:   func() time.Duration { return tcpIdleTimeout },
				MaxTCPQueries: maxTCPQueries,
			},
		)
	}
	return out
}

// serve runs the servers until ctx ends or one fails, then shuts them all down.
// started counts the servers that listen. stopQueries cancels the queries in
// flight; it runs first, so the shutdown does not wait for a slow upstream.
func serve(ctx context.Context, srvs []*dns.Server, socks []socket, started *sync.WaitGroup, ready, stopQueries func()) error {
	failed := make(chan error, len(srvs))
	for _, srv := range srvs {
		go func() { failed <- srv.ActivateAndServe() }()
	}
	allUp := make(chan struct{})
	go func() { started.Wait(); close(allUp) }()

	pending, runErr := awaitStop(ctx, failed, allUp, ready, len(srvs))
	stopQueries()
	return errors.Join(runErr, shutdownServers(ctx, srvs, socks, failed, pending))
}

// awaitStop blocks until ctx ends or one of the servers stops by itself. It
// calls ready once every server listens. It returns how many servers have not
// reported yet and the error of a server that stopped.
func awaitStop(ctx context.Context, failed <-chan error, allUp <-chan struct{}, ready func(), servers int) (int, error) {
	var runErr error
	pending := servers
	stopped := func(err error) {
		pending--
		if err != nil {
			runErr = fmt.Errorf("native backend: server stopped: %w", err)
		}
	}
	select {
	case <-allUp:
		ready()
		select {
		case <-ctx.Done():
		case err := <-failed:
			stopped(err)
		}
	case <-ctx.Done():
	case err := <-failed:
		stopped(err)
	}
	return pending, runErr
}

// shutdownServers stops every server and closes the sockets, then waits for the
// pending servers to report, for at most shutdownTimeout.
func shutdownServers(ctx context.Context, srvs []*dns.Server, socks []socket, failed <-chan error, pending int) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	var errs []error
	for _, srv := range srvs {
		if err := srv.ShutdownContext(stopCtx); err != nil && !isNotStarted(err) {
			errs = append(errs, fmt.Errorf("native backend: shutdown: %w", err))
		}
	}
	// miekg only stops a server that is running. One that had not started yet
	// when Shutdown ran would start serving afterwards and never return, so the
	// sockets are closed as well: such a server then fails its first read and
	// returns at once.
	for _, s := range socks {
		_ = s.udp.Close()
		_ = s.tcp.Close()
	}
	deadline := time.After(shutdownTimeout)
	for pending > 0 {
		select {
		case err := <-failed:
			pending--
			if err != nil && !errors.Is(err, net.ErrClosed) {
				errs = append(errs, fmt.Errorf("native backend: server stopped: %w", err))
			}
		case <-deadline:
			errs = append(errs, errors.New("native backend: servers did not stop in time"))
			pending = 0
		}
	}
	return errors.Join(errs...)
}

// isNotStarted reports miekg's error for shutting down a server that already
// stopped, which is not a failure here.
func isNotStarted(err error) bool {
	var dnsErr *dns.Error
	return errors.As(err, &dnsErr) && dnsErr.Error() == "dns: server not started"
}
