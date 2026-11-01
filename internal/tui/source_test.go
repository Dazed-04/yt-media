package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"yt-downloader/internal/config"
	"yt-downloader/internal/downloader"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	}
	r := []rune(s)
	return tea.KeyPressMsg{Code: r[0], Text: s}
}

func newSourceModel() *Model {
	m := &Model{Width: 100, Height: 30, SearchSource: "youtube"}
	m.Config.DownloadOptions = config.DefaultDownloadOptions()
	return m
}

func TestSourcePickerSwitchesSearchSource(t *testing.T) {
	m := newSourceModel()
	if m.overlayOpen() {
		t.Fatal("overlay open at start")
	}
	m.handleBrowseKey(key("s"))
	if !m.SourceOpen || !m.overlayOpen() {
		t.Fatal("s did not open the picker")
	}
	m.handleKey(key("down"))
	m.handleKey(key("enter"))
	if m.SourceOpen || m.SearchSource != "soundcloud" {
		t.Errorf("open=%v source=%q", m.SourceOpen, m.SearchSource)
	}
	// reopening starts on the current source; esc changes nothing
	m.handleBrowseKey(key("s"))
	if searchSources[m.SourceCursor].key != "soundcloud" {
		t.Errorf("cursor on %d", m.SourceCursor)
	}
	m.handleKey(key("up"))
	m.handleKey(key("esc"))
	if m.SourceOpen || m.SearchSource != "soundcloud" {
		t.Errorf("esc changed the source: open=%v %q", m.SourceOpen, m.SearchSource)
	}
	// wraps around
	m.showSources()
	m.handleKey(key("down"))
	m.handleKey(key("down"))
	if m.SourceCursor != 1 {
		t.Errorf("cursor = %d after wrapping", m.SourceCursor)
	}
}

func TestSourcePickerFromTheSearchBox(t *testing.T) {
	m := newSourceModel()
	m.Searching = true
	m.handleKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if !m.SourceOpen {
		t.Fatal("ctrl+s did not open the picker while typing")
	}
	m.handleKey(key("esc"))
	if !m.Searching || m.SearchQuery != "" {
		t.Errorf("typing state changed: searching=%v query=%q", m.Searching, m.SearchQuery)
	}
	m.handleKey(key("s")) // a plain s is just a letter while typing
	if m.SourceOpen || m.SearchQuery != "s" {
		t.Errorf("open=%v query=%q", m.SourceOpen, m.SearchQuery)
	}
}

func TestSourceOverlayAndBadge(t *testing.T) {
	m := newSourceModel()
	box := m.renderSourceOverlay()
	for _, w := range []string{"YouTube", "SoundCloud", "Search on"} {
		if !strings.Contains(box, w) {
			t.Errorf("overlay lacks %q", w)
		}
	}
	if lipgloss.Width(box) > m.Width {
		t.Errorf("overlay is %d wide on a %d screen", lipgloss.Width(box), m.Width)
	}
	if m.sourceBadge(" SEARCH ") != " SEARCH " {
		t.Errorf("badge for the default source: %q", m.sourceBadge(" SEARCH "))
	}
	m.SearchSource = "soundcloud"
	if got := m.sourceBadge(" INSERT "); got != " INSERT · SoundCloud " {
		t.Errorf("badge = %q", got)
	}
}

func TestSoundcloudFilterKeepsRankingAndDropsSnippets(t *testing.T) {
	vs := []downloader.Video{
		{ID: "a", Title: "Totally unrelated looking title", Duration: "3:05"},
		{ID: "b", Title: "30s preview", Duration: "0:30"},
		{ID: "a", Title: "dup", Duration: "3:05"},
		{ID: "c", Title: "Live set", Duration: "1:02:03"},
		{ID: "d", Title: "No duration known"},
	}
	got := filterSoundcloud(vs)
	var ids []string
	for _, v := range got {
		ids = append(ids, v.ID)
	}
	if strings.Join(ids, "") != "acd" {
		t.Errorf("ids = %v, want a c d in ranking order, without the snippet and the repeat", ids)
	}
	if got := filterSoundcloud([]downloader.Video{{ID: "x", Duration: "0:10"}}); len(got) != 1 {
		t.Error("when everything is short, keep it rather than show nothing")
	}
	// words of the query are not required in the title for SoundCloud
	m := newSourceModel()
	m.SearchSource = "soundcloud"
	if r := m.filterResults(vs, "hollow knight"); len(r) != 3 {
		t.Errorf("soundcloud results were matched against the query: %d", len(r))
	}
	m.SearchSource = "youtube"
	yt := []downloader.Video{{ID: "1", Title: "Hollow Knight OST"}, {ID: "2", Title: "cat video"}}
	if r := m.filterResults(yt, "hollow knight"); len(r) != 1 || r[0].ID != "1" {
		t.Errorf("youtube relevance filter changed: %+v", r)
	}
}

func TestFetchPlaceholderNamesTheSource(t *testing.T) {
	m := newSourceModel()
	m.SearchQuery = "hollow knight"
	if got := m.fetchText(); got != "Fetching YouTube data..." {
		t.Errorf("youtube: %q", got)
	}
	m.SearchSource = "soundcloud"
	if got := m.fetchText(); got != "Fetching SoundCloud data..." {
		t.Errorf("soundcloud: %q", got)
	}
	m.SearchQuery = "https://artist.bandcamp.com/album/x"
	if got := m.fetchText(); got != "Fetching link data..." {
		t.Errorf("link: %q", got)
	}
}
