package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mikemich/appsassin/internal/graveyard"
	"github.com/mikemich/appsassin/internal/scan"
)

func (m *model) View() string {
	if m.mode == modeHelp {
		return m.helpView()
	}
	if m.graveyard {
		if m.minimal {
			return m.minimalGraveView()
		}
		return m.graveView()
	}
	if m.minimal {
		return m.minimalView()
	}
	if m.everything {
		return m.allView()
	}
	return m.fullView()
}

func (m *model) reclaimable() int64 {
	var b int64
	for _, t := range m.rows {
		b += t.TreeRSS
	}
	return b
}

func (m *model) stealthTag() string {
	if m.stealthOn {
		return sOK.Render("◉ stealth")
	}
	return sDim.Render("○ stealth")
}

// spread places left and right on one line of width w.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// scrollTo returns the offset that keeps cursor inside a window of h of n rows.
func scrollTo(cursor, offset, n, h int) int {
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+h {
		offset = cursor - h + 1
	}
	return max(0, min(offset, max(0, n-h)))
}

// scroll keeps the cursor inside the visible window.
func (m *model) scroll(h int) { m.offset = scrollTo(m.cursor, m.offset, len(m.rows), h) }

func (m *model) emptyLine() string {
	switch {
	case m.err != nil:
		return sErr.Render("scan failed: " + m.err.Error())
	case m.res == nil:
		return sDim.Render("casing the joint…")
	case m.filter != "":
		return sDim.Render("nothing matches “" + m.filter + "”")
	case m.everything:
		return sDim.Render("nothing of yours is running?")
	default:
		return sOK.Render("✓ all clear") + sDim.Render(" — nothing stray above threshold "+fmt.Sprint(m.threshold))
	}
}

func ageOf(t *scan.Target, now time.Time) string {
	return scan.Human(now.Sub(t.Started))
}

func (m *model) fullView() string {
	w := max(40, m.width)
	now := time.Now()
	var b strings.Builder

	left := sBadge.Render("⚔ APPSASSIN") + "  " + sDim.Render(fmt.Sprintf("%d targets · %s reclaimable", len(m.rows), scan.Bytes(m.reclaimable())))
	right := m.stealthTag()
	if m.kills > 0 {
		right = sAccent.Render(fmt.Sprintf("☠ %d", m.kills)) + sDim.Render(" · ") + right
	}
	b.WriteString(spread(left, right, w) + "\n")
	opts := fmt.Sprintf("threshold ≥%d · sort %s", m.threshold, m.sort)
	if m.showAll {
		opts = "showing everything · sort " + m.sort.String()
	}
	if m.filter != "" {
		opts += " · filter “" + m.filter + "”"
	}
	if m.res != nil {
		opts += fmt.Sprintf(" · %d procs scanned in %dms", m.res.Scanned, m.res.Took.Milliseconds())
	}
	b.WriteString(sFaint.Render(opts) + "\n")

	// Columns: cursor(2) mark(2) score(3) bar(5) name kind(7) mem(6) age(5) why
	nameW := max(12, min(34, (w-36)*2/5))
	whyW := max(0, w-36-nameW)
	b.WriteString(sDim.Render("    "+fitR("SCR", 3)+"       "+fit("TARGET", nameW)+" "+fit("KIND", 7)+" "+fitR("MEM", 6)+" "+fitR("AGE", 5)+"  "+fit("WHY", whyW)) + "\n")

	h := m.listHeight()
	m.scroll(h)
	lines := 0
	if len(m.rows) == 0 {
		b.WriteString("  " + m.emptyLine() + "\n")
		lines++
	}
	for i := m.offset; i < len(m.rows) && lines < h; i++ {
		t := m.rows[i]
		sel := i == m.cursor
		// Every segment carries the highlight itself; wrapping the finished row
		// would lose the background at each inner reset.
		on := func(s lipgloss.Style) lipgloss.Style {
			if sel {
				return s.Background(rowBg)
			}
			return s
		}
		sp := on(sText).Render(" ")
		cur, mark := on(sText).Render("  "), on(sFaint).Render("○ ")
		if sel {
			cur = on(sAccent).Render("▸ ")
		}
		if m.marked[t.PID] {
			mark = on(sAccent).Render("◉ ")
		}
		score := on(lipgloss.NewStyle().Foreground(scoreColor(t.Score)).Bold(true)).Render(fitR(fmt.Sprint(t.Score), 3))
		name := on(sText).Render(fit(t.Name, nameW))
		if sel {
			name = on(sBold).Render(fit(t.Name, nameW))
		}
		kind := on(lipgloss.NewStyle().Foreground(kindColor(t.Kind))).Render(fit(string(t.Kind), 7))
		row := cur + mark + score + sp + bar(t.Score, on) + sp + name + sp + kind + sp +
			on(sText).Render(fitR(scan.Bytes(t.TreeRSS), 6)) + sp + on(sDim).Render(fitR(ageOf(t, now), 5)) + sp + sp +
			on(sDim).Render(fit(strings.Join(t.Reasons, " · "), whyW))
		if pad := w - lipgloss.Width(row); pad > 0 {
			row += on(sText).Render(strings.Repeat(" ", pad))
		}
		b.WriteString(row + "\n")
		lines++
	}
	for ; lines < h; lines++ {
		b.WriteString("\n")
	}

	b.WriteString(m.detail(w, now) + "\n")
	b.WriteString(m.statusLine(w) + "\n")
	b.WriteString(m.helpLine(w))
	return b.String()
}

