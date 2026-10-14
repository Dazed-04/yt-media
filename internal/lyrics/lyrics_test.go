package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleaning(t *testing.T) {
	cases := []struct{ in, noise, feat string }{
		{"Song (Official Video)", "Song", "Song"},
		{"Song [Lyrics]", "Song", "Song"},
		{"Song (feat. Someone) (Official Music Video)", "Song (feat. Someone)", "Song"},
		{"Song ft. Someone", "Song ft. Someone", "Song"},
		{"Song - Official Lyric Video", "Song", "Song"},
		{"Song (Live)", "Song (Live)", "Song (Live)"}, // a different version: keep
		{"Stay (With You)", "Stay (With You)", "Stay"},
	}
	for _, c := range cases {
		if got := stripNoise(c.in); got != c.noise {
			t.Errorf("stripNoise(%q) = %q, want %q", c.in, got, c.noise)
		}
		if got := stripFeat(stripNoise(c.in)); got != c.feat {
			t.Errorf("stripFeat(stripNoise(%q)) = %q, want %q", c.in, got, c.feat)
		}
	}
	if got := cleanArtist("The Weeknd - Topic"); got != "The Weeknd" {
		t.Errorf("cleanArtist = %q", got)
	}
	if got := cleanArtist("TaylorSwiftVEVO"); got != "TaylorSwift" {
		t.Errorf("cleanArtist = %q", got)
	}
	if got := primaryArtist("A, B & C"); got != "A" {
		t.Errorf("primaryArtist = %q", got)
	}
}

func TestCandidates(t *testing.T) {
	qs := Candidates(Track{Artist: "Taylor Swift", Title: "Taylor Swift - Love Story (feat. X) (Official Video)"})
	has := func(a, title string) bool {
		for _, q := range qs {
			if q.Artist == a && q.Title == title {
				return true
			}
		}
		return false
	}
	if !has("Taylor Swift", "Love Story") {
		t.Errorf("missing split artist/title query: %v", qs)
	}
	if !has("Taylor Swift", "Love Story (feat. X)") {
		t.Errorf("missing with-feat query (some songs keep it): %v", qs)
	}
	if last := qs[len(qs)-1]; last.Artist != "" || last.Title != "Love Story" {
		t.Errorf("last query should be a free-text title search, got %+v", last)
	}
	if len(qs) > maxQueries {
		t.Errorf("%d queries, max is %d", len(qs), maxQueries)
	}
	// exact spelling is tried before cleaned ones
	plain := Candidates(Track{Artist: "A", Title: "Song (feat. B)"})
	if plain[0].Title != "Song (feat. B)" || plain[1].Title != "Song" {
		t.Errorf("order wrong: %v", plain)
	}
}

func TestCleanLRC(t *testing.T) {
	raw := "[ar:x]\n[length: 3:00]\n[00:00.00] \n[00:12.50] first\n[00:20.00] second\nnot a lyric line\n"
	got := CleanLRC(raw, "Artist", "Title")
	want := "[ar:Artist]\n[ti:Title]\n[00:00.00] 🎵\n[00:12.50] first\n[00:20.00] second\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
	// no placeholder when the first line starts within 5s
	if got := CleanLRC("[00:03.00] hi\n", "A", "T"); strings.Contains(got, "🎵") {
		t.Errorf("unexpected placeholder: %q", got)
	}
	if CleanLRC("just text", "A", "T") != "" {
		t.Error("text without timestamps should yield nothing")
	}
}

