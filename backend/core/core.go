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

const (
	NetworkUDP = "udp"
	NetworkTCP = "tcp"
	NetworkTLS = "tls"
)

type Backend interface {
	Name() string

	Run(ctx context.Context) error
}

type Bound interface {
	Ready() <-chan struct{}

	Addr(network string) net.Addr
}

type Deps struct {
	Cfg    config.Config
	Engine *engine.Engine
	Log    *slog.Logger
}

type Factory func(Deps) (Backend, error)

type Registry map[string]Factory

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

func (r Registry) Names() []string {
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (r Registry) knownNames() string {
	if len(r) == 0 {
		return "none"
	}
	return strings.Join(r.Names(), ", ")
}
