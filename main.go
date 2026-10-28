// appsassin hunts down forgotten apps and stray processes on macOS.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mikemich/appsassin/internal/config"
	"github.com/mikemich/appsassin/internal/graveyard"
	"github.com/mikemich/appsassin/internal/scan"
	"github.com/mikemich/appsassin/internal/stealth"
	"github.com/mikemich/appsassin/internal/tui"
)

// version is overridden at release time via -ldflags "-X main.version=...".
var version = "0.2.0"

const usage = `appsassin — finds forgotten apps & stray processes and takes them out.

usage:
  appsassin [-m] [-e] [--demo]   interactive hit list (-m / --minimal for compact mode,
                                 -e / --everything to start on everything running,
                                 --demo for made-up targets and simulated kills)
  appsassin kill <name|pid|:port> [-y] [-f]
                                 kill anything you're running, e.g. kill slack, kill :3000
  appsassin ps [--json]          everything you're running: apps, processes, ports
  appsassin list [--all] [--json]
                                 print targets and exit
  appsassin sweep [--dry-run] [--apps] [--threshold N]
                                 one unattended kill pass, like stealth mode does
  appsassin graveyard [-n N] [--json]
                                 recent kills, newest first (from the TUI, sweep and stealth)
  appsassin revive <n|name>      bring a kill back: reopen the app, or re-run the
                                 command in its old directory
  appsassin stealth <cmd>        silent background sweeper
      start [--dry-run]          run detached for this login session
      stop                       stop it
      status                     is it running?
      log [-n N]                 show what it did
      install                    start automatically at every login (LaunchAgent)
      uninstall                  remove the LaunchAgent
  appsassin config               show config path and contents
  appsassin version

Nothing owned by macOS or another user is ever touched.
`

func main() {
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	cfg := config.Load()

	var err error
	switch cmd {
	case "":
		fs := flag.NewFlagSet("appsassin", flag.ExitOnError)
		fs.Usage = flag.Usage
		minimal := fs.Bool("minimal", false, "")
		fs.BoolVar(minimal, "m", false, "")
		demo := fs.Bool("demo", false, "")
		every := fs.Bool("everything", false, "")
		fs.BoolVar(every, "e", false, "")
		_ = fs.Parse(args)
		err = tui.Run(cfg, *minimal, *demo, *every)
	case "list", "ls":
		err = list(cfg, args)
	case "ps":
		err = ps(cfg, args)
	case "kill":
		err = kill(cfg, args)
	case "sweep":
		err = sweep(cfg, args)
	case "graveyard", "graves":
		err = graveyardCmd(args)
	case "revive":
		err = revive(args)
	case "stealth":
		err = stealthCmd(args)
	case "config":
		b, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Printf("%s\n%s\n", config.Path(), b)
	case "version", "--version":
		fmt.Println("appsassin", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "appsassin:", err)
		os.Exit(1)
	}
}

func list(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	all := fs.Bool("all", false, "include targets below the threshold")
	asJSON := fs.Bool("json", false, "machine-readable output")
	_ = fs.Parse(args)

	res, err := scan.Scan(scan.Options{Config: cfg, Tracker: scan.LoadTracker()})
	if err != nil {
		return err
	}
	var ts []*scan.Target
	for _, t := range res.Targets {
		if *all || t.Score >= cfg.Threshold {
			ts = append(ts, t)
		}
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(ts)
	}
	if len(ts) == 0 {
		fmt.Println("✓ all clear")
		return nil
	}
	for _, t := range ts {
		fmt.Printf("%3d  %-7s %7d  %-32.32s %6s  %s\n", t.Score, t.Kind, t.PID, t.Name, scan.Bytes(t.TreeRSS), strings.Join(t.Reasons, " · "))
	}
	return nil
}

func sweep(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("sweep", flag.ExitOnError)
	dry := fs.Bool("dry-run", false, "show what would be killed")
	apps := fs.Bool("apps", false, "also quit windowless apps idle longer than stealth.appIdle")
	th := fs.Int("threshold", cfg.Stealth.Threshold, "minimum score")
	_ = fs.Parse(args)
	cfg.Stealth.Threshold = *th

	logf := func(f string, a ...any) { fmt.Printf(f+"\n", a...) }
	// A one-off sweep has no watch history of its own, so apps only qualify
	// when the tracker has actually seen them go unused.
	rs, err := stealth.Sweep(cfg, scan.LoadTracker(), time.Now(), *apps, *dry, "sweep", logf)
	if err == nil && len(rs) == 0 {
		fmt.Printf("✓ nothing scores ≥%d — all clear\n", *th)
	}
	return err
}

