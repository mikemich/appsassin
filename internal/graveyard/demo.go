package graveyard

import (
	"strings"
	"time"

	"github.com/mikemich/appsassin/internal/scan"
)

// Demo returns a made-up graveyard for screenshots and try-outs, newest first.
func Demo(now time.Time) []Entry {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	at := func(d time.Duration) *time.Time { t := ago(d); return &t }
	mb := func(n int64) int64 { return n << 20 }
	return []Entry{
		{Killed: ago(14 * time.Minute), By: "stealth", Kind: scan.KindOrphan, Name: "webpack --watch", PID: 70412,
			Comm: "/opt/homebrew/bin/node", Args: "node /Users/you/code/admin/node_modules/.bin/webpack --watch",
			Argv: []string{"node", "/Users/you/code/admin/node_modules/.bin/webpack", "--watch"}, Exe: "/opt/homebrew/bin/node",
			Cwd: "/Users/you/code/admin", Mem: mb(412), Score: 92,
			Reasons: []string{"orphaned (parent exited)", "dev runtime", "idle", "running 3d", "412M held"}},
		{Killed: ago(73 * time.Minute), By: "stealth", Kind: scan.KindHelper, Name: "Figma Helper (Renderer)", PID: 66120,
			Comm:   "/Applications/Figma.app/Contents/Frameworks/Figma Helper (Renderer).app/Contents/MacOS/Figma Helper (Renderer)",
			Bundle: "/Applications/Figma.app", Cwd: "/", Mem: mb(290), Score: 85,
			Reasons: []string{"outlived Figma", "idle", "running 9h", "290M held"}},
		{Killed: ago(3*time.Hour + 8*time.Minute), By: "tui", Kind: scan.KindOrphan, Name: "ruby bin/rails server", PID: 58844,
			Comm: "/opt/homebrew/opt/ruby/bin/ruby", Args: "ruby bin/rails server -p 3001",
			Argv: []string{"ruby", "bin/rails", "server", "-p", "3001"}, Exe: "/opt/homebrew/opt/ruby/bin/ruby",
			Cwd: "/Users/you/code/billing", Ports: []int{3001}, Mem: mb(188), Score: 90,
			Reasons: []string{"orphaned (parent exited)", "dev runtime", "listening :3001", "idle", "running 26h", "188M held"}},
		{Killed: ago(6*time.Hour + 41*time.Minute), By: "stealth", Kind: scan.KindApp, Name: "Microsoft Excel", PID: 4410,
			Comm: "/Applications/Microsoft Excel.app/Contents/MacOS/Microsoft Excel", Bundle: "/Applications/Microsoft Excel.app",
			BundleID: "com.microsoft.Excel", Cwd: "/", Mem: mb(524), Score: 85,
			Reasons: []string{"no windows", "unused 7h", "idle", "524M held"},
			Revived: at(5*time.Hour + 12*time.Minute)},
		{Killed: ago(9 * time.Hour), By: "tui", Kind: scan.KindOrphan, Name: "ngrok http 3000", PID: 51207,
			Comm: "/opt/homebrew/bin/ngrok", Args: "ngrok http 3000", Argv: []string{"ngrok", "http", "3000"},
			Exe: "/opt/homebrew/bin/ngrok", Cwd: "/Users/you/code/storefront", Ports: []int{4040}, Mem: mb(31), Score: 80,
			Reasons: []string{"orphaned (parent exited)", "dev runtime", "listening :4040", "idle", "running 2d"}},
		{Killed: ago(26 * time.Hour), By: "stealth", Kind: scan.KindOrphan, Name: "tsc --watch", PID: 39981,
			Comm: "/opt/homebrew/bin/node", Args: "node /Users/you/code/sdk/node_modules/.bin/tsc --watch",
			Argv: []string{"node", "/Users/you/code/sdk/node_modules/.bin/tsc", "--watch"}, Exe: "/opt/homebrew/bin/node",
			Cwd: "/Users/you/code/sdk", Mem: mb(236), Score: 87,
			Reasons: []string{"orphaned (parent exited)", "dev runtime", "idle", "running 4d", "236M held"},
			Revived: at(25 * time.Hour), RevivedPID: 40012},
		{Killed: ago(31 * time.Hour), By: "stealth", Kind: scan.KindApp, Name: "Preview", PID: 9031,
			Comm: "/System/Applications/Preview.app/Contents/MacOS/Preview", Bundle: "/System/Applications/Preview.app",
			BundleID: "com.apple.Preview", Cwd: "/", Mem: mb(74), Score: 75,
			Reasons: []string{"no windows", "hidden", "unused 9h", "idle"}},
		{Killed: ago(52 * time.Hour), By: "sweep", Kind: scan.KindOrphan, Name: "redis-server *:6379", PID: 21170,
			Comm: "/opt/homebrew/bin/redis-server", Args: "redis-server *:6379",
			Argv: []string{"redis-server", "*:6379"}, Exe: "/opt/homebrew/bin/redis-server",
			Cwd: "/Users/you", Ports: []int{6379}, Mem: mb(12), Score: 80,
			Reasons: []string{"orphaned (parent exited)", "dev runtime", "listening :6379", "idle", "running 8d"}},
	}
}

// DemoKill is FromTarget for a simulated kill, with a plausible working
// directory since demo targets have none to look up.
func DemoKill(t *scan.Target, at time.Time) Entry {
	e := FromTarget(t, "tui", at)
	switch {
	case t.Kind != scan.KindOrphan:
		e.Cwd = "/"
	case strings.Contains(t.Args, " /") && strings.Contains(t.Args, "/node_modules/"):
		fs := strings.Fields(t.Args[:strings.Index(t.Args, "/node_modules/")])
		e.Cwd = fs[len(fs)-1]
	default:
		e.Cwd = "/Users/you/code"
	}
	return e
}
