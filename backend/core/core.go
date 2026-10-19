package core

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"

	"github.com/shiinobu/hecatedns/backend/engine"
	"github.com/shiinobu/hecatedns/backend/lib/config"
)

// Networks accepted by Bound.Addr.
const (
	NetworkUDP = "udp"
	NetworkTCP = "tcp"
)

// Backend is a DNS server implementation that serves clients through the
// engine.
type Backend interface {
	// Name returns the backend name used in the configuration.
	Name() string
	// Run serves until ctx ends (it then shuts down and returns nil, or the
	// shutdown error) or until a fatal error, which it returns. A backend is
	// run at most once.
	Run(ctx context.Context) error
}

// Bound is implemented by backends that can report where they listen. Tests
// use it to find a random port, and `hecate run` to report the address.
type Bound interface {
	// Ready is closed once the listeners are up, or once startup failed; in
	// that case Addr returns nil and Run returns the error.
	Ready() <-chan struct{}
	// Addr returns the first listen address for NetworkUDP or NetworkTCP, nil
	// if there is none (yet).
	Addr(network string) net.Addr
}

// Deps is what a backend needs to start.
type Deps struct {
	Cfg    config.Config
	Engine *engine.Engine
	Log    *slog.Logger
}

// Factory builds a backend. It validates the configuration and returns an
// error instead of a half-built backend.
type Factory func(Deps) (Backend, error)

// Registry maps backend names to factories. The caller (cmd/hecate, the
// conformance tests) builds it, so this package never imports the backend
// implementations and no import cycle can arise.
type Registry map[string]Factory

// New builds the backend called name. An unknown name is an error that lists
// the known names.
func (r Registry) New(name string, deps Deps) (Backend, error) {
	factory, ok := r[name]
	if !ok {
		return nil, fmt.Errorf("backend %q is unknown, known backends: %s", name, r.knownNames())
	}
	if factory == nil {
		return nil, fmt.Errorf("backend %q has no factory", name)
	}
	b, err := factory(deps)
	if err != nil {
		return nil, fmt.Errorf("backend %s: %w", name, err)
	}
	if b == nil {
		return nil, fmt.Errorf("backend %s: the factory returned no backend", name)
	}
	return b, nil
}

// Names returns the registered backend names, sorted.
func (r Registry) Names() []string {
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// knownNames returns the sorted names joined for an error message.
func (r Registry) knownNames() string {
	if len(r) == 0 {
		return "none"
	}
	return strings.Join(r.Names(), ", ")
}
