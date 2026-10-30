package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDownloadOptionsDefaultsAndJSON(t *testing.T) {
	cfg := defaults()
	if err := json.Unmarshal([]byte(`{"audio_format":"OPUS","max_video_height":1080}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.AudioFormatOrDefault() != "opus" || cfg.MaxVideoHeight != 1080 {
		t.Errorf("parsed = %+v", cfg.DownloadOptions)
	}
	if cfg.Player() != "mpv" { // untouched keys keep their defaults
		t.Errorf("player = %q", cfg.Player())
	}
}

func TestDownloadOptionHelpers(t *testing.T) {
	o := DownloadOptions{}
	if o.AudioFormatOrDefault() != "mp3" || o.VideoSelector() != "bestvideo+bestaudio/best" || o.CookieArgs() != nil || o.SponsorblockArgs() != nil {
		t.Errorf("zero value: %q %q", o.AudioFormatOrDefault(), o.VideoSelector())
	}
	o = DownloadOptions{AudioFormat: "wav", MaxVideoHeight: 720, CookiesFromBrowser: " firefox ", SponsorblockRemove: "Music_Offtopic, intro"}
	if o.AudioFormatOrDefault() != "mp3" {
		t.Error("unknown format must fall back to mp3")
	}
	if got := o.VideoSelector(); got != "bestvideo[height<=720]+bestaudio/best[height<=720]" {
		t.Errorf("selector = %q", got)
	}
	if got := o.CookieArgs(); len(got) != 2 || got[1] != "firefox" {
		t.Errorf("cookies = %v", got)
	}
	if got := o.SponsorblockArgs(); len(got) != 2 || got[1] != "music_offtopic,intro" {
		t.Errorf("sponsorblock = %v", got)
	}
	// things that must never reach yt-dlp as options
	if (DownloadOptions{CookiesFromBrowser: "--exec rm"}).CookieArgs() != nil {
		t.Error("an option-looking cookie value was passed on")
	}
	if (DownloadOptions{SponsorblockRemove: "intro,bogus"}).SponsorblockArgs() != nil {
		t.Error("an unknown sponsorblock category was passed on")
	}
}

func TestPreviewOptions(t *testing.T) {
	var o DownloadOptions
	if o.PreviewAudioSelector() != "bestaudio/best" || o.PreviewVideoSelector() != "bestvideo[height<=1080]+bestaudio/best[height<=1080]" || o.PreviewArgs() != nil {
		t.Errorf("defaults: %q %q %v", o.PreviewAudioSelector(), o.PreviewVideoSelector(), o.PreviewArgs())
	}
	o = DownloadOptions{MaxVideoHeight: 2160, CookiesFromBrowser: "firefox", PreviewYtdlpArgs: []string{"--extractor-args", " youtube:player_client=web_safari ", ""}}
	if got := o.PreviewVideoSelector(); got != "bestvideo[height<=2160]+bestaudio/best[height<=2160]" {
		t.Errorf("a configured height must be respected, got %q", got)
	}
	want := []string{"--cookies-from-browser", "firefox", "--extractor-args", "youtube:player_client=web_safari"}
	if got := o.PreviewArgs(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("args = %v", got)
	}
	o.PreviewAudioFormat, o.PreviewVideoFormat = " 140/bestaudio ", "best[height<=480]"
	if o.PreviewAudioSelector() != "140/bestaudio" || o.PreviewVideoSelector() != "best[height<=480]" {
		t.Error("explicit formats must win")
	}
}

func TestPreviewModeAndClient(t *testing.T) {
	for in, want := range map[string]string{"": "auto", "Stream": "stream", " auto ": "auto", "download": "download", "live": "auto"} {
		if got := (DownloadOptions{PreviewMode: in}).PreviewModeOrDefault(); got != want {
			t.Errorf("mode %q -> %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"": "", " web_safari ": "web_safari", "default,-tv": "default,-tv", "a b": "", "x;rm": "", "--exec": "--exec"} {
		if got := (DownloadOptions{PreviewClient: in}).PreviewClientOrEmpty(); got != want {
			t.Errorf("client %q -> %q, want %q", in, got, want)
		}
	}
}

func TestPreviewWait(t *testing.T) {
	for in, want := range map[int]int{0: 5, -3: 5, 1: 6, 10: 15, 9999: 300} {
		if got := (DownloadOptions{PreviewWaitSeconds: in}).PreviewWait(); got.Seconds() != float64(want) {
			t.Errorf("extra %d -> %v, want %ds", in, got, want)
		}
	}
}