func ago(t time.Time) string { return scan.Human(time.Since(t)) + " ago" }

func graveyardCmd(args []string) error {
	fs := flag.NewFlagSet("graveyard", flag.ExitOnError)
	n := fs.Int("n", -1, "entries to show, 0 for all (default 20, or all with --json)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	_ = fs.Parse(args)

	es, err := graveyard.Load()
	if err != nil {
		return err
	}
	limit := *n
	if limit < 0 && !*asJSON {
		limit = 20
	}
	more := 0
	if limit > 0 && len(es) > limit {
		more = len(es) - limit
		es = es[:limit]
	}
	if *asJSON {
		if es == nil {
			es = []graveyard.Entry{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(es)
	}
	if len(es) == 0 {
		fmt.Println("☠ the graveyard is empty — nothing has been killed yet")
		return nil
	}
	for i, e := range es {
		mark := " "
		if e.Revived != nil {
			mark = "↺"
		}
		fmt.Printf("%3d %s %8s  %-7s %-30.30s %-7s %s\n", i+1, mark, ago(e.Killed), e.Kind, e.Name, e.By, e.What())
	}
	if more > 0 {
		fmt.Printf("    … %d older (-n 0 shows all)\n", more)
	}
	fmt.Println("\nbring one back: appsassin revive <n|name>")
	return nil
}

func revive(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: appsassin revive <n|name> (numbers as in `appsassin graveyard`)")
	}
	es, err := graveyard.Load()
	if err != nil {
		return err
	}
	e, err := graveyard.Find(es, strings.Join(args, " "))
	if err != nil {
		return err
	}
	rv, err := graveyard.Revive(e)
	if err != nil {
		return err
	}
	fmt.Printf("↺ revived %s (%s, killed %s by %s)\n  ran: %s\n", e.Name, e.Kind, ago(e.Killed), e.By, rv.Cmd)
	if rv.PID > 0 {
		fmt.Printf("  pid: %d\n", rv.PID)
	}
	if rv.Log != "" {
		fmt.Printf("  log: %s\n", rv.Log)
	}
	return nil
}

func stealthCmd(args []string) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("stealth "+sub, flag.ExitOnError)
	dry := fs.Bool("dry-run", false, "log what would be killed without killing")
	n := fs.Int("n", 30, "log lines")
	_ = fs.Parse(rest)

	switch sub {
	case "run": // daemon body, used by start and the LaunchAgent
		return stealth.Run(*dry)
	case "start":
		pid, err := stealth.Start(*dry)
		if err == nil {
			fmt.Printf("◉ stealth mode engaged (pid %d) · log: %s\n", pid, config.LogPath())
		}
		return err
	case "stop":
		if err := stealth.Stop(); err != nil {
			return err
		}
		fmt.Println("○ stealth mode disengaged")
		if stealth.Installed() {
			fmt.Println("  (LaunchAgent still installed — it will return at next login; `appsassin stealth uninstall` to remove)")
		}
	case "status":
		if pid, ok := stealth.Running(); ok {
			fmt.Printf("◉ running (pid %d)\n", pid)
		} else {
			fmt.Println("○ not running")
		}
		fmt.Printf("  at login: %v\n  log: %s\n", stealth.Installed(), config.LogPath())
	case "log":
		c := exec.Command("tail", "-n", fmt.Sprint(*n), config.LogPath())
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		return c.Run()
	case "install":
		if err := stealth.Install(); err != nil {
			return err
		}
		fmt.Println("◉ stealth mode will now run silently at every login")
	case "uninstall":
		if err := stealth.Uninstall(); err != nil {
			return err
		}
		fmt.Println("○ stealth LaunchAgent removed")
	default:
		return fmt.Errorf("unknown stealth command %q", sub)
	}
	return nil
}
