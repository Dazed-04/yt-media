package tui

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"yt-downloader/internal/downloader"

	tea "charm.land/bubbletea/v2"
)

type (
	downloadFinishedMsg struct {
		Index int
		Err   error
	}
	searchResultsMsg  []downloader.Video
	errorMsg          error
	thumbnailReadyMsg struct{ videoID string }
)

type checkLibraryMsg struct {
	ToDownload []DownloadReq
	Skipped    []string
}

var progressRe = regexp.MustCompile(`\[download\]\s+([0-9.]+)\%`)

func waitForProgress(ch chan JobProgressMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func (m *Model) startWorkerQueue() tea.Cmd {
	var cmds []tea.Cmd
	for i := 0; i < len(m.Jobs); i++ {
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

func (m *Model) doSearch() tea.Cmd {
	return func() tea.Msg {
		finalQuery := m.SearchQuery
		results, err := downloader.SearchYoutube(finalQuery, 15)
		if err != nil {
			return errorMsg(err)
		}
		return searchResultsMsg(results)
	}
}

func (m *Model) downloadItem(jobIndex int) tea.Cmd {
	return func() tea.Msg {
		req := m.Jobs[jobIndex].Req
		v := req.Video

		var baseDir string
		if m.ChoiceMode == "Single" {
			baseDir = filepath.Join(m.User, "Music", "single")
			if req.Type == "Video" {
				baseDir = filepath.Join(m.User, "Videos", "single")
			}
		} else {
			artist := sanitize(v.Artist)
			if artist == "" {
				artist = "Unknown"
			}
			album := sanitize(v.Album)
			if album == "" {
				album = "Playlist"
			}
			dirName := fmt.Sprintf("%s - %s", artist, album)
			baseDir = filepath.Join(m.User, "Music", dirName)
			if req.Type == "Video" {
				baseDir = filepath.Join(m.User, "Videos", dirName)
			}
		}

		_ = os.MkdirAll(baseDir, 0o755)
		outputPath := filepath.Join(baseDir, "%(title)s.%(ext)s")

		args := []string{"-f"}
		if req.Type == "Audio" {
			args = append(args, "bestaudio/best", "-o", outputPath, "--extract-audio", "--audio-format", "mp3")
		} else {
			args = append(args, "bestvideo+bestaudio/best", "-o", outputPath)
		}

		args = append(args, "--embed-metadata", "--embed-thumbnail", "--newline", "--no-colors", v.ID)

		cmd := exec.Command("yt-dlp", args...)
		stdout, _ := cmd.StdoutPipe()
		cmd.Stderr = log.Writer()

		if err := cmd.Start(); err != nil {
			return downloadFinishedMsg{Index: jobIndex, Err: err}
		}

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			matches := progressRe.FindStringSubmatch(line)
			if len(matches) == 2 {
				if p, err := strconv.ParseFloat(matches[1], 64); err == nil {
					m.ProgressChan <- JobProgressMsg{Index: jobIndex, Progress: p / 100.0}
				}
			}
		}

		err := cmd.Wait()
		m.ProgressChan <- JobProgressMsg{Index: jobIndex, Progress: 1.0}
		return downloadFinishedMsg{Index: jobIndex, Err: err}
	}
}

func thumbCachePath(id string) string {
	return filepath.Join(thumbCacheDir(), id+".jpg")
}

func thumbCacheDir() string {
	dir := filepath.Join(os.Getenv("HOME"), ".cache", "yt-downloader", "thumbs")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func thumbURL(v downloader.Video) string {
	url := v.Thumbnail
	if url == "" {
		return "https://i.ytimg.com/vi/" + v.ID + "/mqdefault.jpg"
	}
	for _, hires := range []string{"maxresdefault", "hqdefault", "sddefault"} {
		url = strings.ReplaceAll(url, hires, "mqdefault")
	}
	if !strings.HasSuffix(url, ".jpg") {
		return "https://i.ytimg.com/vi/" + v.ID + "/mqdefault.jpg"
	}
	return url
}

func fetchOneThumbnail(videos []downloader.Video, idx int) tea.Cmd {
	if idx >= len(videos) {
		return nil
	}
	v := videos[idx]
	return func() tea.Msg {
		dest := thumbCachePath(v.ID)
		if _, err := os.Stat(dest); err == nil {
			return thumbnailReadyMsg{videoID: v.ID}
		}
		cmd := exec.Command("curl", "--silent", "--max-time", "6", "--output", dest, thumbURL(v))
		cmd.Stderr = nil
		cmd.Stdout = nil
		_ = cmd.Run()
		return thumbnailReadyMsg{videoID: v.ID}
	}
}

func sanitize(s string) string {
	s = strings.ToLower(s)
	reg := regexp.MustCompile(`[^a-z0-9\s]+`)
	s = reg.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func libSanitize(s string) string {
	s = strings.ToLower(s)
	reg := regexp.MustCompile(`[^a-z0-9]+`)
	s = reg.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func (m *Model) checkLibraryManual(items []DownloadReq) tea.Cmd {
	return func() tea.Msg {
		var toDownload []DownloadReq
		var skipped []string

		basePath := filepath.Join(m.User, "Music")
		_ = os.MkdirAll(basePath, 0o755)

		for _, req := range items {
			target := libSanitize(req.Video.Title)
			found := false
			filepath.WalkDir(basePath, func(path string, d os.DirEntry, err error) error {
				if err != nil || found {
					return nil
				}
				if d.IsDir() {
					if d.Name() == "lyrics" {
						return filepath.SkipDir
					}
					return nil
				}
				filename := libSanitize(strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())))
				if strings.Contains(filename, target) {
					found = true
				}
				return nil
			})
			if found {
				skipped = append(skipped, req.Video.Title)
			} else {
				toDownload = append(toDownload, req)
			}
		}

		return checkLibraryMsg{ToDownload: toDownload, Skipped: skipped}
	}
}
