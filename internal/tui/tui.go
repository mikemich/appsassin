// Package tui is the interactive hit list.
package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mikemich/appsassin/internal/config"
	"github.com/mikemich/appsassin/internal/reaper"
	"github.com/mikemich/appsassin/internal/scan"
	"github.com/mikemich/appsassin/internal/stealth"
)

const refreshEvery = 3 * time.Second

type sortMode int

const (
	sortScore sortMode = iota
	sortMem
	sortAge
	sortName
)

func (s sortMode) String() string {
	return [...]string{"score", "memory", "age", "name"}[s]
}

type mode int

const (
	modeList mode = iota
	modeFilter
	modeConfirm
	modeStealthConfirm
	modeHelp
)

type (
	scanMsg struct {
		res *scan.Result
		err error
	}
	stealthMsg struct {
		pid int
		on  bool
		err error
	}
	killMsg  []reaper.Result
	tickMsg  time.Time
	clearMsg int
)

type model struct {
	cfg     config.Config
	tracker *scan.Tracker

	res      *scan.Result
	rows     []*scan.Target
	cursor   int
	offset   int
	marked   map[int]bool
	scanning bool
	err      error

	minimal   bool
	threshold int
	showAll   bool
	sort      sortMode
	filter    string
	mode      mode

	pending []*scan.Target
	force   bool
	busy    bool

	status    string
	statusErr bool
	statusSeq int

	stealthPID int
	stealthOn  bool

	kills int
	freed int64

	demo bool         // fake data, simulated kills — for screenshots & try-outs
	gone map[int]bool // demo targets already "killed"

	width, height int
}

