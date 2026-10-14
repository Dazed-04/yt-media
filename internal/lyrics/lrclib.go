package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ErrNotFound means the service has no suitable synced lyrics.
var ErrNotFound = errors.New("no synced lyrics found")

// durTolerance is how far (in seconds) a result's length may differ from the
// file's. A bigger gap is usually a different version (live, extended) whose
// timestamps wouldn't line up.
const durTolerance = 6.0

// Variables so tests can point at a local server.
var (
	baseURL    = "https://lrclib.net"
	httpClient = &http.Client{Timeout: 10 * time.Second}
	userAgent  = "yt-media (terminal yt-dlp wrapper)"
)

// Match is lyrics found for a track.
type Match struct {
	LRC    string // synced lyrics, LRC format
	Artist string
	Title  string
}

type result struct {
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	AlbumName    string  `json:"albumName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

func get(ctx context.Context, path string, params url.Values, out any) (found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return false, nil
	case resp.StatusCode != http.StatusOK:
		return false, fmt.Errorf("lrclib: HTTP %d", resp.StatusCode)
	}
	return true, json.NewDecoder(resp.Body).Decode(out)
}

// Find looks for synced lyrics for t. It returns ErrNotFound when the service
// has none, and any other error when the service couldn't be reached.
func Find(ctx context.Context, t Track) (Match, error) {
	titles := titleVariants(t)

	// Exact lookup first: cheap and unambiguous when we know album and length.
	if t.Album != "" && t.Duration > 0 && t.Artist != "" {
		var r result
		params := url.Values{
			"artist_name": {cleanArtist(t.Artist)},
			"track_name":  {stripNoise(t.Title)},
			"album_name":  {t.Album},
			"duration":    {strconv.Itoa(int(math.Round(t.Duration)))},
		}
		ok, err := get(ctx, "/api/get", params, &r)
		if err != nil {
			return Match{}, err
		}
		if ok && usable(r) {
			return Match{LRC: r.SyncedLyrics, Artist: r.ArtistName, Title: r.TrackName}, nil
		}
	}

	// A free-text search matches songs by title alone, so for it the artist is
	// checked against what we know about the track.
	var knownArtists []string
	if a := primaryArtist(cleanArtist(t.Artist)); a != "" {
		knownArtists = append(knownArtists, a)
	}
	for _, title := range []string{t.Title, t.AltTitle} {
		if left, _, ok := splitArtistTitle(stripNoise(title)); ok {
			knownArtists = append(knownArtists, primaryArtist(cleanArtist(left)))
		}
	}

	for _, q := range Candidates(t) {
		artists := knownArtists
		if q.Artist != "" {
			artists = []string{q.Artist}
		}
		params := url.Values{}
		if q.Artist != "" {
			params.Set("track_name", q.Title)
			params.Set("artist_name", q.Artist)
		} else {
			params.Set("q", q.Title)
		}
		var rs []result
		if _, err := get(ctx, "/api/search", params, &rs); err != nil {
			return Match{}, err
		}
		if r, ok := pick(rs, artists, titles, t.Duration); ok {
			return Match{LRC: r.SyncedLyrics, Artist: r.ArtistName, Title: r.TrackName}, nil
		}
	}
	return Match{}, ErrNotFound
}

func usable(r result) bool {
	return !r.Instrumental && strings.TrimSpace(r.SyncedLyrics) != ""
}

// pick chooses the best result: it must have synced lyrics, belong to
// the same artist and song, and (when we know it) have about the right length.
// Of those, the closest length wins.
func pick(rs []result, artists, titles []string, duration float64) (result, bool) {
	best, bestDiff := -1, math.MaxFloat64
	for i, r := range rs {
		if !usable(r) || !anyArtistMatches(artists, r.ArtistName) || !titleMatches(titles, r.TrackName) {
			continue
		}
		diff := 0.0
		if duration > 0 && r.Duration > 0 {
			if diff = math.Abs(r.Duration - duration); diff > durTolerance {
				continue
			}
		}
		if diff < bestDiff {
			best, bestDiff = i, diff
		}
	}
	if best < 0 {
		return result{}, false
	}
	return rs[best], true
}

// norm lowercases s and keeps only letters and digits.
func norm(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// anyArtistMatches is true when no artist is known, or one of them matches.
func anyArtistMatches(wants []string, got string) bool {
	if len(wants) == 0 {
		return true
	}
	for _, w := range wants {
		if artistMatches(w, got) {
			return true
		}
	}
	return false
}

func artistMatches(want, got string) bool {
	if want == "" {
		return true
	}
	nw, ng := norm(want), norm(got)
	if nw == "" || ng == "" {
		return false
	}
	if strings.Contains(ng, nw) || strings.Contains(nw, ng) {
		return true
	}
	for _, w := range strings.Fields(strings.ToLower(want)) {
		if nwd := norm(w); len(nwd) >= 3 && strings.Contains(ng, nwd) {
			return true
		}
	}
	return false
}

func titleMatches(wants []string, got string) bool {
	ng := norm(got)
	if ng == "" {
		return false
	}
	for _, w := range wants {
		nw := norm(w)
		switch {
		case nw == "":
		case nw == ng:
			return true
		case len(nw) >= 4 && len(ng) >= 4 && (strings.Contains(ng, nw) || strings.Contains(nw, ng)):
			return true
		}
	}
	return false
}
