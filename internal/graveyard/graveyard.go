// Package graveyard remembers everything appsassin killed, with enough
// detail to bring it back: any kill, including stealth mode's, can be undone.
package graveyard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/mikemich/appsassin/internal/config"
	"github.com/mikemich/appsassin/internal/scan"
)

// Max is how many kills the graveyard keeps; older ones are forgotten.
const Max = 500

// Entry is one kill.
type Entry struct {
	Killed     time.Time  `json:"killed"`
	By         string     `json:"by"` // tui, stealth or sweep
	Kind       scan.Kind  `json:"kind"`
	Name       string     `json:"name"`
	PID        int        `json:"pid"`
	Comm       string     `json:"comm"`
	Args       string     `json:"args,omitempty"` // command line as ps shows it
	Exe        string     `json:"exe,omitempty"`  // executable, as the kernel reports it
	Argv       []string   `json:"argv,omitempty"` // exact argument vector
	Cwd        string     `json:"cwd,omitempty"`
	Bundle     string     `json:"bundle,omitempty"`
	BundleID   string     `json:"bundleId,omitempty"`
	Ports      []int      `json:"ports,omitempty"`
	Mem        int64      `json:"mem,omitempty"`
	Score      int        `json:"score,omitempty"`
	Reasons    []string   `json:"reasons,omitempty"`
	Revived    *time.Time `json:"revived,omitempty"`
	RevivedPID int        `json:"revivedPid,omitempty"`
}

// What is a one-line description of what the entry was running.
func (e Entry) What() string {
	switch {
	case e.Kind.IsApp() && e.BundleID != "":
		return e.BundleID
	case e.Args != "":
		return e.Args
	case e.Bundle != "":
		return e.Bundle
	}
	return e.Comm
}

func (e Entry) same(o Entry) bool { return e.PID == o.PID && e.Killed.Equal(o.Killed) }

// Path is the graveyard file: JSON lines, oldest first.
func Path() string { return filepath.Join(config.SupportDir(), "graveyard.jsonl") }

// LogDir is where revived processes write their output.
func LogDir() string { return filepath.Join(filepath.Dir(config.LogPath()), "appsassin-revived") }

// FromTarget describes t without looking anything up.
func FromTarget(t *scan.Target, by string, at time.Time) Entry {
	return Entry{
		Killed: at, By: by, Kind: t.Kind, Name: t.Name, PID: t.PID, Comm: t.Comm, Args: t.Args,
		Bundle: t.Bundle, BundleID: t.BundleID, Ports: t.Ports, Mem: t.TreeRSS, Score: t.Score, Reasons: t.Reasons,
	}
}

// Capture records everything needed to revive t. Call it while t is still alive.
func Capture(t *scan.Target, by string) Entry {
	e := FromTarget(t, by, time.Now())
	e.Cwd = cwd(t.PID)
	e.Exe, e.Argv = procArgs(t.PID)
	return e
}

func cwd(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return line[1:]
		}
	}
	return ""
}

// procArgs reads the exact executable path and argv from the kernel.
// Layout of kern.procargs2: argc (int32), exec path, NUL padding, argv..., env...
func procArgs(pid int) (string, []string) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(b) < 4 {
		return "", nil
	}
	argc := int(binary.NativeEndian.Uint32(b))
	b = b[4:]
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return "", nil
	}
	exe := string(b[:i])
	b = bytes.TrimLeft(b[i:], "\x00")
	var argv []string
	for len(argv) < argc && len(b) > 0 {
		i := bytes.IndexByte(b, 0)
		if i < 0 {
			argv = append(argv, string(b))
			break
		}
		argv = append(argv, string(b[:i]))
		b = b[i+1:]
	}
	return exe, argv
}

func read() ([]Entry, error) {
	f, err := os.Open(Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var es []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			es = append(es, e)
		}
	}
	return es, sc.Err()
}

