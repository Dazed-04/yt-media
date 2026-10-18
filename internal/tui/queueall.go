package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// queueAll (a) stages every result currently listed that isn't staged yet.
func (m *Model) queueAll() tea.Cmd {
	added := 0
	for _, v := range m.FilteredList {
		if m.queueIndex(v.ID) < 0 {
			m.Queue = append(m.Queue, DownloadReq{Video: v, Type: m.ChoiceType})
			added++
		}
	}
	if added == 0 {
		return m.flash("Nothing to add", false)
	}
	m.persistQueue()
	return m.flash(fmt.Sprintf("Added %d to queue (%d)", added, len(m.Queue)), false)
}
