package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"yt-downloader/internal/downloader"
	"yt-downloader/internal/lyrics"
)

// Where a search goes. Pasted links work for any site whatever is chosen
// here; this is only for typed searches. Only sites yt-dlp can search appear.
type searchSource struct {
	key  string // as in config: search_source
	name string
}

var searchSources = []searchSource{
	{"youtube", "YouTube"},
	{"soundcloud", "SoundCloud"},
}

func sourceName(key string) string {
	for _, s := range searchSources {
		if s.key == key {
			return s.name
		}
	}
	return "YouTube"
}

// sourceName is the display name of the source searches go to now.
func (m *Model) sourceName() string { return sourceName(m.SearchSource) }

// sourceBadge adds the source to the search bar's label when it isn't the
// usual one, so it is always clear where a search will go.
func (m *Model) sourceBadge(label string) string {
	if m.SearchSource == "" || m.SearchSource == "youtube" {
		return label
	}
	return strings.TrimRight(label, " ") + " · " + m.sourceName() + " "
}

// fetchText is the placeholder shown while a search runs: it names where the
// search is going, or says "link" for a pasted link (which can be any site).
func (m *Model) fetchText() string {
	if isWebLink(m.SearchQuery) {
		return "Fetching link data..."
	}
	return "Fetching " + m.sourceName() + " data..."
}

// filterResults narrows search results for the query. YouTube searches return
// plenty of loosely related videos, so they are kept only when a title or
// artist shares a word with the query; SoundCloud is handled separately.
func (m *Model) filterResults(videos []downloader.Video, query string) []downloader.Video {
	if m.SearchSource == "soundcloud" {
		return filterSoundcloud(videos)
	}
	return filterRelevant(videos, query)
}

// minSoundcloudSeconds is the shortest track kept: shorter ones are previews
// and snippets.
const minSoundcloudSeconds = 45

// filterSoundcloud cleans a SoundCloud result list. SoundCloud already ranks
// by its own relevance (tags and descriptions count, and titles often leave
// out the artist), so results are not matched against the query's words:
// repeats and under-a-minute snippets are dropped and the order is kept.
func filterSoundcloud(videos []downloader.Video) []downloader.Video {
	seen := make(map[string]bool, len(videos))
	var out []downloader.Video
	for _, v := range videos {
		if seen[v.ID] {
			continue
		}
		seen[v.ID] = true
		if secs := lyrics.ParseClock(v.Duration); secs > 0 && secs < minSoundcloudSeconds {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return dedupe(videos)
	}
	return out
}

// ---------- the picker window ----------

func (m *Model) showSources() {
	m.QueueOpen, m.ErrorsOpen = false, false
	m.SourceOpen = true
	m.SourceCursor = 0
	for i, s := range searchSources {
		if s.key == m.SearchSource {
			m.SourceCursor = i
		}
	}
}

func (m *Model) handleSourceKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "s", "ctrl+s":
		m.SourceOpen = false
	case "up", "k":
		m.SourceCursor = (m.SourceCursor + len(searchSources) - 1) % len(searchSources)
	case "down", "j":
		m.SourceCursor = (m.SourceCursor + 1) % len(searchSources)
	case "enter":
		m.SourceOpen = false
		chosen := searchSources[clampIdx(m.SourceCursor, len(searchSources))]
		if chosen.key != m.SearchSource {
			m.SearchSource = chosen.key
			return m, m.flash("Searching "+chosen.name, false)
		}
	}
	return m, nil
}

func (m *Model) renderSourceOverlay() string {
	s := newWinStyles()
	w := min(m.overlayWidth(), 44)
	inner := w - 4
	header := s.header(inner, "Search on", m.sourceName())

	var rows []string
	for i, src := range searchSources {
		mark := "  "
		style := s.base
		if i == m.SourceCursor {
			mark, style = "▸ ", s.accent
		}
		tick := ""
		if src.key == m.SearchSource {
			tick = " ✓"
		}
		rows = append(rows, s.row(inner, style.Render(safeTruncate(fmt.Sprintf("%s%s%s", mark, src.name, tick), inner))))
	}
	hints := [][]string{{"enter: select", "↑/↓: move", "esc: close"}}
	return windowBox(s, w, header, [][]string{rows}, hints)
}
