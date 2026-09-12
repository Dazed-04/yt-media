package main

import (
	"fmt"
	"os"

	"yt-downloader/internal/config"
	"yt-downloader/internal/tui"

	tea "charm.land/bubbletea/v2"
)

func main() {
	f, err := tea.LogToFile("debug.log", "debug")
	if err != nil {
		fmt.Printf("Fatal: Could not initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	cfg := config.Load()
	p := tea.NewProgram(tui.InitialModel(cfg))

	if _, err := p.Run(); err != nil {
		fmt.Printf("Application error: %v\n", err)
		os.Exit(1)
	}
}
