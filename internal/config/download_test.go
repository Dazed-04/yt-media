package config

import (
	"encoding/json"
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
