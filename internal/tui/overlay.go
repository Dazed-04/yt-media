package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The two popup windows: the download queue (w) and the error list (e).
// This file holds their state changes, key handling and rendering.

// ---------- state ----------

func (m *Model) overlayOpen() bool {
	return m.QueueOpen || m.ErrorsOpen
}

func (m *Model) showQueue() {
	m.ErrorsOpen = false
	m.QueueOpen = true
	m.QueueCursor = clampIdx(m.QueueCursor, len(m.Failed)+len(m.Queue))
}

func (m *Model) showErrors() {
	m.QueueOpen = false
	m.ErrorsOpen = true
	m.ErrorCursor = clampIdx(m.ErrorCursor, len(m.Failed))
}

// ---------- keys: queue window ----------

func (m *Model) handleQueueKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	nf := len(m.Failed)
	n := nf + len(m.Queue) // one cursor over failed items, then staged items
	switch msg.String() {
	case "esc", "q", "w":
		m.QueueOpen = false
	case "tab", "e":
		m.showErrors()
	case "up", "k":
		if n > 0 {
			m.QueueCursor = (clampIdx(m.QueueCursor, n) - 1 + n) % n
		}
	case "down", "j":
		if n > 0 {
			m.QueueCursor = (clampIdx(m.QueueCursor, n) + 1) % n
		}
	case "d", "x", "delete":
		if n > 0 {
			c := clampIdx(m.QueueCursor, n)
			if c < nf {
				m.removeFailed(c)
			} else {
				m.removeFromQueue(c - nf)
			}
			m.QueueCursor = clampIdx(m.QueueCursor, len(m.Failed)+len(m.Queue))
		}
	case "c":
		m.Queue = nil
		m.QueueCursor = 0
		m.Failed = nil
		m.ErrorCursor = 0
		m.persistQueue()
	case "t":
		if qi := clampIdx(m.QueueCursor, n) - nf; qi >= 0 && qi < len(m.Queue) {
			q := &m.Queue[qi]
			if q.Type == "Audio" {
				q.Type = "Video"
			} else {
				q.Type = "Audio"
			}
			m.persistQueue()
		}
	case "r":
		return m, m.retryFailed()
	case "enter":
		return m, m.startQueue()
	}
	return m, nil
}

// ---------- keys: error window ----------

func (m *Model) handleErrorsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	n := len(m.Failed)
	switch msg.String() {
	case "esc", "q", "e":
		if m.ErrorExpanded { // first esc/q collapses the detail, second closes
			m.ErrorExpanded = false
		} else {
			m.ErrorsOpen = false
		}
	case "tab", "w":
		m.ErrorExpanded = false
		m.showQueue()
	case "enter":
		if n > 0 {
			m.ErrorExpanded = !m.ErrorExpanded
		}
	case "up", "k":
		m.ErrorExpanded = false
		if n > 0 {
			m.ErrorCursor = (clampIdx(m.ErrorCursor, n) - 1 + n) % n
		}
	case "down", "j":
		m.ErrorExpanded = false
		if n > 0 {
			m.ErrorCursor = (clampIdx(m.ErrorCursor, n) + 1) % n
		}
	case "d", "x", "delete":
		m.ErrorExpanded = false
		m.removeFailed(m.ErrorCursor)
	case "c":
		m.ErrorExpanded = false
		m.Failed = nil
		m.ErrorCursor = 0
		m.persistQueue()
	case "r":
		m.ErrorExpanded = false
		cmd := m.retryFailed()
		m.showQueue() // show the retried downloads running
		return m, cmd
	}
	return m, nil
}

// ---------- window chrome ----------

// The windows paint their own solid background. A terminal cell can't be
// half transparent, and the rounded border sits in the middle of its cells,
// so without a fill you get a ragged halo around the border. With one, the
// window reads as a single clean rectangle.
type winStyles struct {
	base, dim, accent, green, red, title lipgloss.Style
}

func newWinStyles() winStyles {
	base := lipgloss.NewStyle().Background(colWinBG).Foreground(colText)
	return winStyles{
		base:   base,
		dim:    base.Foreground(colDim),
		accent: base.Foreground(colGold).Bold(true),
		green:  base.Foreground(colGreen),
		red:    base.Foreground(colRed),
		title:  base.Foreground(colBlue).Bold(true),
	}
}

// row joins pre-styled segments and pads the line to the inner width on the
// window background, so every line is a solid, equal-width strip.
func (s winStyles) row(inner int, segs ...string) string {
	line := strings.Join(segs, "")
	if pad := inner - lipgloss.Width(line); pad > 0 {
		line += s.base.Render(strings.Repeat(" ", pad))
	}
	return line
}

