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

type queueFile struct {
	Items []DownloadReq `json:"items"`
}

func queuePath() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(dir, "yt-media", "queue.json")
}

func loadQueue() []DownloadReq {
	data, err := os.ReadFile(queuePath())
	if err != nil {
		return nil
	}
	var qf queueFile
	if err := json.Unmarshal(data, &qf); err != nil {
		log.Printf("[QUEUE] ignoring unreadable queue file: %v", err)
		return nil
	}
	return qf.Items
}

func saveQueue(items []DownloadReq) {
	path := queuePath()
	if len(items) == 0 {
		_ = os.Remove(path)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("[QUEUE] mkdir failed: %v", err)
		return
	}
	data, err := json.MarshalIndent(queueFile{Items: items}, "", "  ")
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

// persistQueue saves everything that still needs downloading: the staged
// queue plus any job that hasn't finished successfully.
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
		if j.Status != StatusDone {
			add(j.Req)
		}
	}
	saveQueue(out)
}

// ---------- queue operations ----------

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
	if m.QueueCursor >= len(m.Queue) {
		m.QueueCursor = max(0, len(m.Queue)-1)
	}
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

// resetBatch clears finished jobs. Failed ones go back to the queue so they
// aren't silently lost and can be retried.
func (m *Model) resetBatch() {
	for _, j := range m.Jobs {
		if j.Status == StatusError && m.queueIndex(j.Req.Video.ID) < 0 {
			m.Queue = append(m.Queue, j.Req)
		}
	}
	m.Jobs = nil
	m.TotalItems = 0
	m.persistQueue()
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

// ---------- queue window ----------

func (m *Model) handleQueueKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	n := len(m.Queue)
	switch msg.String() {
	case "esc", "q", "w":
		m.QueueOpen = false
	case "up", "k":
		if n > 0 {
			m.QueueCursor = (m.QueueCursor - 1 + n) % n
		}
	case "down", "j":
		if n > 0 {
			m.QueueCursor = (m.QueueCursor + 1) % n
		}
	case "d", "x", "delete":
		m.removeFromQueue(m.QueueCursor)
	case "c":
		m.Queue = nil
		m.QueueCursor = 0
		m.persistQueue()
	case "tab":
		if n > 0 {
			q := &m.Queue[m.QueueCursor]
			if q.Type == "Audio" {
				q.Type = "Video"
			} else {
				q.Type = "Audio"
			}
			m.persistQueue()
		}
	case "enter":
		return m, m.startQueue()
	}
	return m, nil
}

func (m *Model) renderQueueOverlay() string {
	w := max(min(m.Width-8, 76), 30)
	inner := w - 4 // border (2) + padding (2)

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#585B70"))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF")).Bold(true)
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8"))
	title := lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA")).Bold(true).Render("Download Queue")

	finished := 0
	for _, j := range m.Jobs {
		if j.Status == StatusDone || j.Status == StatusError {
			finished++
		}
	}
	summary := fmt.Sprintf("%d queued", len(m.Queue))
	if m.TotalItems > 0 {
		summary += fmt.Sprintf(" · batch %d/%d", finished, m.TotalItems)
	}

	// Running / failed jobs from the current batch (finished ones are hidden).
	var jobLines []string
	hidden := 0
	for _, j := range m.Jobs {
		if j.Status == StatusDone {
			continue
		}
		if len(jobLines) >= 4 {
			hidden++
			continue
		}
		icon, pct, style := "…", "", dim
		switch j.Status {
		case StatusDownloading:
			icon, pct, style = "↓", fmt.Sprintf("%d%%", int(j.Progress*100)), green
		case StatusError:
			icon, style = "✗", red
		}
		label := safeTruncate(j.Req.Video.GetDisplayString(), inner-7)
		jobLines = append(jobLines, style.Render(fmt.Sprintf("%s %-*s %4s", icon, inner-7, label, pct)))
	}
	if hidden > 0 {
		jobLines = append(jobLines, dim.Render(fmt.Sprintf("+%d more", hidden)))
	}

	// Staged queue, scrolled so the cursor is always visible.
	vis := max(3, min(10, m.Height-12-len(jobLines)))
	var queueLines []string
	if len(m.Queue) == 0 {
		queueLines = append(queueLines, dim.Render(safeTruncate("Empty - press space on a result to add it", inner)))
	} else {
		start := 0
		if m.QueueCursor >= vis {
			start = m.QueueCursor - vis + 1
		}
		end := min(start+vis, len(m.Queue))
		for i := start; i < end; i++ {
			q := m.Queue[i]
			badge := "A"
			if q.Type == "Video" {
				badge = "V"
			}
			row := fmt.Sprintf("[%s] %s", badge, safeTruncate(q.Video.GetDisplayString(), inner-6))
			if i == m.QueueCursor {
				queueLines = append(queueLines, accent.Render("› "+row))
			} else {
				queueLines = append(queueLines, "  "+row)
			}
		}
		if len(m.Queue) > vis {
			queueLines = append(queueLines, dim.Render(fmt.Sprintf("  %d-%d of %d", start+1, end, len(m.Queue))))
		}
	}

	parts := []string{title, dim.Render(summary), ""}
	if len(jobLines) > 0 {
		parts = append(parts, jobLines...)
		parts = append(parts, dim.Render(strings.Repeat("─", inner)))
	}
	parts = append(parts, queueLines...)
	parts = append(parts, "", dim.Render(safeTruncate("enter: download all • d: remove • c: clear • tab: A/V • esc: close", inner)))

	return lipgloss.NewStyle().
		Width(w).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#89B4FA")).
		Padding(0, 1).
		Render(strings.Join(parts, "\n"))
}
