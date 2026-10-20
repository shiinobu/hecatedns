package controller

import (
	"log/slog"
	"testing"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

func TestDashboardAPIOptions_KeepTheLifetimeCountsInTheDataDir(t *testing.T) {
	opts := dashboardAPIOptions(config.Config{DataDir: "state-dir"}, slog.New(slog.DiscardHandler))

	if opts.DataDir != "state-dir" {
		t.Errorf("DataDir = %q, want %q", opts.DataDir, "state-dir")
	}
}
