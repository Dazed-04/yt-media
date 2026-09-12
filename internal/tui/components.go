package tui

import (
	"strings"

	"yt-downloader/internal/downloader"
)

func (m *Model) FilterList() {
	if m.SearchQuery == "" {
		m.FilteredList = m.MusicList
		return
	}

	var filtered []downloader.Video
	query := strings.ToLower(m.SearchQuery)

	// Filter based on query
	for _, item := range m.MusicList {
		titleMatch := strings.Contains(strings.ToLower(item.Title), query)
		artistMatch := strings.Contains(strings.ToLower(item.Artist), query)

		if titleMatch || artistMatch {
			filtered = append(filtered, item)
		}
	}
	m.FilteredList = filtered

	// Reset cursor to prevent out of bounds error
	if len(m.FilteredList) == 0 {
		m.Cursor = 0
	} else if m.Cursor >= len(m.FilteredList) {
		m.Cursor = len(m.FilteredList) - 1
	}
}
