package lists

// agy wrote this

import (
	"compress/gzip"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

func newTLSServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func testSource(url string) config.Source {
	return config.Source{
		Name:    "sample-list",
		URL:     url,
		Kind:    config.KindBlock,
		Format:  config.FormatDomains,
		Enabled: true,
	}
}

func TestFetcher_FetchAndCache(t *testing.T) {
	fixedNow := time.Date(2026, 10, 18, 20, 45, 0, 0, time.Local)
	srv, client := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `W/"v1-etag"`)
		_, _ = w.Write([]byte("# comment\nads.example.com\n*.pop.example.net\nbad_single_label\n"))
	})

	dir := t.TempDir()
	f := &Fetcher{
		Dir:    dir,
		Client: client,
		Now:    func() time.Time { return fixedNow },
	}
	src := testSource(srv.URL + "/list.txt")

	res, err := f.Fetch(context.Background(), src)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.NotModified {
		t.Errorf("NotModified = true on first fetch, want false")
	}
	if res.Meta.ETag != `W/"v1-etag"` || res.Meta.Count != 2 || res.Meta.Rejected != 1 {
		t.Errorf("Meta = %+v, want ETag W/\"v1-etag\", Count 2, Rejected 1", res.Meta)
	}
	if !res.Set.Match("ads.example.com") || !res.Set.Match("a.pop.example.net") {
		t.Errorf("fetched DomainSet did not match expected entries")
	}

	// Verify files on disk and LoadCached.
	set, meta, err := LoadCached(dir, src)
	if err != nil {
		t.Fatalf("LoadCached: %v", err)
	}
	if meta.ETag != `W/"v1-etag"` || meta.Count != 2 || meta.Rejected != 1 || !meta.FetchedAt.Equal(fixedNow) {
		t.Errorf("LoadCached meta = %+v", meta)
	}
	if !set.Match("ads.example.com") || set.Len() != 2 {
		t.Errorf("LoadCached set len = %d", set.Len())
	}
}

func TestFetcher_ConditionalGet304(t *testing.T) {
	var hits atomic.Int32
	srv, client := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("ETag", `"strong-etag-1"`)
			_, _ = w.Write([]byte("ads.example.com\ntracker.example.org\n"))
			return
		}
		if got := r.Header.Get("If-None-Match"); got != `"strong-etag-1"` {
			t.Errorf("If-None-Match = %q, want \"strong-etag-1\"", got)
			http.Error(w, "missing etag", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNotModified)
	})

	dir := t.TempDir()
	now := time.Date(2026, 10, 18, 20, 0, 0, 0, time.Local)
	f := &Fetcher{
		Dir:    dir,
		Client: client,
		Now:    func() time.Time { return now },
	}
	src := testSource(srv.URL + "/list.txt")

	if _, err := f.Fetch(context.Background(), src); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}

	// Advance clock and fetch again -> 304 Not Modified.
	later := now.Add(time.Hour)
	f.Now = func() time.Time { return later }
	res, err := f.Fetch(context.Background(), src)
	if err != nil {
		t.Fatalf("second Fetch (304): %v", err)
	}
	if !res.NotModified {
		t.Errorf("NotModified = false, want true")
	}
	if res.Set.Len() != 2 || !res.Set.Match("ads.example.com") {
		t.Errorf("expected cached set returned on 304, got len=%d", res.Set.Len())
	}
	if !res.Meta.FetchedAt.Equal(later) || res.Meta.ETag != `"strong-etag-1"` {
		t.Errorf("meta after 304 = %+v", res.Meta)
	}
}

func TestFetcher_GzipDecodedCapEnforced(t *testing.T) {
	// Serve gzip-compressed payload whose compressed size is tiny (< 1 KiB)
	// but whose decoded size exceeds MaxBytes (4 KiB in this test).
	srv, client := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		for i := 0; i < 400; i++ {
			_, _ = gz.Write([]byte("ads.example.com\n"))
		}
		_ = gz.Close()
	})

	dir := t.TempDir()
	f := &Fetcher{
		Dir:      dir,
		Client:   client,
		MaxBytes: 1024, // 1 KiB decoded limit
	}
	src := testSource(srv.URL + "/bomb.txt")

	if _, err := f.Fetch(context.Background(), src); !errors.Is(err, ErrListTooLarge) {
		t.Fatalf("Fetch gzip bomb err = %v, want %v", err, ErrListTooLarge)
	}
	if _, err := os.Stat(filepath.Join(dir, src.Name+".txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no list file written when cap exceeded, stat err = %v", err)
	}
}

