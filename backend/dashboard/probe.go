package dashboard

import (
	"net/http"
	"strings"
)

const (
	probePath        = "/probe"
	probeSampleLabel = "probe"
)

func (s *Server) isProbeName(name string) bool {
	label, base, found := strings.Cut(name, ".")
	if !found {
		return false
	}
	_, known := s.probes[base]
	return known && validLabel(label)
}

func (s *Server) isProbeRequest(r *http.Request, host string) bool {
	if r.Method != http.MethodGet || r.URL.Path != probePath {
		return false
	}
	name, port, hasPort := strings.Cut(host, ":")
	if hasPort {
		if _, bound := s.portText[port]; !bound {
			return false
		}
	}
	return s.isProbeName(name)
}

func (s *Server) serveProbe(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
