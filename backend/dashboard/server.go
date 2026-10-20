package dashboard

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/certstore"
)

const (
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 10 * time.Second
	defaultWriteTimeout      = 15 * time.Second
	defaultIdleTimeout       = 30 * time.Second
	defaultShutdownTimeout   = 5 * time.Second
	defaultMaxHeaderBytes    = 8 << 10
	defaultMaxBodyBytes      = 4 << 10
	defaultMaxConns          = 32
	defaultMaxInFlight       = 16

	alpnHTTP11 = "http/1.1"
	httpsPort  = 443
)

var (
	errUnknownServerName = errors.New("dashboard: tls server name is not served")
	errAlreadyRunning    = errors.New("dashboard: the server is already running")
)

type Options struct {
	Listen       []netip.AddrPort
	Names        []string
	ProbeDomains []string
	Certs        *certstore.Store
	Assets       fs.FS
	API          http.Handler
	Log          *slog.Logger

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
	MaxBodyBytes      int64
	MaxConns          int
	MaxInFlight       int
}

type Counters struct {
	UnknownSNI    uint64
	UnknownHost   uint64
	RejectedConns uint64
	Busy          uint64
}

type counters struct {
	unknownSNI, unknownHost, rejectedConns, busy atomic.Uint64
}

type Server struct {
	opts      Options
	tls       bool
	names     map[string]struct{}
	probes    map[string]struct{}
	portText  map[string]struct{}
	hosts     map[string]struct{}
	csp       string
	assets    map[string]asset
	listeners []*limitListener
	addrs     []netip.AddrPort
	handler   http.Handler
	inFlight  chan struct{}
	stats     counters
	started   atomic.Bool
}

func New(opts Options) (*Server, error) {
	opts = withDefaults(opts)
	assets, assetErr := loadAssets(opts.Assets)
	if err := errors.Join(validate(opts), assetErr); err != nil {
		return nil, err
	}
	s := &Server{
		opts:     opts,
		tls:      opts.Certs != nil,
		assets:   assets,
		inFlight: make(chan struct{}, opts.MaxInFlight),
		names:    make(map[string]struct{}, len(opts.Names)),
		probes:   make(map[string]struct{}, len(opts.ProbeDomains)),
		portText: make(map[string]struct{}),
	}
	for _, name := range opts.Names {
		s.names[name] = struct{}{}
	}
	for _, domain := range opts.ProbeDomains {
		s.probes[domain] = struct{}{}
	}
	if err := s.bind(); err != nil {
		return nil, err
	}
	for _, port := range s.ports() {
		s.portText[strconv.Itoa(int(port))] = struct{}{}
	}
	s.hosts = s.allowedHosts()
	s.csp = buildCSP(opts.ProbeDomains, s.ports())
	s.handler = s.routes()
	return s, nil
}

func withDefaults(o Options) Options {
	o.Names = append([]string(nil), o.Names...)
	o.ProbeDomains = append([]string(nil), o.ProbeDomains...)
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	o.ReadHeaderTimeout = orDefault(o.ReadHeaderTimeout, defaultReadHeaderTimeout)
	o.ReadTimeout = orDefault(o.ReadTimeout, defaultReadTimeout)
	o.WriteTimeout = orDefault(o.WriteTimeout, defaultWriteTimeout)
	o.IdleTimeout = orDefault(o.IdleTimeout, defaultIdleTimeout)
	o.ShutdownTimeout = orDefault(o.ShutdownTimeout, defaultShutdownTimeout)
	o.MaxHeaderBytes = orDefault(o.MaxHeaderBytes, defaultMaxHeaderBytes)
	o.MaxBodyBytes = orDefault(o.MaxBodyBytes, defaultMaxBodyBytes)
	o.MaxConns = orDefault(o.MaxConns, defaultMaxConns)
	o.MaxInFlight = orDefault(o.MaxInFlight, defaultMaxInFlight)
	return o
}

func orDefault[T time.Duration | int | int64](v, def T) T {
	if v <= 0 {
		return def
	}
	return v
}

func validate(o Options) error {
	var errs []error
	if len(o.Listen) == 0 {
		errs = append(errs, errors.New("dashboard: no listen address"))
	}
	for i, ap := range o.Listen {
		switch {
		case !ap.IsValid():
			errs = append(errs, fmt.Errorf("dashboard: listen[%d] is not a valid address", i))
		case o.Certs == nil && !ap.Addr().IsLoopback():
			errs = append(errs, fmt.Errorf("dashboard: listen %s is not loopback and needs certificates", ap))
		}
	}
	if o.Certs != nil && len(o.Names) == 0 {
		errs = append(errs, errors.New("dashboard: serving TLS needs at least one name"))
	}
	for i, name := range o.Names {
		switch {
		case !validHostName(name):
			errs = append(errs, fmt.Errorf("dashboard: names[%d] is not a valid lowercase host name", i))
		case o.Certs != nil && !o.Certs.Covers(name):
			errs = append(errs, fmt.Errorf("dashboard: names[%d] is not covered by any certificate", i))
		}
	}
	for i, domain := range o.ProbeDomains {
		switch {
		case !validHostName(domain):
			errs = append(errs, fmt.Errorf("dashboard: probe_domains[%d] is not a valid lowercase host name", i))
		case o.Certs != nil && !o.Certs.Covers(probeSampleLabel+"."+domain):
			errs = append(errs, fmt.Errorf("dashboard: probe_domains[%d] is not covered by any certificate", i))
		}
	}
	return errors.Join(errs...)
}