func (m *model) detail(w int, now time.Time) string {
	inner := w - 4
	lines := make([]string, 4)
	if t := m.current(); t != nil {
		mem := scan.Bytes(t.RSS)
		if len(t.Children) > 0 {
			mem += fmt.Sprintf(" (+%d children, %s total)", len(t.Children), scan.Bytes(t.TreeRSS))
		}
		lines[0] = sBold.Render(t.Name) + sDim.Render(fmt.Sprintf("  pid %d · ppid %d · started %s ago · cpu %.1f%% · %s", t.PID, t.PPID, scan.Human(now.Sub(t.Started)), t.CPU, mem))
		cmd := t.Args
		if cmd == "" {
			cmd = t.Comm
		}
		lines[1] = sText.Render(fit(cmd, inner))
		label := "why "
		if !t.Stray {
			label = "info "
		}
		lines[2] = sDim.Render(label) + sText.Render(fit(strings.Join(t.Reasons, " · "), inner-len(label)))
		switch t.Kind {
		case scan.KindMenu:
			lines[3] = sDim.Render(t.BundleID + " · menu-bar or background app · x asks it to quit")
		case scan.KindService:
			lines[3] = sDim.Render("started by launchd — if it's set to keep alive, it will come right back")
		case scan.KindProc:
			what := "x sends SIGTERM, then SIGKILL if it ignores that"
			if n := len(t.Children); n > 0 {
				what = fmt.Sprintf("x sends SIGTERM to it and its %d child process%s, then SIGKILL if they ignore it", n, map[bool]string{true: "", false: "es"}[n == 1])
			}
			lines[3] = sDim.Render(what)
		case scan.KindApp:
			used := "last used " + scan.Human(t.Idle(now)) + " ago"
			if m.res != nil && t.PID == m.res.FrontPID {
				used = "in front right now"
			} else if !t.UsedKnown {
				used = "not seen in front since appsassin started watching"
			}
			lines[3] = sDim.Render(fmt.Sprintf("%s · %d windows (%d on screen) · %s", t.BundleID, t.Windows, t.OnScreen, used))
		case scan.KindHelper:
			lines[3] = sDim.Render("belongs to " + t.Bundle + ", which is no longer running")
		default:
			lines[3] = sDim.Render("parent process is gone; adopted by launchd and nobody is watching it")
		}
	} else {
		lines[0] = sDim.Render("no target selected")
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, inner, "…")
	}
	return sBox.Width(w - 2).Render(strings.Join(lines, "\n"))
}

func (m *model) statusLine(w int) string {
	switch {
	case m.mode == modeConfirm:
		var mem int64
		for _, t := range m.pending {
			mem += t.TreeRSS
		}
		what := m.pending[0].Name
		if len(m.pending) > 1 {
			what = fmt.Sprintf("%d targets", len(m.pending))
		}
		verb := "Take out"
		if m.force {
			verb = "FORCE KILL"
		}
		return sConfirm.Render(fmt.Sprintf("%s %s (%s)?", verb, what, scan.Bytes(mem))) + sDim.Render("  y / n")
	case m.mode == modeStealthConfirm:
		st := m.cfg.Stealth
		apps := ""
		if st.Apps {
			apps = ", quits windowless apps unused " + scan.Human(st.AppIdleDur())
		}
		return sConfirm.Render("Engage stealth mode?") + sDim.Render(fmt.Sprintf(
			" y / n · silently kills strays ≥%d every %s%s", st.Threshold, scan.Human(st.IntervalDur()), apps))
	case m.mode == modeFilter:
		return sAccent.Render("/ ") + sText.Render(m.filter) + sAccent.Render("█") + sDim.Render("  enter keep · esc clear")
	case m.status != "":
		st := sOK
		if m.statusErr {
			st = sErr
		}
		return st.Render(ansi.Truncate(m.status, w, "…"))
	case len(m.marked) > 0:
		return sAccent.Render(fmt.Sprintf("%d marked", len(m.marked))) + sDim.Render(" · x to take them out · esc to clear")
	}
	return ""
}

