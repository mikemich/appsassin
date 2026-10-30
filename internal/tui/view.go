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
	if m.booting {
		return m.bootView()
	}
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
		return sOK.Render("● stealth")
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

// fill pads the list area so the panel and footer stay at the bottom.
func fill(b *strings.Builder, lines, h int) {
	for ; lines < h; lines++ {
		b.WriteString("\n")
	}
}

func (m *model) scanned() string {
	if m.res == nil {
		return ""
	}
	return fmt.Sprintf("%d procs in %dms", m.res.Scanned, m.res.Took.Milliseconds())
}

func (m *model) filterNote() string {
	if m.filter == "" {
		return ""
	}
	return sAccent.Render("filter “"+m.filter+"”") + sDim.Render(" esc clears")
}

func (m *model) fullView() string {
	w := max(40, m.width)
	now := time.Now()
	var b strings.Builder

	b.WriteString(m.topBar(w) + "\n")
	thr := fmt.Sprintf("score ≥%d", m.threshold)
	if m.showAll {
		thr = "every score"
	}
	b.WriteString(metaLine(sText.Render(scan.Bytes(m.reclaimable()))+sDim.Render(" reclaimable"), thr, "sort "+m.sort.String(), m.filterNote(), m.scanned()) + "\n")

	// Columns: lead(4) score(3) bar(5) name kind(9) mem(6) age(5) why
	const fixed = 4 + 3 + 1 + 5 + 1 + 1 + 9 + 1 + 6 + 1 + 5 + 2
	nameW := max(12, min(34, (w-fixed)*2/5))
	whyW := max(0, w-fixed-nameW)
	b.WriteString(colHead("    "+fitR("SCR", 3)+"       "+fit("TARGET", nameW)+" "+fit("KIND", 9)+" "+fitR("MEM", 6)+" "+fitR("AGE", 5)+"  WHY", w) + "\n")

	h := m.listHeight()
	m.scroll(h)
	lines := 0
	if len(m.rows) == 0 {
		b.WriteString("    " + m.emptyLine() + "\n")
		lines++
	}
	for i := m.offset; i < len(m.rows) && lines < h; i++ {
		t := m.rows[i]
		sel := i == m.cursor
		on := rowStyle(sel)
		sp := on(sText).Render(" ")
		score := on(lipgloss.NewStyle().Foreground(scoreColor(t.Score)).Bold(true)).Render(fitR(fmt.Sprint(t.Score), 3))
		name := on(sText).Render(fit(t.Name, nameW))
		if sel {
			name = on(sBold).Render(fit(t.Name, nameW))
		}
		row := lead(on, sel, markGlyph(on, m.marked[t.PID])) + score + sp + bar(t.Score, on) + sp + name + sp +
			kindCell(on, t.Kind, 9) + sp + on(sText).Render(fitR(scan.Bytes(t.TreeRSS), 6)) + sp +
			on(sDim).Render(fitR(ageOf(t, now), 5)) + sp + sp + why(t, whyW, on)
		b.WriteString(finish(row, w, on) + "\n")
		lines++
	}
	fill(&b, lines, h)

	b.WriteString(m.detail(w, now) + "\n")
	b.WriteString(m.statusLine(w) + "\n")
	b.WriteString(m.helpLine(w))
	return b.String()
}