func (s *Server) bind() error {
	slots := make(chan struct{}, s.opts.MaxConns)
	for _, ap := range s.opts.Listen {
		network := "tcp6"
		if ap.Addr().Unmap().Is4() {
			network = "tcp4"
		}
		ln, err := net.Listen(network, ap.String())
		if err != nil {
			return errors.Join(fmt.Errorf("dashboard: listen %s: %w", ap, err), s.Close())
		}
		bound, err := netip.ParseAddrPort(ln.Addr().String())
		if err != nil {
			return errors.Join(fmt.Errorf("dashboard: bound address %s: %w", ln.Addr(), err), ln.Close(), s.Close())
		}
		s.addrs = append(s.addrs, bound)
		s.listeners = append(s.listeners, newLimitListener(ln, slots, &s.stats.rejectedConns, s.opts.Log))
	}
	return nil
}

func (s *Server) ports() []uint16 {
	var out []uint16
	seen := make(map[uint16]bool, len(s.addrs))
	for _, ap := range s.addrs {
		if !seen[ap.Port()] {
			seen[ap.Port()] = true
			out = append(out, ap.Port())
		}
	}
	return out
}

func (s *Server) allowedHosts() map[string]struct{} {
	hosts := make(map[string]struct{})
	for _, name := range s.opts.Names {
		hosts[name] = struct{}{}
		for _, port := range s.ports() {
			hosts[name+":"+strconv.Itoa(int(port))] = struct{}{}
		}
	}
	if s.tls {
		return hosts
	}
	for _, ap := range s.addrs {
		hosts["localhost:"+strconv.Itoa(int(ap.Port()))] = struct{}{}
		hosts[ap.String()] = struct{}{}
	}
	return hosts
}

func (s *Server) Addrs() []netip.AddrPort {
	return append([]netip.AddrPort(nil), s.addrs...)
}

func (s *Server) Counters() Counters {
	return Counters{
		UnknownSNI:    s.stats.unknownSNI.Load(),
		UnknownHost:   s.stats.unknownHost.Load(),
		RejectedConns: s.stats.rejectedConns.Load(),
		Busy:          s.stats.busy.Load(),
	}
}

func (s *Server) Close() error {
	var errs []error
	for _, ln := range s.listeners {
		errs = append(errs, ln.Close())
	}
	return errors.Join(errs...)
}

func (s *Server) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errAlreadyRunning
	}
	srv := s.httpServer()
	errs := make(chan error, len(s.listeners))
	for _, ln := range s.listeners {
		go func(l net.Listener) { errs <- srv.Serve(l) }(s.wrapTLS(ln))
	}
	pending := len(s.listeners)
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errs:
		pending--
	}
	sctx, cancel := context.WithTimeout(context.Background(), s.opts.ShutdownTimeout)
	defer cancel()
	shutErr := srv.Shutdown(sctx)
	if shutErr != nil {
		shutErr = errors.Join(shutErr, srv.Close())
	}
	all := []error{ignoreClosed(serveErr), shutErr}
	for range pending {
		all = append(all, ignoreClosed(<-errs))
	}
	return errors.Join(all...)
}

func ignoreClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) httpServer() *http.Server {
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	return &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		ReadTimeout:       s.opts.ReadTimeout,
		WriteTimeout:      s.opts.WriteTimeout,
		IdleTimeout:       s.opts.IdleTimeout,
		MaxHeaderBytes:    s.opts.MaxHeaderBytes,
		Protocols:         &protocols,
		TLSNextProto:      map[string]func(*http.Server, *tls.Conn, http.Handler){},
		ErrorLog:          slog.NewLogLogger(s.opts.Log.Handler(), slog.LevelDebug),
	}
}

func (s *Server) wrapTLS(ln net.Listener) net.Listener {
	if !s.tls {
		return ln
	}
	return tls.NewListener(ln, &tls.Config{
		MinVersion:     tls.VersionTLS13,
		NextProtos:     []string{alpnHTTP11},
		GetCertificate: s.getCertificate,
	})
}

func (s *Server) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(hello.ServerName)
	if _, ok := s.names[name]; !ok && !s.isProbeName(name) {
		s.stats.unknownSNI.Add(1)
		return nil, errUnknownServerName
	}
	return s.opts.Certs.GetCertificate(hello)
}
