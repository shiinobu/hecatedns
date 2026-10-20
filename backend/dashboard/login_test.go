package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	sessionLifetime  = 30 * 24 * time.Hour
	unauthorizedJSON = `{"error":"unauthorized","message":"Sign in required."}`
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type authFixture struct {
	handler  http.Handler
	clock    *fakeClock
	key      string
	hashFile string
	dir      string
	reached  atomic.Int64
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f := &authFixture{
		clock:    &fakeClock{now: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)},
		key:      key,
		dir:      dir,
		hashFile: filepath.Join(dir, "key.sha256"),
	}
	writeKeyHash(t, f.hashFile, HashKey(key)+"\n", time.Now().Add(-time.Hour))
	f.handler = f.wrap(t, filepath.Join(dir, "cookie.secret"))
	return f
}

func (f *authFixture) wrap(t *testing.T, secretFile string) http.Handler {
	t.Helper()
	a, err := NewAuth(AuthOptions{KeyHashFile: f.hashFile, SecretFile: secretFile, SessionDays: 30, Now: f.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	return a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.reached.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
}

func apiRequest(method, path, remote string, overTLS bool, body string, cookie *http.Cookie) *http.Request {
	scheme := "http://"
	if overTLS {
		scheme = "https://"
	}
	r := httptest.NewRequest(method, scheme+testHost+path, strings.NewReader(body))
	r.RemoteAddr = remote
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

func loginBody(key string) string {
	return `{"key":"` + key + `"}`
}

func (f *authFixture) do(r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, r)
	return rec
}

func (f *authFixture) login(key, remote string, overTLS bool) *httptest.ResponseRecorder {
	return f.do(apiRequest(http.MethodPost, "/api/login", remote, overTLS, loginBody(key), nil))
}

func onlyCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, status %d: %s", len(cookies), rec.Code, rec.Body)
	}
	return cookies[0]
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body apiError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return body.Error
}

