package dashboard

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func serve(s *Server, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, r)
	return rec
}

func mutating(method, origin, contentType, marker, body string) *http.Request {
	r := httptest.NewRequest(method, "http://"+testHost+"/api/rules", strings.NewReader(body))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if marker != "" {
		r.Header.Set(dashboardHeader, marker)
	}
	return r
}

func TestHandler_SecurityHeadersOnEveryResponse(t *testing.T) {
	s := newTestServer(t, func(o *Options) { o.ProbeDomains = []string{testProbe, "backup.example.net"} })
	port := itoa(s.Addrs()[0].Port())
	wantCSP := "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self' https://*." + testProbe + ":" + port +
		" https://*.backup.example.net:" + port + "; img-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	for _, path := range []string{"/", "/api/status", "/nope"} {
		rec := serve(s, httptest.NewRequest(http.MethodGet, "http://"+testHost+path, nil))
		h := rec.Header()
		if h.Get("Content-Security-Policy") != wantCSP {
			t.Fatalf("%s: csp %q", path, h.Get("Content-Security-Policy"))
		}
		for key, want := range map[string]string{
			"Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY", "Cross-Origin-Opener-Policy": "same-origin",
			"X-Content-Type-Options": "nosniff",
		} {
			if h.Get(key) != want {
				t.Errorf("%s: %s = %q", path, key, h.Get(key))
			}
		}
		if _, ok := h["Permissions-Policy"]; !ok || h.Get("Strict-Transport-Security") != "" {
			t.Errorf("%s: permissions-policy present %v, hsts %q", path, ok, h.Get("Strict-Transport-Security"))
		}
	}
	rec := serve(s, httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/status", nil))
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Type") != jsonContentType {
		t.Fatalf("api headers: %v", rec.Header())
	}
	if got := buildCSP([]string{testProbe}, []uint16{443}); !strings.Contains(got, "https://*."+testProbe+";") {
		t.Fatalf("port 443 must be omitted: %s", got)
	}
}

func TestHandler_MutatingRequestGuard(t *testing.T) {
	s := newTestServer(t, nil)
	origin := "http://" + testHost
	cases := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"ok", mutating(http.MethodPost, origin, "application/json; charset=utf-8", "1", "{}"), http.StatusOK},
		{"method", mutating(http.MethodPut, origin, "application/json", "1", "{}"), http.StatusMethodNotAllowed},
		{"content type", mutating(http.MethodPost, origin, "text/plain", "1", "{}"), http.StatusUnsupportedMediaType},
		{"marker", mutating(http.MethodPost, origin, "application/json", "", "{}"), http.StatusForbidden},
		{"origin", mutating(http.MethodPost, "https://evil.example.org", "application/json", "1", "{}"), http.StatusForbidden},
		{"body", mutating(http.MethodPost, origin, "application/json", "1", strings.Repeat("x", defaultMaxBodyBytes+1)), http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(s, tc.req)
			if rec.Code != tc.want {
				t.Fatalf("got %d want %d: %s", rec.Code, tc.want, rec.Body)
			}
			if tc.want != http.StatusOK && !strings.HasPrefix(rec.Body.String(), `{"error":"`) {
				t.Fatalf("error body %q", rec.Body)
			}
		})
	}
}

func TestHandler_InFlightCap(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := newTestServer(t, func(o *Options) {
		o.API = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entered <- struct{}{}
			<-release
		})
	})
	var wg sync.WaitGroup
	for range defaultMaxInFlight {
		wg.Go(func() { serve(s, httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/slow", nil)) })
	}
	for range defaultMaxInFlight {
		<-entered
	}
	rec := serve(s, httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/slow", nil))
	close(release)
	wg.Wait()
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d, retry-after %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestHandler_StaticAssetsWithETag(t *testing.T) {
	s := newTestServer(t, nil)
	rec := serve(s, httptest.NewRequest(http.MethodGet, "http://"+testHost+"/app.js", nil))
	etag := rec.Header().Get("ETag")
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" || !strings.HasPrefix(etag, `"`) ||
		rec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" ||
		rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("got %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	req := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/app.js", nil)
	req.Header.Set("If-None-Match", etag)
	rec = serve(s, req)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("got %d %q", rec.Code, rec.Body)
	}
	if rec := serve(s, httptest.NewRequest(http.MethodGet, "http://"+testHost+"/index.html", nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("only the three asset paths are served, got %d", rec.Code)
	}
}

func TestHandler_EveryAssetIsRevalidatedSoANewReleaseNeverMixesWithCachedFiles(t *testing.T) {
	s := newTestServer(t, func(o *Options) {
		o.Assets = assetsWith(fstest.MapFS{
			"theme.css":    {Data: []byte("a{}")},
			"favicon.svg":  {Data: []byte("<svg/>")},
			"icon-192.png": {Data: []byte("png")},
		})
	})
	for _, path := range []string{"/", "/app.js", "/theme.css", "/favicon.svg", "/icon-192.png"} {
		rec := serve(s, httptest.NewRequest(http.MethodGet, "http://"+testHost+path, nil))
		etag := rec.Header().Get("ETag")
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" || etag == "" {
			t.Errorf("%s = %d, cache-control %q, etag %q", path, rec.Code, rec.Header().Get("Cache-Control"), etag)
			continue
		}
		req := httptest.NewRequest(http.MethodGet, "http://"+testHost+path, nil)
		req.Header.Set("If-None-Match", etag)
		rec = serve(s, req)
		if rec.Code != http.StatusNotModified || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s with its etag = %d, cache-control %q, want 304 and no-cache", path, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
}

func TestHandler_FaviconIsAnEmptyCacheableAnswer(t *testing.T) {
	s := newTestServer(t, nil)
	rec := serve(s, httptest.NewRequest(http.MethodGet, "http://"+testHost+"/favicon.ico", nil))
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 || rec.Header().Get("Cache-Control") != "max-age=86400" {
		t.Fatalf("got %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	rec = serve(s, httptest.NewRequest(http.MethodHead, "http://"+testHost+"/favicon.ico", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("HEAD got %d", rec.Code)
	}
	rec = serve(s, httptest.NewRequest(http.MethodPut, "http://"+testHost+"/favicon.ico", nil))
	if rec.Code < 400 {
		t.Fatalf("PUT got %d", rec.Code)
	}
}

func TestHandler_RequestLogLeaksNothing(t *testing.T) {
	var buf bytes.Buffer
	s := newTestServer(t, func(o *Options) { o.Log = slog.New(slog.NewTextHandler(&buf, nil)) })
	req := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/status?key=SECRETQUERY", nil)
	req.Header.Set("Cookie", "hecate_session=SECRETCOOKIE")
	serve(s, req)
	out := buf.String()
	for _, banned := range []string{testHost, "SECRETCOOKIE", "SECRETQUERY", "key="} {
		if strings.Contains(out, banned) {
			t.Fatalf("log contains %q: %s", banned, out)
		}
	}
	for _, want := range []string{"path=/api/status", "status=200", "remote=192.0.2.1", "method=GET"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log lacks %q: %s", want, out)
		}
	}
}