func key(k, desc string) string { return sKey.Render(k) + " " + sDim.Render(desc) }

func (m *model) helpLine(w int) string {
	if m.everything {
		return keyLine(w,
			key("↑↓", "move"), key("/", "find"), key("x", "kill"), key("K", "force"), key("space", "mark"),
			key("tab", "strays"), key("s", "sort"), key("g", "graveyard"), key("m", "minimal"), key("?", "help"), key("q", "quit"),
		)
	}
	return keyLine(w,
		key("↑↓", "move"), key("space", "mark"), key("x", "kill"), key("K", "force"),
		key("tab", "everything"), key("/", "filter"), key("s", "sort"), key("i", "spare"), key("m", "minimal"),
		key("S", "stealth"), key("g", "graveyard"), key("?", "help"), key("q", "quit"),
	)
}

// keyLine joins key hints, dropping whatever doesn't fit in w.
func keyLine(w int, parts ...string) string {
	out := ""
	for _, p := range parts {
		next := out
		if next != "" {
			next += sFaint.Render("  ")
		}
		next += p
		if lipgloss.Width(next) > w {
			break
		}
		out = next
	}
	return out
}

func (m *model) minimalView() string {
	w := max(30, m.width)
	var b strings.Builder
	head := sAccent.Render("appsassin") + sDim.Render(fmt.Sprintf(" %d · %s", len(m.rows), scan.Bytes(m.reclaimable())))
	if m.everything {
		head = sAccent.Render("appsassin") + sDim.Render(fmt.Sprintf(" running %d · %s", len(m.rows), scan.Bytes(m.reclaimable())))
	}
	b.WriteString(spread(head, m.stealthTag(), w) + "\n")

	h := m.listHeight()
	m.scroll(h)
	lines := 0
	if len(m.rows) == 0 {
		b.WriteString(m.emptyLine() + "\n")
		lines++
	}
	for i := m.offset; i < len(m.rows) && lines < h; i++ {
		t := m.rows[i]
		cur := " "
		if i == m.cursor {
			cur = sAccent.Render("›")
		}
		mark := "  "
		if m.marked[t.PID] {
			mark = sAccent.Render("◉ ")
		}
		score := lipgloss.NewStyle().Foreground(scoreColor(t.Score)).Render(fitR(fmt.Sprint(t.Score), 2))
		why := ""
		if len(t.Reasons) > 0 {
			why = t.Reasons[0]
		}
		if m.everything {
			score = lipgloss.NewStyle().Foreground(kindColor(t.Kind)).Render(fit(kindShort(t.Kind), 2))
			why = fmt.Sprintf("%.0f%%", t.CPU)
			if len(t.Ports) > 0 {
				why += " · listening " + portsOf(t)
			}
		}
		nameW := max(10, min(28, w/3))
		row := cur + mark + score + " " + sText.Render(fit(t.Name, nameW)) + " " +
			sText.Render(fitR(scan.Bytes(t.TreeRSS), 6)) + "  " + sDim.Render(why)
		b.WriteString(ansi.Truncate(row, w, "…") + "\n")
		lines++
	}
	for ; lines < h; lines++ {
		b.WriteString("\n")
	}
	if s := m.statusLine(w); s != "" {
		b.WriteString(s)
	} else {
		other := "all"
		if m.everything {
			other = "strays"
		}
		b.WriteString(keyLine(w, key("x", "kill"), key("/", "find"), key("tab", other), key("g", "graves"), key("m", "full"), key("?", "help"), key("q", "quit")))
	}
	return b.String()
}

// kindShort is a two-letter kind tag for minimal mode.
func kindShort(k scan.Kind) string {
	switch k {
	case scan.KindApp:
		return "ap"
	case scan.KindMenu:
		return "mb"
	case scan.KindService:
		return "sv"
	case scan.KindHelper:
		return "hp"
	case scan.KindOrphan:
		return "or"
	}
	return "pr"
}