// update rewrites the graveyard under a lock, so the TUI and stealth mode
// can bury things at the same time.
func update(fn func([]Entry) []Entry) error {
	if err := os.MkdirAll(config.SupportDir(), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(Path()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	es, err := read()
	if err != nil {
		return err
	}
	es = fn(es)
	if len(es) > Max {
		es = es[len(es)-Max:]
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range es {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	// Command lines can carry tokens, so the file stays private.
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}

// Bury adds a kill to the graveyard.
func Bury(e Entry) error {
	return update(func(es []Entry) []Entry { return append(es, e) })
}

// Load returns the graveyard, newest first.
func Load() ([]Entry, error) {
	es, err := read()
	for i, j := 0, len(es)-1; i < j; i, j = i+1, j-1 {
		es[i], es[j] = es[j], es[i]
	}
	return es, err
}

// Find picks an entry by 1-based position (newest first) or by name.
// Names match the display name or bundle ID exactly, then as a substring;
// the most recent match wins.
func Find(es []Entry, q string) (Entry, error) {
	q = strings.TrimSpace(q)
	if len(es) == 0 {
		return Entry{}, errors.New("the graveyard is empty")
	}
	if n, err := strconv.Atoi(q); err == nil {
		if n < 1 || n > len(es) {
			return Entry{}, fmt.Errorf("no grave #%d (there are %d)", n, len(es))
		}
		return es[n-1], nil
	}
	lq := strings.ToLower(q)
	for _, e := range es {
		if strings.ToLower(e.Name) == lq || strings.ToLower(e.BundleID) == lq {
			return e, nil
		}
	}
	for _, e := range es {
		if strings.Contains(strings.ToLower(e.Name), lq) || strings.Contains(strings.ToLower(e.What()), lq) {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("nothing called %q in the graveyard", q)
}

// Revived reports PIDs brought back from the graveyard and when, so stealth
// mode doesn't immediately kill them again.
func Revived() map[int]time.Time {
	es, _ := read()
	out := map[int]time.Time{}
	for _, e := range es {
		if e.Revived != nil && e.RevivedPID > 0 {
			out[e.RevivedPID] = *e.Revived
		}
	}
	return out
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func quote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (e Entry) dir() string {
	if e.Cwd != "" {
		if st, err := os.Stat(e.Cwd); err == nil && st.IsDir() {
			return e.Cwd
		}
	}
	h, _ := os.UserHomeDir()
	return h
}

// Command is the shell equivalent of what Revive runs.
func Command(e Entry) string {
	dir := e.Cwd
	if dir == "" {
		dir = "~"
	}
	return command(e, dir)
}

func command(e Entry, dir string) string {
	if e.Kind.IsApp() {
		if e.BundleID != "" {
			return "open -b " + quote(e.BundleID)
		}
		return "open " + quote(e.Bundle)
	}
	run := e.Args
	if len(e.Argv) > 0 {
		q := make([]string, len(e.Argv))
		for i, a := range e.Argv {
			q[i] = quote(a)
		}
		run = strings.Join(q, " ")
	}
	if run == "" {
		return "" // nothing recorded to re-run; Revive refuses too
	}
	if dir != "~" {
		dir = quote(dir)
	}
	return "cd " + dir + " && " + run
}

// Revival is what Revive did.
type Revival struct {
	Cmd string // shell equivalent of what ran
	PID int    // new process (0 for apps, which LaunchServices starts)
	Log string // where a process's output goes
}

var unsafeName = regexp.MustCompile(`[^a-z0-9._-]+`)

// Revive relaunches e: apps through LaunchServices, processes by re-running
// their command line in their old working directory, detached, with output
// going to a log file. The entry is marked as revived.
func Revive(e Entry) (Revival, error) {
	rv := Revival{Cmd: command(e, e.dir())}
	if e.Kind.IsApp() {
		var out []byte
		var err error
		if e.BundleID != "" {
			out, err = exec.Command("open", "-b", e.BundleID).CombinedOutput()
		}
		if (e.BundleID == "" || err != nil) && e.Bundle != "" {
			rv.Cmd = "open " + quote(e.Bundle)
			out, err = exec.Command("open", e.Bundle).CombinedOutput()
		}
		if err != nil {
			return rv, fmt.Errorf("%s: %s", rv.Cmd, strings.TrimSpace(string(out)))
		}
	} else {
		var cmd *exec.Cmd
		switch {
		case len(e.Argv) > 0 && filepath.IsAbs(e.Exe):
			cmd = &exec.Cmd{Path: e.Exe, Args: e.Argv}
		case len(e.Argv) > 0:
			cmd = exec.Command(e.Argv[0], e.Argv[1:]...)
		case e.Args != "":
			// Old or partial record: ps output lost the original quoting.
			cmd = exec.Command("/bin/sh", "-c", e.Args)
		default:
			return rv, errors.New("no command line was recorded")
		}
		cmd.Dir = e.dir()
		if err := os.MkdirAll(LogDir(), 0o755); err != nil {
			return rv, err
		}
		slug := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(e.Name), "-"), "-")
		if len(slug) > 40 {
			slug = slug[:40]
		}
		rv.Log = filepath.Join(LogDir(), slug+"-"+time.Now().Format("20060102-150405")+".log")
		lf, err := os.OpenFile(rv.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return rv, err
		}
		defer lf.Close()
		cmd.Stdout, cmd.Stderr = lf, lf
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return rv, err
		}
		rv.PID = cmd.Process.Pid
		// Reap it if it exits while we're still around (the TUI); otherwise
		// launchd adopts it when we exit.
		go func() { _ = cmd.Wait() }()
	}
	now := time.Now()
	err := update(func(es []Entry) []Entry {
		for i := range es {
			if es[i].same(e) {
				es[i].Revived, es[i].RevivedPID = &now, rv.PID
			}
		}
		return es
	})
	return rv, err
}
