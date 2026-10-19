package native

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const (
	certExpiryWarning  = 14 * 24 * time.Hour
	certExpiryLogEvery = time.Hour

	protoTLS = "tls"
	alpnDoT  = "dot"
)

var (
	errServerNameNotServed = errors.New("tls server name is not served")
	errOutsideValidity     = errors.New("the certificate is outside its validity dates")
)

type certStamp struct {
	certTime, keyTime time.Time
	certSize, keySize int64
}

type loadedCert struct {
	pair  *tls.Certificate
	leaf  *x509.Certificate
	stamp certStamp
}

type certStore struct {
	certFile, keyFile string
	names             []string
	now               func() time.Time
	log               *slog.Logger

	cur           atomic.Pointer[loadedCert]
	rejected      certStamp
	lastExpiryAt  time.Time
	lastReadErrAt time.Time
}

func newCertStore(d config.DoT, now func() time.Time, log *slog.Logger) (*certStore, error) {
	s := &certStore{certFile: d.CertFile, keyFile: d.KeyFile, names: d.Names, now: now, log: log}
	loaded, err := s.load()
	if err != nil {
		return nil, err
	}
	s.cur.Store(loaded)
	return s, nil
}

func (s *certStore) stamp() (certStamp, error) {
	cert, err := os.Stat(s.certFile)
	if err != nil {
		return certStamp{}, err
	}
	key, err := os.Stat(s.keyFile)
	if err != nil {
		return certStamp{}, err
	}
	return certStamp{certTime: cert.ModTime(), keyTime: key.ModTime(), certSize: cert.Size(), keySize: key.Size()}, nil
}

func (s *certStore) load() (*loadedCert, error) {
	stamp, err := s.stamp()
	if err != nil {
		return nil, fmt.Errorf("dot: reading the certificate files: %w", err)
	}
	pair, err := tls.LoadX509KeyPair(s.certFile, s.keyFile)
	if err != nil {
		return nil, fmt.Errorf("dot: loading the certificate %s and the key %s: %w", s.certFile, s.keyFile, err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("dot: parsing the certificate %s: %w", s.certFile, err)
	}
	now := s.now()
	switch {
	case now.After(leaf.NotAfter):
		return nil, fmt.Errorf("dot: the certificate expired at %s: %w", leaf.NotAfter.UTC().Format(time.RFC3339), errOutsideValidity)
	case now.Before(leaf.NotBefore):
		return nil, fmt.Errorf("dot: the certificate is not valid before %s: %w", leaf.NotBefore.UTC().Format(time.RFC3339), errOutsideValidity)
	}
	for i, name := range s.names {
		if leaf.VerifyHostname(name) != nil {
			return nil, fmt.Errorf("dot: the certificate does not cover dot.names[%d]", i)
		}
	}
	pair.Leaf = leaf
	return &loadedCert{pair: &pair, leaf: leaf, stamp: stamp}, nil
}

func (s *certStore) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return s.cur.Load().pair, nil
}

func (s *certStore) watch(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		s.check()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *certStore) check() {
	cur := s.cur.Load()
	stamp, err := s.stamp()
	switch {
	case err != nil:
		s.reportUnreadable(err)
	case stamp != cur.stamp && stamp != s.rejected:
		cur = s.reload(stamp, cur)
	}
	s.warnIfExpiring(cur)
}

func (s *certStore) reportUnreadable(err error) {
	now := s.now()
	if now.Sub(s.lastReadErrAt) < certExpiryLogEvery {
		return
	}
	s.lastReadErrAt = now
	s.log.Error("the DoT certificate files cannot be read, the old certificate stays in use", "err", err)
}

func (s *certStore) reload(stamp certStamp, cur *loadedCert) *loadedCert {
	next, err := s.load()
	if err != nil {
		if !errors.Is(err, errOutsideValidity) {
			s.rejected = stamp
		}
		s.log.Error("the DoT certificate could not be reloaded, the old one stays in use", "err", err)
		return cur
	}
	s.cur.Store(next)
	s.lastExpiryAt = time.Time{}
	s.log.Info("the DoT certificate was reloaded", "expires", next.leaf.NotAfter.UTC().Format(time.RFC3339))
	return next
}

func (s *certStore) warnIfExpiring(cur *loadedCert) {
	now := s.now()
	left := cur.leaf.NotAfter.Sub(now)
	if left >= certExpiryWarning || now.Sub(s.lastExpiryAt) < certExpiryLogEvery {
		return
	}
	s.lastExpiryAt = now
	expires := cur.leaf.NotAfter.UTC().Format(time.RFC3339)
	if left <= 0 {
		s.log.Error("the DoT certificate has expired, clients cannot connect", "expired", expires)
		return
	}
	s.log.Warn("the DoT certificate expires soon, renew it", "expires", expires, "hours_left", int(left/time.Hour))
}

func serverNameKey(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, name)
}

func (h *handler) dotTLSConfig(store *certStore, names []string) *tls.Config {
	served := make(map[string]struct{}, len(names))
	for _, name := range names {
		served[serverNameKey(name)] = struct{}{}
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{alpnDoT},
		GetCertificate: store.get,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			if _, ok := served[serverNameKey(hello.ServerName)]; ok {
				return nil, nil
			}
			h.drop("unknown tls server name", remoteIP(hello.Conn))
			return nil, errServerNameNotServed
		},
	}
}

func newDoT(cfg config.Config, now func() time.Time, log *slog.Logger) (*certStore, []netip.AddrPort, error) {
	if !cfg.DoT.Enabled {
		return nil, nil, nil
	}
	addrs, listenErr := listenAddrs(cfg.DoT.Listen)
	store, certErr := newCertStore(cfg.DoT, now, log)
	if err := errors.Join(listenErr, certErr); err != nil {
		return nil, nil, err
	}
	return store, addrs, nil
}

func closersOf(socks []socket, dots []*dotServer) []io.Closer {
	var out []io.Closer
	for _, s := range socks {
		out = append(out, s.udp, s.tcp)
	}
	for _, l := range dots {
		out = append(out, l)
	}
	return out
}
