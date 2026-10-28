// Package stealth runs appsassin as a silent, low-priority background
// sweeper: no UI, no output, just a log of who got taken out.
package stealth

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mikemich/appsassin/internal/config"
	"github.com/mikemich/appsassin/internal/graveyard"
	"github.com/mikemich/appsassin/internal/macos"
	"github.com/mikemich/appsassin/internal/reaper"
	"github.com/mikemich/appsassin/internal/scan"
)

const agentLabel = "com.appsassin.stealth"

func pidFile() string { return filepath.Join(config.SupportDir(), "stealth.pid") }

func agentPath() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Library", "LaunchAgents", agentLabel+".plist")
}

// Running reports the daemon's PID if it is alive.
func Running() (int, bool) {
	b, err := os.ReadFile(pidFile())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return 0, false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return 0, false
	}
	// Guard against a stale pid file pointing at an unrelated process.
	if c := scan.Comm(pid); c == "" || !strings.Contains(filepath.Base(c), "appsassin") {
		return 0, false
	}
	return pid, true
}

// Installed reports whether the login LaunchAgent exists.
func Installed() bool {
	_, err := os.Stat(agentPath())
	return err == nil
}

// Start launches a detached daemon and returns its PID.
func Start(dryRun bool) (int, error) {
	if pid, ok := Running(); ok {
		return pid, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	args := []string{"stealth", "run"}
	if dryRun {
		args = append(args, "--dry-run")
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	for i := 0; i < 30; i++ {
		if p, ok := Running(); ok {
			return p, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return pid, nil
}

// Stop terminates the daemon for this login session.
func Stop() error {
	pid, ok := Running()
	if !ok {
		return errors.New("stealth mode is not running")
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}

// Install registers a LaunchAgent so stealth mode starts at every login.
func Install() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key>
	<array><string>%s</string><string>stealth</string><string>run</string></array>
	<key>RunAtLoad</key><true/>
	<key>ProcessType</key><string>Background</string>
	<key>LowPriorityIO</key><true/>
	<key>Nice</key><integer>15</integer>
	<key>StandardOutPath</key><string>/dev/null</string>
	<key>StandardErrorPath</key><string>/dev/null</string>
</dict>
</plist>
`, agentLabel, exe)
	_ = Stop()
	if err := os.MkdirAll(filepath.Dir(agentPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(agentPath(), []byte(plist), 0o644); err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+agentLabel).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain, agentPath()).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall removes the LaunchAgent and stops the daemon.
func Uninstall() error {
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+agentLabel).Run()
	_ = Stop()
	if err := os.Remove(agentPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Eligible decides whether an unattended sweep may kill t.
// watchedSince is when continuous app-usage tracking began; an app is only
// eligible once it was actually seen going unused, never just assumed idle
// because it launched long ago.
//
// Runaways go by the runaway rules alone, whatever their score: only with
// runaway.kill on, and never launchd jobs, which would just be restarted.
func Eligible(t *scan.Target, cfg config.Config, now, watchedSince time.Time, apps bool) bool {
	if t.Runaway != "" {
		return cfg.Runaway.Kill && t.Kind != scan.KindService
	}
	if t.Score < cfg.Stealth.Threshold {
		return false
	}
	switch t.Kind {
	case scan.KindOrphan, scan.KindHelper:
		return true
	case scan.KindApp:
		idle := cfg.Stealth.AppIdleDur()
		covered := t.UsedKnown || now.Sub(watchedSince) >= idle
		return apps && t.Windows == 0 && t.Idle(now) >= idle && covered
	}
	return false
}

// Sweep runs one unattended pass. Apps are only ever asked to quit politely.
// Kills go to the graveyard credited to by. Anything revived from the
// graveyard is left alone, and so is the app in front. wd may be nil, in
// which case nothing counts as a runaway.
func Sweep(cfg config.Config, tr *scan.Tracker, wd *scan.Watchdog, watchedSince time.Time, apps, dryRun bool, by string, logf func(string, ...any)) ([]reaper.Result, error) {
	return sweep(cfg, tr, wd, watchedSince, apps, dryRun, by, logf, nil)
}

// SweepRunaways is a pass that only takes out runaways, for when the
// watchdog spots one between sweeps.
func SweepRunaways(cfg config.Config, tr *scan.Tracker, wd *scan.Watchdog, dryRun bool, by string, logf func(string, ...any)) ([]reaper.Result, error) {
	return sweep(cfg, tr, wd, time.Now(), false, dryRun, by, logf, func(t *scan.Target) bool { return t.Runaway != "" })
}

func sweep(cfg config.Config, tr *scan.Tracker, wd *scan.Watchdog, watchedSince time.Time, apps, dryRun bool, by string,
	logf func(string, ...any), only func(*scan.Target) bool) ([]reaper.Result, error) {
	res, err := scan.Scan(scan.Options{Config: cfg, Tracker: tr, Watch: wd})
	if err != nil {
		return nil, err
	}
	revived := graveyard.Revived()
	var out []reaper.Result
	for _, t := range res.Targets {
		if (only != nil && !only(t)) || t.PID == res.FrontPID || !Eligible(t, cfg, res.At, watchedSince, apps) {
			continue
		}
		if at, ok := revived[t.PID]; ok && !t.Started.Before(at.Add(-time.Minute)) {
			continue
		}
		desc := fmt.Sprintf("%s pid=%d %q score=%d mem=%s why=[%s]",
			t.Kind, t.PID, t.Name, t.Score, scan.Bytes(t.TreeRSS), strings.Join(t.Reasons, ", "))
		if t.Runaway != "" {
			desc = "runaway " + desc
		}
		if dryRun {
			logf("would kill %s", desc)
			out = append(out, reaper.Result{Target: t, OK: true, Msg: "dry run"})
			continue
		}
		r := reaper.Kill(t, false, by)
		if r.OK {
			logf("killed %s (%s)", desc, r.Msg)
		} else {
			logf("failed %s: %s", desc, r.Msg)
		}
		out = append(out, r)
	}
	return out, nil
}

// notify tells the user what a sweep took out and how to undo it.
func notify(rs []reaper.Result) error {
	var names []string
	var freed int64
	for _, r := range rs {
		if r.OK {
			names = append(names, r.Target.Name)
			freed += r.Target.TreeRSS
		}
	}
	if len(names) == 0 {
		return nil
	}
	title := fmt.Sprintf("appsassin took out %d stray%s", len(names), map[bool]string{true: "", false: "s"}[len(names) == 1])
	sub := strings.Join(names, ", ")
	if len(sub) > 60 {
		sub = sub[:57] + "…"
	}
	return macos.Notify(title, sub, scan.Bytes(freed)+" freed · undo: appsassin undo")
}

// SetNotify turns sweep notifications on or off in the config.
func SetNotify(on bool) error {
	cfg := config.Load()
	cfg.Stealth.Notify = on
	return cfg.Save()
}

// Run is the daemon body.
func Run(dryRun bool) error {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 15)
	if pid, ok := Running(); ok && pid != os.Getpid() {
		return fmt.Errorf("stealth mode already running (pid %d)", pid)
	}
	if err := os.MkdirAll(config.SupportDir(), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(pidFile(), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return err
	}
	defer os.Remove(pidFile())

	_ = os.MkdirAll(filepath.Dir(config.LogPath()), 0o755)
	lf, err := os.OpenFile(config.LogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	logf := func(format string, a ...any) {
		fmt.Fprintf(lf, "%s  %s\n", time.Now().Format("Jan 02 15:04:05"), fmt.Sprintf(format, a...))
	}

	cfg := config.Load()
	tr := scan.LoadTracker()
	wd := scan.LoadWatchdog()
	started := time.Now()
	mode := ""
	if dryRun || cfg.Stealth.DryRun {
		mode = " (dry run)"
	}
	logf("stealth mode engaged%s: sweep every %s, threshold %d%s", mode, cfg.Stealth.IntervalDur(), cfg.Stealth.Threshold, runawayNote(cfg.Runaway))
	passed := map[int]bool{} // runaways the last runaway pass left alone (ignored, the front app, failed)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	watch := time.NewTicker(20 * time.Second)
	defer watch.Stop()
	nextSweep := time.Now().Add(cfg.Stealth.IntervalDur())

	for {
		select {
		case <-sigs:
			_ = tr.Save()
			_ = wd.Save()
			logf("stealth mode disengaged")
			return nil
		case now := <-watch.C:
			if front := macos.FrontPID(); front > 0 {
				if info, ok := macos.App(front); ok {
					tr.Touch(info.BundlePath, now)
				}
			}
			cfg = config.Load() // pick up edits without a restart
			// Sample usage on every wake, so a runaway is caught soon after it
			// has been over the line long enough, not at the next sweep.
			if cfg.Runaway.Enabled() {
				pids, err := wd.Sample(now, cfg.Runaway)
				if err != nil {
					logf("watchdog error: %v", err)
				}
				fresh := false
				cur := map[int]bool{}
				for _, pid := range pids {
					cur[pid] = true
					fresh = fresh || !passed[pid]
				}
				passed = cur
				if fresh && cfg.Runaway.Kill {
					rs, err := SweepRunaways(cfg, tr, wd, dryRun || cfg.Stealth.DryRun, "stealth", logf)
					if err != nil {
						logf("runaway sweep error: %v", err)
					}
					if cfg.Stealth.Notify && !dryRun && !cfg.Stealth.DryRun {
						if err := notify(rs); err != nil {
							logf("notification failed: %v", err)
						}
					}
				}
				_ = wd.Save()
			}
			if now.Before(nextSweep) {
				continue
			}
			rs, err := Sweep(cfg, tr, wd, started, cfg.Stealth.Apps, dryRun || cfg.Stealth.DryRun, "stealth", logf)
			if err != nil {
				logf("sweep error: %v", err)
			}
			if cfg.Stealth.Notify && !dryRun && !cfg.Stealth.DryRun {
				if err := notify(rs); err != nil {
					logf("notification failed: %v", err)
				}
			}
			_ = tr.Save()
			_ = wd.Save()
			nextSweep = now.Add(cfg.Stealth.IntervalDur())
		}
	}
}

func runawayNote(r config.Runaway) string {
	switch {
	case !r.Enabled():
		return ", runaway watchdog off"
	case r.Kill:
		return ", kills runaways (" + r.Describe() + ")"
	}
	return ", flags runaways (" + r.Describe() + ") without killing them"
}
