// Package album finds the real album of a song on MusicBrainz and writes it
// into the file's tags.
//
// YouTube rarely knows a song's album, so downloaded songs show up as
// "Unknown Album". MusicBrainz knows which releases a recording appeared on,
// including the original album when the song was also released as a single.
package album

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"yt-downloader/internal/media"
	"yt-downloader/internal/netutil"
	"yt-downloader/internal/songname"
)

// ErrNotFound means MusicBrainz has no confident match.
var ErrNotFound = errors.New("no album found")

// Variables so tests can point at a local server.
var (
	baseURL    = "https://musicbrainz.org"
	httpClient = &http.Client{Timeout: 15 * time.Second}
	// MusicBrainz requires a User-Agent that identifies the app and a way to
	// reach its author. Put your repository URL or e-mail here.
	userAgent = "yt-media/1.0 ( https://github.com/your-name/yt-media )"

	// MusicBrainz allows one request per second, in total. The limiter is shared
	// by every caller (the TUI's downloads and the album command alike), and
	// "503 / 429" answers are waited out and retried.
	limiter = netutil.NewLimiter(1100 * time.Millisecond)
	retry   = netutil.Retry{Max: 4, Base: 5 * time.Second, MaxWait: 60 * time.Second}
)

// durTolerance is how far (seconds) a recording's length may differ from the
// file's before it is considered a different version.
const durTolerance = 8.0

type recording struct {
	Title        string `json:"title"`
	Length       int    `json:"length"` // milliseconds, may be 0
	ArtistCredit []struct {
		Name   string `json:"name"`
		Artist struct {
			Name string `json:"name"`
		} `json:"artist"`
	} `json:"artist-credit"`
	Releases []release `json:"releases"`
}

type release struct {
	Title        string `json:"title"`
	Status       string `json:"status"`
	Date         string `json:"date"`
	ReleaseGroup struct {
		PrimaryType    string   `json:"primary-type"`
		SecondaryTypes []string `json:"secondary-types"`
	} `json:"release-group"`
}

func (r recording) artists() []string {
	var out []string
	for _, c := range r.ArtistCredit {
		out = append(out, c.Name, c.Artist.Name)
	}
	return out
}

func quote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
	return `"` + s + `"`
}

func search(ctx context.Context, q songname.Query) ([]recording, error) {
	query := "recording:" + quote(q.Title)
	if q.Artist != "" {
		query += " AND artist:" + quote(q.Artist)
	}
	params := url.Values{"query": {query}, "fmt": {"json"}, "limit": {"15"}}

	resp, err := netutil.Do(ctx, httpClient, limiter, retry, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/ws/2/recording?"+params.Encode(), nil)
		if err == nil {
			req.Header.Set("User-Agent", userAgent)
			req.Header.Set("Accept", "application/json")
		}
		return req, err
	})
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("musicbrainz: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Recordings []recording `json:"recordings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Recordings, nil
}

type traceKey struct{}

// WithTrace returns a context whose lookups report what they try and why
// results were rejected to fn. It powers "yt-media album --verbose".
func WithTrace(ctx context.Context, fn func(string)) context.Context {
	return context.WithValue(ctx, traceKey{}, fn)
}

func tracef(ctx context.Context, format string, args ...any) {
	if fn, ok := ctx.Value(traceKey{}).(func(string)); ok {
		fn(fmt.Sprintf(format, args...))
	}
}

// Find returns the album the track originally appeared on.
func Find(ctx context.Context, t songname.Track) (string, error) {
	titles := songname.TitleVariants(t)
	known := songname.KnownArtists(t)
	tracef(ctx, "looking for %q by %v, length %.0fs", titles, known, t.Duration)

	for _, q := range songname.CleanestFirst(t) {
		artists := known
		if q.Artist != "" {
			artists = []string{q.Artist}
		}
		tracef(ctx, "search: artist=%q title=%q", q.Artist, q.Title)
		recs, err := search(ctx, q)
		if err != nil {
			return "", err
		}
		tracef(ctx, "  %d recordings", len(recs))
		if name, ok := pickAlbum(ctx, recs, artists, titles, t.Duration); ok {
			tracef(ctx, "  -> %q", name)
			return name, nil
		}
	}
	return "", ErrNotFound
}