func (m *model) detail(w int, now time.Time) string {
	t := m.current()
	if t == nil {
		return panel(w, sDim.Render("nothing selected"), "", make([]string, 4))
	}
	title := sBold.Render(t.Name) + " " + lipgloss.NewStyle().Foreground(kindColor(t.Kind)).Render(string(t.Kind))
	note := sDim.Render(scan.Bytes(t.TreeRSS) + " · " + scan.Human(now.Sub(t.Started)))
	if t.Stray && t.Score > 0 {
		note = lipgloss.NewStyle().Foreground(scoreColor(t.Score)).Bold(true).Render(fmt.Sprintf("score %d", t.Score)) +
			sDim.Render(" · ") + note
	}

	cmd := t.Args
	if cmd == "" {
		cmd = t.Comm
	}
	label := "why"
	if !t.Stray {
		label = "info"
	}
	proc := fmt.Sprintf("%d · ppid %d · cpu %.1f%% · %s", t.PID, t.PPID, t.CPU, scan.Bytes(t.RSS))
	if len(t.Children) > 0 {
		proc += fmt.Sprintf(" + %d child%s (%s total)", len(t.Children), map[bool]string{true: "", false: "ren"}[len(t.Children) == 1], scan.Bytes(t.TreeRSS))
	}

	var extra string
	switch t.Kind {
	case scan.KindMenu:
		extra = t.BundleID + " · menu-bar or background app · x asks it to quit"
	case scan.KindService:
		extra = "started by launchd — if it's set to keep alive, it will come right back"
	case scan.KindProc:
		extra = "x sends SIGTERM, then SIGKILL if it ignores that"
		if n := len(t.Children); n > 0 {
			extra = fmt.Sprintf("x sends SIGTERM to it and its %d child process%s, then SIGKILL if they ignore it", n, map[bool]string{true: "", false: "es"}[n == 1])
		}
	case scan.KindApp:
		used := "last used " + scan.Human(t.Idle(now)) + " ago"
		if m.res != nil && t.PID == m.res.FrontPID {
			used = "in front right now"
		} else if !t.UsedKnown {
			used = "not seen in front since appsassin started watching"
		}
		extra = fmt.Sprintf("%s · %d windows (%d on screen) · %s", t.BundleID, t.Windows, t.OnScreen, used)
	case scan.KindHelper:
		extra = "belongs to " + t.Bundle + ", which is no longer running"
	default:
		extra = "parent process is gone; adopted by launchd and nobody is watching it"
	}
	noteLine := kv("note", sDim.Render(extra))
	if t.Runaway != "" {
		noteLine = kv("alert", m.runawayLine(t))
	}

	return panel(w, title, note, []string{
		kv("cmd", sText.Render(cmd)),
		kv(label, sText.Render(strings.Join(t.Reasons, sDim.Render(" · ")))),
		kv("pid", sDim.Render(proc)),
		noteLine,
	})
}

// runawayLine says what makes t a runaway and what stealth mode will do about it.
func (m *model) runawayLine(t *scan.Target) string {
	r := m.cfg.Runaway
	what := "stealth mode won't kill it (runaway.kill is off)"
	switch {
	case t.Kind == scan.KindService:
		what = "a launchd job, so stealth mode won't kill it (launchd would restart it)"
	case m.res != nil && t.PID == m.res.FrontPID:
		what = "in front, so stealth mode won't touch it"
	case r.Kill && t.Kind.IsApp():
		what = "stealth mode will ask it to quit"
	case r.Kill:
		what = "stealth mode will kill it"
	}
	return sRunaway.Render("runaway") + sDim.Render(" · "+r.Describe()+" · "+what)
}

// why renders t's reasons in w cells, with a runaway's reason picked out.
func why(t *scan.Target, w int, on func(lipgloss.Style) lipgloss.Style) string {
	if t.Runaway == "" || len(t.Reasons) == 0 || t.Reasons[0] != t.Runaway {
		return on(sDim).Render(fit(strings.Join(t.Reasons, " · "), w))
	}
	hw := min(ansi.StringWidth(t.Runaway), w)
	rest := strings.Join(t.Reasons[1:], " · ")
	if rest != "" {
		rest = " · " + rest
	}
	return on(sRunaway).Render(fit(t.Runaway, hw)) + on(sDim).Render(fit(rest, w-hw))
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
		verb := "☠ Take out"
		if m.force {
			verb = "☠ FORCE KILL"
		}
		return sConfirm.Render(fmt.Sprintf("%s %s (%s)?", verb, what, scan.Bytes(mem))) + "  " + key("y", "yes") + "  " + key("n", "no")
	case m.mode == modeStealthConfirm:
		st := m.cfg.Stealth
		apps := ""
		if st.Apps {
			apps = ", quits windowless apps unused " + scan.Human(st.AppIdleDur())
		}
		if r := m.cfg.Runaway; r.Kill && r.Enabled() {
			apps += ", and runaways"
		}
		return sConfirm.Render("Engage stealth mode?") + "  " + key("y", "yes") + "  " + key("n", "no") + sDim.Render(fmt.Sprintf(
			" · silently kills strays ≥%d every %s%s", st.Threshold, scan.Human(st.IntervalDur()), apps))
	case m.mode == modeFilter:
		return sKeycap.Render("/") + " " + sText.Render(m.filter) + sAccent.Render("▏") + sDim.Render("  enter keeps · esc clears")
	case m.status != "":
		st := sOK
		if m.statusErr {
			st = sErr
		}
		return st.Render(ansi.Truncate(m.status, w, "…"))
	case len(m.marked) > 0:
		return sAccent.Render(fmt.Sprintf("● %d marked", len(m.marked))) + "  " + key("x", "take them out") + "  " + key("esc", "clear")
	}
	return ""
}

