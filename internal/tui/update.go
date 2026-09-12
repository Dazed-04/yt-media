package tui

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"yt-downloader/internal/downloader"

	tea "charm.land/bubbletea/v2"
)

type (
	blinkMsg       struct{}
	spinnerTickMsg struct{}
)

type previewRenderedMsg struct {
	videoID string
	preview renderedPreview
	err     error
}

func tickSpinner() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func renderPreviewCmd(videoID, imagePath string, cols, rows int) tea.Cmd {
	return func() tea.Msg {
		p, err := renderPlaceholder(imagePath, cols, rows)
		return previewRenderedMsg{videoID: videoID, preview: p, err: err}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.Searching {
			switch msg.String() {
			case "enter":
				if strings.TrimSpace(m.SearchQuery) == "" {
					return m, nil
				}
				m.Searching = false
				m.IsFetching = true
				m.SpinnerIdx = 0

				if m.DownloadState == StatusDone {
					m.DownloadState = StatusIdle
					m.purgePreviewCache()
				}

				return m, tea.Batch(m.doSearch(), tickSpinner(), m.showCachedPreview())
			case "esc":
				m.Searching = false
				return m, nil
			case "backspace":
				if len(m.SearchQuery) > 0 {
					m.SearchQuery = m.SearchQuery[:len(m.SearchQuery)-1]
				}
			case "space":
				m.SearchQuery += " "
			default:
				if len(msg.String()) == 1 {
					m.SearchQuery += msg.String()
				}
			}
			m.FilteredList = m.MusicList
			if m.SearchQuery != "" {
				query := strings.ToLower(m.SearchQuery)
				var filtered []downloader.Video
				for _, item := range m.MusicList {
					if strings.Contains(strings.ToLower(item.Title), query) || strings.Contains(strings.ToLower(item.Artist), query) {
						filtered = append(filtered, item)
					}
				}
				m.FilteredList = filtered
			}
			m.Cursor = 0
			m.ListOffset = 0
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "q":
			m.Cleanup()
			return m, tea.Quit

		case "/":
			m.Searching = true
			m.Blink = true
			return m, tea.Tick(time.Millisecond*500, func(t time.Time) tea.Msg {
				return blinkMsg{}
			})

		case "t":
			if m.ChoiceType == "Audio" {
				m.ChoiceType = "Video"
			} else {
				m.ChoiceType = "Audio"
			}
			var sizeCmd tea.Cmd
			if len(m.FilteredList) > 0 {
				sizeCmd = m.fetchSizeCmd(m.FilteredList[m.Cursor], m.ChoiceType)
			}
			return m, sizeCmd

		case "esc":
			if m.DownloadState == StatusDone {
				m.DownloadState = StatusIdle
				m.Jobs = nil
			}
			m.resetInFlight()
			m.SearchQuery = ""
			m.FilteredList = m.MusicList
			m.Cursor = 0
			m.ListOffset = 0
			m.Selected = make(map[int]string)
			m.purgePreviewCache()
			return m, m.showCachedPreview()

		case "up", "k", "shift+tab":
			if len(m.FilteredList) == 0 {
				return m, nil
			}
			m.Cursor--
			if m.Cursor < 0 {
				m.Cursor = len(m.FilteredList) - 1
				m.ListOffset = max(0, len(m.FilteredList)-m.VisibleListH)
			} else if m.Cursor < m.ListOffset {
				m.ListOffset = m.Cursor
			}
			v := m.FilteredList[m.Cursor]
			return m, tea.Batch(m.showCachedPreview(), m.priorityFetchCmd(v), m.fetchSizeCmd(v, m.ChoiceType))

		case "down", "j", "tab":
			if len(m.FilteredList) == 0 {
				return m, nil
			}
			m.Cursor++
			if m.Cursor >= len(m.FilteredList) {
				m.Cursor = 0
				m.ListOffset = 0
			} else if m.Cursor >= m.ListOffset+m.VisibleListH {
				m.ListOffset = m.Cursor - m.VisibleListH + 1
			}
			v := m.FilteredList[m.Cursor]
			return m, tea.Batch(m.showCachedPreview(), m.priorityFetchCmd(v), m.fetchSizeCmd(v, m.ChoiceType))

		case "space":
			if len(m.FilteredList) == 0 {
				return m, nil
			}
			if _, exists := m.Selected[m.Cursor]; exists {
				delete(m.Selected, m.Cursor)
			} else {
				m.Selected[m.Cursor] = m.ChoiceType
			}
			m.Cursor++
			if m.Cursor >= len(m.FilteredList) {
				m.Cursor = 0
				m.ListOffset = 0
			} else if m.Cursor >= m.ListOffset+m.VisibleListH {
				m.ListOffset = m.Cursor - m.VisibleListH + 1
			}
			v := m.FilteredList[m.Cursor]
			return m, tea.Batch(m.showCachedPreview(), m.priorityFetchCmd(v), m.fetchSizeCmd(v, m.ChoiceType))

		case "enter":
			if len(m.FilteredList) == 0 {
				return m, nil
			}
			m.SkippedItems = nil
			var toCheck []DownloadReq
			if len(m.Selected) == 0 {
				toCheck = append(toCheck, DownloadReq{Video: m.FilteredList[m.Cursor], Type: m.ChoiceType})
			} else {
				for i, v := range m.FilteredList {
					if typ, ok := m.Selected[i]; ok {
						toCheck = append(toCheck, DownloadReq{Video: v, Type: typ})
					}
				}
			}
			m.Selected = make(map[int]string)
			return m, m.checkLibraryManual(toCheck)
		}

	case blinkMsg:
		if m.Searching {
			m.Blink = !m.Blink
			return m, tea.Tick(time.Millisecond*500, func(t time.Time) tea.Msg {
				return blinkMsg{}
			})
		}

	case spinnerTickMsg:
		if m.IsFetching {
			m.SpinnerIdx++
			return m, tickSpinner()
		}

	case sizeFetchedMsg:
		cacheKey := msg.videoID + "-" + msg.format
		m.SizeCache[cacheKey] = msg.sizeStr
		delete(m.SizeInFlight, cacheKey)
		return m, nil

	case thumbnailReadyMsg:
		var cmds []tea.Cmd
		if len(m.FilteredList) > 0 && m.Cursor < len(m.FilteredList) &&
			m.FilteredList[m.Cursor].ID == msg.videoID {
			cols, rows := m.previewImageDims()
			cmds = append(cmds, renderPreviewCmd(msg.videoID, thumbCachePath(msg.videoID), cols, rows))
		}
		m.prefetchIdx++
		cmds = append(cmds, fetchOneThumbnail(m.prefetchList, m.prefetchIdx))
		return m, tea.Batch(cmds...)

	case searchResultsMsg:
		m.IsFetching = false
		m.purgePreviewCache()

		normalize := func(s string) string {
			s = strings.ToLower(s)
			return strings.Map(func(r rune) rune {
				if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
					return r
				}
				return -1
			}, s)
		}

		var strictlyRelevant []downloader.Video
		seenIDs := make(map[string]bool)
		cleanQuery := normalize(m.SearchQuery)
		queryWords := strings.Fields(cleanQuery)

		for _, v := range msg {
			if seenIDs[v.ID] {
				continue
			}
			cleanTitle := normalize(v.Title)
			cleanArtist := normalize(v.Artist)
			match := false
			for _, word := range queryWords {
				if strings.Contains(cleanTitle, word) || strings.Contains(cleanArtist, word) {
					match = true
					break
				}
			}
			if match {
				strictlyRelevant = append(strictlyRelevant, v)
				seenIDs[v.ID] = true
			}
		}
		if len(strictlyRelevant) == 0 {
			strictlyRelevant = msg
		}

		m.MusicList = strictlyRelevant
		m.FilteredList = strictlyRelevant
		m.Cursor = 0
		m.ListOffset = 0
		m.prefetchList = strictlyRelevant
		m.prefetchIdx = 0

		// Also start fetching the size for the first item
		var cmds []tea.Cmd
		cmds = append(cmds, fetchOneThumbnail(m.prefetchList, 0))
		if len(m.FilteredList) > 0 {
			cmds = append(cmds, m.fetchSizeCmd(m.FilteredList[0], m.ChoiceType))
		}
		return m, tea.Batch(cmds...)

	case checkLibraryMsg:
		m.Searching = false
		m.SkippedItems = msg.Skipped
		m.resetInFlight()

		if m.Jobs == nil {
			m.Jobs = make([]DownloadJob, 0)
		}

		for _, req := range msg.ToDownload {
			m.Jobs = append(m.Jobs, DownloadJob{
				Req:    req,
				Status: StatusPending,
			})
		}

		m.TotalItems = len(m.Jobs)

		if len(m.Jobs) == 0 {
			if m.TotalItems == 0 {
				m.DownloadState = StatusDone
				m.purgePreviewCache()
				return m, m.showCachedPreview()
			}
			return m, nil
		}

		if m.DownloadState == StatusIdle || m.DownloadState == StatusDone {
			m.DownloadState = StatusDownloading
			m.purgePreviewCache()
			m.ProgressChan = make(chan JobProgressMsg, 500)
			return m, tea.Batch(m.startWorkerQueue(), waitForProgress(m.ProgressChan), m.showCachedPreview())
		}

		return m, m.startWorkerQueue()

	case JobProgressMsg:
		m.Jobs[msg.Index].Progress = msg.Progress
		return m, waitForProgress(m.ProgressChan)

	case downloadFinishedMsg:
		m.ActiveWorkers--
		if msg.Err != nil {
			log.Printf("Download job %d failed: %v", msg.Index, msg.Err)
			m.Jobs[msg.Index].Status = StatusError
			m.Jobs[msg.Index].Err = msg.Err
		} else {
			m.Jobs[msg.Index].Status = StatusDone
			m.Jobs[msg.Index].Progress = 1.0
		}

		allDone := true
		for _, job := range m.Jobs {
			if job.Status == StatusPending || job.Status == StatusDownloading {
				allDone = false
				break
			}
		}

		if allDone {
			m.DownloadState = StatusDone
			return m, nil
		}
		return m, m.startWorkerQueue()

	case errorMsg:
		log.Printf("Caught error in TUI: %v", msg)
		m.IsFetching = false
		m.resetInFlight()
		return m, nil

	case previewRenderedMsg:
		if m.inFlight != nil {
			delete(m.inFlight, msg.videoID)
		}
		if msg.err != nil {
			log.Printf("[PREVIEW] render failed for %s: %v", msg.videoID, msg.err)
			return m, nil
		}
		if msg.preview.kittyBytes != nil {
			os.Stdout.Write(msg.preview.kittyBytes)
		}
		if m.previewCache == nil {
			m.previewCache = make(map[string]renderedPreview)
		}
		m.previewCache[msg.videoID] = msg.preview
		return m, nil

	case tea.WindowSizeMsg:
		m.Width, m.Height = msg.Width, msg.Height
		m.ready = true
		m.purgePreviewCache()
		return m, m.showCachedPreview()
	}

	return m, nil
}