func portsOf(t *scan.Target) string {
	ps := make([]string, len(t.Ports))
	for i, p := range t.Ports {
		ps[i] = fmt.Sprintf(":%d", p)
	}
	return strings.Join(ps, " ")
}

// allView lists everything the user is running, so anything can be found and killed.
func (m *model) allView() string {
	w := max(40, m.width)
	now := time.Now()
	var b strings.Builder

	apps, procs := 0, 0
	for _, t := range m.rows {
		if t.Kind.IsApp() {
			apps++
		} else {
			procs++
		}
	}
	left := sBadge.Render("⚔ APPSASSIN") + "  " + sAccent.Render("▤ everything running") +
		sDim.Render(fmt.Sprintf("  %d app%s · %d process%s · %s", apps, plural(apps), procs,
			map[bool]string{true: "", false: "es"}[procs == 1], scan.Bytes(m.reclaimable())))
	right := m.stealthTag()
	if m.kills > 0 {
		right = sAccent.Render(fmt.Sprintf("☠ %d", m.kills)) + sDim.Render(" · ") + right
	}
	b.WriteString(spread(left, right, w) + "\n")
	opts := "sort " + m.sort.String() + " · / to find anything by name, pid or port · tab back to strays"
	if m.filter != "" {
		opts = "sort " + m.sort.String() + " · filter “" + m.filter + "” · esc clears"
	}
	b.WriteString(sFaint.Render(opts) + "\n")

	// Columns: cursor(2) mark(2) name kind(8) cpu(5) mem(6) age(5) info
	nameW := max(14, min(36, (w-34)*2/5))
	infoW := max(0, w-34-nameW)
	b.WriteString(sDim.Render("    "+fit("NAME", nameW)+" "+fit("KIND", 8)+" "+fitR("CPU", 5)+" "+fitR("MEM", 6)+" "+fitR("AGE", 5)+"  "+fit("INFO", infoW)) + "\n")

	h := m.listHeight()
	m.scroll(h)
	lines := 0
	if len(m.rows) == 0 {
		b.WriteString("  " + m.emptyLine() + "\n")
		lines++
	}
	for i := m.offset; i < len(m.rows) && lines < h; i++ {
		t := m.rows[i]
		sel := i == m.cursor
		on := func(s lipgloss.Style) lipgloss.Style {
			if sel {
				return s.Background(rowBg)
			}
			return s
		}
		sp := on(sText).Render(" ")
		cur, mark := on(sText).Render("  "), on(sFaint).Render("○ ")
		if sel {
			cur = on(sAccent).Render("▸ ")
		}
		if m.marked[t.PID] {
			mark = on(sAccent).Render("◉ ")
		}
		name := on(sText).Render(fit(t.Name, nameW))
		if sel {
			name = on(sBold).Render(fit(t.Name, nameW))
		}
		cpu := on(sDim).Render(fitR(fmt.Sprintf("%.0f%%", t.CPU), 5))
		if t.CPU >= 25 {
			cpu = on(lipgloss.NewStyle().Foreground(ember)).Render(fitR(fmt.Sprintf("%.0f%%", t.CPU), 5))
		}
		info := on(sDim).Render(fit(strings.Join(t.Reasons, " · "), infoW))
		if t.Stray && t.Score >= m.threshold {
			tag := fmt.Sprintf("stray %d · ", t.Score)
			tw := ansi.StringWidth(tag)
			info = on(lipgloss.NewStyle().Foreground(scoreColor(t.Score))).Render(fit(tag, min(tw, infoW))) +
				on(sDim).Render(fit(strings.Join(t.Reasons, " · "), max(0, infoW-tw)))
		}
		row := cur + mark + name + sp + on(lipgloss.NewStyle().Foreground(kindColor(t.Kind))).Render(fit(string(t.Kind), 8)) + sp +
			cpu + sp + on(sText).Render(fitR(scan.Bytes(t.TreeRSS), 6)) + sp + on(sDim).Render(fitR(ageOf(t, now), 5)) + sp + sp + info
		if pad := w - lipgloss.Width(row); pad > 0 {
			row += on(sText).Render(strings.Repeat(" ", pad))
		}
		b.WriteString(row + "\n")
		lines++
	}
	for ; lines < h; lines++ {
		b.WriteString("\n")
	}

	b.WriteString(m.detail(w, now) + "\n")
	b.WriteString(m.statusLine(w) + "\n")
	b.WriteString(m.helpLine(w))
	return b.String()
}

