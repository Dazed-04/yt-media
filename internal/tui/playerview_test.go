package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"yt-downloader/internal/config"
	"yt-downloader/internal/downloader"
)

func TestDedupeKeepsOrder(t *testing.T) {
	vs := []downloader.Video{{ID: "aaaaaaaaaaa"}, {ID: "bbbbbbbbbbb"}, {ID: "aaaaaaaaaaa"}}
	got := dedupe(vs)
	if len(got) != 2 || got[0].ID != "aaaaaaaaaaa" || got[1].ID != "bbbbbbbbbbb" {
		t.Errorf("dedupe = %+v", got)
	}
}

func TestFmtClockAndBar(t *testing.T) {
	for in, want := range map[float64]string{-3: "0:00", 5: "0:05", 65: "1:05", 3725: "1:02:05"} {
		if got := fmtClock(in); got != want {
			t.Errorf("fmtClock(%v) = %q, want %q", in, got, want)
		}
	}
	on, off := progressBar(50, 200, 20)
	if len([]rune(on)) != 5 || len([]rune(off)) != 15 {
		t.Errorf("bar = %d+%d", len([]rune(on)), len([]rune(off)))
	}
	on, off = progressBar(10, 0, 8) // length not known yet
	if on != "" || len([]rune(off)) != 8 {
		t.Errorf("unknown-length bar = %q %q", on, off)
	}
	if on, _ = progressBar(999, 100, 10); len([]rune(on)) != 10 {
		t.Error("bar overflows past the end")
	}
}

func TestPreviewWithoutMpvShowsError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	m := &Model{ChoiceType: "Video"}
	m.Config.DownloadOptions = config.DefaultDownloadOptions()
	m.startPreview(downloader.Video{ID: "aaaaaaaaaaa"})
	if !m.FlashErr || !strings.Contains(m.Flash, "mpv not found") {
		t.Errorf("flash = %q err=%v", m.Flash, m.FlashErr)
	}
	m.ChoiceType = "Audio"
	m.startPreview(downloader.Video{ID: "aaaaaaaaaaa"})
	if m.PlayerOpen || !strings.Contains(m.Flash, "mpv not found") {
		t.Errorf("audio: open=%v flash=%q", m.PlayerOpen, m.Flash)
	}
}

func TestQueueAllSkipsStaged(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m := &Model{ChoiceType: "Audio"}
	m.FilteredList = []downloader.Video{{ID: "aaaaaaaaaaa"}, {ID: "bbbbbbbbbbb"}}
	m.Queue = []DownloadReq{{Video: m.FilteredList[0], Type: "Audio"}}
	m.queueAll()
	if len(m.Queue) != 2 {
		t.Fatalf("queue = %d", len(m.Queue))
	}
	m.queueAll()
	if len(m.Queue) != 2 || m.Flash != "Nothing to add" {
		t.Errorf("second run: %d %q", len(m.Queue), m.Flash)
	}
}

func TestWideTitlesStayInsideTheirLine(t *testing.T) {
	title := "VK Blanka  - Vickeblanka / 『Black Catcher』 (official music video)"
	if got := safeTruncate(title, 30); lipgloss.Width(got) > 30 {
		t.Errorf("truncated to %d columns, want <= 30: %q", lipgloss.Width(got), got)
	}
	if got := safeTruncate("abc", 10); got != "abc" {
		t.Errorf("short string changed: %q", got)
	}

	// the queue window's running row must not exceed the window width
	m := &Model{Width: 100, Height: 30}
	m.Jobs = []DownloadJob{{Status: StatusDownloading, Progress: 0, Req: DownloadReq{Video: downloader.Video{Title: title, Artist: "VK Blanka"}}}}
	box := m.renderQueueOverlay()
	w := lipgloss.Width(strings.Split(box, "\n")[0])
	for i, l := range strings.Split(box, "\n") {
		if lipgloss.Width(l) != w {
			t.Errorf("line %d is %d columns, want %d", i, lipgloss.Width(l), w)
		}
	}
	for _, l := range strings.Split(stripANSI(box), "\n") {
		if strings.Contains(l, "↓") && !strings.Contains(l, "0%") {
			t.Errorf("percentage not on the title's line: %q", l)
		}
	}
}

