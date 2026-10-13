package tui

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"yt-downloader/internal/downloader"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ---------- persistence ----------

// FailedItem is a download that errored out. It is kept (and persisted) until
// the user retries or clears it, independent of which batch it came from.
type FailedItem struct {
	Req    DownloadReq `json:"req"`
	Err    string      `json:"err"`
	Detail string      `json:"detail,omitempty"`
}

type queueFile struct {
	Items  []DownloadReq `json:"items"`
	Failed []FailedItem  `json:"failed,omitempty"`
}

func queuePath() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(dir, "yt-media", "queue.json")
}

func loadQueue() ([]DownloadReq, []FailedItem) {
	data, err := os.ReadFile(queuePath())
	if err != nil {
		return nil, nil
	}
	var qf queueFile
	if err := json.Unmarshal(data, &qf); err != nil {
		log.Printf("[QUEUE] ignoring unreadable queue file: %v", err)
		return nil, nil
	}
	return qf.Items, qf.Failed
}

func saveQueue(items []DownloadReq, failed []FailedItem) {
	path := queuePath()
	if len(items) == 0 && len(failed) == 0 {
		_ = os.Remove(path)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("[QUEUE] mkdir failed: %v", err)
		return
	}
	data, err := json.MarshalIndent(queueFile{Items: items, Failed: failed}, "", "  ")
	if err != nil {
		log.Printf("[QUEUE] marshal failed: %v", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("[QUEUE] write failed: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("[QUEUE] rename failed: %v", err)
	}
}

// persistQueue saves everything that still needs attention: the staged
// queue, any job that hasn't finished yet, and the failed list.
func (m *Model) persistQueue() {
	seen := map[string]bool{}
	var out []DownloadReq
	add := func(r DownloadReq) {
		if !seen[r.Video.ID] {
			seen[r.Video.ID] = true
			out = append(out, r)
		}
	}
	for _, r := range m.Queue {
		add(r)
	}
	for _, j := range m.Jobs {
		if j.Status == StatusPending || j.Status == StatusDownloading {
			add(j.Req)
		}
	}
	saveQueue(out, m.Failed)
}

// ---------- queue operations ----------

func clampIdx(i, n int) int {
	if n <= 0 {
		return 0
	}
	return min(max(i, 0), n-1)
}

func (m *Model) queueIndex(id string) int {
	for i, q := range m.Queue {
		if q.Video.ID == id {
			return i
		}
	}
	return -1
}

// toggleQueue adds v (using the current download type) or removes it.
// Returns true if it was added.
func (m *Model) toggleQueue(v downloader.Video) bool {
	if i := m.queueIndex(v.ID); i >= 0 {
		m.removeFromQueue(i)
		return false
	}
	m.Queue = append(m.Queue, DownloadReq{Video: v, Type: m.ChoiceType})
	m.persistQueue()
	return true
}

func (m *Model) removeFromQueue(i int) {
	if i < 0 || i >= len(m.Queue) {
		return
	}
	m.Queue = append(m.Queue[:i], m.Queue[i+1:]...)
	m.persistQueue()
}

// startQueue hands the whole queue to the download pipeline as one batch.
func (m *Model) startQueue() tea.Cmd {
	if len(m.Queue) == 0 {
		return m.flash("Queue is empty", false)
	}
	items := m.Queue
	m.Queue = nil
	m.QueueCursor = 0
	return m.checkLibraryManual(items)
}

// resetBatch clears the finished jobs of the previous batch. Failures are
// not touched: they live in m.Failed until retried or cleared, so starting
// a new batch can never push a failed item back into the normal queue.
func (m *Model) resetBatch() {
	m.Jobs = nil
	m.TotalItems = 0
	m.persistQueue()
}

// ---------- failures ----------

// recordFailure adds (or updates) an entry in the failed list.
func (m *Model) recordFailure(req DownloadReq, err error, detail string) {
	text := err.Error()
	for i := range m.Failed {
		if m.Failed[i].Req.Video.ID == req.Video.ID {
			m.Failed[i].Err = text
			m.Failed[i].Detail = detail
			return
		}
	}
	m.Failed = append(m.Failed, FailedItem{Req: req, Err: text, Detail: detail})
}

func (m *Model) removeFailed(i int) {
	if i < 0 || i >= len(m.Failed) {
		return
	}
	m.Failed = append(m.Failed[:i], m.Failed[i+1:]...)
	if m.ErrorCursor >= len(m.Failed) {
		m.ErrorCursor = max(0, len(m.Failed)-1)
	}
	m.persistQueue()
}

// clearFailure drops a video from the failed list (e.g. it just succeeded).
func (m *Model) clearFailure(id string) {
	for i := range m.Failed {
		if m.Failed[i].Req.Video.ID == id {
			m.removeFailed(i)
			return
		}
	}
}

// retryFailed sends every failed item back through the download pipeline.
func (m *Model) retryFailed() tea.Cmd {
	if len(m.Failed) == 0 {
		return m.flash("No failed downloads to retry", false)
	}
	items := make([]DownloadReq, 0, len(m.Failed))
	for _, f := range m.Failed {
		items = append(items, f.Req)
	}
	m.Failed = nil
	m.ErrorCursor = 0
	m.persistQueue()
	return tea.Batch(
		m.flash(fmt.Sprintf("Retrying %d failed download(s)", len(items)), false),
		m.checkLibraryManual(items),
	)
}

// batchFinishedCmd returns a notification cmd if this batch qualifies.
func (m *Model) batchFinishedCmd() tea.Cmd {
	cfg := m.Config
	if !cfg.NotifyOnBatch || m.TotalItems < cfg.NotifyMinItems {
		return nil
	}
	if cfg.NotifyOnlyUnfocused && m.Focused {
		return nil
	}
	ok, failed := 0, 0
	for _, j := range m.Jobs {
		switch j.Status {
		case StatusDone:
			ok++
		case StatusError:
			failed++
		}
	}
	return func() tea.Msg {
		notifyBatchComplete(ok, failed)
		return nil
	}
}

// ---------- footer flash messages ----------

func (m *Model) flash(text string, isErr bool) tea.Cmd {
	m.Flash, m.FlashErr = text, isErr
	m.flashSeq++
	seq := m.flashSeq
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg {
		return flashClearMsg{seq: seq}
	})
}

// ---------- popup windows: state + key handling ----------

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
		m.showQueue()
		return m, cmd
	}
	return m, nil
}

