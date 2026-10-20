package dashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
)

const (
	indexFile           = "index.html"
	htmlContentType     = "text/html; charset=utf-8"
	etagBytes           = 8
	assetCacheControl   = "max-age=86400, must-revalidate"
	faviconPath         = "/favicon.ico"
	faviconCacheControl = "max-age=86400"
	weakETagPrefix      = "W/"
	maxAssetFiles       = 16
	maxAssetBytes       = 1 << 20
)

var assetContentTypes = map[string]string{
	".js":  "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8",
}

type asset struct {
	body        []byte
	etag        string
	contentType string
}

type assetRoute struct {
	path, file, contentType string
}

func assetRoutes(entries []fs.DirEntry) []assetRoute {
	routes := []assetRoute{{path: "/", file: indexFile, contentType: htmlContentType}}
	for _, entry := range entries {
		name := entry.Name()
		if contentType, ok := assetContentTypes[path.Ext(name)]; ok && entry.Type().IsRegular() && validAssetName(name) {
			routes = append(routes, assetRoute{path: "/" + name, file: name, contentType: contentType})
		}
	}
	return routes
}

func validAssetName(name string) bool {
	if name[0] == '.' {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func loadAssets(fsys fs.FS) (map[string]asset, error) {
	out := make(map[string]asset)
	if fsys == nil {
		return out, nil
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("dashboard: reading the asset folder: %w", err)
	}
	routes := assetRoutes(entries)
	if len(routes) > maxAssetFiles+1 {
		return nil, fmt.Errorf("dashboard: too many asset files (more than %d scripts and stylesheets)", maxAssetFiles)
	}
	for _, rt := range routes {
		loaded, err := readAsset(fsys, rt)
		if err != nil {
			return nil, err
		}
		out[rt.path] = loaded
	}
	return out, nil
}

func readAsset(fsys fs.FS, rt assetRoute) (asset, error) {
	body, err := fs.ReadFile(fsys, rt.file)
	if err != nil {
		return asset{}, fmt.Errorf("dashboard: reading the asset %s: %w", rt.file, err)
	}
	if len(body) > maxAssetBytes {
		return asset{}, fmt.Errorf("dashboard: the asset %s is larger than %d bytes", rt.file, maxAssetBytes)
	}
	sum := sha256.Sum256(body)
	return asset{body: body, etag: `"` + hex.EncodeToString(sum[:etagBytes]) + `"`, contentType: rt.contentType}, nil
}

func (s *Server) serveFavicon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	w.Header().Set("Cache-Control", faviconCacheControl)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) {
	a, ok := s.assets[r.URL.Path]
	if !ok {
		s.writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	h := w.Header()
	h.Set("ETag", a.etag)
	h.Set("Cache-Control", assetCacheControl)
	if etagMatches(r.Header.Get("If-None-Match"), a.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", a.contentType)
	h.Set("Content-Length", strconv.Itoa(len(a.body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(a.body); err != nil {
		s.opts.Log.Debug("writing an asset failed", "err", err)
	}
}

func etagMatches(header, etag string) bool {
	for part := range strings.SplitSeq(header, ",") {
		candidate := strings.TrimPrefix(strings.TrimSpace(part), weakETagPrefix)
		if candidate == etag || candidate == "*" {
			return true
		}
	}
	return false
}
