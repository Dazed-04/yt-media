package tui

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"yt-downloader/internal/lyrics"
	"yt-downloader/internal/media"
	"yt-downloader/internal/songname"
)

// wantsSplit reports whether a finished audio download should be cut into
// one file per chapter (split_chapters), when the video has chapters.
func (p downloadParams) wantsSplit() bool {
	return p.cfg.SplitChapters && p.req.Type == "Audio"
}

// splitTrack is one file cut from a long upload and what is known about it.
type splitTrack struct {
	path  string
	track lyrics.Track
}

// splitIntoTracks cuts src, the file yt-dlp wrote, into one file per chapter
// under Artist/Album and removes src. Every piece is its own song: its title
// is the chapter's, its length the chapter's, so lyrics are looked up per
// song. ok is false (and nothing is changed) when the video has fewer than two
// chapters or the cut fails; the download then stays one file.
func (p downloadParams) splitIntoTracks(src, albumName string) (tracks []splitTrack, ok bool) {
	data, err := os.ReadFile(p.chaptersFile)
	if err != nil {
		return nil, false
	}
	chapters := media.ParseChapters(string(data))
	if len(chapters) < 2 {
		log.Printf("[SPLIT] %s: no chapters, kept as one file", filepath.Base(src))
		return nil, false
	}

	v := p.req.Video
	albumTitle := firstNonEmpty(albumName, albumFromTitle(v.Title), v.Title)
	artist := artistFolder(v)
	dir := p.outputDir(albumTitle)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	files, err := media.SplitChapters(ctx, src, dir, chapters, media.SplitMeta{Artist: artist, Album: albumTitle})
	if err != nil {
		log.Printf("[SPLIT] %s: %v (kept as one file)", filepath.Base(src), err)
		return nil, false
	}
	os.Remove(src)
	if filepath.Dir(src) != dir {
		os.Remove(filepath.Dir(src)) // only succeeds when it is empty (e.g. "Unknown Album")
	}
	log.Printf("[SPLIT] %s: %d tracks in %s", filepath.Base(src), len(files), dir)

	for i, f := range files {
		c := chapters[i]
		tracks = append(tracks, splitTrack{path: f, track: lyrics.Track{
			Artist:   artist,
			Title:    media.ChapterTitle(c.Title),
			Album:    albumTitle,
			Duration: c.End - c.Start,
		}})
	}
	return tracks, true
}

var fullAlbumRe = regexp.MustCompile(`(?i)\s*[(\[]\s*(?:full|complete|entire)\s+(?:album|ost|soundtrack)\s*[)\]]|\s*[-–—|:]\s*(?:full|complete|entire)\s+(?:album|ost|soundtrack)\s*$`)

// albumFromTitle makes an album name from an upload's title: the usual
// decoration ("(Official Video)", "(Full Album)") is dropped.
func albumFromTitle(title string) string {
	t := songname.StripNoise(title)
	t = strings.TrimSpace(fullAlbumRe.ReplaceAllString(t, ""))
	return t
}

// ensureLyricsFn looks lyrics up for one finished file. It is a variable so
// tests can see what is asked for without reaching the network.
var ensureLyricsFn = func(opts lyrics.Options, kind lyrics.Kind, file string, t lyrics.Track) lyrics.Outcome {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res := lyrics.Ensure(ctx, opts, kind, file, t)
	if res.Err != nil {
		log.Printf("[LYRICS] %s: %s: %v", filepath.Base(file), res.Outcome, res.Err)
	} else {
		log.Printf("[LYRICS] %s: %s", filepath.Base(file), res.Outcome)
	}
	return res.Outcome
}

// lyricsForSplit looks up lyrics for every piece and reports whether none of
// them had any.
func (p downloadParams) lyricsForSplit(tracks []splitTrack) (allMissing bool) {
	if !p.wantsLyrics() || len(tracks) == 0 {
		return false
	}
	missing := 0
	for _, t := range tracks {
		if ensureLyricsFn(p.lyrics, lyrics.Audio, t.path, t.track) == lyrics.NotFound {
			missing++
		}
	}
	return missing == len(tracks)
}
