package album

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"yt-downloader/internal/media"
	"yt-downloader/internal/netutil"
	"yt-downloader/internal/songname"
)

const weBallJSON = `{"recordings":[
 {"title":"We Ball","length":200000,"artist-credit":[{"name":"Ace Hood","artist":{"name":"Ace Hood"}}],
  "releases":[
   {"title":"Greatest Hits","status":"Official","date":"2020-01-01","release-group":{"primary-type":"Album","secondary-types":["Compilation"]}},
   {"title":"We Ball","status":"Official","date":"2011-05-01","release-group":{"primary-type":"Single"}},
   {"title":"Body Bag 3","status":"Official","date":"2012-02-02","release-group":{"primary-type":"Album"}}]},
 {"title":"We Ball","length":200000,"artist-credit":[{"name":"Someone Else","artist":{"name":"Someone Else"}}],
  "releases":[{"title":"Wrong Album","status":"Official","date":"2000","release-group":{"primary-type":"Album"}}]}
]}`

func server(t *testing.T, hits *atomic.Int32, body string) {
	t.Helper()
	limiter.SetInterval(0)
	retry = netutil.Retry{Max: 2, Base: time.Millisecond}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !strings.Contains(r.Header.Get("User-Agent"), "yt-media") {
			t.Errorf("missing user agent")
		}
		q := r.URL.Query().Get("query")
		if strings.Contains(q, "We Ball") || strings.Contains(q, "we ball") {
			w.Write([]byte(body))
			return
		}
		w.Write([]byte(`{"recordings":[]}`))
	}))
	oldURL := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { srv.Close(); baseURL = oldURL })
}

func TestFindPrefersOriginalAlbum(t *testing.T) {
	var hits atomic.Int32
	server(t, &hits, weBallJSON)
	got, err := Find(context.Background(), songname.Track{Artist: "Ace Hood - Topic", Title: "We Ball (Official Video)", Duration: 201})
	if err != nil || got != "Body Bag 3" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestFindWrongLengthOrArtistIsNotFound(t *testing.T) {
	var hits atomic.Int32
	server(t, &hits, weBallJSON)
	// YouTube videos often carry an intro, so a wrong length alone is not
	// a reason to give up
	if got, err := Find(context.Background(), songname.Track{Artist: "Ace Hood", Title: "We Ball", Duration: 400}); err != nil || got != "Body Bag 3" {
		t.Errorf("length mismatch: %q, %v", got, err)
	}
	if _, err := Find(context.Background(), songname.Track{Artist: "Nobody", Title: "We Ball"}); err != ErrNotFound {
		t.Errorf("artist mismatch: %v", err)
	}
}

func TestFindSingleIsBetterThanNothing(t *testing.T) {
	body := `{"recordings":[{"title":"We Ball","artist-credit":[{"name":"Ace Hood","artist":{"name":"Ace Hood"}}],
	 "releases":[{"title":"We Ball","status":"Official","date":"2011","release-group":{"primary-type":"Single"}}]}]}`
	var hits atomic.Int32
	server(t, &hits, body)
	got, err := Find(context.Background(), songname.Track{Artist: "Ace Hood", Title: "We Ball"})
	if err != nil || got != "We Ball" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestFindServiceDown(t *testing.T) {
	limiter.SetInterval(0)
	retry = netutil.Retry{Max: 2, Base: time.Millisecond}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	old := baseURL
	baseURL = srv.URL
	defer func() { baseURL = old }()
	_, err := Find(context.Background(), songname.Track{Artist: "A", Title: "B"})
	if err == nil || err == ErrNotFound {
		t.Fatalf("err = %v", err)
	}
}

func mp3(t *testing.T, path string, tags ...string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg missing")
	}
	args := []string{"-y", "-v", "error", "-f", "lavfi", "-i", "sine=d=1", "-c:a", "libmp3lame"}
	for i := 0; i+1 < len(tags); i += 2 {
		args = append(args, "-metadata", tags[i]+"="+tags[i+1])
	}
	if out, err := exec.Command("ffmpeg", append(args, path)...).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
}

func TestEnsure(t *testing.T) {
	var hits atomic.Int32
	server(t, &hits, strings.ReplaceAll(weBallJSON, `"length":200000`, `"length":1000`)) // test files are 1s long
	dir := t.TempDir()

	f := filepath.Join(dir, "a.mp3")
	mp3(t, f, "artist", "Ace Hood", "title", "We Ball")
	res := Ensure(context.Background(), f, songname.Track{})
	if res.Outcome != Tagged || res.Album != "Body Bag 3" {
		t.Fatalf("res = %+v", res)
	}
	in, _ := media.Probe(context.Background(), f)
	if in.Tags["album"] != "Body Bag 3" {
		t.Errorf("album tag = %q", in.Tags["album"])
	}

	// already tagged: no request is made, tag untouched
	n := hits.Load()
	if res := Ensure(context.Background(), f, songname.Track{}); res.Outcome != HasAlbum || hits.Load() != n {
		t.Errorf("second run: %+v, hits %d->%d", res, n, hits.Load())
	}

	// untagged file named "Artist - Title" is found via its name
	g := filepath.Join(dir, "Ace Hood - We Ball (Official Video).mp3")
	mp3(t, g)
	if res := Ensure(context.Background(), g, songname.Track{}); res.Outcome != Tagged {
		t.Errorf("by name: %+v", res)
	}

	// unknown song stays untagged
	h := filepath.Join(dir, "Foo - Bar.mp3")
	mp3(t, h)
	if res := Ensure(context.Background(), h, songname.Track{}); res.Outcome != NotFound {
		t.Errorf("unknown: %+v", res)
	}
	in, _ = media.Probe(context.Background(), h)
	if in.Tags["album"] != "" {
		t.Errorf("album written for unknown song")
	}
}

func TestRateLimit(t *testing.T) {
	var hits atomic.Int32
	server(t, &hits, weBallJSON)
	limiter.SetInterval(120 * time.Millisecond)
	defer limiter.SetInterval(0)
	start := time.Now()
	for range 3 {
		if _, err := search(context.Background(), songname.Query{Artist: "Ace Hood", Title: "We Ball"}); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 200*time.Millisecond {
		t.Errorf("3 requests took only %v", d)
	}
}

func TestRateLimitedThenRecovers(t *testing.T) {
	var hits atomic.Int32
	limiter.SetInterval(0)
	retry = netutil.Retry{Max: 3, Base: time.Millisecond}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(weBallJSON))
	}))
	defer srv.Close()
	old := baseURL
	baseURL = srv.URL
	defer func() { baseURL = old }()

	got, err := Find(context.Background(), songname.Track{Artist: "Ace Hood", Title: "We Ball"})
	if err != nil || got != "Body Bag 3" {
		t.Fatalf("got %q, %v (hits %d)", got, err, hits.Load())
	}
}