// ---------- popup windows: rendering ----------

// The windows paint their own solid background. A terminal cell can't be
// half transparent, and the rounded border sits in the middle of its cells,
// so without a fill you get a ragged halo around the border. With one, the
// window reads as a single clean rectangle.
var (
	winBG    = lipgloss.Color("#181825")
	winText  = lipgloss.Color("#CDD6F4")
	winDim   = lipgloss.Color("#585B70")
	winBlue  = lipgloss.Color("#89B4FA")
	winGold  = lipgloss.Color("#F9E2AF")
	winGreen = lipgloss.Color("#A6E3A1")
	winRed   = lipgloss.Color("#F38BA8")
)

type winStyles struct {
	base, dim, accent, green, red, title lipgloss.Style
}

func newWinStyles() winStyles {
	base := lipgloss.NewStyle().Background(winBG).Foreground(winText)
	return winStyles{
		base:   base,
		dim:    base.Foreground(winDim),
		accent: base.Foreground(winGold).Bold(true),
		green:  base.Foreground(winGreen),
		red:    base.Foreground(winRed),
		title:  base.Foreground(winBlue).Bold(true),
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
		Background(winBG).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(winBlue).
		BorderBackground(winBG).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func (m *Model) overlayWidth() int {
	return max(min(m.Width-8, 76), 30)
}

func (m *Model) renderQueueOverlay() string {
	s := newWinStyles()
	w := m.overlayWidth()
	inner := w - 4

	var active []DownloadJob
	finished := 0
	for _, j := range m.Jobs {
		switch j.Status {
		case StatusPending, StatusDownloading:
			active = append(active, j)
		case StatusDone, StatusError:
			finished++
		}
	}

	// "2/5 done" only while a batch is running: finished (done + failed)
	// out of the jobs in the current batch.
	var progress, failed string
	if len(active) > 0 {
		progress = fmt.Sprintf("%d/%d done", finished, len(m.Jobs))
	}
	if n := len(m.Failed); n > 0 {
		failed = fmt.Sprintf("%d failed", n)
	}
	header := s.header(inner, "Download Queue", fmt.Sprintf("%d queued", len(m.Queue)), progress, failed)

	const maxShown = 3
	var running []string
	for i, j := range active {
		if i == maxShown {
			running = append(running, s.row(inner, s.dim.Render(fmt.Sprintf("+%d more downloading", len(active)-maxShown))))
			break
		}
		icon, pct, style := "…", "", s.dim
		if j.Status == StatusDownloading {
			icon, pct, style = "↓", fmt.Sprintf("%d%%", int(j.Progress*100)), s.green
		}
		label := safeTruncate(j.Req.Video.GetDisplayString(), inner-7)
		running = append(running, s.row(inner, style.Render(fmt.Sprintf("%s %-*s %4s", icon, inner-7, label, pct))))
	}
	nf, nq := len(m.Failed), len(m.Queue)
	total := nf + nq
	vis := max(3, min(10, m.Height-16-len(running)))
	cur := clampIdx(m.QueueCursor, total)
	start := 0
	if cur >= vis {
		start = cur - vis + 1
	}
	end := min(start+vis, total)

	var failedRows, rows []string
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
			rows = append(rows, s.row(inner, s.accent.Render("› "+text)))
		} else {
			rows = append(rows, s.row(inner, s.base.Render("  "+text)))
		}
	}
	if total > vis {
		pos := s.row(inner, s.dim.Render(fmt.Sprintf("  %d-%d of %d", start+1, end, total)))
		if len(rows) > 0 {
			rows = append(rows, pos)
		} else {
			failedRows = append(failedRows, pos)
		}
	}

	hints := [][]string{
		{"enter: download all", "r: retry failed", "t: A/V"},
		{"d: remove", "c: clear", fmt.Sprintf("tab: errors [%d]", nf), "esc: close"},
	}
	return windowBox(s, w, header, [][]string{running, failedRows, rows}, hints)
}

