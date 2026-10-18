package config

import (
	"fmt"
	"strings"
)

// DownloadOptions are the yt-dlp and player settings. It is embedded in
// Config, so its keys sit at the top level of config.json.
type DownloadOptions struct {
	AudioFormat        string `json:"audio_format"`         // mp3 | opus | flac | m4a
	MaxVideoHeight     int    `json:"max_video_height"`     // 0 = no limit (e.g. 1080)
	CookiesFromBrowser string `json:"cookies_from_browser"` // "" or e.g. "firefox", "chrome:Profile 1"
	SponsorblockRemove string `json:"sponsorblock_remove"`  // "" or categories, e.g. "music_offtopic"
	PreviewPlayer      string `json:"preview_player"`       // program used by the p key; default mpv
}

// DefaultDownloadOptions are used for keys missing from config.json.
func DefaultDownloadOptions() DownloadOptions {
	return DownloadOptions{AudioFormat: "mp3", PreviewPlayer: "mpv"}
}

// AudioFormats are the formats the audio_format option accepts.
var AudioFormats = []string{"mp3", "opus", "flac", "m4a"}

// AudioFormatOrDefault is audio_format, or "mp3" when it is blank or unknown.
func (o DownloadOptions) AudioFormatOrDefault() string {
	f := strings.ToLower(strings.TrimSpace(o.AudioFormat))
	for _, ok := range AudioFormats {
		if f == ok {
			return f
		}
	}
	return "mp3"
}

// VideoSelector is the yt-dlp -f value for video, capped at max_video_height.
func (o DownloadOptions) VideoSelector() string {
	if h := o.MaxVideoHeight; h > 0 {
		return fmt.Sprintf("bestvideo[height<=%d]+bestaudio/best[height<=%d]", h, h)
	}
	return "bestvideo+bestaudio/best"
}

// CookieArgs are the yt-dlp arguments for cookies_from_browser (none when it
// is blank, or looks like an option rather than a browser name).
func (o DownloadOptions) CookieArgs() []string {
	v := strings.TrimSpace(o.CookiesFromBrowser)
	if v == "" || strings.HasPrefix(v, "-") {
		return nil
	}
	return []string{"--cookies-from-browser", v}
}

var sponsorblockCategories = map[string]bool{
	"sponsor": true, "intro": true, "outro": true, "selfpromo": true, "preview": true,
	"filler": true, "interaction": true, "music_offtopic": true, "chapter": true, "all": true,
}

// SponsorblockArgs are the yt-dlp arguments for sponsorblock_remove. An
// unknown category disables the option rather than being passed on.
func (o DownloadOptions) SponsorblockArgs() []string {
	v := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(o.SponsorblockRemove), " ", ""))
	if v == "" {
		return nil
	}
	for _, c := range strings.Split(v, ",") {
		if !sponsorblockCategories[c] {
			return nil
		}
	}
	return []string{"--sponsorblock-remove", v}
}

// Player is the preview program, "mpv" unless configured.
func (o DownloadOptions) Player() string {
	if p := strings.TrimSpace(o.PreviewPlayer); p != "" {
		return p
	}
	return "mpv"
}
