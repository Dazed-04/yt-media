package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Chapter is one section of a long upload, as yt-dlp reports it.
type Chapter struct {
	Start float64 `json:"start_time"`
	End   float64 `json:"end_time"`
	Title string  `json:"title"`
}

// ParseChapters reads yt-dlp's "%(chapters)j" output. "NA", empty or
// unreadable input means there are no chapters. Sections shorter than a
// second are dropped.
func ParseChapters(s string) []Chapter {
	var all []Chapter
	if json.Unmarshal([]byte(strings.TrimSpace(s)), &all) != nil {
		return nil
	}
	var out []Chapter
	for _, c := range all {
		if c.End-c.Start >= 1 {
			out = append(out, c)
		}
	}
	return out
}

var (
	trackPrefixRe = regexp.MustCompile(`^\s*[\[(]?\d{1,3}\s*[.\-):\]]+\s*`)
	timePrefixRe  = regexp.MustCompile(`^\s*\d{1,2}:\d{2}(?::\d{2})?\s*[-–—.:]?\s*`)
	badFileRe     = regexp.MustCompile(`[<>:"|?*\x00-\x1f]`)
)

// ChapterTitle cleans a chapter title for use as a song title: a leading
// track number ("01. ", "3 - ") or time stamp ("12:30 ") is removed.
func ChapterTitle(s string) string {
	t := strings.TrimSpace(s)
	if c := timePrefixRe.ReplaceAllString(t, ""); c != "" {
		t = c
	}
	if c := trackPrefixRe.ReplaceAllString(t, ""); c != "" {
		t = c
	}
	return strings.TrimSpace(t)
}

// SplitMeta is what every split file gets besides its own title and number.
type SplitMeta struct {
	Artist string
	Album  string
}

// SplitChapters cuts src into one file per chapter in outDir without
// re-encoding, named "NN - Title.ext" and tagged with the chapter's title,
// its number, and meta; the source's cover art is kept. Nothing is written
// unless every chapter succeeds, and src is left untouched: the caller
// removes it. It returns the new files in order.
func SplitChapters(ctx context.Context, src, outDir string, chapters []Chapter, meta SplitMeta) ([]string, error) {
	if len(chapters) < 2 {
		return nil, fmt.Errorf("fewer than two chapters")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	ext := filepath.Ext(src)
	ogg := strings.EqualFold(ext, ".opus") || strings.EqualFold(ext, ".ogg")

	cover := ""
	if ogg {
		cover = extractCover(ctx, src)
	}
	src0, _ := Probe(ctx, src)
	var made []string
	fail := func(err error) ([]string, error) {
		for _, f := range made {
			os.Remove(f)
		}
		return nil, err
	}

	for i, c := range chapters {
		title := ChapterTitle(c.Title)
		if title == "" {
			title = fmt.Sprintf("Track %d", i+1)
		}
		name := fmt.Sprintf("%02d - %s%s", i+1, safeFileName(title), ext)
		dst := filepath.Join(outDir, name)

		tags := map[string]string{
			"title": title,
			"track": fmt.Sprintf("%d/%d", i+1, len(chapters)),
		}
		if meta.Artist != "" {
			tags["artist"] = meta.Artist
		}
		if meta.Album != "" {
			tags["album"] = meta.Album
		}
		for _, k := range []string{"date", "genre"} {
			if v := src0.Tags[k]; v != "" {
				tags[k] = v
			}
		}

		args := []string{"-y", "-v", "error", "-ss", fmt.Sprint(c.Start), "-i", src, "-t", fmt.Sprint(c.End - c.Start)}
		if ogg {
			args = append(args, "-map", "0:a")
		} else {
			args = append(args, "-map", "0")
		}
		args = append(args, "-map_metadata", "-1", "-c", "copy")
		if strings.EqualFold(ext, ".flac") {
			// a copied FLAC keeps the whole file's length in its header;
			// re-encoding (lossless) writes the piece's own
			args = append(args, "-c:a", "flac")
		}
		if !ogg {
			for k, v := range tags {
				args = append(args, "-metadata", k+"="+v)
			}
			if strings.EqualFold(ext, ".mp3") {
				args = append(args, "-id3v2_version", "3", "-write_id3v1", "1")
			}
		}
		args = append(args, dst)
		made = append(made, dst)
		if err := runFFmpeg(ctx, args); err != nil {
			return fail(err)
		}
		if ogg { // tags and cover go in through the metadata-file route
			if err := retagOgg(ctx, dst, tags, cover); err != nil {
				return fail(err)
			}
		}
	}
	return made, nil
}

// retagOgg replaces an Ogg file's tags with exactly tags (plus cover).
func retagOgg(ctx context.Context, path string, tags map[string]string, cover string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tag-*"+filepath.Ext(path))
	if err != nil {
		return err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if err := writeOggTags(ctx, path, tmp.Name(), tags, cover); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func safeFileName(s string) string {
	s = strings.NewReplacer("/", "_", "\\", "_").Replace(s)
	s = badFileRe.ReplaceAllString(s, "")
	s = strings.Trim(strings.Join(strings.Fields(s), " "), " .")
	if r := []rune(s); len(r) > 100 {
		s = strings.TrimRight(string(r[:100]), " .")
	}
	if s == "" {
		return "track"
	}
	return s
}
