// Package scan finds stray apps and processes and scores how forgotten they look.
package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/mikemich/appsassin/internal/config"
	"github.com/mikemich/appsassin/internal/macos"
)

type Kind string

const (
	KindApp    Kind = "app"    // GUI app with a Dock icon
	KindHelper Kind = "helper" // helper process whose parent app has quit
	KindOrphan Kind = "orphan" // process whose parent died (closed terminal, crashed tool…)

	// Non-strays, only listed when scanning with Options.Everything.
	KindMenu    Kind = "menubar" // menu-bar or background-only app
	KindService Kind = "service" // launchd job (may be restarted after a kill)
	KindProc    Kind = "process" // anything else the user is running
)

// IsApp reports whether k is quit through NSRunningApplication rather than signals.
func (k Kind) IsApp() bool { return k == KindApp || k == KindMenu }

// Target is a candidate for assassination.
type Target struct {
	PID, PPID int
	Kind      Kind
	Name      string // human display name
	Comm      string // executable path / title, used to re-verify identity before killing
	Args      string
	Bundle    string // enclosing .app bundle, if any
	BundleID  string
	CPU       float64
	RSS       int64 // own resident memory
	TreeRSS   int64 // including descendants
	Started   time.Time
	Ports     []int
	Children  []int // descendant PIDs, killed alongside
	Windows   int
	OnScreen  int
	Hidden    bool
	LastUsed  time.Time // apps only
	UsedKnown bool      // LastUsed was observed rather than assumed from launch time
	TTY       string
	Score     int
	Stray     bool   // looks forgotten; false for things listed only by Options.Everything
	Runaway   string // why it counts as a runaway (stuck burning CPU or swelling), if it does
	Reasons   []string
}

// Key identifies the target across relaunches (for the usage tracker).
func (t *Target) Key() string {
	if t.Bundle != "" {
		return t.Bundle
	}
	return t.Comm
}

// Idle is how long an app has gone without being front-most.
func (t *Target) Idle(now time.Time) time.Duration { return now.Sub(t.LastUsed) }

type Result struct {
	Targets  []*Target       // scored > 0 (or everything, with Options.Everything), sorted by score desc
	All      map[int]*Target // with Options.Everything: every listed process, including ones folded into a parent
	Scanned  int             // processes examined
	FrontPID int
	At       time.Time
	Took     time.Duration
}

type Options struct {
	Config  config.Config
	Tracker *Tracker  // optional
	Watch   *Watchdog // optional; without it nothing is a runaway

	// Everything also lists apps and processes that aren't strays, so anything
	// the user runs can be found and killed. Strays keep their scores.
	Everything bool
}

var (
	systemPrefixes = []string{
		"/System/Library/", "/System/iOSSupport/", "/usr/libexec/", "/usr/sbin/", "/sbin/",
		"/Library/Apple/", "/System/Cryptexes/OS/",
	}
	protectedBundleIDs = map[string]bool{
		"com.apple.finder": true, "com.apple.dock": true, "com.apple.loginwindow": true,
		"com.apple.systemuiserver": true, "com.apple.controlcenter": true,
	}
	devRuntime   = regexp.MustCompile(`(?i)^(node|npm|npx|pnpm|yarn|bun|bunx|deno|python[0-9.]*|ruby|rails|bundle|java|gradle|mvn|php|go|cargo|vite|next|webpack|esbuild|tsc|tsx|ts-node|expo|metro|uvicorn|gunicorn|flask|jupyter.*|watchman|ngrok|cloudflared|redis-server|postgres|mysqld|mongod|opencode|claude|codex|gemini)$`)
	interpreters = regexp.MustCompile(`(?i)^(node|python[0-9.]*|ruby|bun|deno|java|php|perl|sh|bash|zsh)$`)
)

func isSystem(comm string) bool {
	for _, p := range systemPrefixes {
		if strings.HasPrefix(comm, p) {
			return true
		}
	}
	return false
}

// outerBundle returns the outermost "Foo.app" directory in path, or "".
func outerBundle(path string) string {
	if i := strings.Index(path, ".app/"); i >= 0 {
		return path[:i+4]
	}
	return ""
}

