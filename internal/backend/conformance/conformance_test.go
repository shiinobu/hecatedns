package conformance

import (
	"testing"

	"github.com/shiinobu/hecatedns/internal/backend/dnsproxybe"
	"github.com/shiinobu/hecatedns/internal/backend/native"
)

// constructors lists every backend under test. Each must pass the same suite
// unchanged.
func constructors() []Constructor {
	return []Constructor{
		{Name: dnsproxybe.Name, Factory: dnsproxybe.New},
		{Name: native.Name, Factory: native.New},
	}
}

func TestConformance(t *testing.T) {
	Run(t, constructors())
}
