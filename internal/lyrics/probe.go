package lyrics

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// probe is what ffprobe reports about a media file.
type probe struct {
	tags         map[string]string // lower-case keys
	duration     float64
	hasLyrics    bool // embedded lyrics tag
	hasSubtitles bool // embedded subtitle stream
}

// probeFile reads tags, length and streams with ffprobe (which ships with
// ffmpeg, already required by yt-dlp for audio extraction).
func probeFile(ctx context.Context, path string) (probe, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "quiet", "-print_format", "json",
		"-show_entries", "format=duration:format_tags:stream=codec_type", path).Output()
	if err != nil {
		return probe{}, err
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
		return probe{}, err
	}

	p := probe{tags: map[string]string{}}
	p.duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	for k, v := range raw.Format.Tags {
		k = strings.ToLower(k)
		p.tags[k] = v
		if (k == "lyrics" || strings.HasPrefix(k, "lyrics-") || k == "unsyncedlyrics") && strings.TrimSpace(v) != "" {
			p.hasLyrics = true
		}
	}
	for _, s := range raw.Streams {
		if s.CodecType == "subtitle" {
			p.hasSubtitles = true
		}
	}
	return p, nil
}

// track builds a Track from the file's tags, falling back to its file name
// ("Artist - Title.mp3") for whatever the tags lack.
func (p probe) track(path string) Track {
	t := Track{
		Artist:   firstNonEmpty(p.tags["artist"], p.tags["album_artist"]),
		Title:    p.tags["title"],
		Album:    p.tags["album"],
		Duration: p.duration,
	}
	if t.Artist == "" || t.Title == "" {
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		name = stripNoise(name)
		if artist, title, ok := splitArtistTitle(name); ok {
			t.Artist = firstNonEmpty(t.Artist, artist)
			t.Title = firstNonEmpty(t.Title, title)
		}
		t.Title = firstNonEmpty(t.Title, name)
	}
	return t
}

// merge fills the gaps of hint (what the caller knows) from the file's own
// tags. The caller's values win.
func merge(hint, fromFile Track) Track {
	return Track{
		Artist:   firstNonEmpty(hint.Artist, fromFile.Artist),
		Title:    firstNonEmpty(hint.Title, fromFile.Title),
		AltTitle: hint.AltTitle,
		Album:    firstNonEmpty(hint.Album, fromFile.Album),
		Duration: func() float64 {
			if fromFile.Duration > 0 { // the file's own length is the most accurate
				return fromFile.Duration
			}
			return hint.Duration
		}(),
	}
}