func appName(bundle string) string {
	return strings.TrimSuffix(filepath.Base(bundle), ".app")
}

func displayName(p *Proc) string {
	base := filepath.Base(p.Comm)
	if !interpreters.MatchString(base) {
		if !strings.HasPrefix(p.Comm, "/") {
			return p.Comm // process retitled itself, e.g. "npm exec expo start"
		}
		return base
	}
	args := strings.Fields(p.Args)
	var parts []string
	for _, a := range args[min(1, len(args)):] {
		if len(parts) == 3 {
			break
		}
		if strings.HasPrefix(a, "-") && len(parts) == 0 {
			continue
		}
		if strings.Contains(a, "/") {
			a = filepath.Base(a)
		}
		parts = append(parts, a)
	}
	if len(parts) == 0 {
		return base
	}
	return ansi.Truncate(base+" "+strings.Join(parts, " "), 48, "…")
}

func isDev(p *Proc, name string) bool {
	first, _, _ := strings.Cut(name, " ")
	return devRuntime.MatchString(filepath.Base(p.Comm)) || devRuntime.MatchString(first)
}

func Human(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func Bytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%dM", b>>20)
	default:
		return fmt.Sprintf("%dK", b>>10)
	}
}

// Scan inspects the system and returns scored targets.
func Scan(opt Options) (*Result, error) {
	start := time.Now()
	var (
		procs map[int]*Proc
		perr  error
		jobs  map[int]bool
		ports map[int][]int
		ws    macos.WindowStats
		front int
		wg    sync.WaitGroup
	)
	wg.Add(4)
	go func() { defer wg.Done(); procs, perr = ListProcs() }()
	go func() { defer wg.Done(); jobs = LaunchdJobs() }()
	go func() { defer wg.Done(); ports = ListeningPorts() }()
	go func() { defer wg.Done(); ws = macos.Windows(); front = macos.FrontPID() }()
	wg.Wait()
	if perr != nil {
		return nil, perr
	}

	now := time.Now()
	me := os.Getuid()
	selfExe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(selfExe); err == nil {
		selfExe = r
	}

	// Never touch ourselves or anything we're running inside (shell, terminal app…).
	protected := map[int]bool{}
	for pid := os.Getpid(); pid > 1; {
		protected[pid] = true
		p := procs[pid]
		if p == nil {
			break
		}
		pid = p.PPID
	}

	// …including the helpers of the app we run in (its main process is protected,
	// so they'd otherwise look like unrelated processes).
	protectedBundles := map[string]bool{}
	for pid := range protected {
		if p := procs[pid]; p != nil {
			if b := outerBundle(p.Comm); b != "" {
				protectedBundles[b] = true
			}
		}
	}

	children := map[int][]int{}
	liveBundles := map[string]bool{}
	for _, p := range procs {
		children[p.PPID] = append(children[p.PPID], p.PID)
		if b := outerBundle(p.Comm); b != "" {
			rest := strings.TrimPrefix(p.Comm, b+"/Contents/MacOS/")
			if rest != p.Comm && !strings.Contains(rest, "/") {
				liveBundles[b] = true
			}
		}
	}

	var frontInfo macos.AppInfo
	if front > 0 {
		frontInfo, _ = macos.App(front)
		if opt.Tracker != nil && frontInfo.BundlePath != "" {
			opt.Tracker.Touch(frontInfo.BundlePath, now)
		}
	}

	rule := opt.Config.Runaway
	watch := opt.Watch != nil && rule.Enabled()
	if watch {
		opt.Watch.Observe(mine(procs), now, rule)
	}

	var targets []*Target
	for _, p := range procs {
		if p.UID != me || p.PID <= 1 || protected[p.PID] || strings.Contains(p.Stat, "Z") {
			continue
		}
		if isSystem(p.Comm) || (selfExe != "" && p.Comm == selfExe) ||
			strings.Contains(p.Comm, ".appex/") || strings.Contains(p.Comm, ".xpc/") ||
			protectedBundles[outerBundle(p.Comm)] {
			continue // system-managed extensions & XPC services come and go on their own
		}
		t := &Target{
			PID: p.PID, PPID: p.PPID, Comm: p.Comm, Args: p.Args, CPU: p.CPU, RSS: p.RSS, TTY: p.TTY,
			Started: now.Add(-p.Elapsed), Ports: ports[p.PID], Bundle: outerBundle(p.Comm),
			Name: clean(displayName(p)), Stray: true,
		}
		score := 0
		add := func(n int, why string) {
			score += n
			if why != "" {
				t.Reasons = append(t.Reasons, why)
			}
		}

		var info macos.AppInfo
		isApp := false
		if t.Bundle != "" {
			info, isApp = macos.App(p.PID)
		}
		setApp := func(k Kind) {
			t.Kind = k
			t.Name = clean(info.Name)
			t.BundleID = info.BundleID
			if info.BundlePath != "" {
				t.Bundle = info.BundlePath
			}
			t.Hidden = info.Hidden
			t.Windows, t.OnScreen = ws.Total[p.PID], ws.OnScreen[p.PID]
		}

		dev := isDev(p, t.Name)
		switch {
		case isApp && info.Policy == macos.PolicyRegular:
			if protectedBundleIDs[info.BundleID] {
				continue
			}
			setApp(KindApp)
			if p.PID == front {
				t.Stray = false // the front app is in use by definition
				t.Reasons = []string{"in front"}
				break
			}
			t.LastUsed = t.Started
			if opt.Tracker != nil {
				if last, ok := opt.Tracker.Get(t.Bundle); ok && !last.Before(t.Started) {
					t.LastUsed, t.UsedKnown = last, true
				}
			}
			if t.Windows == 0 {
				add(40, "no windows")
			}
			if t.Hidden {
				add(10, "hidden")
			}
			idle := t.Idle(now)
			pts := 0
			switch {
			case idle > 24*time.Hour:
				pts = 25
			case idle > 6*time.Hour:
				pts = 15
			case idle > time.Hour:
				pts = 8
			}
			if t.UsedKnown {
				add(pts, "unused "+Human(idle))
			} else {
				add(pts/2, "untouched since launch? ("+Human(idle)+")")
			}
			if t.OnScreen > 0 {
				add(-15, "")
			}

		case isApp && !dev:
			setApp(KindMenu) // menu-bar agents & background-only apps exist on purpose
			t.Stray = false

		default:
			// Stray = adopted by launchd (its parent died) without being a launchd
			// job, and not attached to any terminal.
			// A dev runtime that merely ships inside an app (Python in Xcode.app)
			// isn't that app's helper.
			if p.PPID != 1 || p.TTY != "??" || jobs[p.PID] || (t.Bundle != "" && liveBundles[t.Bundle] && !dev) {
				if interactiveShell(p) {
					continue // a terminal tab, not something to hunt
				}
				t.Kind, t.Stray = KindProc, false
				if jobs[p.PID] {
					t.Kind = KindService
				}
				break
			}
			if t.Bundle != "" && !dev {
				t.Kind = KindHelper
				add(55, "outlived "+appName(t.Bundle))
			} else {
				t.Kind = KindOrphan
				add(45, "orphaned (parent exited)")
				if dev {
					add(15, "dev runtime")
				}
			}
		}

		ignored := opt.Config.Ignored(t.Name, p.Comm, t.BundleID, t.Bundle)
		if t.Stray && ignored {
			t.Stray, t.Reasons = false, []string{"spared (ignore list)"}
		}
		if watch && !ignored && !runawaySpared(rule, procs, p, t.Name, t.BundleID, t.Bundle) {
			t.Runaway = opt.Watch.Check(p, now, rule).Reason()
		}
		if !t.Stray {
			// Runaways are hunted even when their parent is alive and well.
			if t.Runaway == "" && !opt.Everything {
				continue
			}
			describe(t, p, jobs[p.PID])
			if t.Runaway != "" {
				t.Score = runawayPts
				t.Reasons = append([]string{t.Runaway}, t.Reasons...)
			}
			targets = append(targets, t)
			continue
		}

		if len(t.Ports) > 0 {
			add(10, "listening "+portList(t.Ports))
		}
		switch {
		case t.Runaway != "":
			add(runawayPts, "")
		case p.CPU >= 25:
			add(-20, fmt.Sprintf("busy %.0f%% CPU", p.CPU))
		case p.CPU < 0.5 && p.CPUTime < p.Elapsed/200: // <0.5% CPU over its whole life
			add(10, "idle")
		}
		if t.Kind != KindApp {
			switch {
			case p.Elapsed > 7*24*time.Hour:
				add(15, "running "+Human(p.Elapsed))
			case p.Elapsed > 24*time.Hour:
				add(10, "running "+Human(p.Elapsed))
			case p.Elapsed > 2*time.Hour:
				add(5, "")
			}
		}
		if t.Runaway != "" {
			t.Reasons = append([]string{t.Runaway}, t.Reasons...)
		}
		t.Score = score
		targets = append(targets, t)
	}

	// Gather descendants and drop targets already covered by an ancestor target.
	byPID := map[int]*Target{}
	for _, t := range targets {
		byPID[t.PID] = t
	}
	descend := func(t *Target) {
		t.TreeRSS = t.RSS
		queue := append([]int(nil), children[t.PID]...)
		for len(queue) > 0 {
			c := queue[0]
			queue = queue[1:]
			cp := procs[c]
			if cp == nil || cp.UID != me || protected[c] {
				continue
			}
			t.Children = append(t.Children, c)
			t.TreeRSS += cp.RSS
			queue = append(queue, children[c]...)
		}
	}
	var roots []*Target
	for _, t := range targets {
		covered := false
		for q := procs[t.PPID]; q != nil && q.PID > 1; q = procs[q.PPID] {
			if byPID[q.PID] != nil {
				covered = true
				break
			}
		}
		// A runaway gets its own row rather than hiding inside its app's.
		if covered && t.Runaway == "" {
			if opt.Everything {
				descend(t) // still findable by PID or port, e.g. by `appsassin kill`
			}
			continue
		}
		descend(t)
		mem := 0
		switch {
		case t.TreeRSS > 2<<30:
			mem = 20
		case t.TreeRSS > 500<<20:
			mem = 10
		case t.TreeRSS > 150<<20:
			mem = 5
		}
		if mem > 0 && t.Stray {
			t.Score += mem
			t.Reasons = append(t.Reasons, Bytes(t.TreeRSS)+" held")
		}
		t.Score = max(0, min(99, t.Score))
		if t.Score > 0 || opt.Everything {
			roots = append(roots, t)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].Score != roots[j].Score {
			return roots[i].Score > roots[j].Score
		}
		return roots[i].TreeRSS > roots[j].TreeRSS
	})

	res := &Result{Targets: roots, Scanned: len(procs), FrontPID: front, At: now, Took: time.Since(start)}
	if opt.Everything {
		res.All = byPID
	}
	return res, nil
}

