package lyrics

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
)

var audioExts = map[string]bool{".mp3": true, ".flac": true, ".m4a": true, ".opus": true, ".ogg": true}

// Event reports one file finished during a Scan.
type Event struct {
	Path        string
	Result      Result
	Done, Total int
}

// Summary counts what a Scan did.
type Summary struct {
	Total, Saved, Exists, Excluded, Embedded, NotFound, Failed int
}

// Scan fetches lyrics for every audio file in the library that doesn't have
// them yet, with a few lookups running at once. on (optional) is called once
// per file, never concurrently.
func Scan(ctx context.Context, o Options, workers int, on func(Event)) (Summary, error) {
	var files []string
	err := filepath.WalkDir(o.MusicDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != o.MusicDir && filepath.Clean(p) == filepath.Clean(o.LyricsDir) {
				return filepath.SkipDir // don't scan the lyrics folder itself
			}
			return nil
		}
		if audioExts[strings.ToLower(filepath.Ext(p))] {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return Summary{}, err
	}

	sum := Summary{Total: len(files)}
	jobs := make(chan string)
	var mu sync.Mutex
	done := 0

	var wg sync.WaitGroup
	for range max(workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				res := Ensure(ctx, o, Audio, p, Track{})

				mu.Lock()
				done++
				switch res.Outcome {
				case Saved:
					sum.Saved++
				case Exists:
					sum.Exists++
				case Excluded:
					sum.Excluded++
				case HasEmbedded:
					sum.Embedded++
				case NotFound:
					sum.NotFound++
				case Failed:
					sum.Failed++
				}
				if on != nil {
					on(Event{Path: p, Result: res, Done: done, Total: sum.Total})
				}
				mu.Unlock()
			}
		}()
	}

feed:
	for _, p := range files {
		select {
		case jobs <- p:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	return sum, ctx.Err()
}
