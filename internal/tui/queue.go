package tui

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"yt-downloader/internal/config"
	"yt-downloader/internal/downloader"

	tea "charm.land/bubbletea/v2"
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
	return filepath.Join(config.CacheDir(), "queue.json")
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

// ---------- staged queue ----------

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

// removeFromQueue deletes entry i. The cursor is the caller's business: in
// the queue window it spans failed items too (see handleQueueKey).
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
