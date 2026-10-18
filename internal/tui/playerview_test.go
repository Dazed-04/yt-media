package tui

import (
	"strings"
	"testing"

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
	for _, w := range []string{"--ytdl-format=bestvideo[height<=1080]+bestaudio/best[height<=1080]", "--ytdl-raw-options=cookies-from-browser=chrome", "-- https://x/y"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in %s", w, got)
		}
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
