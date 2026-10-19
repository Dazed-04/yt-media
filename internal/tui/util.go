package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// maxNameLines caps how many lines a long title may take in the info box.
const maxNameLines = 4

// wrapLines wraps value to width and keeps at most maxLines lines, ending the
// last one with "..." when text was cut.
func wrapLines(value string, width, maxLines int) []string {
	if width < 1 {
		width = 1
	}
	rendered := lipgloss.NewStyle().Width(width).Render(value)
	lines := strings.Split(rendered, "\n")
	if len(lines) <= maxLines {
		return lines
	}
	lines = lines[:maxLines]
	lines[maxLines-1] = safeTruncate(lines[maxLines-1], width-1) + "..."
	return lines
}

// safeTruncate cuts s to at most maxLen terminal columns. Wide characters
// (CJK, full-width brackets, many emoji) take two columns, so counting runes
// would let such titles overflow their line.
func safeTruncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxLen {
		return s
	}
	cols := 0
	var out []rune
	for _, r := range s {
		w := lipgloss.Width(string(r))
		if cols+w > maxLen {
			break
		}
		out = append(out, r)
		cols += w
	}
	return string(out)
}

// padRight pads s with spaces to width columns (display width, not runes).
func padRight(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// clampIdx keeps i inside [0, n-1] (0 when the list is empty).
func clampIdx(i, n int) int {
	if n <= 0 {
		return 0
	}
	return min(max(i, 0), n-1)
}

// firstNonEmpty returns the first argument that isn't "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
