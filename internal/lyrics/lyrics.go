// Package lyrics finds synced lyrics (LRCLIB) for downloaded songs and saves
// them next to your library as .lrc files, or as .srt subtitles for videos.
package lyrics

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// Kind is the type of media a file is.
type Kind int

const (
	Audio Kind = iota
	Video
)

// Outcome says what Ensure did for one file.
type Outcome int

const (
	Saved       Outcome = iota // lyrics were found and written
	Exists                     // a lyrics/subtitle file is already there
	Excluded                   // the file is under an excluded path
	HasEmbedded                // the file already carries lyrics (audio) or subtitles (video)
	Disabled                   // this kind of media isn't enabled (videos, by default)
	NotFound                   // nobody has synced lyrics for it
	Failed                     // the lookup itself failed (offline, service down, can't write)
)

func (o Outcome) String() string {
	return [...]string{"saved", "exists", "excluded", "embedded", "disabled", "not found", "failed"}[o]
}

// Result is the outcome for one file.
type Result struct {
	Outcome Outcome
	Path    string // the file written (or already present)
	Err     error  // set for Failed
}

// Ensure makes sure media has lyrics: it does nothing if they already exist,
// otherwise looks them up and writes them.
//
// hint is what the caller knows about the song (e.g. from YouTube metadata);
// gaps are filled from the file's own tags, then its name.
func Ensure(ctx context.Context, o Options, kind Kind, media string, hint Track) Result {
	var target string
	switch kind {
	case Video:
		if !o.VideoSRT {
			return Result{Outcome: Disabled}
		}
		target = srtPath(media)
	default:
		if o.excluded(media) {
			return Result{Outcome: Excluded}
		}
		target = o.lrcPath(media)
	}
	if exists(target) {
		return Result{Outcome: Exists, Path: target}
	}

	// ffprobe failing (not installed, unreadable file) only costs us the
	// "already embedded" check and the tags; carry on with what we have.
	p, err := probeFile(ctx, media)
	if err == nil && ((kind == Audio && p.hasLyrics) || (kind == Video && p.hasSubtitles)) {
		return Result{Outcome: HasEmbedded}
	}
	track := merge(hint, p.track(media))

	lrc, err := lookup(ctx, o, track)
	if errors.Is(err, ErrNotFound) {
		return Result{Outcome: NotFound}
	}
	if err != nil {
		return Result{Outcome: Failed, Err: err}
	}

	var data string
	if kind == Video {
		data = LRCToSRT(lrc)
	} else {
		data = CleanLRC(lrc, firstNonEmpty(track.Artist, "Unknown"), firstNonEmpty(track.Title, "Unknown"))
	}
	if data == "" {
		return Result{Outcome: NotFound}
	}
	if err := writeFile(target, data); err != nil {
		return Result{Outcome: Failed, Err: err}
	}
	return Result{Outcome: Saved, Path: target}
}

// lookup asks LRCLIB, then the optional fallback command.
func lookup(ctx context.Context, o Options, t Track) (string, error) {
	m, err := Find(ctx, t)
	if err == nil {
		return m.LRC, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	if strings.TrimSpace(o.Fallback) != "" {
		if lrc, ferr := runFallback(ctx, o.Fallback, t); ferr == nil {
			return lrc, nil
		}
	}
	return "", ErrNotFound
}

// runFallback runs a user-configured command that writes an .lrc file, for
// example:
//
//	syncedlyrics {query} -o {output} --synced-only -p musixmatch
//
// {query} and {output} are replaced as whole arguments (no shell is involved).
// The command is only run after LRCLIB found nothing.
func runFallback(ctx context.Context, cmdline string, t Track) (string, error) {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return "", ErrNotFound
	}
	tmp, err := os.CreateTemp("", "yt-media-lyrics-*.lrc")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	query := strings.TrimSpace(cleanArtist(t.Artist) + " " + stripFeat(stripNoise(firstNonEmpty(t.Title, t.AltTitle))))
	args := make([]string, 0, len(fields)-1)
	for _, f := range fields[1:] {
		switch f {
		case "{query}":
			f = query
		case "{output}":
			f = tmp.Name()
		}
		args = append(args, f)
	}
	if err := exec.CommandContext(ctx, fields[0], args...).Run(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(tmp.Name())
	if err != nil || CleanLRC(string(data), "", "") == "" {
		return "", ErrNotFound
	}
	return string(data), nil
}