func (m *model) helpLine(w int) string {
	if m.everything {
		return keyLine(w,
			key("↑↓", "move"), key("/", "find"), key("x", "kill"), key("K", "force"), key("space", "mark"),
			key("tab", "strays"), key("s", "sort"), key("g", "graveyard"), key("m", "minimal"), key("?", "help"), key("q", "quit"),
		)
	}
	return keyLine(w,
		key("↑↓", "move"), key("space", "mark"), key("x", "kill"), key("K", "force"),
		key("tab", "everything"), key("/", "filter"), key("s", "sort"), key("i", "spare"),
		key("S", "stealth"), key("m", "minimal"), key("g", "graveyard"), key("?", "help"), key("q", "quit"),
	)
}

// keyLine joins key hints, dropping whatever doesn't fit in w.
func keyLine(w int, parts ...string) string {
	out := ""
	for _, p := range parts {
		next := out
		if next != "" {
			next += "  "
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
	head := logo() + sDim.Render(fmt.Sprintf("  %d strays · %s", len(m.rows), scan.Bytes(m.reclaimable())))
	if m.everything {
		head = logo() + sDim.Render(fmt.Sprintf("  %d running · %s", len(m.rows), scan.Bytes(m.reclaimable())))
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
			cur = sAccent.Render("▌")
		}
		mark := sFaint.Render("·") + " "
		if m.marked[t.PID] {
			mark = sAccent.Render("●") + " "
		}
		score := lipgloss.NewStyle().Foreground(scoreColor(t.Score)).Bold(true).Render(fitR(fmt.Sprint(t.Score), 2))
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
		whyS := sDim
		if t.Runaway != "" {
			why, whyS = t.Runaway, sRunaway
		}
		nameW := max(10, min(28, w/3))
		name := sText.Render(fit(t.Name, nameW))
		if i == m.cursor {
			name = sBold.Render(fit(t.Name, nameW))
		}
		row := cur + " " + mark + score + " " + name + " " +
			sText.Render(fitR(scan.Bytes(t.TreeRSS), 6)) + "  " + whyS.Render(why)
		b.WriteString(ansi.Truncate(row, w, "…") + "\n")
		lines++
	}
	fill(&b, lines, h)
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
	b.WriteString(m.topBar(w) + "\n")
	hint := sDim.Render("press ") + sText.Render("/") + sDim.Render(" to find anything by name, pid or :port")
	if m.filter != "" {
		hint = m.filterNote()
	}
	b.WriteString(metaLine(
		fmt.Sprintf("%d app%s", apps, plural(apps)),
		fmt.Sprintf("%d process%s", procs, map[bool]string{true: "", false: "es"}[procs == 1]),
		sText.Render(scan.Bytes(m.reclaimable()))+sDim.Render(" in use"),
		"sort "+m.sort.String(), hint) + "\n")

	// Columns: lead(4) name kind(9) cpu(5) mem(6) age(5) info
	const fixed = 4 + 1 + 9 + 1 + 5 + 1 + 6 + 1 + 5 + 2
	nameW := max(14, min(36, (w-fixed)*2/5))
	infoW := max(0, w-fixed-nameW)
	b.WriteString(colHead("    "+fit("NAME", nameW)+" "+fit("KIND", 9)+" "+fitR("CPU", 5)+" "+fitR("MEM", 6)+" "+fitR("AGE", 5)+"  INFO", w) + "\n")

	h := m.listHeight()
	m.scroll(h)
	lines := 0
	if len(m.rows) == 0 {
		b.WriteString("    " + m.emptyLine() + "\n")
		lines++
	}
	for i := m.offset; i < len(m.rows) && lines < h; i++ {
		t := m.rows[i]
		sel := i == m.cursor
		on := rowStyle(sel)
		sp := on(sText).Render(" ")
		name := on(sText).Render(fit(t.Name, nameW))
		if sel {
			name = on(sBold).Render(fit(t.Name, nameW))
		}
		cpu := on(sDim).Render(fitR(fmt.Sprintf("%.0f%%", t.CPU), 5))
		if t.CPU >= 25 {
			cpu = on(lipgloss.NewStyle().Foreground(ember)).Render(fitR(fmt.Sprintf("%.0f%%", t.CPU), 5))
		}
		info := why(t, infoW, on)
		if t.Stray && t.Score >= m.threshold {
			tag := fmt.Sprintf("stray %d · ", t.Score)
			tw := ansi.StringWidth(tag)
			info = on(lipgloss.NewStyle().Foreground(scoreColor(t.Score)).Bold(true)).Render(fit(tag, min(tw, infoW))) +
				why(t, max(0, infoW-tw), on)
		}
		row := lead(on, sel, markGlyph(on, m.marked[t.PID])) + name + sp + kindCell(on, t.Kind, 9) + sp +
			cpu + sp + on(sText).Render(fitR(scan.Bytes(t.TreeRSS), 6)) + sp + on(sDim).Render(fitR(ageOf(t, now), 5)) + sp + sp + info
		b.WriteString(finish(row, w, on) + "\n")
		lines++
	}
	fill(&b, lines, h)

	b.WriteString(m.detail(w, now) + "\n")
	b.WriteString(m.statusLine(w) + "\n")
	b.WriteString(m.helpLine(w))
	return b.String()
}

func (m *model) helpView() string {
	sections := []struct {
		title string
		rows  [][2]string
	}{
		{"move & mark", [][2]string{
			{"↑↓  j k", "move"},
			{"home  G", "top / bottom"},
			{"space", "mark target"},
			{"a", "mark all / none"},
			{"esc", "clear marks and filter"},
		}},
		{"take out", [][2]string{
			{"x", "take out marked (or current) — apps are asked to quit politely"},
			{"K", "force kill (SIGKILL, no save prompts)"},
			{"i", "spare: add current target to the ignore list forever"},
			{"g", "graveyard: every kill, newest first · r revives the selected one"},
		}},
		{"look around", [][2]string{
			{"tab", "switch between strays and everything running"},
			{"/", "find (by name, pid, port, path…)"},
			{"s", "cycle sort: score → memory → age → name → cpu"},
			{"+  -", "raise / lower score threshold"},
			{"A", "show everything with any stray score"},
			{"m", "toggle minimal mode"},
			{"S", "toggle stealth mode (silent background sweeper)"},
			{"r", "rescan now"},
			{"q", "quit"},
		}},
	}
	lines := []string{logo() + sDim.Render("  keys")}
	for _, s := range sections {
		lines = append(lines, "", sAccent.Render("  "+s.title))
		for _, r := range s.rows {
			var caps []string
			for _, k := range strings.Fields(r[0]) {
				caps = append(caps, sKeycap.Render(k))
			}
			c := strings.Join(caps, " ")
			lines = append(lines, "    "+c+strings.Repeat(" ", max(1, 14-lipgloss.Width(c)))+sDim.Render(r[1]))
		}
	}
	about := []string{
		"",
		sDim.Render("  Orphaned processes, helpers that outlived their app, and windowless apps nobody"),
		sDim.Render("  has touched score highest. Busy processes score lower, unless they're runaways:"),
		sDim.Render("  " + m.cfg.Runaway.Describe() + "."),
	}
	if len(lines)+len(about)+2 <= m.height {
		lines = append(lines, about...) // only when it fits; the keys matter more
	}
	lines = append(lines, "", sFaint.Render("  any key to return"))
	return strings.Join(lines, "\n")
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

	b.WriteString(m.topBar(w) + "\n")
	where := "demo graveyard"
	if !m.demo {
		where = strings.Replace(graveyard.Path(), home(), "~", 1)
	}
	b.WriteString(metaLine(
		fmt.Sprintf("%d kill%s", len(m.graves), plural(len(m.graves))),
		sOK.Render(fmt.Sprintf("%d revived", m.revivedCount())),
		"newest first", sText.Render("r")+sDim.Render(" brings one back"), sFaint.Render(where)) + "\n")

	// Columns: lead(4) killed(8) name kind(9) by(7) mem(6) command
	const fixed = 4 + 8 + 1 + 1 + 9 + 1 + 7 + 1 + 6 + 2
	nameW := max(12, min(30, (w-fixed)*2/5))
	cmdW := max(0, w-fixed-nameW)
	b.WriteString(colHead("    "+fit("KILLED", 8)+" "+fit("TARGET", nameW)+" "+fit("KIND", 9)+" "+fit("BY", 7)+" "+fitR("MEM", 6)+"  COMMAND", w) + "\n")

	h := m.listHeight()
	m.goffset = scrollTo(m.gcursor, m.goffset, len(m.graves), h)
	lines := 0
	if len(m.graves) == 0 {
		b.WriteString("    " + m.graveEmpty() + "\n")
		lines++
	}
	for i := m.goffset; i < len(m.graves) && lines < h; i++ {
		e := m.graves[i]
		sel := i == m.gcursor
		on := rowStyle(sel)
		sp := on(sText).Render(" ")
		mark := on(sFaint).Render("☠")
		if e.Revived != nil {
			mark = on(sOK).Render("↺")
		}
		name := on(sText).Render(fit(e.Name, nameW))
		if sel {
			name = on(sBold).Render(fit(e.Name, nameW))
		}
		row := lead(on, sel, mark) + on(sDim).Render(fit(killedAgo(e, now), 8)) + sp + name + sp +
			kindCell(on, e.Kind, 9) + sp + on(sDim).Render(fit(e.By, 7)) + sp + on(sText).Render(fitR(scan.Bytes(e.Mem), 6)) + sp + sp +
			on(sDim).Render(fit(e.What(), cmdW))
		b.WriteString(finish(row, w, on) + "\n")
		lines++
	}
	fill(&b, lines, h)

	b.WriteString(m.graveDetail(w, now) + "\n")
	b.WriteString(m.statusLine(w) + "\n")
	b.WriteString(keyLine(w, key("↑↓", "move"), key("r", "revive"), key("g", "back"), key("m", "minimal"), key("?", "help"), key("q", "quit")))
	return b.String()
}

func (m *model) graveDetail(w int, now time.Time) string {
	e := m.currentGrave()
	if e == nil {
		return panel(w, sDim.Render("nothing selected"), "", make([]string, 4))
	}
	title := sBold.Render(e.Name) + " " + lipgloss.NewStyle().Foreground(kindColor(e.Kind)).Render(string(e.Kind))
	note := sDim.Render(fmt.Sprintf("killed %s by %s · %s", killedAgo(*e, now), e.By, scan.Bytes(e.Mem)))

	var l1, l2 string
	if e.Kind.IsApp() {
		l1 = kv("app", sText.Render(e.Bundle))
		l2 = kv("id", sDim.Render(e.BundleID))
	} else {
		cmd := e.Args
		if cmd == "" {
			cmd = e.Comm
		}
		l1 = kv("cmd", sText.Render(cmd))
		where := e.Cwd
		if where == "" {
			where = "working directory unknown"
		}
		if len(e.Ports) > 0 {
			ps := make([]string, len(e.Ports))
			for i, p := range e.Ports {
				ps[i] = fmt.Sprintf(":%d", p)
			}
			where += " · was listening " + strings.Join(ps, " ")
		}
		l2 = kv("dir", sDim.Render(where))
	}
	why := fmt.Sprintf("score %d · %s", e.Score, strings.Join(e.Reasons, " · "))
	run := sDim.Render("r runs ") + sText.Render(graveyard.Command(*e))
	if graveyard.Command(*e) == "" {
		run = sDim.Render("can't be revived: no command line was recorded")
	}
	if e.Revived != nil {
		back := "↺ revived " + scan.Human(now.Sub(*e.Revived)) + " ago"
		if e.RevivedPID > 0 {
			back += fmt.Sprintf(" (pid %d)", e.RevivedPID)
		}
		run = sOK.Render(back) + sDim.Render(" · r again runs ") + sText.Render(graveyard.Command(*e))
	}
	return panel(w, title, note, []string{l1, l2, kv("why", sDim.Render(why)), kv("undo", run)})
}

func (m *model) minimalGraveView() string {
	w := max(30, m.width)
	now := time.Now()
	var b strings.Builder
	head := logo() + sDim.Render(fmt.Sprintf("  graveyard · %d", len(m.graves)))
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
			cur = sAccent.Render("▌")
		}
		mark := sFaint.Render("☠") + " "
		if e.Revived != nil {
			mark = sOK.Render("↺") + " "
		}
		nameW := max(10, min(28, w/3))
		row := cur + " " + mark + sDim.Render(fitR(scan.Human(now.Sub(e.Killed)), 4)) + " " + sText.Render(fit(e.Name, nameW)) + " " +
			lipgloss.NewStyle().Foreground(kindColor(e.Kind)).Render(fit(string(e.Kind), 7)) + " " + sDim.Render(e.By)
		b.WriteString(ansi.Truncate(row, w, "…") + "\n")
		lines++
	}
	fill(&b, lines, h)
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