func TestFetcher_FailureKeepsOldCache(t *testing.T) {
	var failNext atomic.Bool
	srv, client := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		if failNext.Load() {
			http.Error(w, "upstream down", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("good.example.com\n"))
	})

	dir := t.TempDir()
	f := &Fetcher{Dir: dir, Client: client}
	src := testSource(srv.URL + "/list.txt")

	if _, err := f.Fetch(context.Background(), src); err != nil {
		t.Fatalf("initial Fetch: %v", err)
	}

	failNext.Store(true)
	if _, err := f.Fetch(context.Background(), src); err == nil {
		t.Fatalf("expected error on 502 response, got nil")
	}

	// Old cache must remain intact.
	set, _, err := LoadCached(dir, src)
	if err != nil {
		t.Fatalf("LoadCached after failed fetch: %v", err)
	}
	if !set.Match("good.example.com") {
		t.Errorf("old cache was lost after failed fetch")
	}
}

func TestFetcher_RejectsHTTPAndInsecureRedirectAndEmptyList(t *testing.T) {
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ads.example.com\n"))
	}))
	t.Cleanup(httpSrv.Close)

	redirSrv, client := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-http":
			http.Redirect(w, r, httpSrv.URL+"/list.txt", http.StatusFound)
		case "/hop1":
			http.Redirect(w, r, "/hop2", http.StatusFound)
		case "/hop2":
			http.Redirect(w, r, "/hop3", http.StatusFound)
		case "/hop3":
			http.Redirect(w, r, "/hop4", http.StatusFound)
		case "/hop4":
			http.Redirect(w, r, "/hop5", http.StatusFound)
		case "/hop5":
			http.Redirect(w, r, "/done", http.StatusFound)
		case "/done":
			_, _ = w.Write([]byte("five-hops.example.com\n"))
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/empty":
			_, _ = w.Write([]byte("# only comments, no domains\n"))
		}
	})

	dir := t.TempDir()
	f := &Fetcher{Dir: dir, Client: client}

	// Plain HTTP URL rejected.
	if _, err := f.Fetch(context.Background(), testSource(httpSrv.URL+"/list.txt")); !errors.Is(err, ErrHTTPSRequired) {
		t.Fatalf("Fetch http:// err = %v, want %v", err, ErrHTTPSRequired)
	}

	// Redirect from HTTPS to HTTP rejected.
	if _, err := f.Fetch(context.Background(), testSource(redirSrv.URL+"/to-http")); !errors.Is(err, ErrHTTPSRequired) {
		t.Fatalf("Fetch redirect to http:// err = %v, want %v", err, ErrHTTPSRequired)
	}

	// Exactly 5 HTTPS redirects succeed.
	hopRes, err := f.Fetch(context.Background(), testSource(redirSrv.URL+"/hop1"))
	if err != nil {
		t.Fatalf("Fetch 5 HTTPS redirects err = %v, want nil", err)
	}
	if !hopRes.Set.Match("five-hops.example.com") {
		t.Errorf("expected five-hops.example.com to match after 5 redirects")
	}

	// Redirect loop (> 5 redirects) rejected.
	if _, err := f.Fetch(context.Background(), testSource(redirSrv.URL+"/loop")); !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("Fetch redirect loop err = %v, want %v", err, ErrTooManyRedirects)
	}

	// Empty list (0 valid domains) rejected so an HTML error page cannot wipe protection.
	if _, err := f.Fetch(context.Background(), testSource(redirSrv.URL+"/empty")); !errors.Is(err, ErrEmptyList) {
		t.Fatalf("Fetch empty list err = %v, want %v", err, ErrEmptyList)
	}

	// Unsafe list name rejected.
	badSrc := testSource(redirSrv.URL + "/empty")
	badSrc.Name = "../escape"
	if _, err := f.Fetch(context.Background(), badSrc); !errors.Is(err, ErrBadListName) {
		t.Fatalf("Fetch unsafe name err = %v, want %v", err, ErrBadListName)
	}
}

func TestFetcher_TimeoutAbortsSlowServer(t *testing.T) {
	srv, client := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})

	f := &Fetcher{
		Dir:     t.TempDir(),
		Client:  client,
		Timeout: 80 * time.Millisecond,
	}
	if _, err := f.Fetch(context.Background(), testSource(srv.URL+"/slow.txt")); err == nil {
		t.Fatalf("expected timeout error from slow server, got nil")
	}
}

