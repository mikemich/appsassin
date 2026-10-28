package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mikemich/appsassin/internal/config"
	"github.com/mikemich/appsassin/internal/graveyard"
	"github.com/mikemich/appsassin/internal/reaper"
	"github.com/mikemich/appsassin/internal/scan"
)

// free kills the most forgotten strays until the requested memory is reclaimed.
func free(cfg config.Config, args []string) error {
	var yes, dry bool
	minScore := cfg.Threshold
	var amount string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-y", "--yes":
			yes = true
		case "--dry-run", "-n":
			dry = true
		case "--threshold":
			if i+1 == len(args) {
				return errors.New("--threshold needs a number")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("--threshold %q is not a number", args[i])
			}
			minScore = n
		default:
			amount = a
		}
	}
	if amount == "" {
		return errors.New("usage: appsassin free <amount> [--dry-run] [-y] [--threshold N]   e.g. appsassin free 2G")
	}
	want, err := scan.ParseSize(amount)
	if err != nil {
		return err
	}

	res, err := scan.Scan(scan.Options{Config: cfg, Tracker: scan.LoadTracker()})
	if err != nil {
		return err
	}
	plan, total := scan.PlanFree(res.Targets, want, minScore)
	if len(plan) == 0 {
		fmt.Printf("✓ nothing stray scores ≥%d, so there's nothing to free safely\n", minScore)
		return nil
	}
	for i, t := range plan {
		fmt.Printf("%3d  %3d  %-7s %7d  %-32.32s %6s  %s\n", i+1, t.Score, t.Kind, t.PID, t.Name, scan.Bytes(t.TreeRSS), strings.Join(t.Reasons, " · "))
	}
	if total < want {
		fmt.Printf("\nevery stray scoring ≥%d together holds %s, short of the %s asked for\n", minScore, scan.Bytes(total), scan.Bytes(want))
	} else {
		fmt.Printf("\n%d stray%s, %s in all\n", len(plan), plural(len(plan)), scan.Bytes(total))
	}
	if dry {
		return nil
	}
	if !yes {
		if !interactive() {
			return errors.New("not a terminal; pass -y to kill without asking")
		}
		if a := ask(fmt.Sprintf("Take out %d to free ~%s? [y/N] ", len(plan), scan.Bytes(total))); a != "y" && a != "yes" {
			fmt.Println("stand down")
			return nil
		}
	}

	var freed int64
	failed := 0
	for _, t := range plan {
		r := reaper.Kill(t, false, "free")
		if r.OK {
			freed += t.TreeRSS
			fmt.Printf("☠ %s %s\n", t.Name, r.Msg)
		} else {
			failed++
			fmt.Printf("✗ %s: %s\n", t.Name, r.Msg)
		}
	}
	fmt.Printf("\n%s freed · undo all of it: appsassin undo\n", scan.Bytes(freed))
	if failed > 0 {
		return fmt.Errorf("%d of %d not killed", failed, len(plan))
	}
	return nil
}

// undo revives the most recent batch of kills: one stealth sweep, one `free`,
// one multi-kill in the app.
func undo(args []string) error {
	yes := len(args) > 0 && (args[0] == "-y" || args[0] == "--yes")
	es, err := graveyard.Load()
	if err != nil {
		return err
	}
	batch := graveyard.LastBatch(es)
	if len(batch) == 0 {
		fmt.Println("nothing to undo: every kill in the graveyard has already been revived")
		return nil
	}
	fmt.Printf("last kills (by %s, %s ago):\n", batch[0].By, scan.Human(time.Since(batch[0].Killed)))
	for _, e := range batch {
		fmt.Printf("  %-7s %-32.32s %s\n", e.Kind, e.Name, e.What())
	}
	if !yes {
		if !interactive() {
			return errors.New("not a terminal; pass -y to revive without asking")
		}
		if a := ask(fmt.Sprintf("Bring %s back? [y/N] ", map[bool]string{true: "it", false: "all " + strconv.Itoa(len(batch))}[len(batch) == 1])); a != "y" && a != "yes" {
			fmt.Println("left in the graveyard")
			return nil
		}
	}
	failed := 0
	for _, e := range batch {
		rv, err := graveyard.Revive(e)
		if err != nil {
			failed++
			fmt.Printf("✗ %s: %v\n", e.Name, err)
			continue
		}
		fmt.Printf("↺ %s · ran %s\n", e.Name, rv.Cmd)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d not revived", failed, len(batch))
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
