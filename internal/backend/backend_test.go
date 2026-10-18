package backend

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubBackend struct{ name string }

func (s stubBackend) Name() string                  { return s.name }
func (s stubBackend) Run(ctx context.Context) error { <-ctx.Done(); return nil }

func TestRegistryNew(t *testing.T) {
	errBuild := errors.New("bad config")
	registry := Registry{
		"zeta":    func(Deps) (Backend, error) { return stubBackend{name: "zeta"}, nil },
		"alpha":   func(Deps) (Backend, error) { return stubBackend{name: "alpha"}, nil },
		"broken":  func(Deps) (Backend, error) { return nil, errBuild },
		"nothing": func(Deps) (Backend, error) { return nil, nil },
		"nilfunc": nil,
	}
	tests := []struct {
		name     string
		backend  string
		wantName string
		wantErr  []string
		wantIs   error
	}{
		{"known backend", "alpha", "alpha", nil, nil},
		{"unknown backend lists the known names sorted", "native", "", []string{`"native"`, "alpha, broken, nilfunc, nothing, zeta"}, nil},
		{"factory error is wrapped", "broken", "", []string{"broken", "bad config"}, errBuild},
		{"factory without backend", "nothing", "", []string{"nothing", "no backend"}, nil},
		{"nil factory", "nilfunc", "", []string{"nilfunc"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := registry.New(tt.backend, Deps{})
			if tt.wantErr == nil {
				if err != nil || b == nil || b.Name() != tt.wantName {
					t.Fatalf("New(%q) = %v, %v; want backend %q", tt.backend, b, err, tt.wantName)
				}
				return
			}
			if err == nil || b != nil {
				t.Fatalf("New(%q) = %v, %v; want an error", tt.backend, b, err)
			}
			for _, part := range tt.wantErr {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("error %q does not contain %q", err, part)
				}
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("error %q does not wrap %v", err, tt.wantIs)
			}
		})
	}
}

func TestRegistryNames(t *testing.T) {
	var empty Registry
	if got := empty.Names(); len(got) != 0 {
		t.Fatalf("empty registry Names() = %v, want none", got)
	}
	if _, err := empty.New("dnsproxy", Deps{}); err == nil || !strings.Contains(err.Error(), "none") {
		t.Fatalf("empty registry New error = %v, want one that says no backend is known", err)
	}
}