// fakeTools puts stand-in mpv and yt-dlp scripts first on PATH.
func fakeTools(t *testing.T, ytdlp, mpv string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"yt-dlp": ytdlp, "mpv": mpv} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// streamURL serves 206 to everything, like a stream URL that is ready.
func streamURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusPartialContent) }))
	t.Cleanup(srv.Close)
	return srv.URL + "/videoplayback"
}

// ytdlpStream is a fake yt-dlp that describes a stream at url, and logs its arguments.
func ytdlpStream(url, log string) string {
	return `echo "$@" >> ` + log + `
printf '{"title":"Vid","requested_formats":[{"url":"` + url + `","vcodec":"avc1","acodec":"none"},{"url":"` + url + `","vcodec":"none","acodec":"opus"}]}\n'`
}

func TestVideoPreviewReportsWhenMpvFailsToOpen(t *testing.T) {
	fakeTools(t, ytdlpStream(streamURL(t), os.DevNull), "echo 'Failed to open the file' >&2; exit 2")
	err := openVideoPreview(config.DefaultDownloadOptions(), "https://x/y")
	if err == nil || !strings.Contains(err.Error(), "Failed to open") {
		t.Errorf("err = %v", err)
	}
}

func TestVideoPreviewReportsResolveFailure(t *testing.T) {
	fakeTools(t, "echo 'ERROR: Video unavailable' >&2; exit 1", "sleep 1")
	err := openVideoPreview(config.DefaultDownloadOptions(), "https://x/y")
	if err == nil || !strings.Contains(err.Error(), "Video unavailable") {
		t.Errorf("err = %v", err)
	}
}

func TestVideoPreviewStreamsTheResolvedUrls(t *testing.T) {
	dir := t.TempDir()
	argsFile, ytLog := filepath.Join(dir, "args"), filepath.Join(dir, "yt")
	url := streamURL(t)
	fakeTools(t, ytdlpStream(url, ytLog), "printf '%s\\n' \"$@\" > "+argsFile+"; sleep 1")
	o := config.DefaultDownloadOptions()
	o.MaxVideoHeight = 1440
	if err := openVideoPreview(o, "https://x/y"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, ytLog); !strings.Contains(got, "bestvideo[height<=1440]+bestaudio/best[height<=1440]") || strings.Contains(got, "cookies") {
		t.Errorf("yt-dlp args = %q", got)
	}
	got := readFile(t, argsFile)
	for _, w := range []string{"--ytdl=no", "--force-media-title=Vid", "--audio-file=" + url, "--\n" + url} {
		if !strings.Contains(got, w) {
			t.Errorf("mpv args lack %q:\n%s", w, got)
		}
	}
}

func TestVideoPreviewFallsBackToDefaultClient(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "yt")
	url := streamURL(t)
	fakeTools(t, `case "$*" in *player_client*) echo "$@" >> `+log+`; echo "ERROR: client refused" >&2; exit 1;; esac
`+ytdlpStream(url, log), "sleep 1")
	o := config.DefaultDownloadOptions()
	o.PreviewClient = "web_safari"
	if err := openVideoPreview(o, "https://x/y"); err != nil {
		t.Fatalf("fallback failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(readFile(t, log)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "player_client=web_safari") || strings.Contains(lines[1], "player_client") {
		t.Errorf("attempts = %q", lines)
	}
	// every client failing: the error is reported, not swallowed
	fakeTools(t, "echo 'ERROR: client refused' >&2; exit 1", "sleep 1")
	if err := openVideoPreview(o, "https://x/y"); err == nil || !strings.Contains(err.Error(), "client refused") {
		t.Errorf("err = %v", err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never written", p)
	return ""
}
