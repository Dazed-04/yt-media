package tui

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"yt-downloader/internal/downloader"

	tea "charm.land/bubbletea/v2"
)

// ---------- thumbnail cache on disk ----------

func thumbCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(dir, "yt-media", "thumbs")
}

func thumbCachePath(id string) string {
	return filepath.Join(thumbCacheDir(), id+".jpg")
}

// thumbURL picks the best-looking thumbnail URL for v.
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

var httpClient = &http.Client{Timeout: 15 * time.Second}

// downloadThumbnailTo saves url to dest, falling back to a smaller size when
// maxresdefault doesn't exist. The file is written to a temp name first, so a
// cut-off download never looks like a cached thumbnail.
func downloadThumbnailTo(url, dest string) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if strings.Contains(url, "maxresdefault") {
			return downloadThumbnailTo(strings.Replace(url, "maxresdefault", "hqdefault", 1), dest)
		}
		return fmt.Errorf("thumbnail fetch: status %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	// Unique temp name: the background prefetch and the cursor fetch can be
	// downloading the same thumbnail at the same moment.
	f, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".*.part")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// ---------- background prefetch ----------

// fetchOneThumbnail downloads the thumbnail for videos[idx]. Each finished
// download triggers the next one, so thumbnails load one after another.
func fetchOneThumbnail(videos []downloader.Video, idx, gen int) tea.Cmd {
	if idx >= len(videos) {
		return nil
	}
	v := videos[idx]
	return func() tea.Msg {
		dest := thumbCachePath(v.ID)
		if _, err := os.Stat(dest); err == nil {
			log.Printf("[THUMB] prefetch #%d %s: already on disk (cursor fetch got there first)", idx, v.ID)
			return thumbnailReadyMsg{videoID: v.ID, gen: gen}
		}
		start := time.Now()
		if err := downloadThumbnailTo(thumbURL(v), dest); err != nil {
			// fall back to a URL guaranteed to exist
			_ = downloadThumbnailTo("https://i.ytimg.com/vi/"+v.ID+"/hqdefault.jpg", dest)
		}
		log.Printf("[THUMB] prefetch #%d %s: downloaded in %s", idx, v.ID, time.Since(start).Round(time.Millisecond))
		return thumbnailReadyMsg{videoID: v.ID, gen: gen}
	}
}

func (m *Model) onThumbnailReady(msg thumbnailReadyMsg) tea.Cmd {
	var cmds []tea.Cmd

	if v, ok := m.current(); ok && v.ID == msg.videoID {
		if cmd := m.renderCmd(v.ID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	// Only the chain started by the current search may advance the prefetch.
	if msg.gen == m.prefetchGen {
		m.prefetchIdx++
		cmds = append(cmds, fetchOneThumbnail(m.prefetchList, m.prefetchIdx, m.prefetchGen))
	}
	return tea.Batch(cmds...)
}

// ---------- rendering the image ----------

// renderCmd renders the cached thumbnail of id, unless that's already done or
// under way.
func (m *Model) renderCmd(id string) tea.Cmd {
	if _, ok := m.previewCache[id]; ok || m.inFlight[id] {
		return nil
	}
	if m.inFlight == nil {
		m.inFlight = make(map[string]bool)
	}
	m.inFlight[id] = true
	cols, rows := m.previewImageDims()
	return renderPreviewCmd(id, thumbCachePath(id), cols, rows)
}

func renderPreviewCmd(videoID, imagePath string, cols, rows int) tea.Cmd {
	return func() tea.Msg {
		p, err := renderPlaceholder(imagePath, cols, rows)
		return previewRenderedMsg{videoID: videoID, preview: p, err: err}
	}
}

// showCachedPreview makes sure the highlighted item's image is rendered, if
// its thumbnail is already on disk.
func (m *Model) showCachedPreview() tea.Cmd {
	v, ok := m.current()
	if !ok {
		return nil
	}
	if _, err := os.Stat(thumbCachePath(v.ID)); err != nil {
		return nil
	}
	return m.renderCmd(v.ID)
}

// priorityFetchCmd downloads and renders the highlighted item's thumbnail
// right away instead of waiting for the background prefetch to reach it.
func (m *Model) priorityFetchCmd(v downloader.Video) tea.Cmd {
	if m.inFlight[v.ID] {
		return nil
	}
	dest := thumbCachePath(v.ID)
	if _, err := os.Stat(dest); err == nil {
		return nil // already prefetched
	}
	log.Printf("[THUMB] cursor fetch %s: not prefetched yet", v.ID)
	url := thumbURL(v) // same URL (and quality) the prefetch would use
	if m.inFlight == nil {
		m.inFlight = make(map[string]bool)
	}
	m.inFlight[v.ID] = true

	// Everything the goroutine needs is read here, on the Update goroutine.
	cols, rows := m.previewImageDims()
	return func() tea.Msg {
		if err := downloadThumbnailTo(url, dest); err != nil {
			// The background prefetch may have saved it in the meantime.
			if _, statErr := os.Stat(dest); statErr != nil {
				return previewRenderedMsg{videoID: v.ID, err: err}
			}
		}
		p, err := renderPlaceholder(dest, cols, rows)
		return previewRenderedMsg{videoID: v.ID, preview: p, err: err}
	}
}

func (m *Model) onPreviewRendered(msg previewRenderedMsg) tea.Cmd {
	delete(m.inFlight, msg.videoID)
	if msg.err != nil {
		log.Printf("[PREVIEW] render failed for %s: %v", msg.videoID, msg.err)
		return nil
	}
	if !m.inResults(msg.videoID) {
		return nil // the result list changed while this was rendering
	}
	if m.previewCache == nil {
		m.previewCache = make(map[string]renderedPreview)
	}
	m.previewCache[msg.videoID] = msg.preview

	// Send the image data through Bubble Tea's own output buffer. Writing to
	// os.Stdout from here can land in the middle of a frame the renderer is
	// drawing, which shows up as text bleeding into the image.
	if msg.preview.kittyBytes != nil {
		return tea.Raw(string(msg.preview.kittyBytes))
	}
	return nil
}

func (m *Model) inResults(id string) bool {
	for _, v := range m.MusicList {
		if v.ID == id {
			return true
		}
	}
	return false
}

// ---------- cache housekeeping ----------

func (m *Model) resetInFlight() {
	m.inFlight = nil
}

// purgePreviewCache forgets every rendered image and deletes it from the
// terminal.
func (m *Model) purgePreviewCache() {
	for _, pv := range m.previewCache {
		if pv.kittyID != 0 {
			m.rawOut = append(m.rawOut, kittyDeleteSeq(pv.kittyID))
		}
	}
	m.previewCache = nil
	m.inFlight = nil
}