func (m *Model) showCachedPreview() tea.Cmd {
	if len(m.FilteredList) == 0 || m.Cursor >= len(m.FilteredList) {
		return nil
	}
	v := m.FilteredList[m.Cursor]
	if _, ok := m.previewCache[v.ID]; ok {
		return nil
	}
	if m.inFlight[v.ID] {
		return nil
	}
	cached := thumbCachePath(v.ID)
	if _, err := os.Stat(cached); err != nil {
		return nil
	}
	if m.inFlight == nil {
		m.inFlight = make(map[string]bool)
	}
	m.inFlight[v.ID] = true
	cols, rows := m.previewImageDims()
	return renderPreviewCmd(v.ID, cached, cols, rows)
}

func (m *Model) priorityFetchCmd(v downloader.Video) tea.Cmd {
	url := v.ThumbnailURL()
	if url == "" || m.inFlight[v.ID] {
		return nil
	}
	if _, err := os.Stat(thumbCachePath(v.ID)); err == nil {
		return nil
	}
	if m.inFlight == nil {
		m.inFlight = make(map[string]bool)
	}
	m.inFlight[v.ID] = true
	return func() tea.Msg {
		if err := downloadThumbnailTo(url, thumbCachePath(v.ID)); err != nil {
			return previewRenderedMsg{videoID: v.ID, err: err}
		}
		cols, rows := m.previewImageDims()
		p, err := renderPlaceholder(thumbCachePath(v.ID), cols, rows)
		return previewRenderedMsg{videoID: v.ID, preview: p, err: err}
	}
}

