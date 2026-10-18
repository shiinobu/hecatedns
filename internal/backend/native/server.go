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
// is how reflection loops start.
func acceptMsg(dh dns.Header) dns.MsgAcceptAction {
	const qrBit = 1 << 15
	switch {
	case dh.Bits&qrBit != 0:
		return dns.MsgIgnore
	case dh.Qdcount != 1 || dh.Ancount > 1 || dh.Nscount > 1 || dh.Arcount > 2:
		return dns.MsgReject
	}
	return dns.MsgAccept
}

// wsaEMsgSize is the Windows error for a UDP datagram larger than the receive
// buffer (WSAEMSGSIZE). Elsewhere the kernel truncates silently.
const wsaEMsgSize = syscall.Errno(10040)

// tolerantReader wraps miekg's reader so that one oversized datagram does not
// stop the UDP server. miekg treats every read error that is not temporary as
// fatal and returns from the serve loop, which would let any host on the network
// switch DNS off with a single packet.
type tolerantReader struct{ dns.Reader }

func (r tolerantReader) ReadUDP(conn *net.UDPConn, timeout time.Duration) ([]byte, *dns.SessionUDP, error) {
	m, s, err := r.Reader.ReadUDP(conn, timeout)
	if errors.Is(err, wsaEMsgSize) {
		return nil, nil, temporaryError{err}
	}
	return m, s, err
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
}

func newLimitListener(l net.Listener, limit int) *limitListener {
	return &limitListener{Listener: l, slots: make(chan struct{}, limit), done: make(chan struct{})}
}

// Accept implements net.Listener.
func (l *limitListener) Accept() (net.Conn, error) {
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
	return &limitConn{Conn: conn, release: func() { <-l.slots }}, nil
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
		socks = append(socks, socket{udp: udp, tcp: newLimitListener(tcp, maxTCPConns)})
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

// serve runs the servers until ctx ends or one fails, then shuts them all down
// with a fresh deadline. started is closed once every server listens.
func serve(ctx context.Context, srvs []*dns.Server, started *sync.WaitGroup, ready func()) error {
	failed := make(chan error, len(srvs))
	for _, srv := range srvs {
		go func() { failed <- srv.ActivateAndServe() }()
	}
	allUp := make(chan struct{})
	go func() { started.Wait(); close(allUp) }()

	var runErr error
	pending := len(srvs)
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

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	errs := []error{runErr}
	for _, srv := range srvs {
		if err := srv.ShutdownContext(stopCtx); err != nil && !isNotStarted(err) {
			errs = append(errs, fmt.Errorf("native backend: shutdown: %w", err))
		}
	}
	for ; pending > 0; pending-- {
		<-failed
	}
	return errors.Join(errs...)
}

// isNotStarted reports miekg's error for shutting down a server that already
// stopped, which is not a failure here.
func isNotStarted(err error) bool {
	var dnsErr *dns.Error
	return errors.As(err, &dnsErr) && dnsErr.Error() == "dns: server not started"
}
