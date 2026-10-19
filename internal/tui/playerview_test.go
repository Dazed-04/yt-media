package tui

import (
	"strings"
	"testing"

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

func TestVideoPreviewArgs(t *testing.T) {
	o := config.DefaultDownloadOptions()
	o.MaxVideoHeight = 1080
	o.CookiesFromBrowser = "chrome"
	got := strings.Join(videoPreviewArgs(o, "https://x/y"), " ")
	for _, w := range []string{"--ytdl-format=bestvideo[height<=1080]+bestaudio/best[height<=1080]", "--ytdl-raw-options-remove=cookies-from-browser", "-- https://x/y"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in %s", w, got)
		}
	}
}

func TestPreviewNeverUsesCookies(t *testing.T) {
	o := config.DefaultDownloadOptions()
	o.CookiesFromBrowser = "firefox:/home/u/.zen/abc.Default (release)"
	got := strings.Join(videoPreviewArgs(o, "https://x/y"), " ")
	if strings.Contains(got, "cookies-from-browser=") || strings.Contains(got, "-add=") {
		t.Errorf("preview passes cookies: %s", got)
	}
	if !strings.Contains(got, "--ytdl-raw-options-remove=cookies-from-browser") {
		t.Errorf("preview should still drop mpv.conf's cookies: %s", got)
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