func (m *Model) renderErrorsOverlay() string {
	s := newWinStyles()
	w := m.overlayWidth()
	inner := w - 4

	header := s.header(inner, "Errors", fmt.Sprintf("%d failed", len(m.Failed)))

	var rows []string
	cur := clampIdx(m.ErrorCursor, len(m.Failed))

	if m.ErrorExpanded && len(m.Failed) > 0 {
		f := m.Failed[cur]
		title := safeTruncate(f.Req.Video.GetDisplayString(), inner-4)
		rows = append(rows, s.row(inner, s.accent.Render("✗ "+title)))

		text := f.Detail
		if strings.TrimSpace(text) == "" {
			text = f.Err // older saved entries have no detail
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

		hints := [][]string{{"enter/esc: back", "r: retry all", "d: remove"}}
		return windowBox(s, w, header, [][]string{rows}, hints)
	}

	// Each entry is a title line plus up to two lines of error text.
	perPage := max(1, min(4, (m.Height-10)/3))
	if len(m.Failed) > 0 {
		start := 0
		if cur >= perPage {
			start = cur - perPage + 1
		}
		end := min(start+perPage, len(m.Failed))
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
	}

	hints := [][]string{
		{"enter: details", "r: retry all", "d: remove"},
		{"c: clear", fmt.Sprintf("tab: queue [%d]", len(m.Queue)), "esc: close"},
	}
	return windowBox(s, w, header, [][]string{rows}, hints)
}
