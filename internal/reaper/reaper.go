// Package reaper terminates targets: politely first, then not.
package reaper

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/mikemich/appsassin/internal/macos"
	"github.com/mikemich/appsassin/internal/scan"
)

type Result struct {
	Target *scan.Target
	OK     bool
	Msg    string
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func waitGone(pids []int, d time.Duration) []int {
	deadline := time.Now().Add(d)
	for {
		var left []int
		for _, p := range pids {
			if alive(p) {
				left = append(left, p)
			}
		}
		if len(left) == 0 || time.Now().After(deadline) {
			return left
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func signalAll(pids []int, sig syscall.Signal) {
	for _, p := range pids {
		_ = syscall.Kill(p, sig)
	}
}

// Kill takes out a target and its descendants.
//
// Apps get a Cmd-Q style quit request and are left alone if they refuse
// (unsaved work) unless force is set. Processes get SIGTERM, then SIGKILL
// after a grace period.
func Kill(t *scan.Target, force bool) Result {
	r := Result{Target: t}
	if c := scan.Comm(t.PID); c == "" {
		r.OK, r.Msg = true, "already gone"
		return r
	} else if c != t.Comm {
		r.Msg = "pid was reused by another process; skipped"
		return r
	}
	tree := append([]int{t.PID}, t.Children...)

	if t.Kind == scan.KindApp && !force {
		if !macos.Terminate(t.PID) {
			r.Msg = "app refused quit request"
			return r
		}
		if left := waitGone([]int{t.PID}, 8*time.Second); len(left) > 0 {
			r.Msg = "app is still open (unsaved changes?) — force with K"
			return r
		}
		// Apps normally take their helpers down with them; mop up stragglers.
		if left := waitGone(t.Children, 2*time.Second); len(left) > 0 {
			signalAll(left, syscall.SIGKILL)
		}
		r.OK, r.Msg = true, "quit"
		return r
	}

	if force {
		signalAll(tree, syscall.SIGKILL)
		left := waitGone(tree, 2*time.Second)
		r.OK = len(left) == 0
		r.Msg = "force-killed"
		if !r.OK {
			r.Msg = fmt.Sprintf("%d process(es) survived SIGKILL", len(left))
		}
		return r
	}

	// Parent first so it can shut its own children down cleanly.
	signalAll([]int{t.PID}, syscall.SIGTERM)
	waitGone([]int{t.PID}, 1500*time.Millisecond)
	signalAll(tree, syscall.SIGTERM)
	left := waitGone(tree, 2*time.Second)
	if len(left) == 0 {
		r.OK, r.Msg = true, "terminated"
		return r
	}
	signalAll(left, syscall.SIGKILL)
	left = waitGone(left, 2*time.Second)
	r.OK = len(left) == 0
	r.Msg = "killed (ignored SIGTERM)"
	if !r.OK {
		r.Msg = fmt.Sprintf("%d process(es) survived", len(left))
	}
	return r
}
