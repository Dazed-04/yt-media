package album

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"yt-downloader/internal/media"
	"yt-downloader/internal/netutil"
	"yt-downloader/internal/songname"
)

// Event reports one file finished during a Scan.
type Event struct {
	Path        string
	Result      Result
	Done, Total int
}

// Summary counts what a Scan did.
type Summary struct {
	Total, Tagged, HasAlbum, NotFound, Failed, Excluded int
}

// isExcluded reports whether rel begins with or matches any excluded path prefix.
func isExcluded(rel string, excludes []string) bool {
	rel = filepath.Clean(rel)
	for _, ex := range excludes {
		ex = filepath.Clean(ex)
		if rel == ex || strings.HasPrefix(rel, ex+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Excluded reports whether file (under root) is in one of the excluded paths,
// which are relative to root.
func Excluded(root, file string, excludes []string) bool {
	rel, err := filepath.Rel(root, file)
	return err == nil && isExcluded(rel, excludes)
}

// giveUpAfter is how many files in a row may fail because MusicBrainz keeps
// refusing requests before the scan stops instead of hammering it.
const giveUpAfter = 3

// Scan fills in the album of every audio file under root that has none.
// Excluded paths are bypassed and counted in Summary.Excluded.
func Scan(ctx context.Context, root string, excludes []string, on func(Event)) (Summary, error) {
	files, err := media.AudioFiles(root, "")
	if err != nil {
		return Summary{}, err
	}

	var candidates []string
	var sum Summary

	for _, p := range files {
		rel, err := filepath.Rel(root, p)
		if err == nil && isExcluded(rel, excludes) {
			sum.Excluded++
			continue
		}
		candidates = append(candidates, p)
	}

	sum.Total = len(candidates)
	refused := 0
	for i, p := range candidates {
		if ctx.Err() != nil {
			break
		}
		res := Ensure(ctx, p, songname.Track{})
		switch res.Outcome {
		case Tagged:
			sum.Tagged++
		case HasAlbum:
			sum.HasAlbum++
		case NotFound:
			sum.NotFound++
		case Failed:
			sum.Failed++
		}
		if on != nil {
			on(Event{Path: p, Result: res, Done: i + 1, Total: sum.Total})
		}
		if res.Outcome == Failed && errors.Is(res.Err, netutil.ErrRateLimited) {
			if refused++; refused >= giveUpAfter {
				return sum, fmt.Errorf("MusicBrainz keeps refusing requests, stopping (run it again later; finished songs are skipped): %w", res.Err)
			}
		} else {
			refused = 0
		}
	}
	return sum, ctx.Err()
}
