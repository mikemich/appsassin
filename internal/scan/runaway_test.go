package scan

import (
	"strings"
	"testing"
	"time"

	"github.com/mikemich/appsassin/internal/config"
)

var rule = config.Runaway{CPU: 90, For: "10m", Memory: "4G"}

// sim feeds a watchdog synthetic samples of one process, the way a scan would.
type sim struct {
	w   *Watchdog
	r   config.Runaway
	now time.Time
	p   *Proc
}

func newSim(age, cpuTime time.Duration, rss int64) *sim {
	s := &sim{w: NewWatchdog(), r: rule, now: time.Unix(1_800_000_000, 0),
		p: &Proc{PID: 4242, Elapsed: age, CPUTime: cpuTime, RSS: rss}}
	s.sample()
	return s
}

func (s *sim) sample() { s.w.Observe(map[int]*Proc{s.p.PID: s.p}, s.now, s.r) }

// run advances d in steps, burning cpu % of a core and growing by grow bytes per step.
func (s *sim) run(d, step time.Duration, cpu float64, grow int64) {
	for el := time.Duration(0); el < d; el += step {
		s.now = s.now.Add(step)
		s.p.Elapsed += step
		s.p.CPUTime += time.Duration(float64(step) * cpu / 100)
		s.p.RSS += grow
		s.sample()
	}
}

func (s *sim) check() Verdict { return s.w.Check(s.p, s.now, s.r) }

func TestSustainedCPU(t *testing.T) {
	s := newSim(time.Hour, time.Minute, 50<<20) // an hour old, mostly idle so far
	s.run(9*time.Minute+40*time.Second, 20*time.Second, 99, 0)
	if v := s.check(); v.Runaway() {
		t.Fatalf("runaway after only %s over the line: %+v", v.Over, v)
	}
	s.run(4*time.Minute, 20*time.Second, 99, 0)
	v := s.check()
	if !v.CPUHit || v.MemHit {
		t.Fatalf("not a CPU runaway after 13m40s at 99%%: %+v", v)
	}
	if want := "runaway: 99% CPU for 13m"; v.Reason() != want {
		t.Errorf("reason %q, want %q", v.Reason(), want)
	}
}

func TestSpikeIsNoise(t *testing.T) {
	s := newSim(time.Hour, time.Minute, 50<<20)
	for range 5 {
		s.run(3*time.Minute, 3*time.Second, 100, 0) // TUI-rate samples
		s.run(time.Minute, 3*time.Second, 2, 0)
	}
	if v := s.check(); v.Runaway() || v.Over != 0 {
		t.Fatalf("bursty process looks sustained: %+v", v)
	}
}

func TestShortDipForgiven(t *testing.T) {
	s := newSim(time.Hour, time.Minute, 50<<20)
	s.run(5*time.Minute, 20*time.Second, 98, 0)
	s.run(20*time.Second, 20*time.Second, 10, 0) // one quiet sample: a GC pause, some I/O
	s.run(5*time.Minute, 20*time.Second, 98, 0)
	if v := s.check(); !v.CPUHit || v.Over < 10*time.Minute {
		t.Fatalf("a 20s dip broke the stretch: %+v", v)
	}

	s = newSim(time.Hour, time.Minute, 50<<20)
	s.run(5*time.Minute, 20*time.Second, 98, 0)
	s.run(time.Minute, 20*time.Second, 10, 0) // properly calmed down
	s.run(5*time.Minute, 20*time.Second, 98, 0)
	if v := s.check(); v.CPUHit || v.Over > 6*time.Minute {
		t.Fatalf("a 1m dip didn't restart the stretch: %+v", v)
	}
}

func TestDipInProgressIsNotOverTheLine(t *testing.T) {
	s := newSim(time.Hour, time.Minute, 50<<20)
	s.run(12*time.Minute, 20*time.Second, 98, 0)
	s.run(20*time.Second, 20*time.Second, 0, 0)
	if v := s.check(); v.CPUHit {
		t.Fatalf("counted as over the line while it's idle: %+v", v)
	}
}

func TestLifetimeAverageOnFirstSight(t *testing.T) {
	s := newSim(14*time.Minute, 13*time.Minute+50*time.Second, 1<<20) // `yes` left running
	v := s.check()
	if !v.CPUHit {
		t.Fatalf("a process that has burned a core since it started isn't a runaway: %+v", v)
	}
	if !strings.HasPrefix(v.Reason(), "runaway: 99% CPU for 14m") {
		t.Errorf("reason %q", v.Reason())
	}

	s = newSim(3*24*time.Hour, 2*time.Hour, 1<<20) // old, mostly idle
	if v := s.check(); v.Over != 0 {
		t.Fatalf("idle process judged busy from its lifetime: %+v", v)
	}
}

func TestPIDReuseStartsOver(t *testing.T) {
	s := newSim(time.Hour, time.Minute, 50<<20)
	s.run(12*time.Minute, 20*time.Second, 99, 0)
	if !s.check().CPUHit {
		t.Fatal("setup: expected a runaway")
	}
	// Same PID, new process started a moment ago.
	s.now = s.now.Add(20 * time.Second)
	s.p.Elapsed, s.p.CPUTime = 5*time.Second, 0
	s.sample()
	if v := s.check(); v.Runaway() || v.Over != 0 {
		t.Fatalf("reused PID inherited the old process's history: %+v", v)
	}
}

func TestStartJitterIsSameProcess(t *testing.T) {
	s := newSim(time.Hour, time.Minute, 50<<20)
	s.run(6*time.Minute, 20*time.Second, 99, 0)
	// ps truncates elapsed time to the second, so the derived start wobbles.
	s.now = s.now.Add(20*time.Second + 1200*time.Millisecond)
	s.p.Elapsed += 20 * time.Second
	s.p.CPUTime += 20 * time.Second
	s.sample()
	s.run(5*time.Minute, 20*time.Second, 99, 0)
	if v := s.check(); !v.CPUHit {
		t.Fatalf("start-time jitter reset the history: %+v", v)
	}
}

