package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mikemich/appsassin/internal/scan"
)

var (
	crimson = lipgloss.Color("#E0245E")
	ember   = lipgloss.Color("#FF8C42")
	amber   = lipgloss.Color("#E5C07B")
	jade    = lipgloss.Color("#5FD787")
	sky     = lipgloss.Color("#61AFEF")
	violet  = lipgloss.Color("#C678DD")
	dim     = lipgloss.AdaptiveColor{Light: "#7A7A7A", Dark: "#7C7C86"}
	faint   = lipgloss.AdaptiveColor{Light: "#BDBDBD", Dark: "#4A4A55"}
	text    = lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#E4E4E4"}
	rowBg   = lipgloss.AdaptiveColor{Light: "#F2DDE3", Dark: "#2A1B20"}

	sBadge   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(crimson).Padding(0, 1)
	sDim     = lipgloss.NewStyle().Foreground(dim)
	sFaint   = lipgloss.NewStyle().Foreground(faint)
	sText    = lipgloss.NewStyle().Foreground(text)
	sBold    = lipgloss.NewStyle().Foreground(text).Bold(true)
	sAccent  = lipgloss.NewStyle().Foreground(crimson).Bold(true)
	sOK      = lipgloss.NewStyle().Foreground(jade)
	sErr     = lipgloss.NewStyle().Foreground(crimson)
	sKey     = lipgloss.NewStyle().Foreground(text).Bold(true)
	sConfirm = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(crimson).Padding(0, 1)
	sBox     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(faint).Padding(0, 1)
)

func scoreColor(s int) lipgloss.TerminalColor {
	switch {
	case s >= 70:
		return crimson
	case s >= 45:
		return ember
	default:
		return amber
	}
}

func kindColor(k scan.Kind) lipgloss.TerminalColor {
	switch k {
	case scan.KindApp:
		return sky
	case scan.KindHelper:
		return violet
	default:
		return ember
	}
}

// fit truncates or pads plain text to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = strings.ReplaceAll(s, "\n", " ")
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

// fitR right-aligns plain text in w cells.
func fitR(s string, w int) string {
	if ansi.StringWidth(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return strings.Repeat(" ", w-ansi.StringWidth(s)) + s
}

func bar(score int, on func(lipgloss.Style) lipgloss.Style) string {
	n := (score + 10) / 20
	return on(lipgloss.NewStyle().Foreground(scoreColor(score))).Render(strings.Repeat("▰", n)) +
		on(sFaint).Render(strings.Repeat("▱", 5-n))
}