func TestScanStopsWhenRefusedRepeatedly(t *testing.T) {
	limiter.SetInterval(0)
	retry = netutil.Retry{Max: 0}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(503)
	}))
	defer srv.Close()
	old := baseURL
	baseURL = srv.URL
	defer func() { baseURL = old }()

	dir := t.TempDir()
	for _, n := range []string{"A - One", "A - Two", "A - Three", "A - Four", "A - Five"} {
		mp3(t, filepath.Join(dir, n+".mp3"))
	}
	sum, err := Scan(context.Background(), dir, nil, nil)
	if !errors.Is(err, netutil.ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	if sum.Failed != giveUpAfter {
		t.Errorf("failed = %d, want %d (stopped early)", sum.Failed, giveUpAfter)
	}
}

func TestScanExcludesPaths(t *testing.T) {
	var hits atomic.Int32
	server(t, &hits, strings.ReplaceAll(weBallJSON, `"length":200000`, `"length":1000`))
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Podcasts"), 0o755)
	mp3(t, filepath.Join(dir, "Podcasts", "Ace Hood - We Ball.mp3"))
	mp3(t, filepath.Join(dir, "Ace Hood - We Ball.mp3"))
	sum, err := Scan(context.Background(), dir, []string{"Podcasts"}, nil)
	if err != nil || sum.Excluded != 1 || sum.Tagged != 1 || sum.Total != 1 {
		t.Fatalf("sum = %+v err = %v", sum, err)
	}
}

func TestIsValidAlbum(t *testing.T) {
	for in, want := range map[string]bool{"": false, "  ": false, "Unknown Album": false, "N/A": false, "Body Bag 3": true} {
		if got := isValidAlbum(in); got != want {
			t.Errorf("isValidAlbum(%q) = %v", in, got)
		}
	}
}

