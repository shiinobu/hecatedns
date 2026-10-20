package dashboard

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultSessionDays = 30
	sessionCookieTLS   = "__Host-hecate_session"
	sessionCookiePlain = "hecate_session"
	sessionMACVersion  = "v1"
	loginPath          = "/api/login"
	logoutPath         = "/api/logout"
	day                = 24 * time.Hour
)

type AuthOptions struct {
	KeyHashFile string
	SecretFile  string
	SessionDays int
	Log         *slog.Logger
	Now         func() time.Time
}

type Auth struct {
	keys     *keyHashFile
	secret   []byte
	lifetime time.Duration
	limiter  *loginLimiter
	log      *slog.Logger
	now      func() time.Time
}

type loginRequest struct {
	Key string `json:"key"`
}

type loginResponse struct {
	OK      bool   `json:"ok"`
	Expires string `json:"expires"`
}

type okResponse struct {
	OK bool `json:"ok"`
}

type rateLimitedError struct {
	apiError
	RetryAfter int `json:"retry_after"`
}

func NewAuth(o AuthOptions) (*Auth, error) {
	if o.KeyHashFile == "" {
		return nil, errors.New("dashboard: the key hash file is not set")
	}
	secret, err := loadSecret(o.SecretFile)
	if err != nil {
		return nil, err
	}
	if o.SessionDays <= 0 {
		o.SessionDays = defaultSessionDays
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	a := &Auth{
		keys:     newKeyHashFile(o.KeyHashFile, o.Log),
		secret:   secret,
		lifetime: time.Duration(o.SessionDays) * day,
		limiter:  newLoginLimiter(loginFailuresPerSource, loginFailuresOverall, maxTrackedLoginSources, loginFailureWindow),
		log:      o.Log,
		now:      o.Now,
	}
	a.keys.current()
	return a, nil
}

func (a *Auth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == loginPath {
			a.login(w, r)
			return
		}
		if !a.validSession(r) {
			a.writeJSON(w, http.StatusUnauthorized, apiError{Error: "unauthorized", Message: "Sign in required."})
			return
		}
		if r.URL.Path == logoutPath {
			a.logout(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Auth) login(w http.ResponseWriter, r *http.Request) {
	if !a.requirePost(w, r) {
		return
	}
	now := a.now()
	source := sourceKey(r.RemoteAddr)
	if wait := a.limiter.retryAfter(source, now); wait > 0 {
		seconds := int(math.Ceil(wait.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		a.writeJSON(w, http.StatusTooManyRequests, rateLimitedError{
			apiError:   apiError{Error: "rate_limited", Message: "Too many failed sign-in attempts. Try again later."},
			RetryAfter: seconds,
		})
		return
	}
	var req loginRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, defaultMaxBodyBytes)).Decode(&req); err != nil {
		a.limiter.fail(source, now)
		a.writeJSON(w, http.StatusBadRequest, apiError{Error: "bad_request", Message: "The request body is not valid JSON."})
		return
	}
	keyHash, ok := a.matchKey(req.Key)
	if !ok {
		a.limiter.fail(source, now)
		a.writeJSON(w, http.StatusUnauthorized, apiError{Error: "bad_key", Message: "The access key is not valid."})
		return
	}
	expiry := now.Add(a.lifetime).Unix()
	a.setCookie(w, r, a.cookieValue(expiry, keyHash), int(a.lifetime/time.Second))
	a.writeJSON(w, http.StatusOK, loginResponse{OK: true, Expires: time.Unix(expiry, 0).In(now.Location()).Format(time.RFC3339)})
}

func (a *Auth) matchKey(key string) (string, bool) {
	stored := a.keys.current()
	normalized, err := NormalizeKey(key)
	if err != nil || stored == "" {
		return "", false
	}
	return stored, subtle.ConstantTimeCompare([]byte(HashKey(normalized)), []byte(stored)) == 1
}

func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	if !a.requirePost(w, r) {
		return
	}
	a.setCookie(w, r, "", -1)
	a.writeJSON(w, http.StatusOK, okResponse{OK: true})
}

func (a *Auth) requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPost {
		return true
	}
	w.Header().Set("Allow", http.MethodPost)
	a.writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method_not_allowed", Message: "Method not allowed."})
	return false
}

func cookieName(r *http.Request) string {
	if r.TLS != nil {
		return sessionCookieTLS
	}
	return sessionCookiePlain
}

func (a *Auth) setCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(r),
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   r.TLS != nil,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *Auth) cookieValue(expiry int64, keyHash string) string {
	expText := strconv.FormatInt(expiry, 10)
	return expText + "." + hex.EncodeToString(a.sign(expText, keyHash))
}

func (a *Auth) sign(expText, keyHash string) []byte {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(sessionMACVersion + "|" + expText + "|" + keyHash))
	return mac.Sum(nil)
}

func (a *Auth) validSession(r *http.Request) bool {
	c, err := r.Cookie(cookieName(r))
	if err != nil {
		return false
	}
	keyHash := a.keys.current()
	if keyHash == "" {
		return false
	}
	expText, macText, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	expiry, err := strconv.ParseInt(expText, 10, 64)
	if err != nil || a.now().Unix() >= expiry {
		return false
	}
	mac, err := hex.DecodeString(macText)
	if err != nil || len(mac) != sha256.Size {
		return false
	}
	return hmac.Equal(mac, a.sign(expText, keyHash))
}

func (a *Auth) writeJSON(w http.ResponseWriter, status int, v any) {
	writeJSON(a.log, w, status, v)
}
