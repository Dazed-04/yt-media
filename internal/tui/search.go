package tui

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"yt-downloader/internal/downloader"

	tea "charm.land/bubbletea/v2"
)

var youtubeURLRe = regexp.MustCompile(`(?:youtube\.com/watch\?v=|youtu\.be/|youtube\.com/shorts/)([\w-]{11})`)

// extractYoutubeID pulls the video ID out of a YouTube link.
func extractYoutubeID(s string) (string, bool) {
	m := youtubeURLRe.FindStringSubmatch(s)
	if len(m) == 2 {
		return m[1], true
	}
	return "", false
}

// doSearch searches YouTube for the query, or fetches a single video when the
// query is a link. The query and limit are copied first: the command runs on
// another goroutine while the user keeps typing.
func (m *Model) doSearch() tea.Cmd {
	query := strings.TrimSpace(m.SearchQuery)
	limit := m.Config.SearchResultLimit
	cookies := m.Config.CookieArgs()

	return func() tea.Msg {
		if id, ok := downloader.PlaylistID(query); ok {
			vs, err := downloader.FetchPlaylist(id, maxPlaylistEntries, cookies)
			if err != nil {
				return errorMsg(err)
			}
			return searchResultsMsg(vs)
		}
		if id, ok := extractYoutubeID(query); ok {
			v, err := downloader.FetchSingle(id)
			if err != nil {
				return errorMsg(err)
			}
			return searchResultsMsg([]downloader.Video{v})
		}
		results, err := downloader.SearchYoutube(query, limit)
		if err != nil {
			return errorMsg(err)
		}
		return searchResultsMsg(results)
	}
}

// onSearchResults installs a fresh result list and starts loading previews.
func (m *Model) onSearchResults(videos []downloader.Video) tea.Cmd {
	m.IsFetching = false
	m.purgePreviewCache()

	var results []downloader.Video
	if _, ok := downloader.PlaylistID(strings.TrimSpace(m.SearchQuery)); ok {
		results = dedupe(videos) // a playlist is the whole list, whatever its titles say
	} else {
		results = filterRelevant(videos, m.SearchQuery)
	}
	m.MusicList = results
	m.FilteredList = results
	m.Cursor = 0
	m.ListOffset = 0

	m.prefetchGen++
	m.prefetchList = results
	m.prefetchIdx = 0

	cmds := []tea.Cmd{fetchOneThumbnail(results, 0, m.prefetchGen)}
	if v, ok := m.current(); ok {
		cmds = append(cmds, m.fetchSizeCmd(v, m.ChoiceType))
	}
	return tea.Batch(cmds...)
}

// maxPlaylistEntries caps how many playlist entries are loaded.
const maxPlaylistEntries = 200

// dedupe drops repeated IDs, keeping order.
func dedupe(videos []downloader.Video) []downloader.Video {
	seen := make(map[string]bool, len(videos))
	out := make([]downloader.Video, 0, len(videos))
	for _, v := range videos {
		if !seen[v.ID] {
			seen[v.ID] = true
			out = append(out, v)
		}
	}
	return out
}

// filterRelevant drops duplicates and results that share no word with the
// query. If nothing matches it returns everything, so a result list is never
// empty just because of the filter.
func filterRelevant(videos []downloader.Video, query string) []downloader.Video {
	words := strings.Fields(normalizeText(query))
	seen := make(map[string]bool)
	var out []downloader.Video

	for _, v := range videos {
		if seen[v.ID] {
			continue
		}
		title, artist := normalizeText(v.Title), normalizeText(v.Artist)
		for _, w := range words {
			if strings.Contains(title, w) || strings.Contains(artist, w) {
				out = append(out, v)
				seen[v.ID] = true
				break
			}
		}
	}
	if len(out) == 0 {
		return videos
	}
	return out
}

// normalizeText lowercases s and keeps only a-z, 0-9 and spaces.
func normalizeText(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			return r
		}
		return -1
	}, strings.ToLower(s))
}

// fetchSizeCmd asks yt-dlp for the download size of v in the given format
// ("Audio" or "Video"). Results are cached in m.SizeCache.
func (m *Model) fetchSizeCmd(v downloader.Video, format string) tea.Cmd {
	key := v.ID + "-" + format
	if m.SizeCache[key] != "" || m.SizeInFlight[key] {
		return nil
	}
	m.SizeInFlight[key] = true
	opts := m.Config.DownloadOptions // copy: the cmd runs on its own goroutine

	return func() tea.Msg {
		args := []string{"--print", "%(filesize,filesize_approx)s"}
		if format == "Audio" {
			args = append(args, "-f", "bestaudio/best")
		} else {
			args = append(args, "-f", opts.VideoSelector())
		}
		args = append(args, opts.CookieArgs()...)
		args = append(args, v.ID)

		sizeStr := "Unknown"
		if out, err := exec.Command("yt-dlp", args...).Output(); err == nil {
			for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
				if n, err := strconv.ParseFloat(line, 64); err == nil {
					sizeStr = fmt.Sprintf("%.1f MB", n/(1024*1024))
					break
				}
			}
		}
		return sizeFetchedMsg{videoID: v.ID, format: format, sizeStr: sizeStr}
	}
}
