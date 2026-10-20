package dashboard

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const probeHost = "k3j9x2m7q1w8e5r4t6y0.ghost.example.org"

func serveGuarded(s *Server, r *http.Request) (rec *httptest.ResponseRecorder, aborted bool) {
	rec = httptest.NewRecorder()
	defer func() {
		if p := recover(); p != nil {
			if err, ok := p.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
				panic(p)
			}
			aborted = true
		}
	}()
	s.handler.ServeHTTP(rec, r)
	return rec, false
}

func TestHandler_ProbeAnswersNoContent(t *testing.T) {
	s := newTestServer(t, nil)
	port := itoa(s.Addrs()[0].Port())
	for _, host := range []string{probeHost, strings.ToUpper(probeHost), probeHost + ":" + port} {
		rec, aborted := serveGuarded(s, httptest.NewRequest(http.MethodGet, "http://"+host+"/probe", nil))
		if aborted || rec.Code != http.StatusNoContent {
			t.Fatalf("host %q: status %d, aborted %v", host, rec.Code, aborted)
		}
		if rec.Header().Get("Cache-Control") != "no-store" || rec.Body.Len() != 0 || rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("host %q: headers %v body %q", host, rec.Header(), rec.Body.String())
		}
	}
}

func TestHandler_ProbeHostsAreClosedForEverythingElse(t *testing.T) {
	s := newTestServer(t, nil)
	cases := []struct{ name, method, host, path string }{
		{"post", http.MethodPost, probeHost, "/probe"},
		{"head", http.MethodHead, probeHost, "/probe"},
		{"root", http.MethodGet, probeHost, "/"},
		{"api", http.MethodGet, probeHost, "/api/status"},
		{"deeper path", http.MethodGet, probeHost, "/probe/x"},
		{"two labels", http.MethodGet, "a." + probeHost, "/probe"},
		{"bare domain", http.MethodGet, testProbe, "/probe"},
		{"other domain", http.MethodGet, "k3j9x2m7q1w8.example.net", "/probe"},
		{"suffix lookalike", http.MethodGet, "k3j9x2m7q1w8.xghost.example.org", "/probe"},
		{"bad label", http.MethodGet, "-bad.ghost.example.org", "/probe"},
		{"wrong port", http.MethodGet, probeHost + ":9", "/probe"},
		{"trailing dot", http.MethodGet, probeHost + ".", "/probe"},
	}
	for _, tc := range cases {
		_, aborted := serveGuarded(s, httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil))
		if !aborted {
			t.Errorf("%s: the connection must be closed without an answer", tc.name)
		}
	}
	if got := s.Counters().UnknownHost; got != uint64(len(cases)) {
		t.Errorf("unknown host counter = %d, want %d", got, len(cases))
	}
}

func TestRun_ProbeHostOverTLS(t *testing.T) {
	store, pool := testStore(t)
	s := newTestServer(t, func(o *Options) { o.Certs = store })
	runServer(t, s)
	addr := s.Addrs()[0]
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", addr.String(),
		&tls.Config{ServerName: probeHost, RootCAs: pool})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	resp, err := rawGet(t, conn, probeHost+":"+itoa(addr.Port()), "/probe")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestNew_RejectsProbeDomainsTheCertificatesDoNotCover(t *testing.T) {
	store, _ := testStore(t)
	s, err := New(Options{
		Listen:       loopback(),
		Names:        []string{testHost},
		ProbeDomains: []string{testProbe, "ghost.example.net"},
		Certs:        store,
		Assets:       testAssets(),
	})
	if err == nil {
		_ = s.Close()
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "probe_domains[1]") || strings.Contains(err.Error(), "probe_domains[0]") {
		t.Fatalf("error %q should name only probe_domains[1]", err)
	}
}