func TestMemoryGrowing(t *testing.T) {
	s := newSim(time.Hour, time.Minute, 4608<<20) // 4.5G, over the 4G line
	s.run(9*time.Minute, 20*time.Second, 5, 30<<20)
	if v := s.check(); v.Runaway() {
		t.Fatalf("memory runaway before a full window: %+v", v)
	}
	s.run(3*time.Minute, 20*time.Second, 5, 30<<20)
	v := s.check()
	if !v.MemHit || v.CPUHit {
		t.Fatalf("not a memory runaway after 12m growing over the line: %+v", v)
	}
	if v.MemFor < 10*time.Minute || v.MemFor > 12*time.Minute {
		t.Errorf("window %s, want 10–12m", v.MemFor)
	}
	if !strings.Contains(v.Reason(), "and growing (+") {
		t.Errorf("reason %q", v.Reason())
	}
}

func TestMemoryBigButFlat(t *testing.T) {
	s := newSim(time.Hour, time.Minute, 6<<30)
	s.run(30*time.Minute, 20*time.Second, 5, 0)
	if v := s.check(); v.Runaway() {
		t.Fatalf("a big, steady process is a runaway: %+v", v)
	}
	// It grew a lot once, long ago, then stopped: only the last window counts.
	s = newSim(time.Hour, time.Minute, 4200<<20)
	s.run(5*time.Minute, 20*time.Second, 5, 100<<20)
	s.run(15*time.Minute, 20*time.Second, 5, 0)
	if v := s.check(); v.MemHit {
		t.Fatalf("growth from 15m ago still counts: %+v", v)
	}
}

func TestMemoryDipResets(t *testing.T) {
	s := newSim(time.Hour, time.Minute, 4608<<20)
	s.run(6*time.Minute, 20*time.Second, 5, 20<<20)
	s.p.RSS = 3 << 30 // freed a lot
	s.run(20*time.Second, 20*time.Second, 5, 0)
	s.p.RSS = 4608 << 20
	s.run(6*time.Minute, 20*time.Second, 5, 20<<20)
	if v := s.check(); v.MemHit {
		t.Fatalf("history survived a dip under the line: %+v", v)
	}
}

func TestMemoryGapForgets(t *testing.T) {
	s := newSim(time.Hour, time.Minute, 4608<<20)
	s.run(5*time.Minute, 20*time.Second, 5, 20<<20)
	s.run(20*time.Minute, 20*time.Minute, 5, 200<<20) // nobody looked for 20m
	if v := s.check(); v.MemHit {
		t.Fatalf("unwatched gap counted as over the line: %+v", v)
	}
}

func TestLongCPUGapUsesAverage(t *testing.T) {
	// CPU time keeps counting while nobody samples, so a gap is still measured.
	s := newSim(time.Hour, time.Minute, 50<<20)
	s.run(15*time.Minute, 15*time.Minute, 97, 0)
	if v := s.check(); !v.CPUHit {
		t.Fatalf("15m at 97%% between two samples isn't a runaway: %+v", v)
	}
	// A sleeping Mac's wall clock runs on while CPU time doesn't.
	s.run(time.Hour, time.Hour, 0, 0)
	if v := s.check(); v.Over != 0 {
		t.Fatalf("stretch survived an idle hour: %+v", v)
	}
}

func TestRulesOff(t *testing.T) {
	s := newSim(time.Hour, time.Minute, 8<<30)
	s.r = config.Runaway{For: "10m"}
	s.run(20*time.Minute, 20*time.Second, 100, 50<<20)
	if v := s.check(); v.Runaway() {
		t.Fatalf("runaway with both checks off: %+v", v)
	}
}

func TestGoneProcessesForgotten(t *testing.T) {
	s := newSim(time.Hour, time.Minute, 50<<20)
	s.now = s.now.Add(20 * time.Second)
	s.w.Observe(map[int]*Proc{}, s.now, s.r)
	if s.w.Seen() {
		t.Fatal("history kept for a process that's gone")
	}
}

func TestSaveLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newSim(time.Hour, time.Minute, 50<<20)
	s.w = LoadWatchdog()
	s.sample()
	s.run(11*time.Minute, 20*time.Second, 99, 0)
	if err := s.w.Save(); err != nil {
		t.Fatal(err)
	}
	s.w = LoadWatchdog()
	if v := s.check(); !v.CPUHit {
		t.Fatalf("history lost across save/load: %+v", v)
	}
}

func TestSparedWithAncestors(t *testing.T) {
	r := config.Runaway{Ignore: []string{"cargo", "ffmpeg"}}
	procs := map[int]*Proc{
		100: {PID: 100, PPID: 1, Comm: "/Users/you/.cargo/bin/cargo"},
		101: {PID: 101, PPID: 100, Comm: "/Users/you/.rustup/toolchains/stable/bin/rustc"},
		200: {PID: 200, PPID: 1, Comm: "/bin/zsh"},
		201: {PID: 201, PPID: 200, Comm: "/usr/bin/yes"},
		300: {PID: 300, PPID: 1, Comm: "/opt/homebrew/bin/ffmpeg"},
	}
	for pid, want := range map[int]bool{100: true, 101: true, 201: false, 300: true} {
		if got := runawaySpared(r, procs, procs[pid]); got != want {
			t.Errorf("pid %d (%s): spared=%v, want %v", pid, procs[pid].Comm, got, want)
		}
	}
}