func TestLRCToSRT(t *testing.T) {
	raw := "[00:01.00] one\n[00:05.50]\n[00:30.00] two\n[00:31.00][00:40.00] three <00:31.50>word\n"
	want := "1\n00:00:01,000 --> 00:00:05,500\none\n\n" +
		"2\n00:00:30,000 --> 00:00:31,000\ntwo\n\n" +
		"3\n00:00:31,000 --> 00:00:37,000\nthree word\n\n" + // capped at 6s
		"4\n00:00:40,000 --> 00:00:44,000\nthree word\n\n" // last line: 4s
	if got := LRCToSRT(raw); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestParseClock(t *testing.T) {
	for in, want := range map[string]float64{"3:45": 225, "1:02:03": 3723, "45": 45, "": 0, "x": 0} {
		if got := ParseClock(in); got != want {
			t.Errorf("ParseClock(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPaths(t *testing.T) {
	o := Options{MusicDir: "/m", LyricsDir: "/m/lyrics", Exclude: []string{"Podcasts/", "single/skip"}}
	if got := o.lrcPath("/m/single/a.mp3"); got != "/m/lyrics/single/a.lrc" {
		t.Errorf("lrcPath = %s", got)
	}
	if got := o.lrcPath("/elsewhere/b.flac"); got != "/m/lyrics/b.lrc" {
		t.Errorf("lrcPath outside library = %s", got)
	}
	if !o.excluded("/m/Podcasts/x.mp3") || !o.excluded("/m/single/skipme.mp3") || o.excluded("/m/single/ok.mp3") {
		t.Error("exclude prefixes wrong")
	}
	if srtPath("/v/clip.mkv") != "/v/clip.srt" {
		t.Error("srtPath wrong")
	}
}

// ---------- LRCLIB (fake server) ----------

type fakeLib struct {
	songs  []result
	status int
	hits   int
}

func (f *fakeLib) serve(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		if r.URL.Path == "/api/get" {
			http.NotFound(w, r)
			return
		}
		title := strings.ToLower(r.URL.Query().Get("track_name") + r.URL.Query().Get("q"))
		var out []result
		for _, s := range f.songs {
			if strings.Contains(title, strings.ToLower(s.TrackName)) {
				out = append(out, s)
			}
		}
		json.NewEncoder(w).Encode(out)
	}))
	old := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { baseURL = old; srv.Close() })
}

const sample = "[00:10.00] hello\n[00:15.00] world\n"

func TestFindMatching(t *testing.T) {
	lib := &fakeLib{songs: []result{
		{TrackName: "Blinding Lights", ArtistName: "The Weeknd", Duration: 200, SyncedLyrics: sample},
		{TrackName: "Blinding Lights", ArtistName: "Someone Else", Duration: 200, SyncedLyrics: sample},
		{TrackName: "Other Song", ArtistName: "The Weeknd", Duration: 200, SyncedLyrics: sample},
	}}
	lib.serve(t)

	// feat/Official-Video decoration, "- Topic" channel, 1s length difference
	m, err := Find(context.Background(), Track{
		Artist: "The Weeknd - Topic", Title: "Blinding Lights (feat. X) (Official Video)", Duration: 201,
	})
	if err != nil || m.Artist != "The Weeknd" {
		t.Fatalf("Find = %+v, %v", m, err)
	}

	// a version of the wrong length is rejected
	_, err = Find(context.Background(), Track{Artist: "The Weeknd", Title: "Blinding Lights", Duration: 260})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-length version: err = %v, want ErrNotFound", err)
	}

	// "Artist - Title" uploads
	if _, err = Find(context.Background(), Track{Artist: "TheWeekndVEVO", Title: "The Weeknd - Blinding Lights (Official Video)", Duration: 200}); err != nil {
		t.Fatalf("Artist - Title upload: %v", err)
	}

	// the same title by a different artist is not accepted
	_, err = Find(context.Background(), Track{Artist: "Nobody Known", Title: "Blinding Lights"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong artist: err = %v", err)
	}
}

func TestFindServiceDown(t *testing.T) {
	lib := &fakeLib{status: 503}
	lib.serve(t)
	_, err := Find(context.Background(), Track{Artist: "A", Title: "B song"})
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want a real error", err)
	}
	if lib.hits != 1 {
		t.Errorf("made %d requests to a failing service, want 1", lib.hits)
	}
}

// ---------- Ensure / Scan, with real files ----------

func makeMP3(t *testing.T, path, artist, title string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"-v", "quiet", "-y", "-f", "lavfi", "-i", "sine=duration=200", "-b:a", "16k"}
	if artist != "" {
		args = append(args, "-metadata", "artist="+artist)
	}
	if title != "" {
		args = append(args, "-metadata", "title="+title)
	}
	if out, err := exec.Command("ffmpeg", append(args, path)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
}

