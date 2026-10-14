package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"yt-downloader/internal/config"
	"yt-downloader/internal/lyrics"
)

// runLyrics implements "yt-media lyrics": fetch lyrics for every song in the
// library that doesn't have them yet. It returns the process exit code.
func runLyrics(args []string) int {
	fs := flag.NewFlagSet("lyrics", flag.ContinueOnError)
	dir := fs.String("dir", "", "music folder to scan (default: music_dir from the config, or ~/Music)")
	workers := fs.Int("workers", 4, "number of lookups to run at once")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg := config.Load()
	home, _ := os.UserHomeDir()
	opts := lyrics.FromConfig(cfg, home)
	if *dir != "" {
		opts.MusicDir = *dir
		if cfg.LyricsDir == "" {
			opts.LyricsDir = filepath.Join(*dir, "lyrics")
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Printf("Scanning %s\nLyrics go to %s\n\n", opts.MusicDir, opts.LyricsDir)

	sum, err := lyrics.Scan(ctx, opts, *workers, func(ev lyrics.Event) {
		name, _ := filepath.Rel(opts.MusicDir, ev.Path)
		switch ev.Result.Outcome {
		case lyrics.Saved:
			fmt.Printf("✅ %s\n", name)
		case lyrics.NotFound:
			fmt.Printf("❌ no lyrics found: %s\n", name)
		case lyrics.Failed:
			fmt.Printf("⚠  %s: %v\n", name, ev.Result.Err)
		}
	})

	fmt.Printf("\n%d files: %d saved, %d not found, %d failed, %d already had lyrics, %d skipped\n",
		sum.Total, sum.Saved, sum.NotFound, sum.Failed, sum.Exists+sum.Embedded, sum.Excluded)

	switch {
	case ctx.Err() != nil:
		fmt.Println("Interrupted.")
		return 130
	case err != nil:
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
