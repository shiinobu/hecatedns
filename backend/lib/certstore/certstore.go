package certstore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const defaultWatchEvery = time.Minute

var (
	errNoPairs        = errors.New("certstore: no certificate pairs given")
	errNoServerName   = errors.New("certstore: the client sent no server name")
	errNameNotCovered = errors.New("certstore: no certificate covers the server name")
)

type Pair struct {
	CertFile string
	KeyFile  string
}

type Config struct {
	Pairs []Pair
	Now   func() time.Time
	Log   *slog.Logger
}

type stamp struct {
	certTime, keyTime time.Time
	certSize, keySize int64
}

type loaded struct {
	cert  *tls.Certificate
	stamp stamp
}

type snapshot struct {
	pairs []*loaded
}

type Store struct {
	pairs []Pair
	now   func() time.Time
	log   *slog.Logger

	cur      atomic.Pointer[snapshot]
	mu       sync.Mutex
	rejected []stamp
}

func New(cfg Config) (*Store, error) {
	if len(cfg.Pairs) == 0 {
		return nil, errNoPairs
	}
	s := &Store{pairs: append([]Pair(nil), cfg.Pairs...), now: cfg.Now, log: cfg.Log}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	snap := &snapshot{pairs: make([]*loaded, len(s.pairs))}
	var errs []error
	for i, p := range s.pairs {
		l, err := s.load(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("certs[%d]: %w", i, err))
			continue
		}
		snap.pairs[i] = l
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	s.rejected = make([]stamp, len(s.pairs))
	s.cur.Store(snap)
	return s, nil
}

func stampOf(p Pair) (stamp, error) {
	cert, err := os.Stat(p.CertFile)
	if err != nil {
		return stamp{}, err
	}
	key, err := os.Stat(p.KeyFile)
	if err != nil {
		return stamp{}, err
	}
	return stamp{certTime: cert.ModTime(), keyTime: key.ModTime(), certSize: cert.Size(), keySize: key.Size()}, nil
}

func (s *Store) load(p Pair) (*loaded, error) {
	st, err := stampOf(p)
	if err != nil {
		return nil, fmt.Errorf("reading the certificate files: %w", err)
	}
	pair, err := tls.LoadX509KeyPair(p.CertFile, p.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("loading the certificate %s and the key %s: %w", p.CertFile, p.KeyFile, err)
	}
	leaf := pair.Leaf
	if leaf == nil {
		leaf, err = x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("parsing the certificate %s: %w", p.CertFile, err)
		}
		pair.Leaf = leaf
	}
	now := s.now()
	if now.After(leaf.NotAfter) || now.Before(leaf.NotBefore) {
		return nil, fmt.Errorf("the certificate %s is outside its validity dates (%s to %s)", p.CertFile,
			leaf.NotBefore.UTC().Format(time.RFC3339), leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	return &loaded{cert: &pair, stamp: st}, nil
}

func (s *Store) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(hello.ServerName)
	if name == "" {
		return nil, errNoServerName
	}
	for _, l := range s.cur.Load().pairs {
		if l.cert.Leaf.VerifyHostname(name) == nil {
			return l.cert, nil
		}
	}
	return nil, errNameNotCovered
}

func (s *Store) NotAfterFor(name string) (time.Time, bool) {
	cert, err := s.GetCertificate(&tls.ClientHelloInfo{ServerName: name})
	if err != nil {
		return time.Time{}, false
	}
	return cert.Leaf.NotAfter, true
}

func (s *Store) Covers(name string) bool {
	_, err := s.GetCertificate(&tls.ClientHelloInfo{ServerName: name})
	return err == nil
}

func (s *Store) NotAfter() time.Time {
	var earliest time.Time
	for _, l := range s.cur.Load().pairs {
		if earliest.IsZero() || l.cert.Leaf.NotAfter.Before(earliest) {
			earliest = l.cert.Leaf.NotAfter
		}
	}
	return earliest
}

func (s *Store) Watch(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = defaultWatchEvery
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		s.Check()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Store) Check() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.pairs {
		s.checkPair(i, p)
	}
}

func (s *Store) checkPair(i int, p Pair) {
	cur := s.cur.Load()
	st, err := stampOf(p)
	if err != nil || st == cur.pairs[i].stamp || st == s.rejected[i] {
		return
	}
	next, err := s.load(p)
	if err != nil {
		s.rejected[i] = st
		s.log.Warn("a certificate could not be reloaded, the old one stays in use", "pair", i, "err", err)
		return
	}
	pairs := append([]*loaded(nil), cur.pairs...)
	pairs[i] = next
	s.cur.Store(&snapshot{pairs: pairs})
	s.log.Info("a certificate was reloaded", "pair", i, "expires", next.cert.Leaf.NotAfter.UTC().Format(time.RFC3339))
}
