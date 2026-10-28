package graveyard

import (
	"testing"
	"time"
)

func TestLastBatch(t *testing.T) {
	now := time.Now()
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	revived := now
	es := []Entry{ // newest first
		{PID: 1, By: "stealth", Killed: at(0), Revived: &revived}, // already back: skipped
		{PID: 2, By: "stealth", Killed: at(5 * time.Second)},
		{PID: 3, By: "stealth", Killed: at(6 * time.Second)},
		{PID: 4, By: "stealth", Killed: at(50 * time.Second)},
		{PID: 5, By: "stealth", Killed: at(10 * time.Minute)}, // an earlier sweep
		{PID: 6, By: "tui", Killed: at(11 * time.Minute)},
	}
	got := LastBatch(es)
	if len(got) != 3 || got[0].PID != 2 || got[1].PID != 3 || got[2].PID != 4 {
		t.Fatalf("got %v, want pids 2 3 4", got)
	}

	// A different source ends the batch even if it's close in time.
	es[3].By = "tui"
	if got := LastBatch(es); len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}

	if got := LastBatch(nil); len(got) != 0 {
		t.Fatalf("empty graveyard gave %v", got)
	}
}
