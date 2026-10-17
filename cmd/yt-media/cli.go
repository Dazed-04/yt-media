package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"yt-downloader/internal/album"
	"yt-downloader/internal/config"
	"yt-downloader/internal/lyrics"
	"yt-downloader/internal/songname"
)

const usage = `yt-media - search and download from YouTube

Usage:
  yt-media                 open the interface
  yt-media lyrics [flags]  fetch lyrics for songs in your library
  yt-media album  [flags]  fill in missing album tags for songs in your library
  yt-media paths           show where the config, queue and log files live
  yt-media help            show this help

Flags (lyrics and album):
  --dir DIR    music folder to scan (default: music_dir from the config, or ~/Music)
  --file FILE  handle just this one file (handy for trying things out)

album only:
  -v, --verbose  show every search and why results were rejected

lyrics only:
  --workers N  lookups to run at once (default 4)

`

// run executes a command and returns the process exit code. It prints to out
// and errw instead of the process streams so it can be tested.
func run(args []string, out, errw io.Writer) int {
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return 0
	case "paths":
		fmt.Fprintf(out, "config: %s\nqueue:  %s\nlog:    %s\n",
			config.Path(), filepath.Join(config.CacheDir(), "queue.json"), config.LogPath())
		return 0
	case "lyrics", "album":
	default:
		fmt.Fprintf(errw, "yt-media: unknown command %q\n\n%s", args[0], usage)
		return 2
	}

	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(errw)
	dir := fs.String("dir", "", "")
	file := fs.String("file", "", "")
	workers := fs.Int("workers", 4, "")
	verbose := fs.Bool("verbose", false, "")
	fs.BoolVar(verbose, "v", false, "")
	fs.Usage = func() { fmt.Fprint(errw, usage) }
	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(errw, "yt-media %s: unexpected argument %q\n", args[0], fs.Arg(0))
		return 2
	}

	cfg := config.Load()
	home, _ := os.UserHomeDir()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if args[0] == "album" {
		if *verbose {
			ctx = album.WithTrace(ctx, func(line string) { fmt.Fprintln(out, "    "+line) })
		}
		return runAlbum(ctx, cfg, home, *dir, *file, out, errw)
	}
	return runLyrics(ctx, cfg, home, *dir, *file, *workers, out, errw)
}

// finish prints the closing line and picks the exit code.
func finish(ctx context.Context, err error, summary string, out, errw io.Writer) int {
	fmt.Fprintln(out, "\n"+summary)
	switch {
	case ctx.Err() != nil:
		fmt.Fprintln(out, "Interrupted.")
		return 130
	case err != nil:
		fmt.Fprintf(errw, "error: %v\n", err)
		return 1
	}
	return 0
}

func runLyrics(ctx context.Context, cfg config.Config, home, dir, file string, workers int, out, errw io.Writer) int {
	opts := lyrics.FromConfig(cfg, home)
	opts.Exclude = cfg.LyricsExclusions()
	if dir != "" {
		opts.MusicDir = dir
		if cfg.LyricsDir == "" {
			opts.LyricsDir = filepath.Join(dir, "lyrics")
		}
	}

	if file != "" {
		res := lyrics.Ensure(ctx, opts, lyrics.Audio, file, lyrics.Track{})
		switch res.Outcome {
		case lyrics.Saved:
			fmt.Fprintf(out, "saved lyrics to %s\n", res.Path)
		case lyrics.Exists:
			fmt.Fprintf(out, "already have lyrics: %s\n", res.Path)
		case lyrics.Failed:
			fmt.Fprintf(errw, "failed: %v\n", res.Err)
			return 1
		default:
			fmt.Fprintf(out, "%s\n", res.Outcome)
		}
		return 0
	}

	fmt.Fprintf(out, "\nScanning %s\nLyrics go to %s: \n\n", opts.MusicDir, opts.LyricsDir)
	sum, err := lyrics.Scan(ctx, opts, workers, func(ev lyrics.Event) {
		name, _ := filepath.Rel(opts.MusicDir, ev.Path)
		prefix := fmt.Sprintf("[%d/%d] ", ev.Done, ev.Total)
		switch ev.Result.Outcome {
		case lyrics.Saved:
			fmt.Fprintf(out, "%ssaved      %s\n", prefix, name)
		case lyrics.NotFound:
			fmt.Fprintf(out, "%snot found  %s\n", prefix, name)
		case lyrics.Failed:
			fmt.Fprintf(out, "%sFAILED     %s: %v\n", prefix, name, ev.Result.Err)
		}
	})
	return finish(ctx, err, fmt.Sprintf("%d files: %d saved, %d not found, %d failed, %d already had lyrics, %d skipped",
		sum.Total, sum.Saved, sum.NotFound, sum.Failed, sum.Exists+sum.Embedded, sum.Excluded), out, errw)
}

func runAlbum(ctx context.Context, cfg config.Config, home, dir, file string, out, errw io.Writer) int {
	if file != "" {
		root := dir
		if root == "" {
			root = cfg.MusicDir
		}
		if root == "" {
			root = filepath.Join(home, "Music")
		}
		if album.Excluded(root, file, cfg.AlbumExclusions()) {
			fmt.Fprintln(out, "skipped: the file is under an excluded path")
			return 0
		}
		res := album.Ensure(ctx, file, songname.Track{})
		switch res.Outcome {
		case album.Tagged:
			fmt.Fprintf(out, "album set to %q\n", res.Album)
		case album.HasAlbum:
			fmt.Fprintf(out, "already has an album: %q\n", res.Album)
		case album.Failed:
			fmt.Fprintf(errw, "failed: %v\n", res.Err)
			return 1
		default:
			fmt.Fprintf(out, "%s\n", res.Outcome)
		}
		return 0
	}

	root := dir
	if root == "" {
		root = cfg.MusicDir
	}
	if root == "" {
		root = filepath.Join(home, "Music")
	}
	fmt.Fprintf(out, "\nScanning %s: \n\n", root)

	excludes := cfg.AlbumExclusions()

	sum, err := album.Scan(ctx, root, excludes, func(ev album.Event) {
		name, _ := filepath.Rel(root, ev.Path)
		prefix := fmt.Sprintf("[%d/%d] ", ev.Done, ev.Total)
		switch ev.Result.Outcome {
		case album.Tagged:
			fmt.Fprintf(out, "%sset to %q  %s\n", prefix, ev.Result.Album, name)
		case album.NotFound:
			fmt.Fprintf(out, "%snot found  %s\n", prefix, name)
		case album.Failed:
			fmt.Fprintf(out, "%sFAILED     %s: %v\n", prefix, name, ev.Result.Err)
		}
	})
	return finish(ctx, err, fmt.Sprintf("%d files: %d tagged, %d not found, %d failed, %d already had an album, %d skipped",
		sum.Total, sum.Tagged, sum.NotFound, sum.Failed, sum.HasAlbum, sum.Excluded), out, errw)
}
