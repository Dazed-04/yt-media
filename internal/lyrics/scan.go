package lyrics

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"yt-downloader/internal/media"
	"yt-downloader/internal/netutil"
)

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

// giveUpAfter is how many files in a row may fail because LRCLIB keeps
// refusing requests before the scan stops instead of hammering it.
const giveUpAfter = 4

// Scan fetches lyrics for every audio file in the library that doesn't have
// them yet, with a few lookups running at once. on (optional) is called once
// per file, never concurrently.
func Scan(ctx context.Context, o Options, workers int, on func(Event)) (Summary, error) {
	files, err := media.AudioFiles(o.MusicDir, o.LyricsDir) // the lyrics folder isn't scanned
	if err != nil {
		return Summary{}, err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stopErr error
	refused := 0

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
				if stopErr != nil { // another worker gave up; this result is just the cancellation
					mu.Unlock()
					continue
				}
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
				if res.Outcome == Failed && errors.Is(res.Err, netutil.ErrRateLimited) {
					if refused++; refused >= giveUpAfter {
						stopErr = fmt.Errorf("LRCLIB keeps refusing requests, stopping (run it again later; finished songs are skipped): %w", res.Err)
						cancel()
					}
				} else {
					refused = 0
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
	if stopErr != nil {
		return sum, stopErr
	}
	return sum, ctx.Err()
}
