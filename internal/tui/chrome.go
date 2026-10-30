package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mikemich/appsassin/internal/scan"
)

// Shared pieces of the full-mode layout: top bar, column header, row lead,
// detail panel and keycaps.

var (
	panelBg  = lipgloss.AdaptiveColor{Light: "#ECE8EE", Dark: "#211D26"}
	keycapBg = lipgloss.AdaptiveColor{Light: "#E2DEE6", Dark: "#2C2833"}
	chipOnBg = lipgloss.AdaptiveColor{Light: "#D5F2E0", Dark: "#173323"}

	sColHead = lipgloss.NewStyle().Foreground(dim).Background(panelBg).Bold(true)
	sKeycap  = lipgloss.NewStyle().Foreground(text).Background(keycapBg).Bold(true).Padding(0, 1)
	sTabOn   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(crimson).Bold(true).Padding(0, 1)
	sTabOff  = lipgloss.NewStyle().Foreground(dim).Padding(0, 1)
	sChipOn  = lipgloss.NewStyle().Foreground(jade).Background(chipOnBg).Bold(true).Padding(0, 1)
	sChipOff = lipgloss.NewStyle().Foreground(dim).Padding(0, 1)
)

// rgb splits a "#RRGGBB" color.
func rgb(hex string) (r, g, b int64) {
	v, _ := strconv.ParseInt(strings.TrimPrefix(hex, "#"), 16, 32)
	return v >> 16 & 0xff, v >> 8 & 0xff, v & 0xff
}

// mix blends two "#RRGGBB" colors; t=0 is a, t=1 is b.
func mix(a, b string, t float64) lipgloss.Color {
	ar, ag, ab := rgb(a)
	br, bg, bb := rgb(b)
	l := func(x, y int64) int64 { return x + int64(float64(y-x)*t+0.5) }
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", l(ar, br), l(ag, bg), l(ab, bb)))
}

// gradient colors each rune of s from crimson to ember.
func gradient(s string, bold bool) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		t := 0.0
		if len(rs) > 1 {
			t = float64(i) / float64(len(rs)-1)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(mix(string(crimson), string(ember), t)).Bold(bold).Render(string(r)))
	}
	return b.String()
}

func logo() string { return gradient("⚔ appsassin", true) }

type view int

const (
	viewStrays view = iota
	viewEverything
	viewGraveyard
)

func (m *model) view() view {
	switch {
	case m.graveyard:
		return viewGraveyard
	case m.everything:
		return viewEverything
	}
	return viewStrays
}

// topBar is the first line of every full-mode view: logo, view tabs and
// status chips.
func (m *model) topBar(w int) string {
	tabs := []struct {
		v     view
		label string
		n     int
	}{
		{viewStrays, "Strays", len(m.rows)},
		{viewEverything, "Everything", len(m.rows)},
		{viewGraveyard, "Graveyard", len(m.graves)},
	}
	left := logo() + "  "
	for _, t := range tabs {
		if t.v == m.view() {
			label := t.label
			if t.n > 0 || t.v != viewGraveyard {
				label += " " + strconv.Itoa(t.n)
			}
			left += sTabOn.Render(label)
		} else {
			left += sTabOff.Render(t.label)
		}
	}

	right := sChipOff.Render("○ stealth")
	if m.stealthOn {
		right = sChipOn.Render("● stealth")
	}
	if m.kills > 0 {
		right = sAccent.Render(fmt.Sprintf("☠ %d", m.kills)) + " " + right
	}
	return spread(left, right, w)
}

// metaLine is the quiet second line under the top bar. Plain parts are
// dimmed; parts that bring their own styling keep it.
func metaLine(parts ...string) string {
	var keep []string
	for _, p := range parts {
		switch {
		case p == "":
		case strings.Contains(p, "\x1b["):
			keep = append(keep, p)
		default:
			keep = append(keep, sDim.Render(p))
		}
	}
	return strings.Join(keep, sFaint.Render(" · "))
}

// colHead renders the column titles on a tinted band the width of the screen.
func colHead(s string, w int) string {
	return sColHead.Render(fit(s, w))
}

// rowStyle tints a row's segments when it's the selected one. Every segment
// carries the background itself; wrapping a finished row would lose it at
// each inner reset.
func rowStyle(sel bool) func(lipgloss.Style) lipgloss.Style {
	return func(s lipgloss.Style) lipgloss.Style {
		if sel {
			return s.Background(rowBg)
		}
		return s
	}
}

// lead is the 4-cell start of a row: a cursor bar and a mark.
func lead(on func(lipgloss.Style) lipgloss.Style, sel bool, mark string) string {
	cur := on(sText).Render(" ")
	if sel {
		cur = on(sAccent).Render("▌")
	}
	return cur + on(sText).Render(" ") + mark + on(sText).Render(" ")
}

func markGlyph(on func(lipgloss.Style) lipgloss.Style, marked bool) string {
	if marked {
		return on(sAccent).Render("●")
	}
	return on(sFaint).Render("·")
}

// kindCell is a colored dot and the kind, w cells wide.
func kindCell(on func(lipgloss.Style) lipgloss.Style, k scan.Kind, w int) string {
	st := lipgloss.NewStyle().Foreground(kindColor(k))
	return on(st).Render("●") + on(sText).Render(" ") + on(st).Render(fit(string(k), w-2))
}

// finish pads a row out to the full width so the selection tint reaches the edge.
func finish(row string, w int, on func(lipgloss.Style) lipgloss.Style) string {
	if pad := w - lipgloss.Width(row); pad > 0 {
		row += on(sText).Render(strings.Repeat(" ", pad))
	}
	return row
}

// kv is one labelled line of a panel.
func kv(label, value string) string { return sDim.Render(fit(label, 6)) + value }

// panel draws a rounded box w cells wide with a title (and optional right-hand
// note) set into its top border, and exactly len(lines) lines of content.
func panel(w int, title, note string, lines []string) string {
	bc := sFaint
	inner := max(1, w-4)
	tw := lipgloss.Width(title)
	var top string
	if note != "" {
		// ╭─ title ─────── note ─╮
		if n := w - tw - lipgloss.Width(note) - 8; n >= 1 {
			top = bc.Render("╭─") + " " + title + " " + bc.Render(strings.Repeat("─", n)) + " " + note + " " + bc.Render("─╮")
		}
	}
	if top == "" {
		n := max(1, w-5-tw)
		top = bc.Render("╭─") + " " + title + " " + bc.Render(strings.Repeat("─", n)+"╮")
	}
	var b strings.Builder
	b.WriteString(ansi.Truncate(top, w, "") + "\n")
	for _, l := range lines {
		l = ansi.Truncate(l, inner, "…")
		b.WriteString(bc.Render("│") + " " + l + strings.Repeat(" ", max(0, inner-lipgloss.Width(l))) + " " + bc.Render("│") + "\n")
	}
	b.WriteString(bc.Render("╰" + strings.Repeat("─", max(0, w-2)) + "╯"))
	return b.String()
}

// key renders a key hint as a keycap and a description.
func key(k, desc string) string { return sKeycap.Render(k) + " " + sDim.Render(desc) }
