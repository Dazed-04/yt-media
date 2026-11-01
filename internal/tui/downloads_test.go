package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"yt-downloader/internal/config"
	"yt-downloader/internal/downloader"
	"yt-downloader/internal/lyrics"
	"yt-downloader/internal/media"
)

func TestBatchStatsExcludeFailuresFromGoal(t *testing.T) {
	m := &Model{Jobs: []DownloadJob{
		{Status: StatusDone},
		{Status: StatusError},
		{Status: StatusDownloading, Progress: 0.5},
		{Status: StatusPending},
	}}
	st := m.batchStats()
	if st.total != 4 || st.done != 1 || st.failed != 1 || st.active != 2 {
		t.Fatalf("unexpected stats %+v", st)
	}
	if st.goal() != 3 {
		t.Fatalf("goal = %d, want 3", st.goal())
	}
	if st.progress != 1.5 {
		t.Fatalf("progress = %v, want 1.5", st.progress)
	}
}

func TestOutputDirIsArtistAlbum(t *testing.T) {
	cfg := config.Config{MusicDir: "/m", VideoDir: "/v"}
	mk := func(typ string, v downloader.Video) downloadParams {
		return downloadParams{req: DownloadReq{Type: typ, Video: v}, cfg: cfg, home: "/h"}
	}
	cases := []struct {
		name  string
		p     downloadParams
		album string
		want  string
	}{
		{"album known", mk("Audio", downloader.Video{Artist: "Ace Hood", Track: "We Ball"}), "Body Bag 3", "/m/Ace Hood/Body Bag 3"},
		{"several artists share a folder", mk("Audio", downloader.Video{Artist: "Ace Hood, Rick Ross"}), "X", "/m/Ace Hood/X"},
		{"topic channel", mk("Audio", downloader.Video{Artist: "The Weeknd - Topic"}), "After Hours", "/m/The Weeknd/After Hours"},
		{"artist from Artist - Title upload", mk("Audio", downloader.Video{Title: "Ace Hood - We Ball (Official Video)", Channel: "Some Label"}), "", "/m/Ace Hood/Unknown Album"},
		{"no album", mk("Audio", downloader.Video{Artist: "Ace Hood"}), "", "/m/Ace Hood/Unknown Album"},
		{"unsafe characters", mk("Audio", downloader.Video{Artist: "AC/DC"}), `Back: In "Black"?`, "/m/AC_DC/Back In Black"},
		{"nothing known", mk("Audio", downloader.Video{Title: "just a title"}), "", "/m/Unknown Artist/Unknown Album"},
		{"video has no album folder", mk("Video", downloader.Video{Artist: "Ace Hood"}), "ignored", "/v/Ace Hood"},
	}
	for _, c := range cases {
		if got := c.p.outputDir(c.album); got != c.want {
			t.Errorf("%s: outputDir = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestOutputTemplateNamesFileAfterTrack(t *testing.T) {
	p := downloadParams{req: DownloadReq{Type: "Audio", Video: downloader.Video{ID: "x", Track: "100% Pure/Love"}}}
	args := p.args("/m/A/B", "")
	var out string
	for i, a := range args {
		if a == "-o" {
			out = args[i+1]
		}
	}
	if out != "/m/A/B/100%% Pure_Love.%(ext)s" {
		t.Errorf("template = %q", out)
	}
	p.req.Video.Track = ""
	for i, a := range p.args("/m/A/B", "") {
		if a == "-o" && p.args("/m/A/B", "")[i+1] != "/m/A/B/%(title)s.%(ext)s" {
			t.Errorf("template without a track = %q", p.args("/m/A/B", "")[i+1])
		}
	}
}

func TestFolderName(t *testing.T) {
	for in, want := range map[string]string{
		"  Hello   World. ": "Hello World",
		"a/b\\c":            "a_b_c",
		`What?*<>|`:         "What",
		"...":               "fb",
	} {
		if got := folderName(in, "fb"); got != want {
			t.Errorf("folderName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A stand-in yt-dlp: writes a real mp3 where the output template says, and
// reports the path like --print-to-file does.
func fakeYtdlp(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	bin := t.TempDir()
	script := `#!/bin/sh
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift ;;
    --print-to-file) case "$2" in *chapters*) chapfile="$3" ;; *) pathfile="$3" ;; esac; shift 2 ;;
  esac
  shift
done
file=$(printf '%s' "$out" | sed 's/%(title)s/Video Title/; s/%(ext)s/mp3/; s/%%/%/g')
mkdir -p "$(dirname "$file")"
ffmpeg -v quiet -y -f lavfi -i sine=d=${FAKE_SECS:-1} "$file" || exit 1
[ -n "$chapfile" ] && printf '%s\n' "${FAKE_CHAPTERS:-NA}" > "$chapfile"
[ -n "$pathfile" ] && printf '%s\n' "$file" > "$pathfile"
echo "[download] 100% of 1MiB"
`
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDownloadFilesUnderArtistAlbumAndTagsIt(t *testing.T) {
	fakeYtdlp(t)
	home := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	p := downloadParams{
		index:    0,
		req:      DownloadReq{Type: "Audio", Video: downloader.Video{ID: "abc", Artist: "Ace Hood", Track: "We Ball", Album: "Body Bag 3"}},
		cfg:      config.Config{MusicDir: filepath.Join(home, "Music")},
		home:     home,
		archive:  filepath.Join(home, "archive.txt"),
		progress: make(chan JobProgressMsg, 16),
		// the album came from YouTube, so no lookup is made, but it is still
		// written into the file's tags
		fetchAlbum: true,
	}
	msg := p.run()
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	want := filepath.Join(home, "Music", "Ace Hood", "Body Bag 3", "We Ball.mp3")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("file not at %s: %v", want, err)
	}
	if in, err := media.Probe(context.Background(), want); err != nil || in.Tags["album"] != "Body Bag 3" {
		t.Errorf("album tag = %q (%v)", in.Tags["album"], err)
	}
	if _, err := os.Stat(filepath.Join(home, "Music", "single")); err == nil {
		t.Error("a single/ folder was created")
	}
}

func TestDownloadWithoutAlbumGoesToUnknownAlbum(t *testing.T) {
	fakeYtdlp(t)
	home := t.TempDir()
	p := downloadParams{
		req:      DownloadReq{Type: "Audio", Video: downloader.Video{ID: "abc", Artist: "Ace Hood"}},
		cfg:      config.Config{MusicDir: filepath.Join(home, "Music")},
		home:     home,
		progress: make(chan JobProgressMsg, 16),
	}
	if msg := p.run(); msg.Err != nil {
		t.Fatal(msg.Err)
	}
	if _, err := os.Stat(filepath.Join(home, "Music", "Ace Hood", "Unknown Album", "Video Title.mp3")); err != nil {
		t.Fatal(err)
	}
}

func joined(a []string) string { return " " + strings.Join(a, " ") + " " }

func TestArgsHonourDownloadOptions(t *testing.T) {
	cfg := config.DefaultDownloadOptions()
	cfg.AudioFormat = "opus"
	cfg.SponsorblockRemove = "music_offtopic"
	cfg.CookiesFromBrowser = "firefox"
	cfg.MaxVideoHeight = 720
	base := downloadParams{home: "/h", archive: "/a", req: DownloadReq{Video: downloader.Video{ID: "abcdefghijk"}}}
	base.cfg.DownloadOptions = cfg

	au := base
	au.req.Type = "Audio"
	got := joined(au.args("/d", ""))
	for _, w := range []string{" --audio-format opus ", " --sponsorblock-remove music_offtopic ", " --cookies-from-browser firefox "} {
		if !strings.Contains(got, w) {
			t.Errorf("audio args lack %q: %s", w, got)
		}
	}

	vi := base
	vi.req.Type = "Video"
	got = joined(vi.args("/d", ""))
	if !strings.Contains(got, "bestvideo[height<=720]+bestaudio/best[height<=720]") || !strings.Contains(got, " --cookies-from-browser firefox ") {
		t.Errorf("video args: %s", got)
	}
	if strings.Contains(got, "sponsorblock") || strings.Contains(got, "--audio-format") {
		t.Errorf("audio-only options leaked into video: %s", got)
	}

	// defaults are unchanged: mp3, no cookies, no sponsorblock
	def := base
	def.cfg.DownloadOptions = config.DefaultDownloadOptions()
	def.req.Type = "Audio"
	got = joined(def.args("/d", ""))
	if !strings.Contains(got, " --audio-format mp3 ") || strings.Contains(got, "cookies") || strings.Contains(got, "sponsorblock") {
		t.Errorf("default args: %s", got)
	}
}

const threeChapters = `[{"start_time": 0, "end_time": 2, "title": "01. Opening"}, {"start_time": 2, "end_time": 4, "title": "Boss Battle"}, {"start_time": 4, "end_time": 5.9, "title": "Ending"}]`

func splitParams(t *testing.T, home string, split bool) downloadParams {
	t.Helper()
	p := downloadParams{
		req:      DownloadReq{Type: "Audio", Video: downloader.Video{ID: "abc", Title: "Game OST (Full Album)", Channel: "Composer - Topic"}},
		cfg:      config.Config{MusicDir: filepath.Join(home, "Music")},
		home:     home,
		archive:  filepath.Join(home, "archive.txt"),
		progress: make(chan JobProgressMsg, 16),
	}
	p.cfg.SplitChapters = split
	return p
}

func TestSplitChaptersFilesEachTrackAndLooksUpLyricsPerTrack(t *testing.T) {
	fakeYtdlp(t)
	t.Setenv("FAKE_SECS", "6")
	t.Setenv("FAKE_CHAPTERS", threeChapters)
	home := t.TempDir()
	p := splitParams(t, home, true)
	p.fetchLyrics = true

	type ask struct {
		file string
		t    lyrics.Track
	}
	var asked []ask
	old := ensureLyricsFn
	ensureLyricsFn = func(_ lyrics.Options, _ lyrics.Kind, file string, tr lyrics.Track) lyrics.Outcome {
		asked = append(asked, ask{file, tr})
		return lyrics.NotFound
	}
	defer func() { ensureLyricsFn = old }()

	msg := p.run()
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	dir := filepath.Join(home, "Music", "Composer", "Game OST")
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, "|") != "01 - Opening.mp3|02 - Boss Battle.mp3|03 - Ending.mp3" {
		t.Fatalf("files in %s = %v (the original must be gone)", dir, names)
	}
	if _, err := os.Stat(filepath.Join(home, "Music", "Composer", "Unknown Album")); err == nil {
		t.Error("the empty Unknown Album folder was left behind")
	}
	in, err := media.Probe(context.Background(), filepath.Join(dir, "02 - Boss Battle.mp3"))
	if err != nil || in.Tags["title"] != "Boss Battle" || in.Tags["album"] != "Game OST" || in.Tags["artist"] != "Composer" {
		t.Errorf("tags = %v (%v)", in.Tags, err)
	}
	// every piece is its own song for the lyrics lookup
	if len(asked) != 3 {
		t.Fatalf("lyrics asked %d times, want 3", len(asked))
	}
	if a := asked[1]; a.t.Title != "Boss Battle" || a.t.Artist != "Composer" || a.t.Duration != 2 || !strings.HasSuffix(a.file, "02 - Boss Battle.mp3") {
		t.Errorf("second lookup = %+v", a)
	}
	if !msg.LyricsMissing {
		t.Error("no lyrics for any piece should be reported")
	}
}

func TestSplitIsOffByDefaultAndSkippedWithoutChapters(t *testing.T) {
	fakeYtdlp(t)
	t.Setenv("FAKE_SECS", "6")
	// off: one file even though the video has chapters
	t.Setenv("FAKE_CHAPTERS", threeChapters)
	home := t.TempDir()
	if msg := splitParams(t, home, false).run(); msg.Err != nil {
		t.Fatal(msg.Err)
	}
	if _, err := os.Stat(filepath.Join(home, "Music", "Composer", "Unknown Album", "Video Title.mp3")); err != nil {
		t.Errorf("split off, but: %v", err)
	}
	// on, but the video has no chapters: also one file
	t.Setenv("FAKE_CHAPTERS", "NA")
	home = t.TempDir()
	if msg := splitParams(t, home, true).run(); msg.Err != nil {
		t.Fatal(msg.Err)
	}
	if _, err := os.Stat(filepath.Join(home, "Music", "Composer", "Unknown Album", "Video Title.mp3")); err != nil {
		t.Errorf("no chapters, but: %v", err)
	}
}

func TestSplitAsksYtDlpForChaptersOnlyWhenOn(t *testing.T) {
	p := downloadParams{req: DownloadReq{Type: "Audio", Video: downloader.Video{ID: "abcdefghijk"}}}
	if strings.Contains(joined(p.args("/d", "")), "chapters") {
		t.Error("chapters requested while split_chapters is off")
	}
	p.chaptersFile = "/tmp/c.json"
	if !strings.Contains(joined(p.args("/d", "")), " after_move:%(chapters)j /tmp/c.json ") {
		t.Errorf("args: %s", joined(p.args("/d", "")))
	}
}

func TestDownloadUsesTheItemsOwnLink(t *testing.T) {
	p := downloadParams{req: DownloadReq{Type: "Audio", Video: downloader.Video{ID: "1", WebpageURL: "https://example.bandcamp.com/track/x"}}}
	a := p.args("/d", "")
	if a[len(a)-2] != "--" || a[len(a)-1] != "https://example.bandcamp.com/track/x" {
		t.Errorf("tail = %v", a[len(a)-2:])
	}
}

func TestAlbumFromTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Game OST (Full Album)":           "Game OST",
		"Hollow Knight - Full Soundtrack": "Hollow Knight",
		"Some Anime OST [Complete OST]":   "Some Anime OST",
		"Plain Title":                     "Plain Title",
	} {
		if got := albumFromTitle(in); got != want {
			t.Errorf("albumFromTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