var shells = map[string]bool{"zsh": true, "bash": true, "fish": true, "sh": true, "tcsh": true, "ksh": true, "nu": true, "login": true}

// interactiveShell is a shell sitting at a terminal prompt: killing it just closes a tab.
func interactiveShell(p *Proc) bool {
	return p.TTY != "??" && shells[strings.TrimPrefix(filepath.Base(p.Comm), "-")]
}

// clean drops invisible formatting characters some apps put in their names
// (WhatsApp's is "\u200eWhatsApp").
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

func portList(ports []int) string {
	ps := make([]string, len(ports))
	for i, port := range ports {
		ps[i] = fmt.Sprintf(":%d", port)
	}
	return strings.Join(ps, " ")
}

// describe fills Reasons for a non-stray with what's useful to know before killing it.
func describe(t *Target, p *Proc, launchdJob bool) {
	add := func(s string) { t.Reasons = append(t.Reasons, s) }
	if len(t.Ports) > 0 {
		add("listening " + portList(t.Ports))
	}
	if t.Kind.IsApp() && t.Windows > 0 {
		add(fmt.Sprintf("%d window%s", t.Windows, map[bool]string{true: "", false: "s"}[t.Windows == 1]))
	}
	if p.TTY != "??" {
		add("in terminal " + p.TTY)
	}
	if launchdJob && !t.Kind.IsApp() {
		add("launchd job, may restart")
	}
	if p.CPU >= 25 && t.Runaway == "" {
		add(fmt.Sprintf("busy %.0f%% CPU", p.CPU))
	}
}
