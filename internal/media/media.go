// Package media reads and writes tags of audio/video files with ffprobe and
// ffmpeg (both already required by yt-dlp for audio extraction).
package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
		"-show_entries", "format=duration:format_tags:stream=codec_type:stream_tags", path).Output()
	if err != nil {
		return Info{}, err
	}

	var raw struct {
		Streams []struct {
			CodecType string            `json:"codec_type"`
			Tags      map[string]string `json:"tags"`
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
		// Ogg (opus) keeps its comments on the audio stream, not the file.
		// File-level tags win where both exist.
		if s.CodecType == "audio" {
			for k, v := range s.Tags {
				k = strings.ToLower(k)
				if _, ok := in.Tags[k]; !ok {
					in.Tags[k] = v
				}
				if (k == "lyrics" || strings.HasPrefix(k, "lyrics-") || k == "unsyncedlyrics") && strings.TrimSpace(v) != "" {
					in.HasLyrics = true
				}
			}
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

// WriteTags sets the given metadata tags on path without re-encoding. The
// file is replaced only when ffmpeg succeeded, so a failure never damages it.
// Cover art is kept.
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

	switch strings.ToLower(ext) {
	case ".opus", ".ogg":
		err = writeOggTags(ctx, path, tmp.Name(), tags)
	default:
		err = writeTagsCopy(ctx, path, tmp.Name(), tags)
	}
	if err != nil {
		return err
	}
	if st, err := os.Stat(tmp.Name()); err != nil || st.Size() == 0 {
		return fmt.Errorf("ffmpeg wrote nothing")
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// writeTagsCopy rewrites every stream unchanged with the tags added (mp3, flac, m4a).
func writeTagsCopy(ctx context.Context, in, out string, tags map[string]string) error {
	args := []string{"-y", "-v", "error", "-i", in, "-map", "0", "-map_metadata", "0", "-c", "copy"}
	for k, v := range tags {
		args = append(args, "-metadata", k+"="+v)
	}
	if strings.EqualFold(filepath.Ext(out), ".mp3") {
		args = append(args, "-id3v2_version", "3", "-write_id3v1", "1")
	}
	args = append(args, out)
	return runFFmpeg(ctx, args)
}

func runFFmpeg(ctx context.Context, args []string) error {
	if out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// writeOggTags handles Ogg Opus. ffmpeg shows the embedded cover (a
// METADATA_BLOCK_PICTURE comment) as a video stream but cannot write one back
// into an Ogg file, so the cover is taken out, the audio and tags are
// rewritten, and the cover goes back in as a comment.
func writeOggTags(ctx context.Context, in, out string, tags map[string]string) error {
	info, err := Probe(ctx, in)
	if err != nil {
		return err
	}
	meta := map[string]string{}
	for k, v := range info.Tags {
		if k != "encoder" && k != "metadata_block_picture" {
			meta[k] = v
		}
	}
	for k, v := range tags {
		meta[strings.ToLower(k)] = v
	}
	if pic := extractCover(ctx, in); pic != "" {
		meta["metadata_block_picture"] = pic
	}

	f, err := os.CreateTemp(filepath.Dir(out), ".meta-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(ffmetadata(meta)); err != nil {
		f.Close()
		return err
	}
	f.Close()

	return runFFmpeg(ctx, []string{"-y", "-v", "error", "-i", in, "-f", "ffmetadata", "-i", f.Name(),
		"-map", "0:a", "-map_metadata", "1", "-c", "copy", out})
}

// ffmetadata renders tags as an ffmpeg metadata file. Such a file has no
// length limit, unlike command-line arguments, and covers are large.
func ffmetadata(tags map[string]string) string {
	esc := strings.NewReplacer("\\", "\\\\", "=", "\\=", ";", "\\;", "#", "\\#", "\n", "\\\n")
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(";FFMETADATA1\n")
	for _, k := range keys {
		b.WriteString(esc.Replace(k) + "=" + esc.Replace(tags[k]) + "\n")
	}
	return b.String()
}

// extractCover returns the file's cover as a base64 FLAC picture block (the
// form Ogg comments use), or "" when there is none.
func extractCover(ctx context.Context, path string) string {
	data, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", path,
		"-map", "0:v:0", "-c", "copy", "-frames:v", "1", "-f", "rawvideo", "-").Output()
	if err != nil || len(data) < 16 {
		return ""
	}
	return pictureBlock(data)
}

// pictureBlock builds a FLAC METADATA_BLOCK_PICTURE (front cover) for image
// bytes, base64 encoded.
func pictureBlock(img []byte) string {
	var mime string
	switch {
	case bytes.HasPrefix(img, []byte{0xFF, 0xD8}):
		mime = "image/jpeg"
	case bytes.HasPrefix(img, []byte("\x89PNG")):
		mime = "image/png"
	case len(img) > 12 && string(img[8:12]) == "WEBP":
		mime = "image/webp"
	default:
		return ""
	}
	var w, h uint32
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(img)); err == nil {
		w, h = uint32(cfg.Width), uint32(cfg.Height)
	}
	var b bytes.Buffer
	put := func(v uint32) { binary.Write(&b, binary.BigEndian, v) }
	put(3) // front cover
	put(uint32(len(mime)))
	b.WriteString(mime)
	put(0) // no description
	put(w)
	put(h)
	put(24) // colour depth
	put(0)  // not indexed
	put(uint32(len(img)))
	b.Write(img)
	return base64.StdEncoding.EncodeToString(b.Bytes())
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