func TestBootstrapResolver_FailsOverFromSilentUDPAndReusesTransport(t *testing.T) {
	// First bootstrap server is a silent UDP blackhole that never replies.
	blackholePC, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen blackhole udp: %v", err)
	}
	t.Cleanup(func() { _ = blackholePC.Close() })

	// Second bootstrap server is a working fake DNS server on 127.0.0.1:0.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	var dnsQueries atomic.Int32
	started := make(chan struct{})
	dnsSrv := &dns.Server{
		PacketConn:        pc,
		NotifyStartedFunc: func() { close(started) },
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			dnsQueries.Add(1)
			resp := new(dns.Msg)
			resp.SetReply(req)
			for _, q := range req.Question {
				if strings.EqualFold(q.Name, "lists.example.com.") && q.Qtype == dns.TypeA {
					resp.Answer = append(resp.Answer, &dns.A{
						Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
						A:   netip.MustParseAddr("127.0.0.1").AsSlice(),
					})
				}
			}
			_ = w.WriteMsg(resp)
		}),
	}
	go func() { _ = dnsSrv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = dnsSrv.Shutdown() })

	tlsSrv, client := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("resolved.example.com\n"))
	})

	port := strconv.Itoa(tlsSrv.Listener.Addr().(*net.TCPAddr).Port)
	baseTr := client.Transport.(*http.Transport).Clone()

	gotAddrs := normalizeBootstrap([]string{" 1.1.1.1 ", "127.0.0.1:5354", "bad"})
	if len(gotAddrs) != 2 || gotAddrs[0] != "1.1.1.1:53" || gotAddrs[1] != "127.0.0.1:5354" {
		t.Fatalf("normalizeBootstrap = %v", gotAddrs)
	}

	f := &Fetcher{
		Dir:              t.TempDir(),
		Bootstrap:        []string{"", blackholePC.LocalAddr().String(), pc.LocalAddr().String()},
		BootstrapTimeout: 50 * time.Millisecond,
		Timeout:          500 * time.Millisecond,
		TLSConfig:        baseTr.TLSClientConfig,
	}

	res, err := f.Fetch(context.Background(), testSource("https://lists.example.com:"+port+"/list.txt"))
	if err != nil {
		t.Fatalf("Fetch via bootstrap DNS failover: %v", err)
	}
	if !res.Set.Match("resolved.example.com") {
		t.Errorf("expected resolved.example.com to match")
	}
	if dnsQueries.Load() == 0 {
		t.Errorf("expected fake bootstrap DNS server to receive queries, got 0")
	}

	// Second call with Client == nil must reuse the same *http.Transport instance.
	tr1 := f.httpClient().Transport
	tr2 := f.httpClient().Transport
	if tr1 == nil || tr1 != tr2 {
		t.Errorf("expected Fetcher to reuse default Transport across calls (%p vs %p)", tr1, tr2)
	}
}

func TestFetcher_OrphanedMetaWithoutListFileDoesNotSendETag(t *testing.T) {
	var gotIfNoneMatch string
	srv, client := newTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		if gotIfNoneMatch != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("fresh.example.com\n"))
	})

	dir := t.TempDir()
	src := testSource(srv.URL + "/list.txt")
	// Write only <name>.meta.json without <name>.txt.
	if err := writeMetaAtomic(dir, src.Name, Meta{ETag: `"v1"`, Count: 1}); err != nil {
		t.Fatalf("writeMetaAtomic: %v", err)
	}

	f := &Fetcher{Dir: dir, Client: client}
	res, err := f.Fetch(context.Background(), src)
	if err != nil {
		t.Fatalf("Fetch with orphaned meta: %v", err)
	}
	if gotIfNoneMatch != "" {
		t.Errorf("If-None-Match = %q, want empty when .txt is missing", gotIfNoneMatch)
	}
	if res.NotModified || !res.Set.Match("fresh.example.com") {
		t.Errorf("unexpected FetchResult: %+v", res)
	}
}

func TestLoadCached_MissingMetaAndDeviceName(t *testing.T) {
	dir := t.TempDir()
	src := testSource("https://example.com/list.txt")
	if err := os.WriteFile(filepath.Join(dir, src.Name+".txt"), []byte("cached.example.com\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	set, meta, err := LoadCached(dir, src)
	if err != nil {
		t.Fatalf("LoadCached without meta: %v", err)
	}
	if set.Len() != 1 || meta.Count != 1 || meta.FetchedAt.IsZero() {
		t.Errorf("LoadCached without meta = (len %d, %+v)", set.Len(), meta)
	}

	devSrc := src
	devSrc.Name = "CON"
	if _, _, err := LoadCached(dir, devSrc); !errors.Is(err, ErrBadListName) {
		t.Errorf("LoadCached device name err = %v, want %v", err, ErrBadListName)
	}
	if _, err := ReadMeta(dir, "COM1"); !errors.Is(err, ErrBadListName) {
		t.Errorf("ReadMeta device name err = %v, want %v", err, ErrBadListName)
	}
}
