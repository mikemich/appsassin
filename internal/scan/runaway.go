package scan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mikemich/appsassin/internal/config"
)

const (
	runawayPts = 50

	startSlack = 2               // seconds; ps reports elapsed time to the second
	minGap     = 1000            // ms between samples worth measuring
	dipGrace   = 30 * 1000       // ms below the CPU line before a stretch counts as over
	memGap     = 5 * 60 * 1000   // ms without a sample before memory history is forgotten
	minGrowth  = int64(64 << 20) // growth over a window that counts as "growing"
	maxDepth   = 64              // ancestor walk limit, in case of a ps race
	usageFile  = "usage.json"
)

// Watchdog keeps a short usage history per process, so a runaway (stuck
// burning CPU, or swelling in memory) can be told from a busy moment: one
// sample is noise. Every scan feeds it, and stealth mode samples it every
// 20s. The history is shared through usage.json, so the TUI, the CLI and
// stealth mode all see the same stretches.
type Watchdog struct {
	mu    sync.Mutex
	path  string
	procs map[int]*usage
}

type memPoint struct {
	At  int64 `json:"t"` // unix ms
	RSS int64 `json:"r"`
}

// usage is one process's history. Times are unix ms, CPU times ms.
type usage struct {
	Start   int64 `json:"s"` // start time in unix seconds; with the PID, who this is
	At      int64 `json:"t"` // last sample
	CPUTime int64 `json:"c"` // cumulative CPU time at At
	RSS     int64 `json:"r"`

	OverSince int64 `json:"o,omitempty"`  // start of the current stretch over the CPU line
	OverCPU   int64 `json:"oc,omitempty"` // CPU time at OverSince
	LowSince  int64 `json:"l,omitempty"`  // start of a dip below the line, while it's short enough to forgive

	Mem []memPoint `json:"m,omitempty"` // RSS samples while over the memory line, oldest first
}

// NewWatchdog returns an empty watchdog that isn't saved anywhere.
func NewWatchdog() *Watchdog { return &Watchdog{procs: map[int]*usage{}} }

// LoadWatchdog picks up the history other appsassin processes saved.
func LoadWatchdog() *Watchdog {
	w := NewWatchdog()
	w.path = filepath.Join(config.SupportDir(), usageFile)
	if b, err := os.ReadFile(w.path); err == nil {
		var disk struct {
			Procs map[int]*usage `json:"procs"`
		}
		if json.Unmarshal(b, &disk) == nil && disk.Procs != nil {
			w.procs = disk.Procs
		}
	}
	return w
}

// Save writes the history for the next appsassin process. The last writer
// wins; every writer loaded the others' history when it started.
func (w *Watchdog) Save() error {
	if w.path == "" {
		return nil
	}
	w.mu.Lock()
	b, err := json.Marshal(struct {
		Procs map[int]*usage `json:"procs"`
	}{w.procs})
	w.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return err
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, w.path)
}

// Seen reports whether the watchdog has history for any process.
func (w *Watchdog) Seen() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.procs) > 0
}

func startOf(p *Proc, now time.Time) int64 { return now.Add(-p.Elapsed).Unix() }

func pct(cpu, wall int64) float64 { return float64(cpu) / float64(wall) * 100 }

// Observe records one sample of every process in procs, taken at now, and
// forgets processes that are gone.
//
// CPU is measured from the growth of each process's CPU time between
// samples, so the rate covers the whole interval rather than the moment ps
// ran. A stretch over the line survives dips shorter than 30s.
func (w *Watchdog) Observe(procs map[int]*Proc, now time.Time, r config.Runaway) {
	w.mu.Lock()
	defer w.mu.Unlock()
	at := now.UnixMilli()
	memLine := r.MemoryBytes()
	win := r.ForDur().Milliseconds()
	spacing := max(minGap, win/10)

	live := make(map[int]*usage, len(procs))
	for pid, p := range procs {
		start := startOf(p, now)
		cpu := p.CPUTime.Milliseconds()
		u := w.procs[pid]
		if u == nil || abs(u.Start-start) > startSlack {
			// New to us (or a reused PID). One sample can't tell a stretch from
			// a spike, but the lifetime average can: a process that has averaged
			// over the line since it started has been over it all along.
			u = &usage{Start: start, At: at, CPUTime: cpu, RSS: p.RSS}
			if life := p.Elapsed.Milliseconds(); r.CPU > 0 && life >= minGap && pct(cpu, life) >= r.CPU {
				u.OverSince = start * 1000
			}
			if memLine > 0 && p.RSS >= memLine {
				u.Mem = []memPoint{{at, p.RSS}}
			}
			live[pid] = u
			continue
		}
		live[pid] = u
		dt := at - u.At
		if dt < minGap {
			continue // too close to the last sample to measure anything
		}

		if r.CPU > 0 && pct(cpu-u.CPUTime, dt) >= r.CPU {
			if u.OverSince == 0 {
				u.OverSince, u.OverCPU = u.At, u.CPUTime
			}
			u.LowSince = 0
		} else if u.OverSince != 0 {
			if u.LowSince == 0 {
				u.LowSince = u.At
			}
			if at-u.LowSince >= dipGrace {
				u.OverSince, u.OverCPU, u.LowSince = 0, 0, 0
			}
		}

		if memLine > 0 && p.RSS >= memLine {
			if dt > memGap {
				u.Mem = nil // it may have dipped while nobody was looking
			}
			if n := len(u.Mem); n == 0 || at-u.Mem[n-1].At >= spacing {
				u.Mem = append(u.Mem, memPoint{at, p.RSS})
			}
			// Keep the newest point at least a window old, and everything after.
			i := 0
			for i+1 < len(u.Mem) && at-u.Mem[i+1].At >= win {
				i++
			}
			u.Mem = u.Mem[i:]
		} else {
			u.Mem = nil
		}
		u.At, u.CPUTime, u.RSS = at, cpu, p.RSS
	}
	w.procs = live
}

