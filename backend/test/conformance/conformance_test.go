package conformance

import (
	"testing"

	"github.com/shiinobu/hecatedns/backend/core/dnsproxy"
	"github.com/shiinobu/hecatedns/backend/core/native"
)

func constructors() []Constructor {
	return []Constructor{
		{Name: dnsproxy.Name, Factory: dnsproxy.New},
		{Name: native.Name, Factory: native.New},
	}
}

func TestConformance(t *testing.T) {
	Run(t, constructors())
}
