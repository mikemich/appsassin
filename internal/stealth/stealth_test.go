package stealth

import (
	"testing"
	"time"

	"github.com/mikemich/appsassin/internal/config"
	"github.com/mikemich/appsassin/internal/scan"
)

func TestEligibleRunaways(t *testing.T) {
	now := time.Now()
	off, on := config.Default(), config.Default()
	on.Runaway.Kill = true
	runaway := "runaway: 99% CPU for 12m"
	cases := []struct {
		name string
		t    scan.Target
		cfg  config.Config
		want bool
	}{
		{"stray orphan, kill off", scan.Target{Kind: scan.KindOrphan, Score: 95, Runaway: runaway}, off, false},
		{"stray orphan, kill on", scan.Target{Kind: scan.KindOrphan, Score: 95, Runaway: runaway}, on, true},
		{"terminal process, kill off", scan.Target{Kind: scan.KindProc, Score: 50, Runaway: runaway}, off, false},
		{"terminal process, kill on", scan.Target{Kind: scan.KindProc, Score: 50, Runaway: runaway}, on, true},
		{"app with windows, kill on", scan.Target{Kind: scan.KindApp, Windows: 3, Score: 50, Runaway: runaway}, on, true},
		{"launchd job, kill on", scan.Target{Kind: scan.KindService, Score: 50, Runaway: runaway}, on, false},
		{"ordinary stray still goes by score", scan.Target{Kind: scan.KindOrphan, Score: 80}, off, true},
		{"ordinary process never", scan.Target{Kind: scan.KindProc, Score: 80}, on, false},
	}
	for _, c := range cases {
		if got := Eligible(&c.t, c.cfg, now, now.Add(-time.Hour), false); got != c.want {
			t.Errorf("%s: eligible=%v, want %v", c.name, got, c.want)
		}
	}
}