// pickAlbum chooses an album from the matching recordings: the title and artist
// must match. Recordings of about the right length are preferred; YouTube
// videos often carry an intro or outro, so when none is close enough the length
// is ignored rather than giving up. Of the matching recordings' releases, a
// studio album (or soundtrack) beats an EP, which beats a single; compilations,
// live albums and the like are only a last resort. Ties go to the earliest
// release, which is normally the original album.
func pickAlbum(ctx context.Context, recs []recording, artists, titles []string, duration float64) (string, bool) {
	var matching []recording // same song, same artist
	for _, r := range recs {
		if !songname.TitleEquals(titles, r.Title) {
			tracef(ctx, "  skip %q: title differs", r.Title)
			continue
		}
		okArtist := len(artists) == 0
		for _, a := range r.artists() {
			if songname.AnyArtistMatches(artists, a) {
				okArtist = true
			}
		}
		if !okArtist {
			tracef(ctx, "  skip %q by %v: artist differs", r.Title, r.artists())
			continue
		}
		matching = append(matching, r)
	}

	if duration > 0 {
		var close []recording
		for _, r := range matching {
			if r.Length == 0 || math.Abs(float64(r.Length)/1000-duration) <= durTolerance {
				close = append(close, r)
			} else {
				tracef(ctx, "  %q: length %.0fs vs %.0fs", r.Title, float64(r.Length)/1000, duration)
			}
		}
		if len(close) > 0 {
			matching = close
		} else if len(matching) > 0 {
			tracef(ctx, "  no recording of the right length; ignoring length")
		}
	}

	type cand struct {
		title string
		rank  int
		date  string
	}
	var cands []cand
	for _, r := range matching {
		for _, rel := range r.Releases {
			if strings.TrimSpace(rel.Title) == "" {
				continue
			}
			cands = append(cands, cand{rel.Title, rank(rel), rel.Date})
		}
	}
	if len(cands) == 0 {
		if len(matching) > 0 {
			tracef(ctx, "  the matching recordings have no releases")
		}
		return "", false
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].rank != cands[j].rank {
			return cands[i].rank < cands[j].rank
		}
		// releases without a date sort last
		di, dj := cands[i].date, cands[j].date
		if (di == "") != (dj == "") {
			return dj == ""
		}
		return di < dj
	})
	return cands[0].title, true
}

// rank orders release kinds; lower is better.
func rank(r release) int {
	rg := r.ReleaseGroup
	n := 0
	switch strings.ToLower(rg.PrimaryType) {
	case "album":
		n = 0
	case "ep":
		n = 10
	case "single":
		n = 20
	default:
		n = 30
	}
	for _, st := range rg.SecondaryTypes {
		if strings.EqualFold(st, "soundtrack") {
			n += 5 // a film's soundtrack is the song's real home, but a studio album still wins
		} else {
			n += 100 // compilation, live, remix, DJ-mix, ...
		}
	}
	if r.Status != "" && !strings.EqualFold(r.Status, "Official") {
		n += 50 // bootleg, promotion
	}
	return n
}

// Outcome says what Ensure did.
type Outcome int

const (
	Tagged   Outcome = iota // the album was found and written
	HasAlbum                // the file already has an album
	NotFound                // MusicBrainz doesn't know the song
	Failed                  // lookup or write failed
)

func (o Outcome) String() string {
	return [...]string{"tagged", "has album", "not found", "failed"}[o]
}

// Result is the outcome for one file.
type Result struct {
	Outcome Outcome
	Album   string
	Err     error
}

// isValidAlbum reports whether raw contains a genuine album name rather than
// an empty string or standard dummy/fallback placeholder.
func isValidAlbum(raw string) bool {
	val := strings.TrimSpace(raw)
	if val == "" {
		return false
	}
	switch strings.ToLower(val) {
	case "n/a", "na", "none", "null", "unknown", "unknown album", "untitled":
		return false
	default:
		return true
	}
}

// Apply writes name into file's album tag unless the file already has a real
// album. The TUI uses it after it has looked the album up (to choose the
// folder), so the lookup isn't made twice.
func Apply(ctx context.Context, file, name string) Result {
	info, err := media.Probe(ctx, file)
	if err != nil {
		return Result{Outcome: Failed, Err: err}
	}
	if isValidAlbum(info.Tags["album"]) {
		return Result{Outcome: HasAlbum, Album: strings.TrimSpace(info.Tags["album"])}
	}
	if err := media.WriteTags(ctx, file, map[string]string{"album": name}); err != nil {
		return Result{Outcome: Failed, Err: err}
	}
	return Result{Outcome: Tagged, Album: name}
}

// Ensure writes the album into file's tags unless it already has one.
// hint is what the caller knows about the song; gaps come from the file.
func Ensure(ctx context.Context, file string, hint songname.Track) Result {
	info, err := media.Probe(ctx, file)
	if err != nil {
		return Result{Outcome: Failed, Err: err}
	}
	if isValidAlbum(info.Tags["album"]) {
		return Result{Outcome: HasAlbum, Album: strings.TrimSpace(info.Tags["album"])}
	}

	// hint.Album is deliberately dropped: it is empty or a placeholder here.
	track := songname.Merge(hint, info.Track(file))
	track.Album = ""

	name, err := Find(ctx, track)
	if errors.Is(err, ErrNotFound) {
		return Result{Outcome: NotFound}
	}
	if err != nil {
		return Result{Outcome: Failed, Err: err}
	}
	return Apply(ctx, file, name)
}

// IsValid reports whether raw is a real album name (not empty or a
// placeholder such as "Unknown Album").
func IsValid(raw string) bool { return isValidAlbum(raw) }
