// Package media reads and writes tags of audio/video files with ffprobe and
// ffmpeg (both already required by yt-dlp for audio extraction).
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"yt-downloader/internal/songname"
)

// Info is what ffprobe reports about a file.
type Info struct {
	Tags         map[string]string // lower-case keys
	Duration     float64
	HasLyrics    bool // embedded lyrics tag
	HasSubtitles bool // embedded subtitle stream
}

// Probe reads tags, length and streams.
func Probe(ctx context.Context, path string) (Info, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "quiet", "-print_format", "json",
		"-show_entries", "format=duration:format_tags:stream=codec_type", path).Output()
	if err != nil {
		return Info{}, err
	}

	var raw struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			Duration string            `json:"duration"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return Info{}, err
	}

	in := Info{Tags: map[string]string{}}
	in.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	for k, v := range raw.Format.Tags {
		k = strings.ToLower(k)
		in.Tags[k] = v
		if (k == "lyrics" || strings.HasPrefix(k, "lyrics-") || k == "unsyncedlyrics") && strings.TrimSpace(v) != "" {
			in.HasLyrics = true
		}
	}
	for _, s := range raw.Streams {
		if s.CodecType == "subtitle" {
			in.HasSubtitles = true
		}
	}
	return in, nil
}

// Track builds a Track from the file's tags, falling back to its file name
// ("Artist - Title.mp3") for whatever the tags lack.
func (in Info) Track(path string) songname.Track {
	t := songname.Track{
		Artist:   songname.FirstNonEmpty(in.Tags["artist"], in.Tags["album_artist"]),
		Title:    in.Tags["title"],
		Album:    in.Tags["album"],
		Duration: in.Duration,
	}
	if t.Artist == "" || t.Title == "" {
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		name = songname.StripNoise(songname.Unsanitize(name))
		if artist, title, ok := songname.SplitArtistTitle(name); ok {
			t.Artist = songname.FirstNonEmpty(t.Artist, artist)
			t.Title = songname.FirstNonEmpty(t.Title, title)
		}
		t.Title = songname.FirstNonEmpty(t.Title, name)
	}
	return t
}

// WriteTags sets the given metadata tags on path, keeping every stream (cover
// art included) and without re-encoding. The file is replaced only when
// ffmpeg succeeded, so a failure never damages it.
func WriteTags(ctx context.Context, path string, tags map[string]string) error {
	if len(tags) == 0 {
		return nil
	}
	dir, ext := filepath.Dir(path), filepath.Ext(path)
	tmp, err := os.CreateTemp(dir, ".tag-*"+ext)
	if err != nil {
		return err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	args := []string{"-y", "-v", "error", "-i", path, "-map", "0", "-map_metadata", "0", "-c", "copy"}
	for k, v := range tags {
		args = append(args, "-metadata", k+"="+v)
	}
	switch strings.ToLower(ext) {
	case ".mp3":
		args = append(args, "-id3v2_version", "3", "-write_id3v1", "1")
	case ".opus", ".ogg":
		// nothing special
	}
	args = append(args, tmp.Name())

	if out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if st, err := os.Stat(tmp.Name()); err != nil || st.Size() == 0 {
		return fmt.Errorf("ffmpeg wrote nothing")
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

var audioExts = map[string]bool{".mp3": true, ".flac": true, ".m4a": true, ".opus": true, ".ogg": true}

// AudioFiles lists the audio files under root, skipping the folder skip (if it
// is inside root).
func AudioFiles(root, skip string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip != "" && p != root && filepath.Clean(p) == filepath.Clean(skip) {
				return filepath.SkipDir
			}
			return nil
		}
		if audioExts[strings.ToLower(filepath.Ext(p))] {
			files = append(files, p)
		}
		return nil
	})
	return files, err
}
