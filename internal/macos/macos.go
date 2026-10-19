//go:build darwin

// Package macos wraps the few Cocoa / CoreGraphics calls appsassin needs:
// window counts per process, running-application metadata, and graceful
// app termination. None of these require Screen Recording or Automation
// permissions.
package macos

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics -framework Foundation
#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>
#include <stdlib.h>
#include <string.h>

// A "real" window: normal layer, not transparent, not a 1px helper.
static int as_real_window(NSDictionary *w) {
	if ([w[(id)kCGWindowLayer] intValue] != 0) return 0;
	NSNumber *alpha = w[(id)kCGWindowAlpha];
	if (alpha != nil && [alpha doubleValue] <= 0.01) return 0;
	CGRect r;
	NSDictionary *b = w[(id)kCGWindowBounds];
	if (b == nil || !CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)b, &r)) return 0;
	return r.size.width >= 100 && r.size.height >= 60;
}

// Fills pids/onscreen with one entry per real window. Returns count.
static int as_windows(int *pids, int *onscreen, int max) {
	int n = 0;
	@autoreleasepool {
		CFArrayRef list = CGWindowListCopyWindowInfo(
			kCGWindowListOptionAll | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
		if (!list) return 0;
		NSArray *arr = (__bridge NSArray *)list;
		for (NSDictionary *w in arr) {
			if (n >= max) break;
			if (!as_real_window(w)) continue;
			pids[n] = [w[(id)kCGWindowOwnerPID] intValue];
			onscreen[n] = [w[(id)kCGWindowIsOnscreen] boolValue] ? 1 : 0;
			n++;
		}
		CFRelease(list);
	}
	return n;
}

// PID owning the front-most on-screen normal window, or -1.
static int as_front_pid(void) {
	int pid = -1;
	@autoreleasepool {
		CFArrayRef list = CGWindowListCopyWindowInfo(
			kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
		if (!list) return -1;
		NSArray *arr = (__bridge NSArray *)list;
		for (NSDictionary *w in arr) {
			if (as_real_window(w)) { pid = [w[(id)kCGWindowOwnerPID] intValue]; break; }
		}
		CFRelease(list);
	}
	return pid;
}

static void as_copy(NSString *s, char *dst, int len) {
	dst[0] = 0;
	if (s == nil) return;
	const char *c = [s UTF8String];
	if (c == NULL) return;
	strncpy(dst, c, len - 1);
	dst[len - 1] = 0;
}

static int as_app(int pid, char *name, char *bid, char *bpath, int len, int *policy, int *hidden) {
	@autoreleasepool {
		NSRunningApplication *a = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
		if (a == nil) return 0;
		as_copy(a.localizedName, name, len);
		as_copy(a.bundleIdentifier, bid, len);
		as_copy(a.bundleURL.path, bpath, len);
		*policy = (int)a.activationPolicy;
		*hidden = a.isHidden ? 1 : 0;
	}
	return 1;
}

static int as_terminate(int pid) {
	@autoreleasepool {
		NSRunningApplication *a = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
		if (a == nil) return 0;
		return [a terminate] ? 1 : 0;
	}
}
*/
import "C"

import "unsafe"

// Activation policies, mirroring NSApplicationActivationPolicy.
const (
	PolicyRegular    = 0 // Dock icon, normal app
	PolicyAccessory  = 1 // menu-bar / agent app
	PolicyProhibited = 2 // background-only
)

// WindowStats holds per-PID counts of real windows.
type WindowStats struct {
	Total    map[int]int // all real windows (any space, minimized included)
	OnScreen map[int]int // currently visible on some display
}

// Windows enumerates every normal window on the system.
func Windows() WindowStats {
	const max = 4096
	pids := make([]C.int, max)
	on := make([]C.int, max)
	n := int(C.as_windows((*C.int)(unsafe.Pointer(&pids[0])), (*C.int)(unsafe.Pointer(&on[0])), max))
	ws := WindowStats{Total: map[int]int{}, OnScreen: map[int]int{}}
	for i := 0; i < n; i++ {
		pid := int(pids[i])
		ws.Total[pid]++
		if on[i] != 0 {
			ws.OnScreen[pid]++
		}
	}
	return ws
}

// FrontPID returns the PID owning the front-most window, or -1.
func FrontPID() int { return int(C.as_front_pid()) }

// AppInfo describes a process LaunchServices knows as an application.
type AppInfo struct {
	Name       string
	BundleID   string
	BundlePath string
	Policy     int
	Hidden     bool
}

// App looks up NSRunningApplication metadata for pid.
func App(pid int) (AppInfo, bool) {
	const n = 1024
	name := (*C.char)(C.malloc(n))
	bid := (*C.char)(C.malloc(n))
	bpath := (*C.char)(C.malloc(n))
	defer C.free(unsafe.Pointer(name))
	defer C.free(unsafe.Pointer(bid))
	defer C.free(unsafe.Pointer(bpath))
	var policy, hidden C.int
	if C.as_app(C.int(pid), name, bid, bpath, n, &policy, &hidden) == 0 {
		return AppInfo{}, false
	}
	return AppInfo{
		Name:       C.GoString(name),
		BundleID:   C.GoString(bid),
		BundlePath: C.GoString(bpath),
		Policy:     int(policy),
		Hidden:     hidden != 0,
	}, true
}

// Terminate asks an app to quit the polite way (same as Cmd-Q). The app may
// refuse, e.g. to prompt about unsaved documents.
func Terminate(pid int) bool { return C.as_terminate(C.int(pid)) != 0 }
