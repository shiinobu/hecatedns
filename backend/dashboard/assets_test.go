package dashboard

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func assetsWith(extra fstest.MapFS) fstest.MapFS {
	fsys := testAssets()
	for name, file := range extra {
		fsys[name] = file
	}
	return fsys
}

func TestAssets_ServesEveryScriptAndStylesheetInTheRootFolder(t *testing.T) {
	s := newTestServer(t, func(o *Options) {
		o.Assets = assetsWith(fstest.MapFS{
			"api.js":    {Data: []byte("export const a = 1")},
			"views.js":  {Data: []byte("export const v = 2")},
			"theme.css": {Data: []byte("a{}")},
		})
	})
	tests := []struct{ path, body, contentType string }{
		{"/api.js", "export const a = 1", "text/javascript; charset=utf-8"},
		{"/views.js", "export const v = 2", "text/javascript; charset=utf-8"},
		{"/theme.css", "a{}", "text/css; charset=utf-8"},
		{"/app.js", "console.log(1)", "text/javascript; charset=utf-8"},
	}
	for _, tt := range tests {
		rec := serve(s, httptest.NewRequest(http.MethodGet, "http://"+testHost+tt.path, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != tt.body || rec.Header().Get("Content-Type") != tt.contentType {
			t.Errorf("%s = %d %q %q", tt.path, rec.Code, rec.Body, rec.Header().Get("Content-Type"))
		}
	}
}

func TestAssets_OnlyScriptsStylesheetsAndTheIndexAreServed(t *testing.T) {
	s := newTestServer(t, func(o *Options) {
		o.Assets = assetsWith(fstest.MapFS{
			"notes.txt":     {Data: []byte("private")},
			"app.js.map":    {Data: []byte("{}")},
			".env":          {Data: []byte("KEY=1")},
			"sub/inner.js":  {Data: []byte("x")},
			"other.html":    {Data: []byte("<p>")},
			"Upper Case.js": {Data: []byte("x")},
		})
	})
	for _, path := range []string{"/notes.txt", "/app.js.map", "/.env", "/sub/inner.js", "/other.html", "/index.html", "/Upper%20Case.js", "/inner.js"} {
		rec := serve(s, httptest.NewRequest(http.MethodGet, "http://"+testHost+path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
}

func TestAssets_TooManyFilesAreRefused(t *testing.T) {
	extra := fstest.MapFS{}
	for i := range maxAssetFiles + 1 {
		extra[fmt.Sprintf("m%d.js", i)] = &fstest.MapFile{Data: []byte("x")}
	}

	_, err := New(Options{Listen: loopback(), Assets: assetsWith(extra)})

	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("err = %v, want a too many files error", err)
	}
}

func TestAssets_AnOversizedFileIsRefused(t *testing.T) {
	big := fstest.MapFS{"big.js": {Data: make([]byte, maxAssetBytes+1)}}

	_, err := New(Options{Listen: loopback(), Assets: assetsWith(big)})

	if err == nil || !strings.Contains(err.Error(), "big.js") {
		t.Fatalf("err = %v, want an error naming the file", err)
	}
}
