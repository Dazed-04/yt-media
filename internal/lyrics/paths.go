package lyrics

import (
	"os"
	"path/filepath"
	"strings"
	"yt-downloader/internal/config"
)

// Options says where lyrics live and what to do.
type Options struct {
	MusicDir  string   // root of the music library; the lyrics folder mirrors its structure
	LyricsDir string   // where .lrc files are written
	Exclude   []string // path prefixes (relative to MusicDir) to leave alone
	VideoSRT  bool     // also write .srt subtitles next to downloaded videos
	Fallback  string   // optional command to try when LRCLIB has nothing; see runFallback
}

// FromConfig builds Options from the user's config. home is the user's home
// directory, used for the default music folder.
func FromConfig(cfg config.Config, home string) Options {
	music := firstNonEmpty(cfg.MusicDir, filepath.Join(home, "Music"))
	return Options{
		MusicDir:  music,
		LyricsDir: firstNonEmpty(cfg.LyricsDir, filepath.Join(music, "lyrics")),
		Exclude:   cfg.LyricsExclude,
		VideoSRT:  cfg.LyricsForVideo,
		Fallback:  cfg.LyricsFallbackCommand,
	}
}

// rel returns p relative to MusicDir, if p is inside it.
func (o Options) rel(p string) (string, bool) {
	r, err := filepath.Rel(o.MusicDir, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return r, true
}

// lrcPath is where the lyrics for an audio file go: LyricsDir/<same relative
// path as in the library>.lrc. Files outside the library go to LyricsDir
// directly.
func (o Options) lrcPath(audio string) string {
	name := audio
	if r, ok := o.rel(audio); ok {
		name = r
	} else {
		name = filepath.Base(audio)
	}
	return filepath.Join(o.LyricsDir, strings.TrimSuffix(name, filepath.Ext(name))+".lrc")
}

// srtPath is the subtitle file next to a video, where players look for it.
func srtPath(video string) string {
	return strings.TrimSuffix(video, filepath.Ext(video)) + ".srt"
}

// excluded reports whether p is under one of the Exclude prefixes.
func (o Options) excluded(p string) bool {
	r, ok := o.rel(p)
	if !ok {
		return false
	}
	r = filepath.ToSlash(r)
	for _, prefix := range o.Exclude {
		if prefix != "" && strings.HasPrefix(r, filepath.ToSlash(prefix)) {
			return true
		}
	}
	return false
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// writeFile writes data to path (creating folders) via a temp file, so a
// crash never leaves a half-written lyrics file that would later be skipped.
func writeFile(path, data string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lyrics-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
