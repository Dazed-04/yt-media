package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"yt-downloader/internal/config"
	"yt-downloader/internal/tui"

	tea "charm.land/bubbletea/v2"
)

// version is set at build time: -ldflags "-X main.version=1.2.3" (the release
// build does this). A plain "go build" reports "dev".
var version = "dev"

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("yt-media", version)
		return
	}
	// Any argument means "run a command and print to the terminal"; only a
	// bare "yt-media" opens the UI. Unknown commands are an error rather than
	// silently opening the UI.
	if len(os.Args) > 1 {
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(runTUI())
}

func runTUI() int {
	// The UI owns the terminal, so logs go to a file in the cache folder
	// (~/.cache/yt-media/debug.log) next to the queue and thumbnails.
	closeLog := openLog()
	defer closeLog()

	p := tea.NewProgram(tui.InitialModel(config.Load()))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Application error: %v\n", err)
		return 1
	}
	return 0
}

// openLog sends the standard logger to the debug log. If the file can't be
// opened the app still runs, just without logs.
func openLog() (closeFn func()) {
	if err := os.MkdirAll(config.CacheDir(), 0o755); err == nil {
		if f, err := tea.LogToFile(config.LogPath(), "debug"); err == nil {
			return func() { f.Close() }
		}
	}
	log.SetOutput(io.Discard)
	return func() {}
}
