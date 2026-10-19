package native

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/miekg/dns"
)

const (
	maxDoTPreConns      = 512
	maxDoTPreConnsPerIP = 8
	maxDoTConns         = 64
	maxDoTConnsPerIP    = 8
	maxDoTQueries       = 1000
	maxDoTInFlight      = 16
	maxDoTInFlightTotal = 256
	maxDoTFrame         = 4096

	dotHandshakeTimeout = 4 * time.Second
	dotIdleTimeout      = 30 * time.Second

	dnsHeaderSize = 12

	reasonTLSBusy      = "too many tls connections"
	reasonTLSBusyIP    = "too many tls connections from one address"
	reasonTLSHandshake = "tls handshake failed"
)

var errBadFrame = errors.New("dot: a frame is shorter than a DNS header or longer than allowed")

type dotLimits struct {
	preTotal, preIP int
	total, perIP    int
	maxQueries      int
	inFlight        int
	inFlightTotal   int
	maxFrame        int

	handshake  time.Duration
	firstQuery time.Duration
	idle       time.Duration
	frame      time.Duration
	write      time.Duration
}

func defaultDoTLimits() dotLimits {
	return dotLimits{
		preTotal: maxDoTPreConns, preIP: maxDoTPreConnsPerIP,
		total: maxDoTConns, perIP: maxDoTConnsPerIP,
		maxQueries: maxDoTQueries, inFlight: maxDoTInFlight, inFlightTotal: maxDoTInFlightTotal, maxFrame: maxDoTFrame,
		handshake: dotHandshakeTimeout, firstQuery: tcpReadTimeout, idle: dotIdleTimeout,
		frame: tcpReadTimeout, write: ioTimeout,
	}
}

type connPool struct {
	mu     sync.Mutex
	total  int
	perIP  int
	used   int
	counts map[netip.Addr]int
}

func newConnPool(total, perIP int) *connPool {
	return &connPool{total: total, perIP: perIP, counts: make(map[netip.Addr]int)}
}

func (p *connPool) acquire(ip netip.Addr) (release func(), refused string) {
	key := limitKey(ip)
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.used >= p.total:
		return nil, reasonTLSBusy
	case p.counts[key] >= p.perIP:
		return nil, reasonTLSBusyIP
	}
	p.used++
	p.counts[key]++
	var once sync.Once
	return func() { once.Do(func() { p.release(key) }) }, ""
}

func (p *connPool) release(key netip.Addr) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.used--
	if p.counts[key]--; p.counts[key] <= 0 {
		delete(p.counts, key)
	}
}

type dotParams struct {
	tls   *tls.Config
	admit func(netip.Addr) bool
	owner *handler
	lim   dotLimits
}

type dotServer struct {
	ln       net.Listener
	cfg      *tls.Config
	handler  dns.Handler
	owner    *handler
	log      *slog.Logger
	admit    func(netip.Addr) bool
	lim      dotLimits
	pre, est *connPool
	wide     chan struct{}
	started  func()

	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	closing bool
	wg      sync.WaitGroup
}

type runner interface {
	ActivateAndServe() error
	ShutdownContext(context.Context) error
}

var _ runner = (*dotServer)(nil)

func bindDoT(addrs []netip.AddrPort, p dotParams) ([]*dotServer, error) {
	pre, est := newConnPool(p.lim.preTotal, p.lim.preIP), newConnPool(p.lim.total, p.lim.perIP)
	wide := make(chan struct{}, p.lim.inFlightTotal)
	var out []*dotServer
	for _, ap := range addrs {
		ln, err := net.Listen(networkFor("tcp", ap), ap.String())
		if err != nil {
			for _, s := range out {
				_ = s.Close()
			}
			return nil, fmt.Errorf("listening on dot %s: %w", ap, err)
		}
		out = append(out, &dotServer{
			ln: ln, cfg: p.tls, handler: p.owner.forProto(protoTLS), owner: p.owner, log: p.owner.log,
			admit: p.admit, lim: p.lim, pre: pre, est: est, wide: wide, conns: make(map[net.Conn]struct{}),
		})
	}
	return out, nil
}

func (s *dotServer) Addr() net.Addr { return s.ln.Addr() }

func (s *dotServer) Close() error { return s.ln.Close() }

func (s *dotServer) ActivateAndServe() error {
	if s.started != nil {
		s.started()
	}
	var backoff acceptBackoff
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if s.isClosing() || errors.Is(err, net.ErrClosed) {
				return nil
			}
			if !backoff.pause(err, nil) {
				return err
			}
			continue
		}
		backoff.reset()
		s.take(conn)
	}
}

func (s *dotServer) isClosing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}

func (s *dotServer) take(conn net.Conn) {
	ip := remoteIP(conn)
	if !s.admit(ip) {
		_ = conn.Close()
		return
	}
	releasePre, refused := s.pre.acquire(ip)
	if refused != "" {
		s.owner.drop(refused, ip)
		_ = conn.Close()
		return
	}
	if !s.track(conn) {
		releasePre()
		_ = conn.Close()
		return
	}
	go s.handle(conn, ip, releasePre)
}

