package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"github.com/miekg/dns"
)

const (
	maxTCPConns = 64

	maxTCPConnsPerIP = 8

	tcpIdleTimeout = 10 * time.Second
	tcpReadTimeout = 5 * time.Second

	maxTCPQueries = 100

	ioTimeout = 5 * time.Second

	udpReadSize = 4096

	shutdownTimeout = 5 * time.Second
)

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

const wsaEMsgSize = syscall.Errno(10040)

const readRetryPause = 10 * time.Millisecond

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

type temporaryError struct{ error }

func (temporaryError) Temporary() bool { return true }
func (temporaryError) Timeout() bool   { return false }

type limitListener struct {
	net.Listener
	slots     chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	admit  func(netip.Addr) bool
	perIP  int
	mu     sync.Mutex
	counts map[netip.Addr]int
}

func newLimitListener(l net.Listener, limit, perIP int, admit func(netip.Addr) bool) *limitListener {
	return &limitListener{
		Listener: l, slots: make(chan struct{}, limit), done: make(chan struct{}),
		admit: admit, perIP: perIP, counts: make(map[netip.Addr]int),
	}
}

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
		if !l.admit(ip) || !l.reserve(ip) {
			_ = conn.Close()
			<-l.slots
			continue
		}
		return &limitConn{Conn: conn, release: func() { l.unreserve(ip); <-l.slots }}, nil
	}
}

func remoteIP(conn net.Conn) netip.Addr {
	ap, err := netip.ParseAddrPort(conn.RemoteAddr().String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

func (l *limitListener) reserve(ip netip.Addr) bool {
	key := limitKey(ip)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts[key] >= l.perIP {
		return false
	}
	l.counts[key]++
	return true
}

func (l *limitListener) unreserve(ip netip.Addr) {
	key := limitKey(ip)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts[key]--; l.counts[key] <= 0 {
		delete(l.counts, key)
	}
}

func (l *limitListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.Listener.Close()
}

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

type socket struct {
	udp net.PacketConn
	tcp *limitListener
}

func bind(addrs []netip.AddrPort, admit func(netip.Addr) bool) ([]socket, error) {
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
		socks = append(socks, socket{udp: udp, tcp: newLimitListener(tcp, maxTCPConns, maxTCPConnsPerIP, admit)})
	}
	return socks, nil
}

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

func serve(ctx context.Context, srvs []*dns.Server, closers []io.Closer, started *sync.WaitGroup, ready, stopQueries func()) error {
	failed := make(chan error, len(srvs))
	for _, srv := range srvs {
		go func() { failed <- srv.ActivateAndServe() }()
	}
	allUp := make(chan struct{})
	go func() { started.Wait(); close(allUp) }()

	pending, runErr := awaitStop(ctx, failed, allUp, ready, len(srvs))
	stopQueries()
	return errors.Join(runErr, shutdownServers(ctx, srvs, closers, failed, pending))
}

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

func shutdownServers(ctx context.Context, srvs []*dns.Server, closers []io.Closer, failed <-chan error, pending int) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	var errs []error
	for _, srv := range srvs {
		if err := srv.ShutdownContext(stopCtx); err != nil && !isNotStarted(err) {
			errs = append(errs, fmt.Errorf("native backend: shutdown: %w", err))
		}
	}

	for _, c := range closers {
		_ = c.Close()
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

func isNotStarted(err error) bool {
	var dnsErr *dns.Error
	return errors.As(err, &dnsErr) && dnsErr.Error() == "dns: server not started"
}