func (m *model) helpView() string {
	rows := [][2]string{
		{"↑↓ / j k", "move"},
		{"home / G", "top / bottom"},
		{"space", "mark target"},
		{"a", "mark all / none"},
		{"x", "take out marked (or current) — apps are asked to quit politely"},
		{"K", "force kill (SIGKILL, no save prompts)"},
		{"i", "spare: add current target to the ignore list forever"},
		{"tab", "switch between strays and everything running"},
		{"/", "find (by name, pid, port, path…)"},
		{"s", "cycle sort: score → memory → age → name → cpu"},
		{"+ / -", "raise / lower score threshold"},
		{"A", "show everything with any stray score"},
		{"m", "toggle minimal mode"},
		{"S", "toggle stealth mode (silent background sweeper)"},
		{"g", "graveyard: every kill, newest first · r revives the selected one"},
		{"r", "rescan now (revive, in the graveyard)"},
		{"esc", "clear marks and filter"},
		{"q", "quit"},
	}
	var b strings.Builder
	b.WriteString(sBadge.Render("⚔ APPSASSIN") + sDim.Render("  keys") + "\n\n")
	for _, r := range rows {
		b.WriteString("  " + sKey.Render(fit(r[0], 12)) + sDim.Render(r[1]) + "\n")
	}
	b.WriteString("\n" + sDim.Render("  Scores: orphaned processes, helpers that outlived their app, and windowless") + "\n")
	b.WriteString(sDim.Render("  apps nobody has touched rank highest. Busy processes rank lower.") + "\n\n")
	b.WriteString(sFaint.Render("  any key to return"))
	return b.String()
}

func killedAgo(e graveyard.Entry, now time.Time) string {
	return scan.Human(now.Sub(e.Killed)) + " ago"
}

func (m *model) revivedCount() int {
	n := 0
	for _, e := range m.graves {
		if e.Revived != nil {
			n++
		}
	}
	return n
}

func (m *model) graveEmpty() string {
	if m.graveErr != nil {
		return sErr.Render("couldn't read the graveyard: " + m.graveErr.Error())
	}
	return sDim.Render("the graveyard is empty — nothing has been killed yet")
}

func (m *model) graveView() string {
	w := max(40, m.width)
	now := time.Now()
	var b strings.Builder

	left := sBadge.Render("⚔ APPSASSIN") + "  " + sAccent.Render("☠ graveyard") +
		sDim.Render(fmt.Sprintf("  %d kill%s · %d revived", len(m.graves), plural(len(m.graves)), m.revivedCount()))
	b.WriteString(spread(left, m.stealthTag(), w) + "\n")
	sub := "newest first · r brings the selected one back · "
	if m.demo {
		sub += "demo graveyard"
	} else {
		sub += strings.Replace(graveyard.Path(), home(), "~", 1)
	}
	b.WriteString(sFaint.Render(sub) + "\n")

	// Columns: cursor(2) mark(2) killed(8) name kind(7) by(7) mem(6) command
	nameW := max(12, min(30, (w-38)*2/5))
	cmdW := max(0, w-38-nameW)
	b.WriteString(sDim.Render("    "+fit("KILLED", 8)+" "+fit("TARGET", nameW)+" "+fit("KIND", 7)+" "+fit("BY", 7)+" "+fitR("MEM", 6)+"  "+fit("COMMAND", cmdW)) + "\n")

	h := m.listHeight()
	m.goffset = scrollTo(m.gcursor, m.goffset, len(m.graves), h)
	lines := 0
	if len(m.graves) == 0 {
		b.WriteString("  " + m.graveEmpty() + "\n")
		lines++
	}
	for i := m.goffset; i < len(m.graves) && lines < h; i++ {
		e := m.graves[i]
		sel := i == m.gcursor
		on := func(s lipgloss.Style) lipgloss.Style {
			if sel {
				return s.Background(rowBg)
			}
			return s
		}
		sp := on(sText).Render(" ")
		cur, mark := on(sText).Render("  "), on(sFaint).Render("☠ ")
		if sel {
			cur = on(sAccent).Render("▸ ")
		}
		if e.Revived != nil {
			mark = on(sOK).Render("↺ ")
		}
		name := on(sText).Render(fit(e.Name, nameW))
		if sel {
			name = on(sBold).Render(fit(e.Name, nameW))
		}
		row := cur + mark + on(sDim).Render(fit(killedAgo(e, now), 8)) + sp + name + sp +
			on(lipgloss.NewStyle().Foreground(kindColor(e.Kind))).Render(fit(string(e.Kind), 7)) + sp +
			on(sDim).Render(fit(e.By, 7)) + sp + on(sText).Render(fitR(scan.Bytes(e.Mem), 6)) + sp + sp +
			on(sDim).Render(fit(e.What(), cmdW))
		if pad := w - lipgloss.Width(row); pad > 0 {
			row += on(sText).Render(strings.Repeat(" ", pad))
		}
		b.WriteString(row + "\n")
		lines++
	}
	for ; lines < h; lines++ {
		b.WriteString("\n")
	}

	b.WriteString(m.graveDetail(w, now) + "\n")
	b.WriteString(m.statusLine(w) + "\n")
	b.WriteString(keyLine(w, key("↑↓", "move"), key("r", "revive"), key("g", "back"), key("m", "minimal"), key("?", "help"), key("q", "quit")))
	return b.String()
}

