package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mikemich/appsassin/internal/scan"
)

// The boot animation plays while the first scan runs, so it costs nothing:
// the wordmark is sliced in by a blade, a cut line runs under it, and the
// tagline settles. It ends as soon as both the animation and the scan are
// done, and any key skips it.

const (
	bootFPS    = 60
	bootReveal = 380 * time.Millisecond // blade crosses the wordmark
	bootCut    = 520 * time.Millisecond // cut line finished
	bootLen    = 640 * time.Millisecond // shortest the whole thing runs
	bootMax    = 4 * time.Second        // stop waiting for a slow scan
)

// The wordmark comes in two sizes; the small one is for narrow terminals.
var (
	wordmarkSmall = []string{
		"▄▀█ █▀█ █▀█ █▀ ▄▀█ █▀ █▀ █ █▄ █",
		"█▀█ █▀▀ █▀▀ ▄█ █▀█ ▄█ ▄█ █ █ ▀█",
	}
	wordmarkBig = bigWord("APPSASSIN")
)

var bigLetters = map[rune][6]string{
	'A': {" █████╗ ", "██╔══██╗", "███████║", "██╔══██║", "██║  ██║", "╚═╝  ╚═╝"},
	'P': {"██████╗ ", "██╔══██╗", "██████╔╝", "██╔═══╝ ", "██║     ", "╚═╝     "},
	'S': {"███████╗", "██╔════╝", "███████╗", "╚════██║", "███████║", "╚══════╝"},
	'I': {"██╗", "██║", "██║", "██║", "██║", "╚═╝"},
	'N': {"███╗   ██╗", "████╗  ██║", "██╔██╗ ██║", "██║╚██╗██║", "██║ ╚████║", "╚═╝  ╚═══╝"},
}

func bigWord(w string) []string {
	rows := make([]string, 6)
	for _, r := range w {
		for i := range rows {
			rows[i] += bigLetters[r][i]
		}
	}
	return rows
}

// wordmark picks the size that fits the terminal.
func (m *model) wordmark() []string {
	if m.width >= 78 && m.height >= 16 {
		return wordmarkBig
	}
	return wordmarkSmall
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type bootTickMsg time.Time

func bootTick() tea.Cmd {
	return tea.Tick(time.Second/bootFPS, func(t time.Time) tea.Msg { return bootTickMsg(t) })
}

// bootFits reports whether the terminal is big enough to bother.
func (m *model) bootFits() bool { return m.width >= 40 && m.height >= 10 }

// bootDone reports whether the animation can hand over to the hit list.
func (m *model) bootDone(now time.Time) bool {
	e := now.Sub(m.bootStart)
	return e >= bootMax || (e >= bootLen && (m.res != nil || m.err != nil))
}

func easeOut(t float64) float64 { return 1 - math.Pow(1-min(max(t, 0), 1), 3) }

func (m *model) bootView() string {
	e := time.Since(m.bootStart)
	word := m.wordmark()
	n := len([]rune(word[0]))
	front := int(easeOut(float64(e)/float64(bootReveal)) * float64(n+3))

	lines := make([]string, len(word))
	for r := range word {
		var b strings.Builder
		for i, ch := range []rune(word[r]) {
			switch {
			case i >= front || ch == ' ':
				b.WriteRune(' ')
			case i >= front-3 && e < bootReveal:
				// The blade's edge: white-hot where it's cutting.
				b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true).Render(string(ch)))
			default:
				c := mix(string(crimson), string(ember), float64(i)/float64(n-1))
				if ch != '█' && ch != '▄' && ch != '▀' {
					c = mix(string(c), "#2A1520", 0.55) // the big font's outline is its drop shadow
				}
				b.WriteString(lipgloss.NewStyle().Foreground(c).Bold(true).Render(string(ch)))
			}
		}
		lines[r] = b.String()
	}

	// The cut: a thin line that races out under the wordmark, blade first.
	cutT := easeOut(float64(e-bootReveal/2) / float64(bootCut-bootReveal/2))
	cutN := int(cutT * float64(n))
	cut := ""
	if cutN > 0 {
		cut = gradient(strings.Repeat("━", cutN), false)
		if cutN < n {
			cut += sBold.Render("╸")
		}
	}
	cut += strings.Repeat(" ", max(0, n-lipgloss.Width(cut)))

	tag := "finds what your Mac forgot · takes it out"
	tagLine := strings.Repeat(" ", len([]rune(tag)))
	if e > bootCut-120*time.Millisecond {
		st := sFaint
		if e > bootCut {
			st = sDim
		}
		tagLine = st.Render(tag)
	}

	status := sFaint.Render(spinner[int(e/(80*time.Millisecond))%len(spinner)] + " casing the joint…")
	switch {
	case m.err != nil:
		status = sErr.Render("scan failed: " + m.err.Error())
	case m.res != nil:
		found := fmt.Sprintf("%d strays", len(m.rows))
		if m.everything {
			found = fmt.Sprintf("%d running", len(m.rows))
		}
		status = sOK.Render("✓ ") + sDim.Render(fmt.Sprintf("%d processes · ", m.res.Scanned)) + sText.Render(found) +
			sDim.Render(" · "+scan.Bytes(m.reclaimable()))
	}

	parts := append(lines, cut, "", tagLine, "", status)
	block := lipgloss.JoinVertical(lipgloss.Center, parts...)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
}
