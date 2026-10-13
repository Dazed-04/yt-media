package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// handleKey sends a key press to whichever layer owns the keyboard right now:
// a popup window, the search box, or the result list.
func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m.quit()
	}
	switch {
	case m.ErrorsOpen:
		return m.handleErrorsKey(msg)
	case m.QueueOpen:
		return m.handleQueueKey(msg)
	case m.Searching:
		return m.handleSearchKey(msg)
	}
	return m.handleBrowseKey(msg)
}

// ---------- typing a search query ----------

func (m *Model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if strings.TrimSpace(m.SearchQuery) == "" {
			return m, nil
		}
		m.Searching = false
		m.IsFetching = true
		m.SpinnerIdx = 0
		if m.DownloadState == StatusDone {
			m.DownloadState = StatusIdle
			m.purgePreviewCache()
		}
		return m, tea.Batch(m.doSearch(), tickSpinner(), m.showCachedPreview())
	case "esc":
		m.Searching = false
		return m, nil
	case "backspace":
		if r := []rune(m.SearchQuery); len(r) > 0 {
			m.SearchQuery = string(r[:len(r)-1])
		}
	case "space":
		m.SearchQuery += " "
	default:
		if len(msg.String()) == 1 {
			m.SearchQuery += msg.String()
		}
	}
	m.resetFilter()
	return m, nil
}

// handlePaste appends pasted text (e.g. a YouTube link) to the search box.
func (m *Model) handlePaste(text string) tea.Cmd {
	if !m.Searching {
		return nil
	}
	m.SearchQuery += text
	m.resetFilter()
	return nil
}

// resetFilter re-filters the list for the current query and returns to the top.
func (m *Model) resetFilter() {
	m.FilterList() // components.go
	m.Cursor = 0
	m.ListOffset = 0
}

// ---------- browsing results ----------

func (m *Model) handleBrowseKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m.quit()

	case "/":
		m.Searching = true
		m.Blink = true
		return m, tickBlink()

	case "w":
		m.showQueue()

	case "e":
		m.showErrors()

	case "r":
		return m, m.retryFailed()

	case "tab":
		if m.ChoiceType == "Audio" {
			m.ChoiceType = "Video"
		} else {
			m.ChoiceType = "Audio"
		}
		if v, ok := m.current(); ok {
			return m, m.fetchSizeCmd(v, m.ChoiceType)
		}

	case "esc":
		if m.DownloadState == StatusDone {
			m.DownloadState = StatusIdle
			m.resetBatch()
		}
		m.resetInFlight()
		m.SearchQuery = ""
		m.FilteredList = m.MusicList
		m.Flash = ""
		m.Cursor = 0
		m.ListOffset = 0
		m.purgePreviewCache()
		return m, m.showCachedPreview()

	case "up", "k":
		return m, m.step(-1)

	case "down", "j":
		return m, m.step(1)

	case "space":
		v, ok := m.current()
		if !ok {
			return m, nil
		}
		text := "Removed from queue (%d)"
		if m.toggleQueue(v) {
			text = "Added to queue (%d)"
		}
		return m, tea.Batch(m.flash(fmt.Sprintf(text, len(m.Queue)), false), m.step(1))

	case "enter":
		v, ok := m.current()
		if !ok {
			return m, nil
		}
		typ := m.ChoiceType
		if i := m.queueIndex(v.ID); i >= 0 { // downloading directly, so take it out of the queue
			typ = m.Queue[i].Type
			m.removeFromQueue(i)
		}
		m.SkippedItems = nil
		return m, m.checkLibraryManual([]DownloadReq{{Video: v, Type: typ}})
	}
	return m, nil
}

// step moves the highlight and starts loading whatever the new item needs
// (preview image and size).
func (m *Model) step(delta int) tea.Cmd {
	if len(m.FilteredList) == 0 {
		return nil
	}
	m.moveCursor(delta)
	v, ok := m.current()
	if !ok {
		return nil
	}
	return tea.Batch(m.showCachedPreview(), m.priorityFetchCmd(v), m.fetchSizeCmd(v, m.ChoiceType))
}
