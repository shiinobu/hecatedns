package dashboard

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	dashboardHeader      = "X-Hecate-Dashboard"
	dashboardHeaderValue = "1"
	jsonMediaType        = "application/json"
	jsonContentType      = "application/json; charset=utf-8"
	retryAfterSeconds    = "1"
	hstsValue            = "max-age=31536000"
	apiPrefix            = "/api/"
	maxLoggedPath        = 256
	maxHostLength        = 253
	maxLabelLength       = 63
	minHostLabels        = 2
)

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (s *Server) routes() http.Handler {
	var h http.Handler = http.HandlerFunc(s.route)
	h = s.capBody(h)
	h = s.guardMutation(h)
	h = s.limitInFlight(h)
	h = s.securityHeaders(h)
	h = s.logRequests(h)
	return s.guardHost(h)
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, apiPrefix) {
		s.serveAsset(w, r)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", jsonContentType)
	if s.opts.API == nil {
		s.writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	s.opts.API.ServeHTTP(w, r)
}

func (s *Server) guardHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.hosts[strings.ToLower(r.Host)]; !ok {
			s.stats.unknownHost.Add(1)
			panic(http.ErrAbortHandler)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", s.csp)
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "")
		h.Set("X-Content-Type-Options", "nosniff")
		if s.tls {
			h.Set("Strict-Transport-Security", hstsValue)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) limitInFlight(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.inFlight <- struct{}{}:
			defer func() { <-s.inFlight }()
			next.ServeHTTP(w, r)
		default:
			s.stats.busy.Add(1)
			w.Header().Set("Retry-After", retryAfterSeconds)
			s.writeError(w, http.StatusServiceUnavailable, "busy", "the dashboard is busy, try again shortly")
		}
	})
}

func (s *Server) guardMutation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
		case r.Method != http.MethodPost:
			w.Header().Set("Allow", "GET, HEAD, POST")
			s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		case !isJSON(r.Header.Get("Content-Type")):
			s.writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "the body must be application/json")
			return
		case r.Header.Get(dashboardHeader) != dashboardHeaderValue:
			s.writeError(w, http.StatusForbidden, "forbidden", "the dashboard header is missing")
			return
		case !s.sameOrigin(r):
			s.writeError(w, http.StatusForbidden, "forbidden", "the request origin is not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isJSON(contentType string) bool {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != jsonMediaType {
		return false
	}
	for key, value := range params {
		if key != "charset" || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

func (s *Server) sameOrigin(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	scheme := "http://"
	if s.tls {
		scheme = "https://"
	}
	return len(origins) == 1 && strings.EqualFold(origins[0], scheme+r.Host)
}

func (s *Server) capBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := s.opts.MaxBodyBytes
		if r.ContentLength > limit {
			s.writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "the request body is too large")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "bad_request", "the request body could not be read")
			return
		}
		if int64(len(body)) > limit {
			s.writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "the request body is too large")
			return
		}
		capped := r.WithContext(r.Context())
		capped.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, capped)
	})
}

func (s *Server) writeError(w http.ResponseWriter, status int, code, message string) {
	writeAPIError(s.opts.Log, w, status, code, message)
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		path := r.URL.EscapedPath()
		if len(path) > maxLoggedPath {
			path = path[:maxLoggedPath]
		}
		s.opts.Log.Info("request", "method", r.Method, "path", path, "status", status, "bytes", sw.bytes,
			"duration_ms", time.Since(start).Milliseconds(), "remote", remoteIP(r.RemoteAddr))
	})
}

func remoteIP(remote string) string {
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		return "unknown"
	}
	return ap.Addr().Unmap().String()
}

func buildCSP(probeDomains []string, ports []uint16) string {
	connect := []string{"'self'"}
	for _, domain := range probeDomains {
		for _, port := range ports {
			source := "https://*." + domain
			if port != httpsPort {
				source += ":" + strconv.Itoa(int(port))
			}
			connect = append(connect, source)
		}
	}
	return "default-src 'none'; script-src 'self'; style-src 'self'; connect-src " + strings.Join(connect, " ") +
		"; img-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}

func validHostName(name string) bool {
	if len(name) == 0 || len(name) > maxHostLength {
		return false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) < minHostLabels {
		return false
	}
	for _, label := range labels {
		if !validLabel(label) {
			return false
		}
	}
	return true
}

func validLabel(label string) bool {
	if len(label) == 0 || len(label) > maxLabelLength || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
