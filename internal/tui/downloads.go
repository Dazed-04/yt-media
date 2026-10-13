package tui

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"yt-downloader/internal/config"

	tea "charm.land/bubbletea/v2"
)

var progressRe = regexp.MustCompile(`\[download\]\s+([0-9.]+)\%`)

// ---------- worker pool ----------

// waitForProgress blocks until the next progress update arrives.
func waitForProgress(ch chan JobProgressMsg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// startWorkerQueue starts pending jobs until MaxWorkers are running.
func (m *Model) startWorkerQueue() tea.Cmd {
	var cmds []tea.Cmd
	for i := range m.Jobs {
		if m.ActiveWorkers >= m.MaxWorkers {
			break
		}
		if m.Jobs[i].Status == StatusPending {
			m.Jobs[i].Status = StatusDownloading
			m.ActiveWorkers++
			cmds = append(cmds, m.downloadItem(i))
		}
	}
	return tea.Batch(cmds...)
}

// downloadParams is everything one download needs. It is copied out of the
// model before the goroutine starts, so the goroutine never reads m.
type downloadParams struct {
	index    int
	req      DownloadReq
	cfg      config.Config
	mode     string // m.ChoiceMode
	home     string
	archive  string
	progress chan<- JobProgressMsg
}

func (m *Model) downloadItem(jobIndex int) tea.Cmd {
	p := downloadParams{
		index:    jobIndex,
		req:      m.Jobs[jobIndex].Req,
		cfg:      m.Config,
		mode:     m.ChoiceMode,
		home:     m.User,
		archive:  m.archivePath(),
		progress: m.ProgressChan,
	}
	return func() tea.Msg { return p.run() }
}

// outputDir is where this download is filed.
func (p downloadParams) outputDir() string {
	root := firstNonEmpty(p.cfg.MusicDir, filepath.Join(p.home, "Music"))
	if p.req.Type == "Video" {
		root = firstNonEmpty(p.cfg.VideoDir, filepath.Join(p.home, "Videos"))
	}
	if p.mode == "Single" {
		return filepath.Join(root, "single")
	}
	artist := firstNonEmpty(sanitize(p.req.Video.Artist), "Unknown")
	album := firstNonEmpty(sanitize(p.req.Video.Album), "Playlist")
	return filepath.Join(root, fmt.Sprintf("%s - %s", artist, album))
}

// args builds the yt-dlp command line. The URL goes after "--" so it can
// never be read as an option value.
func (p downloadParams) args(dir string) []string {
	out := filepath.Join(dir, "%(title)s.%(ext)s")

	var args []string
	if p.req.Type == "Audio" {
		args = []string{"-f", "bestaudio/best", "-o", out, "--extract-audio", "--audio-format", "mp3"}
	} else {
		args = []string{"-f", "bestvideo+bestaudio/best", "-o", out}
	}
	return append(args,
		"--embed-metadata", "--embed-thumbnail",
		"--newline",
		"--download-archive", p.archive,
		"--",
		"https://www.youtube.com/watch?v="+p.req.Video.ID,
	)
}

// run executes yt-dlp, streaming progress, and reports how it ended.
func (p downloadParams) run() downloadFinishedMsg {
	fail := func(err error) downloadFinishedMsg {
		return downloadFinishedMsg{Index: p.index, Err: err}
	}

	dir := p.outputDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail(err)
	}
	args := p.args(dir)
	log.Printf("[DL] yt-dlp %q", args)

	cmd := exec.Command("yt-dlp", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fail(err)
	}
	// Must be set before Start(). Errors go to debug.log and to a buffer
	// that is turned into the message shown in the UI.
	var stderr bytes.Buffer
	cmd.Stderr = io.MultiWriter(log.Writer(), &stderr)

	if err := cmd.Start(); err != nil {
		return fail(err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if match := progressRe.FindStringSubmatch(scanner.Text()); len(match) == 2 {
			if pct, err := strconv.ParseFloat(match[1], 64); err == nil {
				p.progress <- JobProgressMsg{Index: p.index, Progress: pct / 100.0}
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		short, detail := parseYtdlpError(stderr.String(), err)
		return downloadFinishedMsg{Index: p.index, Err: errors.New(short), Detail: detail}
	}
	p.progress <- JobProgressMsg{Index: p.index, Progress: 1.0}
	return downloadFinishedMsg{Index: p.index}
}

var nonAlnumRe = regexp.MustCompile(`[^a-z0-9\s]+`)

// sanitize reduces a tag to lowercase letters, digits and spaces for use in a
// folder name.
func sanitize(s string) string {
	return strings.TrimSpace(nonAlnumRe.ReplaceAllString(strings.ToLower(s), ""))
}

// ---------- download archive (already-downloaded detection) ----------

func (m *Model) archivePath() string {
	if m.Config.ArchiveFile != "" {
		return m.Config.ArchiveFile
	}
	return filepath.Join(filepath.Dir(config.Path()), "archive.txt")
}

// loadArchiveIDs reads yt-dlp's archive file ("youtube <id>" per line).
func loadArchiveIDs(path string) map[string]bool {
	ids := make(map[string]bool)
	data, err := os.ReadFile(path)
	if err != nil {
		return ids
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 {
			ids[fields[1]] = true
		}
	}
	return ids
}

// checkLibraryManual splits items into new downloads and ones already in the
// archive.
func (m *Model) checkLibraryManual(items []DownloadReq) tea.Cmd {
	archive := m.archivePath() // read here, not in the goroutine
	return func() tea.Msg {
		archived := loadArchiveIDs(archive)
		var toDownload []DownloadReq
		var skipped []string
		for _, req := range items {
			if archived[req.Video.ID] {
				skipped = append(skipped, req.Video.Title)
			} else {
				toDownload = append(toDownload, req)
			}
		}
		return checkLibraryMsg{ToDownload: toDownload, Skipped: skipped}
	}
}

// ---------- batch state ----------

// batchStats summarises the jobs of the current batch.
//
// Failed jobs are reported separately and are NOT part of the goal: a batch
// of 5 with 1 failure is "4 of 4 done" plus "1 failed", so the progress bar
// can still reach 100% and a failure never looks like finished work.
type batchStats struct {
	total    int     // jobs in the batch
	done     int     // finished successfully
	failed   int     // finished with an error
	active   int     // pending or downloading
	progress float64 // done jobs count 1, running jobs their fraction
}

func (m *Model) batchStats() batchStats {
	st := batchStats{total: len(m.Jobs)}
	for _, j := range m.Jobs {
		switch j.Status {
		case StatusDone:
			st.done++
			st.progress += 1.0
		case StatusError:
			st.failed++
		case StatusPending:
			st.active++
		case StatusDownloading:
			st.active++
			st.progress += j.Progress
		}
	}
	return st
}

// goal is the number of jobs that can still succeed.
func (s batchStats) goal() int { return s.total - s.failed }

// resetBatch clears the finished jobs of the previous batch. Failures are
// not touched: they live in m.Failed until retried or cleared, so starting
// a new batch can never push a failed item back into the normal queue.
func (m *Model) resetBatch() {
	m.Jobs = nil
	m.TotalItems = 0
	m.persistQueue()
}

// ---------- download message handlers ----------

func (m *Model) onCheckLibrary(msg checkLibraryMsg) tea.Cmd {
	m.Searching = false
	m.SkippedItems = msg.Skipped
	m.resetInFlight()

	var flashCmd tea.Cmd
	if len(msg.Skipped) > 0 {
		flashCmd = m.flash(fmt.Sprintf("Skipped %d already downloaded", len(msg.Skipped)), false)
	}

	idle := m.DownloadState == StatusIdle || m.DownloadState == StatusDone
	if idle {
		m.resetBatch()
	}
	for _, req := range msg.ToDownload {
		m.Jobs = append(m.Jobs, DownloadJob{Req: req, Status: StatusPending})
	}
	m.TotalItems = len(m.Jobs)
	m.persistQueue()

	if len(msg.ToDownload) == 0 {
		if len(m.Jobs) == 0 && m.DownloadState != StatusIdle {
			m.DownloadState = StatusIdle
			m.purgePreviewCache()
			return tea.Batch(flashCmd, m.showCachedPreview())
		}
		return flashCmd
	}

	if idle {
		m.DownloadState = StatusDownloading
		m.purgePreviewCache()
		m.ProgressChan = make(chan JobProgressMsg, 500)
		return tea.Batch(flashCmd, m.startWorkerQueue(), waitForProgress(m.ProgressChan), m.showCachedPreview())
	}
	return tea.Batch(flashCmd, m.startWorkerQueue())
}

func (m *Model) onJobProgress(msg JobProgressMsg) tea.Cmd {
	if msg.Index < len(m.Jobs) {
		m.Jobs[msg.Index].Progress = msg.Progress
	}
	return waitForProgress(m.ProgressChan)
}

func (m *Model) onDownloadFinished(msg downloadFinishedMsg) tea.Cmd {
	m.ActiveWorkers = max(0, m.ActiveWorkers-1)
	if msg.Index < 0 || msg.Index >= len(m.Jobs) {
		return nil // the batch was reset while this download was running
	}

	job := &m.Jobs[msg.Index]
	var flashCmd tea.Cmd
	if msg.Err != nil {
		log.Printf("Download job %d failed: %v", msg.Index, msg.Err)
		job.Status = StatusError
		job.Err = msg.Err
		m.recordFailure(job.Req, msg.Err, msg.Detail)
		title := safeTruncate(job.Req.Video.Title, 30)
		flashCmd = m.flash(fmt.Sprintf("Failed: %s - %v (e: details)", title, msg.Err), true)
	} else {
		job.Status = StatusDone
		job.Progress = 1.0
		m.clearFailure(job.Req.Video.ID)
	}
	m.persistQueue()

	if m.batchStats().active > 0 {
		return tea.Batch(flashCmd, m.startWorkerQueue())
	}
	m.DownloadState = StatusDone
	return tea.Batch(flashCmd, m.batchFinishedCmd())
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
	st := m.batchStats()
	return func() tea.Msg {
		notifyBatchComplete(st.done, st.failed)
		return nil
	}
}