// Run starts the TUI.
func Run(cfg config.Config, minimal, demo bool) error {
	m := &model{
		cfg:       cfg,
		tracker:   scan.LoadTracker(),
		marked:    map[int]bool{},
		minimal:   minimal || cfg.Minimal,
		threshold: cfg.Threshold,
		width:     100,
		height:    30,
		demo:      demo,
		gone:      map[int]bool{},
	}
	if demo {
		m.threshold = 30
	} else {
		m.stealthPID, m.stealthOn = stealth.Running()
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if !demo {
		_ = m.tracker.Save()
	}
	return err
}

func (m *model) Init() tea.Cmd {
	m.scanning = true
	return tea.Batch(m.scanCmd(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) scanCmd() tea.Cmd {
	cfg, tr := m.cfg, m.tracker
	if m.demo {
		gone := map[int]bool{}
		for k, v := range m.gone {
			gone[k] = v
		}
		return func() tea.Msg { return scanMsg{scan.Demo(gone), nil} }
	}
	return func() tea.Msg {
		r, err := scan.Scan(scan.Options{Config: cfg, Tracker: tr})
		_ = tr.Save()
		return scanMsg{r, err}
	}
}

func stealthStatus() tea.Msg {
	pid, on := stealth.Running()
	return stealthMsg{pid: pid, on: on}
}

func (m *model) setStatus(s string, isErr bool) tea.Cmd {
	m.status, m.statusErr = s, isErr
	m.statusSeq++
	seq := m.statusSeq
	return tea.Tick(6*time.Second, func(time.Time) tea.Msg { return clearMsg(seq) })
}

// rebuild applies threshold, filter and sort, keeping the cursor on the same PID.
func (m *model) rebuild() {
	cur := -1
	if m.cursor < len(m.rows) {
		cur = m.rows[m.cursor].PID
	}
	m.rows = m.rows[:0]
	if m.res != nil {
		q := strings.ToLower(m.filter)
		for _, t := range m.res.Targets {
			if !m.showAll && t.Score < m.threshold {
				continue
			}
			if q != "" {
				hay := strings.ToLower(t.Name + " " + t.Comm + " " + t.Args + " " + string(t.Kind) + " " + strings.Join(t.Reasons, " ") + fmt.Sprint(" ", t.PID))
				if !strings.Contains(hay, q) {
					continue
				}
			}
			m.rows = append(m.rows, t)
		}
	}
	less := map[sortMode]func(a, b *scan.Target) bool{
		sortScore: func(a, b *scan.Target) bool { return a.Score > b.Score },
		sortMem:   func(a, b *scan.Target) bool { return a.TreeRSS > b.TreeRSS },
		sortAge:   func(a, b *scan.Target) bool { return a.Started.Before(b.Started) },
		sortName:  func(a, b *scan.Target) bool { return strings.ToLower(a.Name) < strings.ToLower(b.Name) },
	}[m.sort]
	sort.SliceStable(m.rows, func(i, j int) bool { return less(m.rows[i], m.rows[j]) })

	live := map[int]bool{}
	m.cursor = min(m.cursor, max(0, len(m.rows)-1))
	for i, t := range m.rows {
		live[t.PID] = true
		if t.PID == cur {
			m.cursor = i
		}
	}
	for pid := range m.marked {
		if !live[pid] {
			delete(m.marked, pid)
		}
	}
}

func (m *model) current() *scan.Target {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return m.rows[m.cursor]
	}
	return nil
}

func (m *model) selection() []*scan.Target {
	var out []*scan.Target
	for _, t := range m.rows {
		if m.marked[t.PID] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		if t := m.current(); t != nil {
			out = append(out, t)
		}
	}
	return out
}

func killCmd(ts []*scan.Target, force, demo bool) tea.Cmd {
	if demo {
		return func() tea.Msg {
			time.Sleep(700 * time.Millisecond)
			out := make([]reaper.Result, len(ts))
			for i, t := range ts {
				out[i] = reaper.Result{Target: t, OK: true, Msg: "terminated"}
			}
			return killMsg(out)
		}
	}
	return func() tea.Msg {
		out := make([]reaper.Result, len(ts))
		var wg sync.WaitGroup
		for i, t := range ts {
			wg.Add(1)
			go func() { defer wg.Done(); out[i] = reaper.Kill(t, force) }()
		}
		wg.Wait()
		return killMsg(out)
	}
}

func toggleStealth(on bool) tea.Cmd {
	return func() tea.Msg {
		var err error
		if on {
			err = stealth.Stop()
			time.Sleep(300 * time.Millisecond)
		} else {
			_, err = stealth.Start(false)
		}
		pid, running := stealth.Running()
		return stealthMsg{pid: pid, on: running, err: err}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		cmds := []tea.Cmd{tick()}
		if !m.demo {
			cmds = append(cmds, stealthStatus)
		}
		if !m.scanning && !m.busy {
			m.scanning = true
			cmds = append(cmds, m.scanCmd())
		}
		return m, tea.Batch(cmds...)

	case scanMsg:
		m.scanning = false
		m.err = msg.err
		if msg.err == nil {
			m.res = msg.res
			m.rebuild()
		}
		return m, nil

	case stealthMsg:
		m.stealthPID, m.stealthOn = msg.pid, msg.on
		if msg.err != nil {
			return m, m.setStatus("stealth: "+msg.err.Error(), true)
		}
		return m, nil

	case clearMsg:
		if int(msg) == m.statusSeq {
			m.status = ""
		}
		return m, nil

	case killMsg:
		m.busy = false
		var ok, bad []string
		var freed int64
		for _, r := range msg {
			if r.OK {
				ok = append(ok, r.Target.Name)
				freed += r.Target.TreeRSS
				delete(m.marked, r.Target.PID)
				m.gone[r.Target.PID] = true
			} else {
				bad = append(bad, r.Target.Name+": "+r.Msg)
			}
		}
		m.kills += len(ok)
		m.freed += freed
		s := ""
		if len(ok) > 0 {
			s = fmt.Sprintf("☠ %d down · %s freed", len(ok), scan.Bytes(freed))
			if m.demo {
				s += " (demo, nothing was harmed)"
			}
		}
		if len(bad) > 0 {
			if s != "" {
				s += " · "
			}
			s += "✗ " + strings.Join(bad, "; ")
		}
		m.scanning = true
		return m, tea.Batch(m.setStatus(s, len(bad) > 0), m.scanCmd())

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *model) listHeight() int {
	if m.minimal {
		return max(1, m.height-2)
	}
	return max(1, m.height-11)
}

func (m *model) move(d int) {
	m.cursor = max(0, min(len(m.rows)-1, m.cursor+d))
}

func (m *model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.mode {
	case modeHelp:
		m.mode = modeList
		return m, nil

	case modeConfirm:
		switch k.String() {
		case "y", "Y":
			m.mode = modeList
			m.busy = true
			n := len(m.pending)
			verb := "taking out"
			if m.force {
				verb = "force-killing"
			}
			m.status, m.statusErr = fmt.Sprintf("%s %d…", verb, n), false
			return m, killCmd(m.pending, m.force, m.demo)
		default:
			m.mode = modeList
			m.pending = nil
			return m, m.setStatus("stand down", false)
		}

	case modeStealthConfirm:
		m.mode = modeList
		if k.String() != "y" && k.String() != "Y" {
			return m, m.setStatus("stealth stays off", false)
		}
		if m.demo {
			m.stealthOn = true
			return m, m.setStatus("◉ stealth mode engaged (demo)", false)
		}
		return m, toggleStealth(false)

	case modeFilter:
		switch k.Type {
		case tea.KeyEnter:
			m.mode = modeList
		case tea.KeyEsc:
			m.mode, m.filter = modeList, ""
		case tea.KeyBackspace:
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
		case tea.KeySpace:
			m.filter += " "
		case tea.KeyRunes:
			m.filter += string(k.Runes)
		}
		m.rebuild()
		return m, nil
	}

	switch k.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+u":
		m.move(-m.listHeight() / 2)
	case "pgdown", "ctrl+d":
		m.move(m.listHeight() / 2)
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = max(0, len(m.rows)-1)
	case " ":
		if t := m.current(); t != nil {
			if m.marked[t.PID] {
				delete(m.marked, t.PID)
			} else {
				m.marked[t.PID] = true
			}
			m.move(1)
		}
	case "a":
		all := len(m.marked) == len(m.rows)
		m.marked = map[int]bool{}
		if !all {
			for _, t := range m.rows {
				m.marked[t.PID] = true
			}
		}
	case "esc":
		m.marked = map[int]bool{}
		m.filter = ""
		m.rebuild()
	case "x", "K":
		if m.busy {
			return m, nil
		}
		m.pending = m.selection()
		if len(m.pending) == 0 {
			return m, nil
		}
		m.force = k.String() == "K"
		m.mode = modeConfirm
	case "i":
		if t := m.current(); t != nil {
			id := t.BundleID
			if id == "" {
				id = t.Name
				if strings.HasPrefix(t.Comm, "/") {
					id = filepath.Base(t.Comm)
				}
			}
			if m.demo {
				m.gone[t.PID] = true
				m.scanning = true
				return m, tea.Batch(m.setStatus("spared "+id+" (added to ignore list)", false), m.scanCmd())
			}
			m.cfg.AddIgnore(id)
			if err := m.cfg.Save(); err != nil {
				return m, m.setStatus("couldn't save config: "+err.Error(), true)
			}
			m.scanning = true
			return m, tea.Batch(m.setStatus("spared "+id+" (added to ignore list)", false), m.scanCmd())
		}
	case "/":
		m.mode = modeFilter
	case "s":
		m.sort = (m.sort + 1) % 4
		m.rebuild()
		return m, m.setStatus("sort: "+m.sort.String(), false)
	case "m":
		m.minimal = !m.minimal
	case "+", "=":
		m.threshold = min(95, m.threshold+5)
		m.rebuild()
	case "-", "_":
		m.threshold = max(5, m.threshold-5)
		m.rebuild()
	case "A":
		m.showAll = !m.showAll
		m.rebuild()
	case "S":
		if m.stealthOn {
			if m.demo {
				m.stealthOn = false
				return m, m.setStatus("○ stealth mode disengaged (demo)", false)
			}
			return m, toggleStealth(true)
		}
		m.mode = modeStealthConfirm
	case "r":
		if !m.scanning {
			m.scanning = true
			return m, m.scanCmd()
		}
	case "?":
		m.mode = modeHelp
	}
	return m, nil
}
