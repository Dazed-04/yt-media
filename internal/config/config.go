// Package config contains the config preferences for the tui
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	MaxWorkers          int     `json:"max_workers"`
	DefaultDownloadType string  `json:"default_download_type"` // "Audio" or "Video"
	SearchResultLimit   int     `json:"search_result_limit"`
	MusicDir            string  `json:"music_dir"`    // blank = ~/Music
	VideoDir            string  `json:"video_dir"`    // blank = ~/Videos
	ArchiveFile         string  `json:"archive_file"` // blank = default config dir
	FontShrinkDelta     float64 `json:"font_shrink_delta"`
	NotifyOnBatch       bool    `json:"notify_on_batch"`
	NotifyMinItems      int     `json:"notify_min_items"` // batch = queue size >= this
	NotifyOnlyUnfocused bool    `json:"notify_only_unfocused"`

	FetchAlbum   bool     `json:"fetch_album"`   // look up the real album (MusicBrainz) for tracks that have none
	AlbumExclude []string `json:"album_exclude"` // paths skipped only by album tagger

	DownloadOptions

	FetchLyrics           bool     `json:"fetch_lyrics"`            // look up lyrics after each download
	LyricsDir             string   `json:"lyrics_dir"`              // blank = <music_dir>/lyrics
	LyricsExclude         []string `json:"lyrics_exclude"`          // paths skipped only by lyrics tagger
	MusicExclude          []string `json:"music_excluse"`           // paths (relative to music_dir) to skip for album & lyrics
	LyricsForVideo        bool     `json:"lyrics_for_video"`        // write .srt subtitles next to videos
	LyricsFallbackCommand string   `json:"lyrics_fallback_command"` // optional, tried when LRCLIB has nothing
}

func defaults() Config {
	return Config{
		MaxWorkers:          3,
		DefaultDownloadType: "Audio",
		SearchResultLimit:   15,
		FontShrinkDelta:     1.5,
		NotifyOnBatch:       true,
		NotifyMinItems:      4,
		NotifyOnlyUnfocused: true,
		FetchLyrics:         true,
		FetchAlbum:          true,
		DownloadOptions:     DefaultDownloadOptions(),
	}
}

// CacheDir is the one folder for everything the app generates: the queue,
// thumbnails and debug.log (~/.cache/yt-media, or $XDG_CACHE_HOME/yt-media).
func CacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".cache")
	}
	return filepath.Join(dir, "yt-media")
}

// LogPath is the debug log, next to the other cache files.
func LogPath() string { return filepath.Join(CacheDir(), "debug.log") }

func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "yt-media", "config.json")
}

// Load reads the config file, writing a commented default one on first run.
// A malformed config falls back to defaults rather than crashing
func Load() Config {
	cfg := defaults()
	path := Path()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			_ = writeDefault(path)
		}
		return cfg
	}

	if err := json.Unmarshal(stripComments(data), &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[config] %s is invalid, using defaults: %v\n", path, err)
		return defaults()
	}
	return cfg
}

// stripComments allows "// like this" full-line comments, since plain JSON
// doesn't support them and a hand-edited config is worth documenting inline.
func stripComments(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			continue
		}
		kept = append(kept, l)
	}
	return []byte(strings.Join(kept, "\n"))
}

const defaultTemplate = `{
	// Number of simultaneous yt-dlp downloads.
  "max_workers": 3,

  // "Audio" or "Video" — the default selected on startup.
  "default_download_type": "Audio",

  // How many results to fetch per search.
  "search_result_limit": 15,

  // Override where files land. Leave blank for ~/Music and ~/Videos.
  "music_dir": "",
  "video_dir": "",

  // Leave blank to use the default location alongside this config file.
  "archive_file": "",

  // Points to shrink the Kitty font by while running, for sharper
  // thumbnails. 0 disables this entirely.
  "font_shrink_delta": 1.5,

  // Desktop notification when a download queue finishes. Only fires for
  // batches (queue size >= notify_min_items), never single downloads.
  "notify_on_batch": true,
  "notify_min_items": 4,

  // Only notify when the terminal isn't focused.
  "notify_only_unfocused": true,

  // YouTube often has no album for a song, so it would show as "Unknown
  // Album". When true, the album is looked up on MusicBrainz after each audio
  // download and written into the file's tags (only if the file has none).
  "fetch_album": true,

  // Look up synced lyrics (LRCLIB) after each audio download and save them
  // as .lrc files. "yt-media lyrics" does the same for your whole library.
  "fetch_lyrics": true,

  // Audio format for downloads: "mp3" (default), "opus", "flac" or "m4a".
  // opus and flac need python-mutagen for embedded cover art.
  "audio_format": "mp3",

  // Cap video downloads (and video previews) at this height, e.g. 1080.
  // 0 = best available. (int not string)
  "max_video_height": 0,

  // Use a browser's cookies, e.g. "firefox" or "chrome" (yt-dlp's
  // --cookies-from-browser). Helps with age-restricted videos and with
  // YouTube asking you to sign in. Blank = off. Used for downloads, size
  // lookups, playlists and previews.
  "cookies_from_browser": "",

  // Audio downloads only: cut non-music parts using SponsorBlock categories,
  // comma-separated, e.g. "music_offtopic" or "sponsor,intro,outro".
  // Blank = off.
  "sponsorblock_remove": "",

  // The program used for previews.
  "preview_player": "mpv",

  // Where .lrc files go. Blank = a "lyrics" folder inside music_dir, mirroring
  // its folder structure.
  "lyrics_dir": "",

  // Paths (relative to music_dir) to skip, matched as prefixes.
  "lyrics_exclude": [],

  // Also write .srt subtitles next to downloaded videos (when they have none).
  "lyrics_for_video": false,

  // Optional: a command to try when LRCLIB has nothing. {query} and {output}
  // are replaced; no shell is used. Example:
  //   "syncedlyrics {query} -o {output} --synced-only -p musixmatch"
  "lyrics_fallback_command": "",
}
`

// AlbumExclusions returns merged paths skipped during album scanning.
func (c Config) AlbumExclusions() []string {
	return append(append([]string{}, c.MusicExclude...), c.AlbumExclude...)
}

// LyricsExclusions returns merged paths skipped during lyrics scanning.
func (c Config) LyricsExclusions() []string {
	return append(append([]string{}, c.MusicExclude...), c.LyricsExclude...)
}

func writeDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultTemplate), 0o644)
}