func (s *dotServer) track(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.conns[conn] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *dotServer) untrack(conn net.Conn) {
	s.mu.Lock()
	delete(s.conns, conn)
	s.mu.Unlock()
	s.wg.Done()
}

func (s *dotServer) handle(conn net.Conn, ip netip.Addr, releasePre func()) {
	defer s.untrack(conn)
	defer conn.Close()
	secure := tls.Server(conn, s.cfg)
	_ = secure.SetDeadline(time.Now().Add(s.lim.handshake))
	err := secure.Handshake()
	releasePre()
	if err != nil {
		if !errors.Is(err, errServerNameNotServed) {
			s.owner.drop(reasonTLSHandshake, ip)
		}
		return
	}
	releaseEst, refused := s.est.acquire(ip)
	if refused != "" {
		s.owner.drop(refused, ip)
		return
	}
	defer releaseEst()
	s.serveConn(secure)
}

func (s *dotServer) serveConn(conn *tls.Conn) {
	w := &dotWriter{conn: conn, timeout: s.lim.write}
	slots := make(chan struct{}, s.lim.inFlight)
	var inFlight sync.WaitGroup
	defer inFlight.Wait()
	wait := s.lim.firstQuery
	for range s.lim.maxQueries {
		frame, err := s.readFrame(conn, wait)
		if err != nil {
			return
		}
		wait = s.lim.idle
		req := s.parse(w, frame)
		if req == nil {
			continue
		}
		slots <- struct{}{}
		s.wide <- struct{}{}
		inFlight.Add(1)
		go func() {
			defer func() { <-s.wide; <-slots; inFlight.Done() }()
			s.run(w, req)
		}()
	}
}

func (s *dotServer) readFrame(conn net.Conn, wait time.Duration) ([]byte, error) {
	var prefix [2]byte
	if err := conn.SetReadDeadline(time.Now().Add(wait)); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint16(prefix[:]))
	if size < dnsHeaderSize || size > s.lim.maxFrame {
		return nil, errBadFrame
	}
	if err := conn.SetReadDeadline(time.Now().Add(s.lim.frame)); err != nil {
		return nil, err
	}
	frame := make([]byte, size)
	_, err := io.ReadFull(conn, frame)
	return frame, err
}

func headerOf(frame []byte) dns.Header {
	return dns.Header{
		Id:      binary.BigEndian.Uint16(frame[0:]),
		Bits:    binary.BigEndian.Uint16(frame[2:]),
		Qdcount: binary.BigEndian.Uint16(frame[4:]),
		Ancount: binary.BigEndian.Uint16(frame[6:]),
		Nscount: binary.BigEndian.Uint16(frame[8:]),
		Arcount: binary.BigEndian.Uint16(frame[10:]),
	}
}

func (s *dotServer) parse(w *dotWriter, frame []byte) *dns.Msg {
	if acceptMsg(headerOf(frame)) != dns.MsgAccept {
		return nil
	}
	req := new(dns.Msg)
	if err := req.Unpack(frame); err != nil {
		_ = w.WriteMsg(new(dns.Msg).SetRcodeFormatError(req))
		return nil
	}
	return req
}

func (s *dotServer) run(w *dotWriter, req *dns.Msg) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("dns handler recovered from a panic", "stage", "dot", "panic", fmt.Sprint(r))
		}
	}()
	s.handler.ServeDNS(w, req)
}

func (s *dotServer) ShutdownContext(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	open := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		open = append(open, c)
	}
	s.mu.Unlock()
	_ = s.ln.Close()
	for _, c := range open {
		_ = c.Close()
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type dotWriter struct {
	conn    *tls.Conn
	timeout time.Duration
	mu      sync.Mutex
}

var _ dns.ResponseWriter = (*dotWriter)(nil)

func (w *dotWriter) LocalAddr() net.Addr  { return w.conn.LocalAddr() }
func (w *dotWriter) RemoteAddr() net.Addr { return w.conn.RemoteAddr() }

func (w *dotWriter) WriteMsg(m *dns.Msg) error {
	packed, err := m.Pack()
	if err != nil {
		return err
	}
	_, err = w.Write(packed)
	return err
}

func (w *dotWriter) Write(b []byte) (int, error) {
	if len(b) > math.MaxUint16 {
		return 0, errors.New("dot: the message does not fit in one frame")
	}
	frame := make([]byte, 2+len(b))
	binary.BigEndian.PutUint16(frame, uint16(len(b)))
	copy(frame[2:], b)
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.SetWriteDeadline(time.Now().Add(w.timeout)); err != nil {
		return 0, err
	}
	n, err := w.conn.Write(frame)
	return max(n-2, 0), err
}

func (w *dotWriter) Close() error        { return w.conn.NetConn().Close() }
func (w *dotWriter) TsigStatus() error   { return nil }
func (w *dotWriter) TsigTimersOnly(bool) {}
func (w *dotWriter) Hijack()             {}
