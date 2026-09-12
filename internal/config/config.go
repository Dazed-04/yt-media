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
}

func defaults() Config {
	return Config{
		MaxWorkers:          3,
		DefaultDownloadType: "Audio",
		SearchResultLimit:   15,
		FontShrinkDelta:     1.5,
		NotifyOnBatch:       true,
		NotifyMinItems:      2,
	}
}

func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "yt-downloader", "config.json")
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
  "notify_min_items": 2
}
`

func writeDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultTemplate), 0o644)
}
