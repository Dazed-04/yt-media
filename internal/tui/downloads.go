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

	"yt-downloader/internal/config"
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

var youtubeURLRe = regexp.MustCompile(`(?:youtube\.com/watch\?v=|youtu\.be/|youtube\.com/shorts/)([\w-]{11})`)

func extractYoutubeID(s string) (string, bool) {
	m := youtubeURLRe.FindStringSubmatch(s)
	if len(m) == 2 {
		return m[1], true
	}
	return "", false
}

func (m *Model) doSearch() tea.Cmd {
	return func() tea.Msg {
		query := strings.TrimSpace(m.SearchQuery)
		if id, ok := extractYoutubeID(query); ok {
			v, err := downloader.FetchSingle(id)
			if err != nil {
				return errorMsg(err)
			}
			return searchResultsMsg([]downloader.Video{v})
		}
		results, err := downloader.SearchYoutube(query, m.Config.SearchResultLimit)
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

		musicRoot := m.Config.MusicDir
		if musicRoot == "" {
			musicRoot = filepath.Join(m.User, "Music")
		}
		videoRoot := m.Config.VideoDir
		if videoRoot == "" {
			videoRoot = filepath.Join(m.User, "Videos")
		}

		var baseDir string
		if m.ChoiceMode == "Single" {
			baseDir = filepath.Join(musicRoot, "single")
			if req.Type == "Video" {
				baseDir = filepath.Join(videoRoot, "single")
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
			baseDir = filepath.Join(musicRoot, dirName)
			if req.Type == "Video" {
				baseDir = filepath.Join(videoRoot, dirName)
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

		args = append(args, "--embed-metadata", "--embed-thumbnail", "--newline", "--no-colors", "--download-archive", v.ID)

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
		return "https://i.ytimg.com/vi/" + v.ID + "/maxresdefault.jpg"
	}
	for _, lower := range []string{"mqdefault", "sddefault", "hqdefault"} {
		url = strings.ReplaceAll(url, lower, "maxresdefault")
	}
	if !strings.HasSuffix(url, ".jpg") {
		return "https://i.ytimg.com/vi/" + v.ID + "/maxresdefault.jpg"
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
		if err := downloadThumbnailTo(thumbURL(v), dest); err != nil {
			// fall back to a URL guaranteed to exist
			_ = downloadThumbnailTo("https://i.ytimg.com/vi/"+v.ID+"/hqdefault.jpg", dest)
		}
		return thumbnailReadyMsg{videoID: v.ID}
	}
}

func sanitize(s string) string {
	s = strings.ToLower(s)
	reg := regexp.MustCompile(`[^a-z0-9\s]+`)
	s = reg.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func (m *Model) archivePath() string {
	if m.Config.ArchiveFile != "" {
		return m.Config.ArchiveFile
	}
	dir := filepath.Dir(config.Path())
	return filepath.Join(dir, "archive.txt")
}

func loadArchiveIDs(path string) map[string]bool {
	ids := make(map[string]bool)
	data, err := os.ReadFile(path)
	if err != nil {
		return ids
	}
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 {
			ids[fields[1]] = true
		}
	}
	return ids
}

func (m *Model) checkLibraryManual(items []DownloadReq) tea.Cmd {
	return func() tea.Msg {
		archived := loadArchiveIDs(m.archivePath())
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