func (m *model) graveDetail(w int, now time.Time) string {
	inner := w - 4
	lines := make([]string, 4)
	if e := m.currentGrave(); e != nil {
		lines[0] = sBold.Render(e.Name) + sDim.Render(fmt.Sprintf("  pid %d · killed %s by %s · score %d · %s",
			e.PID, killedAgo(*e, now), e.By, e.Score, scan.Bytes(e.Mem)))
		if e.Kind.IsApp() {
			lines[1] = sText.Render(fit(e.Bundle, inner))
			lines[2] = sDim.Render(e.BundleID + " · why " + strings.Join(e.Reasons, " · "))
		} else {
			cmd := e.Args
			if cmd == "" {
				cmd = e.Comm
			}
			lines[1] = sText.Render(fit(cmd, inner))
			where := "in " + e.Cwd
			if e.Cwd == "" {
				where = "working directory unknown"
			}
			if len(e.Ports) > 0 {
				ps := make([]string, len(e.Ports))
				for i, p := range e.Ports {
					ps[i] = fmt.Sprintf(":%d", p)
				}
				where += " · was listening " + strings.Join(ps, " ")
			}
			lines[2] = sDim.Render(where)
		}
		run := sDim.Render("r runs ") + sText.Render(graveyard.Command(*e))
		if e.Revived != nil {
			back := "↺ revived " + scan.Human(now.Sub(*e.Revived)) + " ago"
			if e.RevivedPID > 0 {
				back += fmt.Sprintf(" (pid %d)", e.RevivedPID)
			}
			run = sOK.Render(back) + sDim.Render(" · r again runs ") + sText.Render(graveyard.Command(*e))
		}
		lines[3] = run
	} else {
		lines[0] = sDim.Render("nothing selected")
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, inner, "…")
	}
	return sBox.Width(w - 2).Render(strings.Join(lines, "\n"))
}

func (m *model) minimalGraveView() string {
	w := max(30, m.width)
	now := time.Now()
	var b strings.Builder
	head := sAccent.Render("appsassin") + sDim.Render(fmt.Sprintf(" graveyard · %d", len(m.graves)))
	b.WriteString(spread(head, m.stealthTag(), w) + "\n")

	h := m.listHeight()
	m.goffset = scrollTo(m.gcursor, m.goffset, len(m.graves), h)
	lines := 0
	if len(m.graves) == 0 {
		b.WriteString(m.graveEmpty() + "\n")
		lines++
	}
	for i := m.goffset; i < len(m.graves) && lines < h; i++ {
		e := m.graves[i]
		cur := " "
		if i == m.gcursor {
			cur = sAccent.Render("›")
		}
		mark := "  "
		if e.Revived != nil {
			mark = sOK.Render("↺ ")
		}
		nameW := max(10, min(28, w/3))
		row := cur + mark + sDim.Render(fitR(scan.Human(now.Sub(e.Killed)), 4)) + " " + sText.Render(fit(e.Name, nameW)) + " " +
			lipgloss.NewStyle().Foreground(kindColor(e.Kind)).Render(fit(string(e.Kind), 6)) + " " + sDim.Render(e.By)
		b.WriteString(ansi.Truncate(row, w, "…") + "\n")
		lines++
	}
	for ; lines < h; lines++ {
		b.WriteString("\n")
	}
	if s := m.statusLine(w); s != "" {
		b.WriteString(s)
	} else {
		b.WriteString(keyLine(w, key("r", "revive"), key("g", "back"), key("m", "full"), key("?", "help"), key("q", "quit")))
	}
	return b.String()
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