// header renders "Title | meta | meta", with the meta parts in the dim style.
func (s winStyles) header(inner int, title string, meta ...string) string {
	segs := []string{s.title.Render(title)}
	for _, mt := range meta {
		if mt != "" {
			segs = append(segs, s.dim.Render(" | "+mt))
		}
	}
	return s.row(inner, segs...)
}

// packHints lays key hints out on as many lines as needed to fit width.
func packHints(width int, parts []string) []string {
	var lines []string
	cur := ""
	for _, p := range parts {
		next := p
		if cur != "" {
			next = cur + " • " + p
		}
		if cur != "" && lipgloss.Width(next) > width {
			lines = append(lines, cur)
			cur = p
		} else {
			cur = next
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// windowBox assembles a window: header, body sections separated by rules, and
// centered hint lines (one entry of hintLines per footer line).
func windowBox(s winStyles, w int, header string, sections [][]string, hintLines [][]string) string {
	inner := w - 4 // border (2) + padding (2)
	lines := []string{header, s.row(inner)}
	hasBody := false
	for _, sec := range sections {
		if len(sec) == 0 {
			continue
		}
		if hasBody {
			lines = append(lines, s.dim.Render(strings.Repeat("─", inner)))
		}
		lines = append(lines, sec...)
		hasBody = true
	}
	if hasBody {
		lines = append(lines, s.row(inner))
	}
	for _, parts := range hintLines {
		for _, h := range packHints(inner, parts) {
			pad := max(0, (inner-lipgloss.Width(h))/2)
			lines = append(lines, s.row(inner, s.base.Render(strings.Repeat(" ", pad)), s.dim.Render(h)))
		}
	}
	return lipgloss.NewStyle().
		Width(w).
		Background(colWinBG).
		Border(lipgloss.OuterHalfBlockBorder()).
		BorderForeground(colWinFrame).
		BorderBackground(colWinBG).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func (m *Model) overlayWidth() int {
	return max(min(m.Width-8, 76), 30)
}

// ---------- rendering: queue window ----------

func (m *Model) renderQueueOverlay() string {
	s := newWinStyles()
	w := m.overlayWidth()
	inner := w - 4
	st := m.batchStats()

	// "2/4 done" only while a batch is running. Failures are counted apart
	// from it, not as finished work.
	var progress, failed string
	if st.active > 0 {
		progress = fmt.Sprintf("%d/%d done", st.done, st.goal())
	}
	if n := len(m.Failed); n > 0 {
		failed = fmt.Sprintf("%d failed", n)
	}
	header := s.header(inner, "Download Queue", fmt.Sprintf("%d queued", len(m.Queue)), progress, failed)

	running := m.runningRows(s, inner)
	failedRows, queuedRows := m.selectableRows(s, inner, len(running))

	hints := [][]string{
		{"enter: download all", "r: retry failed", "t: A/V"},
		{"d: remove", "c: clear", fmt.Sprintf("tab: errors [%d]", len(m.Failed)), "esc: close"},
	}
	return windowBox(s, w, header, [][]string{running, failedRows, queuedRows}, hints)
}

// runningRows lists the downloads in progress (at most 3; not selectable).
func (m *Model) runningRows(s winStyles, inner int) []string {
	const maxShown = 3
	var rows []string
	shown, total := 0, 0
	for _, j := range m.Jobs {
		if j.Status != StatusPending && j.Status != StatusDownloading {
			continue
		}
		total++
		if shown == maxShown {
			continue
		}
		shown++
		icon, pct, style := "…", "", s.dim
		if j.Status == StatusDownloading {
			icon, pct, style = "↓", fmt.Sprintf("%d%%", int(j.Progress*100)), s.green
		}
		label := safeTruncate(j.Req.Video.GetDisplayString(), inner-7)
		rows = append(rows, s.row(inner, style.Render(fmt.Sprintf("%s %-*s %4s", icon, inner-7, label, pct))))
	}
	if total > maxShown {
		rows = append(rows, s.row(inner, s.dim.Render(fmt.Sprintf("+%d more downloading", total-maxShown))))
	}
	return rows
}

// selectableRows renders the failed items followed by the staged queue as one
// scrolling list under a single cursor, and returns the two groups separately
// so the window can draw a rule between them.
func (m *Model) selectableRows(s winStyles, inner, runningRows int) (failedRows, queuedRows []string) {
	nf := len(m.Failed)
	total := nf + len(m.Queue)
	vis := max(3, min(10, m.Height-16-runningRows))
	cur := clampIdx(m.QueueCursor, total)
	start := 0
	if cur >= vis {
		start = cur - vis + 1
	}
	end := min(start+vis, total)

	for i := start; i < end; i++ {
		if i < nf {
			text := "✗ " + safeTruncate(m.Failed[i].Req.Video.GetDisplayString(), inner-4)
			if i == cur {
				failedRows = append(failedRows, s.row(inner, s.accent.Render("› "+text)))
			} else {
				failedRows = append(failedRows, s.row(inner, s.red.Render("  "+text)))
			}
			continue
		}
		q := m.Queue[i-nf]
		badge := "A"
		if q.Type == "Video" {
			badge = "V"
		}
		text := fmt.Sprintf("[%s] %s", badge, safeTruncate(q.Video.GetDisplayString(), inner-6))
		if i == cur {
			queuedRows = append(queuedRows, s.row(inner, s.accent.Render("› "+text)))
		} else {
			queuedRows = append(queuedRows, s.row(inner, s.base.Render("  "+text)))
		}
	}

	if total > vis {
		pos := s.row(inner, s.dim.Render(fmt.Sprintf("  %d-%d of %d", start+1, end, total)))
		if len(queuedRows) > 0 {
			queuedRows = append(queuedRows, pos)
		} else {
			failedRows = append(failedRows, pos)
		}
	}
	return failedRows, queuedRows
}

// ---------- rendering: error window ----------

func (m *Model) renderErrorsOverlay() string {
	s := newWinStyles()
	w := m.overlayWidth()
	inner := w - 4

	header := s.header(inner, "Errors", fmt.Sprintf("%d failed", len(m.Failed)))
	cur := clampIdx(m.ErrorCursor, len(m.Failed))

	if m.ErrorExpanded && len(m.Failed) > 0 {
		hints := [][]string{{"enter/esc: back", "r: retry all", "d: remove"}}
		return windowBox(s, w, header, [][]string{m.errorDetailRows(s, inner, m.Failed[cur])}, hints)
	}

	hints := [][]string{
		{"enter: details", "r: retry all", "d: remove"},
		{"c: clear", fmt.Sprintf("tab: queue [%d]", len(m.Queue)), "esc: close"},
	}
	return windowBox(s, w, header, [][]string{m.errorListRows(s, inner, cur)}, hints)
}

// errorListRows shows each failure as a title plus up to two lines of its
// message, scrolled so the cursor stays visible.
func (m *Model) errorListRows(s winStyles, inner, cur int) []string {
	if len(m.Failed) == 0 {
		return nil
	}
	perPage := max(1, min(4, (m.Height-10)/3))
	start := 0
	if cur >= perPage {
		start = cur - perPage + 1
	}
	end := min(start+perPage, len(m.Failed))

	var rows []string
	for i := start; i < end; i++ {
		f := m.Failed[i]
		title := safeTruncate(f.Req.Video.GetDisplayString(), inner-4)
		if i == cur {
			rows = append(rows, s.row(inner, s.accent.Render("› ✗ "+title)))
		} else {
			rows = append(rows, s.row(inner, s.red.Render("  ✗ "+title)))
		}
		for _, ln := range wrapLines(f.Err, inner-4, 2) {
			rows = append(rows, s.row(inner, s.dim.Render("    "+strings.TrimRight(ln, " "))))
		}
	}
	if len(m.Failed) > perPage {
		rows = append(rows, s.row(inner, s.dim.Render(fmt.Sprintf("  %d-%d of %d", start+1, end, len(m.Failed)))))
	}
	return rows
}

// errorDetailRows shows one failure with the tail of yt-dlp's output.
func (m *Model) errorDetailRows(s winStyles, inner int, f FailedItem) []string {
	title := safeTruncate(f.Req.Video.GetDisplayString(), inner-4)
	rows := []string{s.row(inner, s.accent.Render("✗ "+title))}

	text := f.Detail
	if strings.TrimSpace(text) == "" {
		text = f.Err // entries saved before details existed
	}
	maxLines := max(3, m.Height-12)
	var out []string
	for raw := range strings.SplitSeq(text, "\n") {
		out = append(out, wrapLines(raw, inner-4, 3)...)
	}
	if len(out) > maxLines {
		out = append(out[:maxLines-1], "…")
	}
	for _, ln := range out {
		rows = append(rows, s.row(inner, s.dim.Render("  "+strings.TrimRight(ln, " "))))
	}
	return rows
}
