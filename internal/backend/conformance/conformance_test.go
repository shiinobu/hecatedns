package conformance

import (
	"testing"

	"xdns/internal/backend/dnsproxybe"
)

// constructors lists every backend under test. The native backend joins in
// Phase 4 and must pass the same suite unchanged.
func constructors() []Constructor {
	return []Constructor{
		{Name: dnsproxybe.Name, Factory: dnsproxybe.New},
	}
}

func TestConformance(t *testing.T) {
	Run(t, constructors())
}