func TestLogin_TLSSetsHostPrefixedCookie(t *testing.T) {
	f := newAuthFixture(t)
	rec := f.login(f.key, "192.0.2.10:5000", true)
	c := onlyCookie(t, rec)
	if rec.Code != http.StatusOK || c.Name != "__Host-hecate_session" || !c.Secure || !c.HttpOnly ||
		c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" || c.MaxAge != int(sessionLifetime.Seconds()) {
		t.Fatalf("status %d cookie %+v", rec.Code, c)
	}
	var body struct {
		OK      bool   `json:"ok"`
		Expires string `json:"expires"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Expires != f.clock.Now().Add(sessionLifetime).Format(time.RFC3339) ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("body %+v headers %v", body, rec.Header())
	}
	if next := f.do(apiRequest(http.MethodGet, "/api/status", "192.0.2.10:5001", true, "", c)); next.Code != http.StatusOK || f.reached.Load() != 1 {
		t.Fatalf("session refused: %d", next.Code)
	}
}

func TestLogin_PlainLoopbackCookieIsNotSecure(t *testing.T) {
	f := newAuthFixture(t)
	c := onlyCookie(t, f.login(f.key, "127.0.0.1:5000", false))
	if c.Name != "hecate_session" || c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Fatalf("cookie %+v", c)
	}
	if rec := f.do(apiRequest(http.MethodGet, "/api/status", "127.0.0.1:5001", false, "", c)); rec.Code != http.StatusOK {
		t.Fatalf("plain session refused: %d", rec.Code)
	}
	if rec := f.do(apiRequest(http.MethodGet, "/api/status", "127.0.0.1:5001", true, "", c)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("plain cookie accepted over TLS: %d", rec.Code)
	}
}

func flipLast(s string) string {
	repl := "0"
	if strings.HasSuffix(s, "0") {
		repl = "1"
	}
	return s[:len(s)-1] + repl
}

func TestSession_RefusesTamperedMalformedForeignAndExpired(t *testing.T) {
	f := newAuthFixture(t)
	const remote = "192.0.2.20:6000"
	c := onlyCookie(t, f.login(f.key, remote, true))
	exp, mac, _ := strings.Cut(c.Value, ".")
	for name, value := range map[string]string{
		"tampered mac": exp + "." + flipLast(mac), "tampered expiry": flipLast(exp) + "." + mac,
		"no dot": exp + mac, "empty mac": exp + ".", "short mac": exp + "." + mac[:10],
		"not hex": exp + "." + strings.Repeat("z", len(mac)), "text expiry": "soon." + mac, "empty": "",
	} {
		bad := &http.Cookie{Name: c.Name, Value: value}
		if rec := f.do(apiRequest(http.MethodGet, "/api/status", remote, true, "", bad)); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d", name, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	f.wrap(t, filepath.Join(f.dir, "other.secret")).ServeHTTP(rec, apiRequest(http.MethodGet, "/api/status", remote, true, "", c))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("cookie from another secret accepted: %d", rec.Code)
	}
	f.clock.Advance(sessionLifetime - time.Second)
	if rec := f.do(apiRequest(http.MethodGet, "/api/status", remote, true, "", c)); rec.Code != http.StatusOK {
		t.Fatalf("cookie refused before expiry: %d", rec.Code)
	}
	f.clock.Advance(time.Second)
	if rec := f.do(apiRequest(http.MethodGet, "/api/status", remote, true, "", c)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired cookie accepted: %d", rec.Code)
	}
}

func TestSession_KeyRotationRevokesOldCookies(t *testing.T) {
	f := newAuthFixture(t)
	const remote = "192.0.2.30:6000"
	old := onlyCookie(t, f.login(f.key, remote, true))
	newKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	writeKeyHash(t, f.hashFile, HashKey(newKey)+"\n", time.Now())
	if rec := f.do(apiRequest(http.MethodGet, "/api/status", remote, true, "", old)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("pre-rotation cookie accepted: %d", rec.Code)
	}
	if rec := f.login(f.key, remote, true); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old key accepted: %d", rec.Code)
	}
	fresh := onlyCookie(t, f.login(newKey, remote, true))
	if rec := f.do(apiRequest(http.MethodGet, "/api/status", remote, true, "", fresh)); rec.Code != http.StatusOK {
		t.Fatalf("new session refused: %d", rec.Code)
	}
}

func TestWrap_UnauthorizedWithoutSession(t *testing.T) {
	f := newAuthFixture(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/status"}, {http.MethodGet, "/api/queries?window=15m"}, {http.MethodGet, "/api/rules"},
		{http.MethodGet, "/api/connection"}, {http.MethodGet, "/api/unknown"}, {http.MethodPost, "/api/rules"},
		{http.MethodPost, "/api/rules/remove"}, {http.MethodPost, "/api/connection/rotate"},
		{http.MethodPost, "/api/connection/remove"}, {http.MethodPost, "/api/logout"}, {http.MethodPost, "/api/login/"},
	} {
		rec := f.do(apiRequest(tc.method, tc.path, "192.0.2.40:1", true, "{}", nil))
		if rec.Code != http.StatusUnauthorized || rec.Body.String() != unauthorizedJSON || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s: %d %q", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	if f.reached.Load() != 0 {
		t.Fatal("an unauthenticated request reached the API")
	}
}

func TestLogin_Outcomes(t *testing.T) {
	f := newAuthFixture(t)
	wrong, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, method, body string
		status             int
		code               string
	}{
		{"ok", http.MethodPost, loginBody(strings.ToLower(f.key)), http.StatusOK, ""},
		{"wrong key", http.MethodPost, loginBody(wrong), http.StatusUnauthorized, "bad_key"},
		{"empty key", http.MethodPost, loginBody(""), http.StatusUnauthorized, "bad_key"},
		{"huge key", http.MethodPost, loginBody(strings.Repeat("A", 3000)), http.StatusUnauthorized, "bad_key"},
		{"bad json", http.MethodPost, `{"key":`, http.StatusBadRequest, "bad_request"},
		{"get", http.MethodGet, "", http.StatusMethodNotAllowed, "method_not_allowed"},
	}
	for i, tc := range cases {
		rec := f.do(apiRequest(tc.method, "/api/login", "198.51.100."+strconv.Itoa(i)+":1", true, tc.body, nil))
		if rec.Code != tc.status || (tc.code != "" && errorCode(t, rec) != tc.code) {
			t.Errorf("%s: %d %q", tc.name, rec.Code, rec.Body)
		}
	}
}

func TestLogout_ClearsCookie(t *testing.T) {
	f := newAuthFixture(t)
	c := onlyCookie(t, f.login(f.key, "192.0.2.50:1", true))
	rec := f.do(apiRequest(http.MethodPost, "/api/logout", "192.0.2.50:1", true, "{}", c))
	cleared := onlyCookie(t, rec)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"ok":true}` || cleared.Name != c.Name ||
		cleared.Value != "" || cleared.MaxAge >= 0 || !cleared.Secure || cleared.Path != "/" {
		t.Fatalf("%d %q cookie %+v", rec.Code, rec.Body, cleared)
	}
	if f.reached.Load() != 0 {
		t.Fatal("logout reached the API")
	}
}

func TestLogin_RateLimitedWithRetryAfter(t *testing.T) {
	f := newAuthFixture(t)
	const remote = "203.0.113.7:1000"
	for range 10 {
		if rec := f.login(f.key, remote, true); rec.Code != http.StatusOK {
			t.Fatalf("successful logins must not count: %d", rec.Code)
		}
	}
	wrong, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	for range loginFailuresPerSource {
		if rec := f.login(wrong, remote, true); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong key: %d", rec.Code)
		}
	}
	f.clock.Advance(20 * time.Second)
	rec := f.login(f.key, remote, true)
	var body struct {
		Error      string `json:"error"`
		RetryAfter int    `json:"retry_after"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "40" ||
		body.Error != "rate_limited" || body.RetryAfter != 40 || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("%d %v %q", rec.Code, rec.Header(), rec.Body)
	}
	if rec := f.login(f.key, "203.0.113.8:1000", true); rec.Code != http.StatusOK {
		t.Fatalf("another source was limited: %d", rec.Code)
	}
	f.clock.Advance(40 * time.Second)
	if rec := f.login(f.key, remote, true); rec.Code != http.StatusOK {
		t.Fatalf("limit did not reset: %d", rec.Code)
	}
}

func TestAuth_ConcurrentLoginAndSessionChecks(t *testing.T) {
	f := newAuthFixture(t)
	c := onlyCookie(t, f.login(f.key, "192.0.2.99:1", true))
	wrong, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	var unexpected atomic.Int64
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Go(func() {
			remote := "203.0.113." + strconv.Itoa(g) + ":4000"
			for i := range 60 {
				var rec *httptest.ResponseRecorder
				var ok map[int]bool
				switch i % 3 {
				case 0:
					rec, ok = f.login(f.key, remote, true), map[int]bool{http.StatusOK: true, http.StatusTooManyRequests: true}
				case 1:
					rec, ok = f.login(wrong, remote, true), map[int]bool{http.StatusUnauthorized: true, http.StatusTooManyRequests: true}
				default:
					rec, ok = f.do(apiRequest(http.MethodGet, "/api/status", remote, true, "", c)), map[int]bool{http.StatusOK: true}
				}
				if !ok[rec.Code] {
					unexpected.Add(1)
				}
				if i%20 == 0 {
					f.clock.Advance(time.Second)
				}
			}
		})
	}
	wg.Wait()
	if n := unexpected.Load(); n != 0 {
		t.Fatalf("%d unexpected statuses", n)
	}
}
