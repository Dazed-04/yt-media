package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"yt-downloader/internal/config"
	"yt-downloader/internal/downloader"
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
    --print-to-file) pathfile="$3"; shift 2 ;;
  esac
  shift
done
file=$(printf '%s' "$out" | sed 's/%(title)s/Video Title/; s/%(ext)s/mp3/; s/%%/%/g')
mkdir -p "$(dirname "$file")"
ffmpeg -v quiet -y -f lavfi -i sine=d=1 "$file" || exit 1
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