// Background task to ask yt-dlp for the precise size
func (m *Model) fetchSizeCmd(v downloader.Video, format string) tea.Cmd {
	cacheKey := v.ID + "-" + format
	if m.SizeCache[cacheKey] != "" || m.SizeInFlight[cacheKey] {
		return nil
	}
	m.SizeInFlight[cacheKey] = true

	return func() tea.Msg {
		args := []string{"--print", "%(filesize,filesize_approx)s"}
		if format == "Audio" {
			args = append(args, "-f", "bestaudio/best")
		} else {
			args = append(args, "-f", "bestvideo+bestaudio/best")
		}
		args = append(args, v.ID)

		cmd := exec.Command("yt-dlp", args...)
		out, err := cmd.Output()

		sizeStr := "Unknown"
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			for _, line := range lines {
				if line != "NA" && line != "" {
					if bytes, err := strconv.ParseFloat(line, 64); err == nil {
						sizeStr = fmt.Sprintf("%.1f MB", bytes/(1024*1024))
						break
					}
				}
			}
		}
		return sizeFetchedMsg{videoID: v.ID, format: format, sizeStr: sizeStr}
	}
}

func downloadThumbnailTo(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if strings.Contains(url, "maxresdefault") {
			fallback := strings.Replace(url, "maxresdefault", "hqdefault", 1)
			return downloadThumbnailTo(fallback, dest)
		}
		return fmt.Errorf("thumbnail fetch: status %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}