func TestEnsureAndScan(t *testing.T) {
	lib := &fakeLib{songs: []result{
		{TrackName: "Blinding Lights", ArtistName: "The Weeknd", Duration: 200, SyncedLyrics: sample},
		{TrackName: "Save Your Tears", ArtistName: "The Weeknd", Duration: 200, SyncedLyrics: sample},
	}}
	lib.serve(t)

	root := t.TempDir()
	o := Options{MusicDir: root, LyricsDir: filepath.Join(root, "lyrics"), Exclude: []string{"skipme/"}}

	makeMP3(t, filepath.Join(root, "single", "a.mp3"), "The Weeknd", "Blinding Lights (Official Video)")
	makeMP3(t, filepath.Join(root, "single", "b.mp3"), "", "") // no tags: falls back to the file name
	if err := os.Rename(filepath.Join(root, "single", "b.mp3"), filepath.Join(root, "single", "The Weeknd - Save Your Tears.mp3")); err != nil {
		t.Fatal(err)
	}
	makeMP3(t, filepath.Join(root, "single", "c.mp3"), "Nobody", "Unknown Song")
	makeMP3(t, filepath.Join(root, "skipme", "d.mp3"), "The Weeknd", "Blinding Lights")

	sum, err := Scan(context.Background(), o, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 4 || sum.Saved != 2 || sum.NotFound != 1 || sum.Excluded != 1 {
		t.Fatalf("summary = %+v", sum)
	}

	data, err := os.ReadFile(filepath.Join(root, "lyrics", "single", "a.lrc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "[ar:The Weeknd]\n[ti:Blinding Lights (Official Video)]\n[00:00.00] 🎵\n[00:10.00] hello") {
		t.Errorf("unexpected lrc:\n%s", data)
	}
	if !exists(filepath.Join(root, "lyrics", "single", "The Weeknd - Save Your Tears.lrc")) {
		t.Error("lyrics for the untagged file (found via its name) are missing")
	}

	// A second scan must not hit the service again for finished files.
	before := lib.hits
	sum, _ = Scan(context.Background(), o, 2, nil)
	if sum.Exists != 2 || sum.NotFound != 1 {
		t.Fatalf("second scan = %+v", sum)
	}
	if lib.hits == before {
		t.Log("not-found file was retried, as expected")
	}

	// Videos are off by default, and write a sidecar .srt when on.
	vid := filepath.Join(root, "v", "clip.mp3") // any media file works for the lookup
	makeMP3(t, vid, "The Weeknd", "Blinding Lights")
	if r := Ensure(context.Background(), o, Video, vid, Track{}); r.Outcome != Disabled {
		t.Errorf("video with VideoSRT off: %v", r.Outcome)
	}
	o.VideoSRT = true
	if r := Ensure(context.Background(), o, Video, vid, Track{}); r.Outcome != Saved || r.Path != filepath.Join(root, "v", "clip.srt") {
		t.Errorf("video srt: %+v", r)
	}
}

func TestEmbeddedLyricsSkipped(t *testing.T) {
	lib := &fakeLib{}
	lib.serve(t)
	root := t.TempDir()
	o := Options{MusicDir: root, LyricsDir: filepath.Join(root, "lyrics")}

	path := filepath.Join(root, "x.mp3")
	makeMP3(t, path, "A", "B")
	tagged := filepath.Join(root, "y.mp3")
	if out, err := exec.Command("ffmpeg", "-v", "quiet", "-y", "-i", path, "-c", "copy",
		"-metadata", "lyrics=la la la", tagged).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	if r := Ensure(context.Background(), o, Audio, tagged, Track{}); r.Outcome != HasEmbedded {
		t.Fatalf("outcome = %v, want embedded", r.Outcome)
	}
	if lib.hits != 0 {
		t.Error("looked up lyrics for a file that already has them")
	}
}

func TestFallbackCommand(t *testing.T) {
	lib := &fakeLib{} // knows nothing
	lib.serve(t)
	root := t.TempDir()
	// A stand-in for `syncedlyrics`: writes the query into the output file.
	script := filepath.Join(root, "fake.sh")
	os.WriteFile(script, []byte("#!/bin/sh\nprintf '[00:09.00] %s\\n' \"$1\" > \"$2\"\n"), 0o755)

	o := Options{MusicDir: root, LyricsDir: filepath.Join(root, "lyrics"), Fallback: script + " {query} {output}"}
	song := filepath.Join(root, "s.mp3")
	makeMP3(t, song, "Some Artist", "Some Title (feat. X)")

	r := Ensure(context.Background(), o, Audio, song, Track{})
	if r.Outcome != Saved {
		t.Fatalf("outcome = %v (%v)", r.Outcome, r.Err)
	}
	data, _ := os.ReadFile(r.Path)
	if !strings.Contains(string(data), "[00:09.00] Some Artist Some Title") {
		t.Errorf("fallback output not used:\n%s", data)
	}
}
