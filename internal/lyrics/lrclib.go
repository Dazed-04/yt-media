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
	"yt-downloader/internal/netutil"
	"yt-downloader/internal/songname"
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

	// Requests from all workers are spaced out, and 429/503 answers are waited
	// out and retried (see netutil.Do).
	limiter = netutil.NewLimiter(350 * time.Millisecond)
	retry   = netutil.Retry{Max: 4, Base: 2 * time.Second, MaxWait: 45 * time.Second}
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
	resp, err := netutil.Do(ctx, httpClient, limiter, retry, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path+"?"+params.Encode(), nil)
		if err == nil {
			req.Header.Set("User-Agent", userAgent)
		}
		return req, err
	})
	if err != nil {
		return false, fmt.Errorf("lrclib: %w", err)
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
	titles := songname.TitleVariants(t)

	// Exact lookup first: cheap and unambiguous when we know album and length.
	if t.Album != "" && t.Duration > 0 && t.Artist != "" {
		var r result
		params := url.Values{
			"artist_name": {songname.CleanArtist(t.Artist)},
			"track_name":  {songname.StripNoise(t.Title)},
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
	knownArtists := songname.KnownArtists(t)

	for _, q := range songname.Candidates(t) {
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
		if !usable(r) || !songname.AnyArtistMatches(artists, r.ArtistName) || !songname.TitleMatches(titles, r.TrackName) {
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
