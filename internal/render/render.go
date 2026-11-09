// Package render turns a plan summary into terminal output: the counts line,
// the plan table and the DESTROY banner.
//
// Colour is decided once, by NewStyle, and every method degrades to plain text
// so CI logs stay readable.
package render

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ScaferuZ/tfw/internal/plan"
)

// ANSI escapes used across tfw output.
const (
	Reset  = "\033[0m"
	Red    = "\033[1;31m"
	Green  = "\033[1;32m"
	Yellow = "\033[1;33m"
	Cyan   = "\033[1;36m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	Banner = "\033[1;97;41m" // bold white on red
)

// BannerWidth is the total width of the red block, in cells.
const BannerWidth = 76

// Style carries the one decision that every method needs: colour or not.
type Style struct{ Enabled bool }

// NewStyle resolves a --color mode ("auto", "always", "never") against NO_COLOR,
// TERM=dumb and whether stdout is a terminal.
func NewStyle(mode string) Style {
	switch mode {
	case "always":
		return Style{Enabled: true}
	case "never":
		return Style{Enabled: false}
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return Style{Enabled: false}
	}
	return Style{Enabled: isTTY(os.Stdout)}
}

// Paint wraps text in an ANSI escape, or returns it unchanged.
func (s Style) Paint(code, text string) string {
	if !s.Enabled || text == "" {
		return text
	}
	return code + text + Reset
}

// Counts prints the one-line plan summary, e.g. "+2 add  ~1 change  -1 destroy".
func (s Style) Counts(w io.Writer, sum plan.Summary) {
	parts := []string{
		s.count(len(sum.Add), Green, "+%d add"),
		s.count(len(sum.Update), Yellow, "~%d change"),
		s.count(len(sum.Destroy), Red, "-%d destroy"),
		s.count(len(sum.Replace), Red, "-/+%d replace"),
		s.count(len(sum.Import), Cyan, "i%d import"),
		s.count(len(sum.Moved), Cyan, "->%d moved"),
	}
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		fmt.Fprintf(w, "%s No changes. Your infrastructure matches the configuration.\n", s.Paint(Green, "OK"))
		return
	}
	fmt.Fprintf(w, "%s %s\n", s.Paint(Bold, "Plan:"), strings.Join(kept, s.Paint(Dim, "  ")))
}

func (s Style) count(n int, colour, format string) string {
	if n == 0 {
		return ""
	}
	return s.Paint(colour, fmt.Sprintf(format, n))
}

// Table prints the plan grouped by change, most dangerous group first.
func (s Style) Table(w io.Writer, sum plan.Summary) error {
	const labelWidth = 14
	type group struct {
		label  string
		colour string
		addrs  []string
	}
	groups := []group{
		{"destroy", Red, sum.Destroy},
		{"replace", Red, sum.Replace},
		{"update", Yellow, sum.Update},
		{"add", Green, sum.Add},
		{"import", Cyan, sum.Import},
		{"moved", Cyan, sum.Moved},
	}

	var b strings.Builder
	b.WriteString(s.Paint(Bold, pad("CHANGE", labelWidth)) + s.Paint(Bold, "RESOURCE") + "\n")
	for _, g := range groups {
		for i, addr := range g.addrs {
			label := ""
			if i == 0 {
				label = fmt.Sprintf("%s (%d)", g.label, len(g.addrs))
			}
			b.WriteString(s.Paint(g.colour, pad(label, labelWidth)) + addr + "\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// Banner prints the big red block listing what is about to be deleted. It is
// printed even without colour (CI logs), because the warning matters more than
// the styling.
func (s Style) Banner(w io.Writer, sum plan.Summary, planPath string) {
	inner := BannerWidth - 5 // "██ " + inner + "██"
	rule := strings.Repeat("█", BannerWidth)

	rows := []string{"", "  !!  D E S T R O Y  !!", ""}
	if n := len(sum.Destroy); n > 0 {
		rows = append(rows, fmt.Sprintf("  %d resource(s) will be DELETED", n))
	}
	if n := len(sum.Replace); n > 0 {
		rows = append(rows, fmt.Sprintf("  %d resource(s) will be REPLACED (destroy + create)", n))
	}
	rows = append(rows, "")
	for _, addr := range sum.Deleted() {
		rows = append(rows, "  x "+truncate(addr, inner-6))
	}
	rows = append(rows, "", `  apply requires typing the word "destroy"`)

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(s.Paint(Banner, rule) + "\n")
	for _, row := range rows {
		b.WriteString(s.Paint(Banner, "██ "+pad(row, inner)+"██") + "\n")
	}
	b.WriteString(s.Paint(Banner, rule) + "\n")
	b.WriteString(s.Paint(Dim, "   plan file: "+planPath) + "\n")
	io.WriteString(w, b.String())
}

func pad(text string, width int) string {
	if n := len([]rune(text)); n >= width {
		return text
	}
	return text + strings.Repeat(" ", width-len([]rune(text)))
}

func truncate(text string, width int) string {
	if len([]rune(text)) <= width {
		return text
	}
	return string([]rune(text)[:width-3]) + "..."
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
