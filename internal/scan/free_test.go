package scan

import "testing"

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"2G": 2 << 30, "2g": 2 << 30, "2GB": 2 << 30, "2GiB": 2 << 30,
		"500M": 500 << 20, "500mb": 500 << 20, "1.5G": 3 << 29, "800": 800 << 20, " 64K ": 64 << 10,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "lots", "-1G", "0", "G"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) should fail", bad)
		}
	}
}

func target(pid, score int, mb int64, stray bool) *Target {
	return &Target{PID: pid, Score: score, TreeRSS: mb << 20, Stray: stray}
}

func pids(ts []*Target) []int {
	out := make([]int, len(ts))
	for i, t := range ts {
		out[i] = t.PID
	}
	return out
}

func TestPlanFree(t *testing.T) {
	ts := []*Target{
		target(1, 50, 900, true),
		target(2, 90, 100, true),
		target(3, 90, 300, true),
		target(4, 20, 4000, true),  // under minScore
		target(5, 99, 8000, false), // not a stray: never picked
		target(6, 70, 200, true),
	}

	// Highest score first, bigger first within a score, stop once covered.
	got, total := PlanFree(ts, 350<<20, 30)
	if want := []int{3, 2}; !equal(pids(got), want) || total != 400<<20 {
		t.Errorf("350M: picked %v (%d), want %v (400M)", pids(got), total, want)
	}

	// Not enough: take every eligible stray and report the shortfall.
	got, total = PlanFree(ts, 10<<30, 30)
	if want := []int{3, 2, 6, 1}; !equal(pids(got), want) || total != 1500<<20 {
		t.Errorf("10G: picked %v (%d), want %v (1500M)", pids(got), total, want)
	}

	if got, _ := PlanFree(ts, 1<<30, 95); len(got) != 0 {
		t.Errorf("minScore 95 should pick nothing, got %v", pids(got))
	}
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
