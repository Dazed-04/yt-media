package config

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// DownloadOptions are the yt-dlp and player settings. It is embedded in
// Config, so its keys sit at the top level of config.json.
type DownloadOptions struct {
	AudioFormat        string `json:"audio_format"`         // mp3 | opus | flac | m4a
	MaxVideoHeight     int    `json:"max_video_height"`     // 0 = no limit (e.g. 1080)
	CookiesFromBrowser string `json:"cookies_from_browser"` // "" or e.g. "firefox", "chrome:Profile 1"
	SponsorblockRemove string `json:"sponsorblock_remove"`  // "" or categories, e.g. "music_offtopic"
	PreviewPlayer      string `json:"preview_player"`       // program used by the p key; default mpv
	SplitChapters      bool   `json:"split_chapters"`
	SearchSource       string `json:"search_source"`

	// Preview tuning. All optional: they exist so a change in YouTube or
	// yt-dlp can be worked around by editing config.json, not rebuilding.
	PreviewMode        string   `json:"preview_mode"`         // audio previews: "download" (default), "stream" or "auto" (stream, fall back to download)
	PreviewClient      string   `json:"preview_client"`       // preferred YouTube client for previews, e.g. "web_safari"; falls back to yt-dlp's default
	PreviewWaitSeconds int      `json:"preview_wait_seconds"` // how long a streamed preview waits for YouTube to accept the stream URL; 0 = 10
	PreviewAudioFormat string   `json:"preview_audio_format"` // yt-dlp -f for audio previews; default bestaudio/best
	PreviewVideoFormat string   `json:"preview_video_format"` // yt-dlp -f for video previews; default follows max_video_height
	PreviewYtdlpArgs   []string `json:"preview_ytdlp_args"`   // extra yt-dlp arguments for previews (streamed or downloaded)
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
	if slices.Contains(AudioFormats, f) {
		return f
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
	for c := range strings.SplitSeq(v, ",") {
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

// DefaultPreviewHeight is the video preview height when neither
// preview_video_format nor max_video_height is set.
const DefaultPreviewHeight = 1080

// PreviewAudioSelector is the yt-dlp -f value for audio previews.
func (o DownloadOptions) PreviewAudioSelector() string {
	if f := strings.TrimSpace(o.PreviewAudioFormat); f != "" {
		return f
	}
	return "bestaudio/best"
}

// PreviewVideoSelector is the yt-dlp -f value for video previews:
// preview_video_format if set, otherwise max_video_height (1080 when that is
// unlimited, so a preview doesn't download 4K).
func (o DownloadOptions) PreviewVideoSelector() string {
	if f := strings.TrimSpace(o.PreviewVideoFormat); f != "" {
		return f
	}
	if o.MaxVideoHeight <= 0 {
		o.MaxVideoHeight = DefaultPreviewHeight
	}
	return o.VideoSelector()
}

// PreviewExtraArgs is preview_ytdlp_args with blank entries dropped. It is
// used whenever a preview asks yt-dlp for something, streamed or downloaded.
func (o DownloadOptions) PreviewExtraArgs() []string {
	var args []string
	for _, a := range o.PreviewYtdlpArgs {
		if a = strings.TrimSpace(a); a != "" {
			args = append(args, a)
		}
	}
	return args
}

// PreviewArgs are the yt-dlp arguments for a preview that is downloaded
// first: the cookies, then preview_ytdlp_args. Streamed previews use only
// PreviewExtraArgs, never cookies.
func (o DownloadOptions) PreviewArgs() []string {
	return append(o.CookieArgs(), o.PreviewExtraArgs()...)
}

// PreviewModeOrDefault is preview_mode, or "auto" when blank or unknown.
func (o DownloadOptions) PreviewModeOrDefault() string {
	switch m := strings.ToLower(strings.TrimSpace(o.PreviewMode)); m {
	case "download", "stream", "auto":
		return m
	}
	return "auto"
}

// PreviewClientOrEmpty is preview_client when it looks like a client list
// ("web_safari", "default,-tv"), else "" (yt-dlp's default). Anything else
// is dropped rather than passed to yt-dlp.
func (o DownloadOptions) PreviewClientOrEmpty() string {
	c := strings.TrimSpace(o.PreviewClient)
	for _, r := range c {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("_,-.", r) {
			return ""
		}
	}
	return c
}

// previewWaitBase is how long a streamed preview always waits for the stream
// URL to start working. preview_wait_seconds is added to it.
const previewWaitBase = 5 * time.Second

// PreviewWait is previewWaitBase plus preview_wait_seconds. Zero or negative
// values add nothing, and the total is capped at 5 minutes.
func (o DownloadOptions) PreviewWait() time.Duration {
	extra := max(o.PreviewWaitSeconds, 0)
	return min(previewWaitBase+time.Duration(extra)*time.Second, 5*time.Minute)
}

// SearchSourceOrDefault is search_source, or "youtube" when blank or unknown.
func (o DownloadOptions) SearchSourceOrDefault() string {
	if s := strings.ToLower(strings.TrimSpace(o.SearchSource)); s == "soundcloud" {
		return s
	}
	return "youtube"
}
