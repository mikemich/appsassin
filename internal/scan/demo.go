package scan

import "time"

// Demo returns a realistic, made-up hit list for screenshots and try-outs.
// PIDs in gone are left out (they were "killed").
func Demo(gone map[int]bool) *Result {
	now := time.Now()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	mb := func(n int64) int64 { return n << 20 }
	all := []*Target{
		{PID: 48213, PPID: 1, Kind: KindOrphan, Name: "next dev", Comm: "/opt/homebrew/bin/node",
			Args: "node /Users/you/code/storefront/node_modules/.bin/next dev", RSS: mb(212), TreeRSS: mb(694),
			Started: ago(52 * time.Hour), Ports: []int{3000}, Children: []int{48220, 48221, 48230},
			Score: 97, Reasons: []string{"orphaned (parent exited)", "dev runtime", "listening :3000", "idle", "running 2d", "694M held"}},
		{PID: 31877, PPID: 1, Kind: KindHelper, Name: "Slack Helper (Renderer)", Bundle: "/Applications/Slack.app",
			Comm: "/Applications/Slack.app/Contents/Frameworks/Slack Helper (Renderer).app/Contents/MacOS/Slack Helper (Renderer)",
			RSS:  mb(388), TreeRSS: mb(388), Started: ago(19 * time.Hour),
			Score: 85, Reasons: []string{"outlived Slack", "idle", "running 19h", "388M held"}},
		{PID: 50551, PPID: 1, Kind: KindOrphan, Name: "vite", Comm: "/opt/homebrew/bin/node",
			Args: "node /Users/you/code/dashboard/node_modules/.bin/vite --port 5173", RSS: mb(141), TreeRSS: mb(166),
			Started: ago(7 * time.Hour), Ports: []int{5173}, Children: []int{50560},
			Score: 85, Reasons: []string{"orphaned (parent exited)", "dev runtime", "listening :5173", "idle", "166M held"}},
		{PID: 22904, PPID: 1, Kind: KindOrphan, Name: "python3 manage.py runserver", Comm: "/opt/homebrew/bin/python3",
			Args: "python3 manage.py runserver 0.0.0.0:8000", RSS: mb(96), TreeRSS: mb(171), Started: ago(4 * 24 * time.Hour),
			Ports: []int{8000}, Children: []int{22911},
			Score: 85, Reasons: []string{"orphaned (parent exited)", "dev runtime", "listening :8000", "idle", "running 4d", "171M held"}},
		{PID: 1450, PPID: 1, Kind: KindApp, Name: "Spotify", BundleID: "com.spotify.client", Bundle: "/Applications/Spotify.app",
			Comm: "/Applications/Spotify.app/Contents/MacOS/Spotify", RSS: mb(402), TreeRSS: mb(611), Children: []int{1460, 1461},
			Started: ago(3 * 24 * time.Hour), LastUsed: ago(51 * time.Hour), UsedKnown: true,
			Score: 85, Reasons: []string{"no windows", "unused 2d", "idle", "611M held"}},
		{PID: 9031, PPID: 1, Kind: KindApp, Name: "Preview", BundleID: "com.apple.Preview", Bundle: "/System/Applications/Preview.app",
			Comm: "/System/Applications/Preview.app/Contents/MacOS/Preview", RSS: mb(74), TreeRSS: mb(74), Hidden: true,
			Started: ago(30 * time.Hour), LastUsed: ago(9 * time.Hour), UsedKnown: true,
			Score: 75, Reasons: []string{"no windows", "hidden", "unused 9h", "idle"}},
		{PID: 7120, PPID: 1, Kind: KindOrphan, Name: "ssh -N -L 5432:db.internal:5432", Comm: "/usr/bin/ssh",
			Args: "ssh -N -L 5432:db.internal:5432 bastion", RSS: mb(6), TreeRSS: mb(6), Started: ago(6 * 24 * time.Hour),
			Ports: []int{5432},
			Score: 80, Reasons: []string{"orphaned (parent exited)", "listening :5432", "idle", "running 6d"}},
		{PID: 3366, PPID: 1, Kind: KindApp, Name: "zoom.us", BundleID: "us.zoom.xos", Bundle: "/Applications/zoom.us.app",
			Comm: "/Applications/zoom.us.app/Contents/MacOS/zoom.us", RSS: mb(233), TreeRSS: mb(233),
			Started: ago(8 * time.Hour), LastUsed: ago(5 * time.Hour), UsedKnown: true,
			Score: 63, Reasons: []string{"no windows", "unused 5h", "idle", "233M held"}},
		{PID: 61002, PPID: 1, Kind: KindOrphan, Name: "jupyter-lab", Comm: "/Users/you/.venv/bin/python",
			Args: "/Users/you/.venv/bin/python /Users/you/.venv/bin/jupyter-lab", RSS: mb(118), TreeRSS: mb(305),
			Started: ago(26 * time.Hour), Ports: []int{8888}, Children: []int{61010, 61011},
			Score: 62, Reasons: []string{"orphaned (parent exited)", "dev runtime", "listening :8888", "busy 31% CPU", "running 26h", "305M held"}},
		{PID: 2290, PPID: 1, Kind: KindHelper, Name: "com.docker.backend", Bundle: "/Applications/Docker.app",
			Comm: "/Applications/Docker.app/Contents/MacOS/com.docker.backend", RSS: mb(58), TreeRSS: mb(58),
			Started: ago(11 * 24 * time.Hour),
			Score:   55, Reasons: []string{"outlived Docker", "running 11d"}},
		{PID: 4187, PPID: 1, Kind: KindApp, Name: "Calculator", BundleID: "com.apple.calculator", Bundle: "/System/Applications/Calculator.app",
			Comm: "/System/Applications/Calculator.app/Contents/MacOS/Calculator", RSS: mb(41), TreeRSS: mb(41),
			Started: ago(2 * time.Hour), LastUsed: ago(2 * time.Hour),
			Score: 50, Reasons: []string{"no windows", "untouched since launch? (2h)", "idle"}},
		{PID: 5875, PPID: 1, Kind: KindApp, Name: "Notes", BundleID: "com.apple.Notes", Bundle: "/System/Applications/Notes.app",
			Comm: "/System/Applications/Notes.app/Contents/MacOS/Notes", RSS: mb(88), TreeRSS: mb(88), Windows: 1,
			Started: ago(5 * 24 * time.Hour), LastUsed: ago(31 * time.Hour), UsedKnown: true,
			Score: 35, Reasons: []string{"unused 31h", "idle"}},
	}
	var ts []*Target
	for _, t := range all {
		if !gone[t.PID] {
			ts = append(ts, t)
		}
	}
	return &Result{Targets: ts, Scanned: 734, At: now, Took: 142 * time.Millisecond}
}
