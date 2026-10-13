package tui

import (
	"log"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

func tickSpinner() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

func tickBlink() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return blinkMsg{} })
}

// Update runs the handler for msg, then sends any terminal escape sequences it
// queued (e.g. image deletions) through Bubble Tea's output buffer, so they
// can't land in the middle of a frame.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	if len(m.rawOut) > 0 {
		cmd = tea.Batch(tea.Raw(strings.Join(m.rawOut, "")), cmd)
		m.rawOut = nil
	}
	return model, cmd
}

// update routes each message to its handler. The handlers live next to the
// feature they belong to: keys.go, search.go, preview.go, downloads.go.
func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	// input
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		return m, m.handlePaste(msg.Content)

	// terminal
	case tea.WindowSizeMsg:
		m.Width, m.Height = msg.Width, msg.Height
		m.ready = true
		m.purgePreviewCache()
		return m, m.showCachedPreview()
	case tea.FocusMsg:
		m.Focused = true
	case tea.BlurMsg:
		m.Focused = false

	// ui ticks
	case blinkMsg:
		if m.Searching {
			m.Blink = !m.Blink
			return m, tickBlink()
		}
	case spinnerTickMsg:
		if m.IsFetching {
			m.SpinnerIdx++
			return m, tickSpinner()
		}
	case flashClearMsg:
		if msg.seq == m.flashSeq {
			m.Flash = ""
		}

	// search + previews
	case searchResultsMsg:
		return m, m.onSearchResults(msg)
	case errorMsg:
		log.Printf("Caught error in TUI: %v", msg)
		m.IsFetching = false
		m.resetInFlight()
		return m, m.flash(friendlyError(msg.Error()), true)
	case sizeFetchedMsg:
		key := msg.videoID + "-" + msg.format
		m.SizeCache[key] = msg.sizeStr
		delete(m.SizeInFlight, key)
	case thumbnailReadyMsg:
		return m, m.onThumbnailReady(msg)
	case previewRenderedMsg:
		return m, m.onPreviewRendered(msg)

	// downloads
	case checkLibraryMsg:
		return m, m.onCheckLibrary(msg)
	case JobProgressMsg:
		return m, m.onJobProgress(msg)
	case downloadFinishedMsg:
		return m, m.onDownloadFinished(msg)
	}

	return m, nil
}
