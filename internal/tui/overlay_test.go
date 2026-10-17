package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// The window frame is made of half-block glyphs on the outer edge of the
// cells, so no filled background sticks out beyond the visible border.
func TestOverlayFrameIsOuterHalfBlocks(t *testing.T) {
	m := &Model{Width: 80, Height: 30, Queue: []DownloadReq{{Type: "Audio"}}}
	for name, box := range map[string]string{"queue": m.renderQueueOverlay(), "errors": m.renderErrorsOverlay()} {
		lines := strings.Split(box, "\n")
		w := lipgloss.Width(lines[0])
		for i, l := range lines {
			if lipgloss.Width(l) != w {
				t.Errorf("%s: line %d is %d wide, want %d", name, i, lipgloss.Width(l), w)
			}
		}
		first, last := stripANSI(lines[0]), stripANSI(lines[len(lines)-1])
		if !strings.HasPrefix(first, "▛▀") || !strings.HasSuffix(first, "▀▜") {
			t.Errorf("%s: top edge = %q", name, first)
		}
		if !strings.HasPrefix(last, "▙▄") || !strings.HasSuffix(last, "▄▟") {
			t.Errorf("%s: bottom edge = %q", name, last)
		}
		mid := stripANSI(lines[1])
		if !strings.HasPrefix(mid, "▌") || !strings.HasSuffix(mid, "▐") {
			t.Errorf("%s: side edges = %q", name, mid)
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
