package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/mikemich/appsassin/internal/config"
	"github.com/mikemich/appsassin/internal/reaper"
	"github.com/mikemich/appsassin/internal/scan"
)

func everything(cfg config.Config) (*scan.Result, error) {
	return scan.Scan(scan.Options{Config: cfg, Tracker: scan.LoadTracker(), Everything: true})
}

func ports(t *scan.Target) string {
	ps := make([]string, len(t.Ports))
	for i, p := range t.Ports {
		ps[i] = fmt.Sprintf(":%d", p)
	}
	return strings.Join(ps, " ")
}

func row(t *scan.Target) string {
	extra := ports(t)
	if t.Stray {
		extra = strings.TrimSpace(fmt.Sprintf("stray %d %s", t.Score, extra))
	}
	return fmt.Sprintf("%-8s %7d  %-34.34s %5.1f%% %6s  %s", t.Kind, t.PID, t.Name, t.CPU, scan.Bytes(t.TreeRSS), extra)
}

// ps lists everything the user is running.
func ps(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("ps", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	_ = fs.Parse(args)
	res, err := everything(cfg)
	if err != nil {
		return err
	}
	ts := res.Targets
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].TreeRSS > ts[j].TreeRSS })
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(ts)
	}
	fmt.Printf("%-8s %7s  %-34s %6s %6s  %s\n", "KIND", "PID", "NAME", "CPU", "MEM", "PORTS / STRAY")
	for _, t := range ts {
		fmt.Println(row(t))
	}
	return nil
}

// find resolves a kill query: a PID, a :port, or part of a name.
func find(res *scan.Result, q string) []*scan.Target {
	if port, err := strconv.Atoi(strings.TrimPrefix(q, ":")); err == nil && strings.HasPrefix(q, ":") {
		var out []*scan.Target
		for _, t := range res.All {
			for _, p := range t.Ports {
				if p == port {
					out = append(out, t)
					break
				}
			}
		}
		return out
	}
	if pid, err := strconv.Atoi(q); err == nil {
		if t := res.All[pid]; t != nil {
			return []*scan.Target{t}
		}
		return nil
	}
	q = strings.ToLower(q)
	var exact, partial []*scan.Target
	for _, t := range res.Targets {
		ids := []string{strings.ToLower(t.Name), strings.ToLower(filepath.Base(t.Comm)), strings.ToLower(t.BundleID)}
		for _, id := range ids {
			if id == q {
				exact = append(exact, t)
				break
			}
		}
		if len(exact) == 0 {
			for _, id := range ids[:2] {
				if strings.Contains(id, q) {
					partial = append(partial, t)
					break
				}
			}
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}

func ask(prompt string) string {
	fmt.Print(prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(strings.ToLower(line))
}

// interactive reports whether stdin is a real terminal someone can answer from.
func interactive() bool {
	_, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TIOCGETA)
	return err == nil
}

// kill finds anything the user is running and kills it, after confirmation.
func kill(cfg config.Config, args []string) error {
	var yes, force bool
	var words []string
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			yes = true
		case "-f", "--force":
			force = true
		default:
			words = append(words, a)
		}
	}
	if len(words) == 0 {
		return errors.New("usage: appsassin kill <name|pid|:port> [-y] [-f]")
	}
	q := strings.Join(words, " ")
	res, err := everything(cfg)
	if err != nil {
		return err
	}
	matches := find(res, q)
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].TreeRSS > matches[j].TreeRSS })
	if len(matches) == 0 {
		return fmt.Errorf("nothing you're running matches %q (try `appsassin ps`)", q)
	}

	var picked []*scan.Target
	switch {
	case len(matches) == 1:
		t := matches[0]
		fmt.Println("  " + row(t))
		if !yes {
			if !interactive() {
				return errors.New("not a terminal; pass -y to kill without asking")
			}
			if a := ask(fmt.Sprintf("Kill %s? [y/N] ", t.Name)); a != "y" && a != "yes" {
				fmt.Println("stand down")
				return nil
			}
		}
		picked = matches
	default:
		for i, t := range matches {
			fmt.Printf("%3d  %s\n", i+1, row(t))
		}
		if yes || !interactive() {
			return fmt.Errorf("%q matches %d things; be more specific, or use a pid or :port", q, len(matches))
		}
		a := ask(fmt.Sprintf("Kill which? [1-%d, a = all, enter = none] ", len(matches)))
		switch a {
		case "":
			fmt.Println("stand down")
			return nil
		case "a", "all":
			picked = matches
		default:
			for _, f := range strings.FieldsFunc(a, func(r rune) bool { return r == ',' || r == ' ' }) {
				n, err := strconv.Atoi(f)
				if err != nil || n < 1 || n > len(matches) {
					return fmt.Errorf("no such choice %q", f)
				}
				picked = append(picked, matches[n-1])
			}
		}
	}

	failed := 0
	for _, t := range picked {
		r := reaper.Kill(t, force, "cli")
		if r.OK {
			fmt.Printf("☠ %s %s · %s freed · undo: appsassin revive %q\n", t.Name, r.Msg, scan.Bytes(t.TreeRSS), t.Name)
		} else {
			failed++
			fmt.Printf("✗ %s: %s\n", t.Name, r.Msg)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d not killed", failed, len(picked))
	}
	return nil
}
