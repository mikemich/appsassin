// appsassin hunts down forgotten apps and stray processes on macOS.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mikemich/appsassin/internal/config"
	"github.com/mikemich/appsassin/internal/scan"
	"github.com/mikemich/appsassin/internal/stealth"
	"github.com/mikemich/appsassin/internal/tui"
)

const version = "0.1.0"

const usage = `appsassin — finds forgotten apps & stray processes and takes them out.

usage:
  appsassin [-m] [--demo]        interactive hit list (-m / --minimal for compact mode,
                                 --demo for made-up targets and simulated kills)
  appsassin list [--all] [--json]
                                 print targets and exit
  appsassin sweep [--dry-run] [--apps] [--threshold N]
                                 one unattended kill pass, like stealth mode does
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
		_ = fs.Parse(args)
		err = tui.Run(cfg, *minimal, *demo)
	case "list", "ls":
		err = list(cfg, args)
	case "sweep":
		err = sweep(cfg, args)
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
	rs, err := stealth.Sweep(cfg, scan.LoadTracker(), time.Now(), *apps, *dry, logf)
	if err == nil && len(rs) == 0 {
		fmt.Printf("✓ nothing scores ≥%d — all clear\n", *th)
	}
	return err
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