func TestPlaceholderAlbumIsReplacedAndApply(t *testing.T) {
	var hits atomic.Int32
	server(t, &hits, strings.ReplaceAll(weBallJSON, `"length":200000`, `"length":1000`))
	dir := t.TempDir()
	f := filepath.Join(dir, "a.mp3")
	mp3(t, f, "artist", "Ace Hood", "title", "We Ball", "album", "Unknown Album")

	if res := Ensure(context.Background(), f, songname.Track{}); res.Outcome != Tagged || res.Album != "Body Bag 3" {
		t.Fatalf("res = %+v", res)
	}
	// Apply never overwrites a real album
	if res := Apply(context.Background(), f, "Other"); res.Outcome != HasAlbum || res.Album != "Body Bag 3" {
		t.Errorf("apply over real album: %+v", res)
	}
	g := filepath.Join(dir, "b.mp3")
	mp3(t, g, "artist", "X", "title", "Y")
	if res := Apply(context.Background(), g, "Chosen"); res.Outcome != Tagged {
		t.Errorf("apply: %+v", res)
	}
	in, _ := media.Probe(context.Background(), g)
	if in.Tags["album"] != "Chosen" {
		t.Errorf("album = %q", in.Tags["album"])
	}
}

func TestExcluded(t *testing.T) {
	root := "/music"
	ex := []string{"Podcasts", "Artist/Live"}
	for file, want := range map[string]bool{
		"/music/Podcasts/a.mp3":      true,
		"/music/Podcasts2/a.mp3":     false,
		"/music/Artist/Live/x/a.mp3": true,
		"/music/Artist/Studio/a.mp3": false,
		"/elsewhere/a.mp3":           false,
	} {
		if got := Excluded(root, file, ex); got != want {
			t.Errorf("Excluded(%s) = %v, want %v", file, got, want)
		}
	}
}

// serveFor answers searches whose query mentions needle with body.
func serveFor(t *testing.T, needle, body string) {
	t.Helper()
	limiter.SetInterval(0)
	retry = netutil.Retry{Max: 0}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), needle) {
			w.Write([]byte(body))
			return
		}
		w.Write([]byte(`{"recordings":[]}`))
	}))
	old := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { srv.Close(); baseURL = old })
}

const annihilateJSON = `{"recordings":[{"title":"Annihilate","length":200000,
 "artist-credit":[{"name":"Metro Boomin","artist":{"name":"Metro Boomin"}},{"name":"Swae Lee","artist":{"name":"Swae Lee"}}],
 "releases":[{"title":"Spider-Man: Across the Spider-Verse (Soundtrack)","status":"Official","date":"2023-06-02","release-group":{"primary-type":"Album","secondary-types":["Soundtrack"]}},
             {"title":"Annihilate","status":"Official","date":"2023-05-26","release-group":{"primary-type":"Single"}}]}]}`

// A music video's file name: full-width characters, a bracketed film title and
// a length well off the studio track's. The real title is "Annihilate".
func TestFindMessyYouTubeTitle(t *testing.T) {
	serveFor(t, `"Annihilate"`, annihilateJSON)
	var trace []string
	ctx := WithTrace(context.Background(), func(s string) { trace = append(trace, s) })
	got, err := Find(ctx, songname.Track{
		Artist:   "Metro Boomin",
		Title:    songname.Unsanitize("Annihilate (Spider-Man： Across the Spider-Verse)"),
		Duration: 245, // 45s of video intro
	})
	if err != nil {
		t.Fatalf("err = %v\n%s", err, strings.Join(trace, "\n"))
	}
	// the film's soundtrack beats the single
	if got != "Spider-Man: Across the Spider-Verse (Soundtrack)" {
		t.Errorf("album = %q", got)
	}
	if !strings.Contains(strings.Join(trace, "\n"), "ignoring length") {
		t.Errorf("trace does not explain the length fallback:\n%s", strings.Join(trace, "\n"))
	}
}

func TestEnsureColonNamedFile(t *testing.T) {
	serveFor(t, `"Heathens"`, `{"recordings":[{"title":"Heathens","length":200000,
	 "artist-credit":[{"name":"Twenty One Pilots","artist":{"name":"Twenty One Pilots"}}],
	 "releases":[{"title":"Suicide Squad: The Album","status":"Official","date":"2016-08-05","release-group":{"primary-type":"Album","secondary-types":["Soundtrack"]}},
	             {"title":"Heathens","status":"Official","date":"2016-06-16","release-group":{"primary-type":"Single"}}]}]}`)
	f := filepath.Join(t.TempDir(), "twenty one pilots： Heathens (from Suicide Squad： The Album) [OFFICIAL VIDEO].mp3")
	mp3(t, f)
	res := Ensure(context.Background(), f, songname.Track{})
	if res.Outcome != Tagged || res.Album != "Suicide Squad: The Album" {
		t.Fatalf("res = %+v", res)
	}
}