// mine keeps the current user's processes, the only ones appsassin judges.
func mine(procs map[int]*Proc) map[int]*Proc {
	me := os.Getuid()
	out := make(map[int]*Proc, len(procs)/2)
	for pid, p := range procs {
		if p.UID == me {
			out[pid] = p
		}
	}
	return out
}

// Sample takes a fresh sample of the user's processes without a full scan,
// and returns the PIDs that now look like runaways. The ignore lists aren't
// applied here; a scan does that.
func (w *Watchdog) Sample(now time.Time, r config.Runaway) ([]int, error) {
	procs, err := ListProcs()
	if err != nil {
		return nil, err
	}
	procs = mine(procs)
	w.Observe(procs, now, r)
	var out []int
	for _, p := range procs {
		if w.Check(p, now, r).Runaway() {
			out = append(out, p.PID)
		}
	}
	return out, nil
}

// Verdict is what the watchdog makes of one process as of its last sample.
type Verdict struct {
	Over   time.Duration // how long it has stayed over the CPU line
	AvgCPU float64       // average % CPU over that stretch
	CPUHit bool          // over the CPU line for the whole window, and still over it

	RSS    int64
	MemFor time.Duration // span of the memory history (≥ the window once it counts)
	Grown  int64         // growth over that span
	MemHit bool          // over the memory line for the whole window, and growing
}

func (v Verdict) Runaway() bool { return v.CPUHit || v.MemHit }

// Reason is the stray reason shown for a runaway, e.g. "runaway: 98% CPU for 14m".
func (v Verdict) Reason() string {
	var parts []string
	if v.CPUHit {
		parts = append(parts, fmt.Sprintf("%.0f%% CPU for %s", v.AvgCPU, Human(v.Over)))
	}
	if v.MemHit {
		parts = append(parts, fmt.Sprintf("%s and growing (+%s in %s)", Bytes(v.RSS), Bytes(v.Grown), Human(v.MemFor)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "runaway: " + strings.Join(parts, ", ")
}

// Check judges p against r using the history recorded so far.
func (w *Watchdog) Check(p *Proc, now time.Time, r config.Runaway) Verdict {
	w.mu.Lock()
	defer w.mu.Unlock()
	u := w.procs[p.PID]
	if u == nil || abs(u.Start-startOf(p, now)) > startSlack {
		return Verdict{}
	}
	win := r.ForDur()
	var v Verdict
	if u.OverSince != 0 && u.At > u.OverSince {
		v.Over = time.Duration(u.At-u.OverSince) * time.Millisecond
		v.AvgCPU = pct(u.CPUTime-u.OverCPU, u.At-u.OverSince)
		v.CPUHit = r.CPU > 0 && u.LowSince == 0 && v.Over >= win
	}
	if len(u.Mem) > 0 {
		v.RSS = u.RSS
		v.MemFor = time.Duration(u.At-u.Mem[0].At) * time.Millisecond
		v.Grown = u.RSS - u.Mem[0].RSS
		v.MemHit = r.MemoryBytes() > 0 && v.MemFor >= win && v.Grown >= minGrowth
	}
	return v
}

// runawaySpared reports whether p, or anything that started it, is on the
// runaway ignore list, so a build's compilers or an encode are left alone.
func runawaySpared(r config.Runaway, procs map[int]*Proc, p *Proc, ids ...string) bool {
	if r.Ignored(append(ids, p.Comm)...) {
		return true
	}
	for q, n := procs[p.PPID], 0; q != nil && q.PID > 1 && n < maxDepth; q, n = procs[q.PPID], n+1 {
		if r.Ignored(q.Comm, displayName(q)) {
			return true
		}
	}
	return false
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
