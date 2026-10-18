package downloader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlaylistID(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.youtube.com/playlist?list=PLabc_123-x":              "PLabc_123-x",
		"https://music.youtube.com/playlist?list=OLAK5uy_abc&si=zzz":     "OLAK5uy_abc",
		"https://www.youtube.com/playlist?si=zzz&list=PLabc":             "PLabc",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=RDdQw4w9WgXcQ": "", // a video, not a playlist
		"just some words": "",
	} {
		got, ok := PlaylistID(in)
		if got != want || ok != (want != "") {
			t.Errorf("PlaylistID(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestFetchPlaylistWithFakeYtdlp(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + argsFile + `"
cat <<'EOF'
{"id":"aaaaaaaaaaa","title":"gloria","channel":"Kendrick Lamar - Topic","duration":235.0,"playlist_title":"Album - GNX","thumbnails":[{"url":"https://i/small.jpg"},{"url":"https://i/big.jpg"}]}
{"id":"bbbbbbbbbbb","title":"Some Video","uploader":"Some Channel","duration":3725}
not json
{"title":"entry without an id"}
EOF
`
	os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte(script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	vids, err := FetchPlaylist("PLx", 50, []string{"--cookies-from-browser", "firefox"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vids) != 2 {
		t.Fatalf("got %d videos: %+v", len(vids), vids)
	}
	a, b := vids[0], vids[1]
	if a.Artist != "Kendrick Lamar" || a.Album != "GNX" || a.Duration != "3:55" || a.Thumbnail != "https://i/big.jpg" {
		t.Errorf("first = %+v", a)
	}
	if b.Artist != "Some Channel" || b.Album != "" || b.Duration != "1:02:05" {
		t.Errorf("second = %+v", b)
	}
	args, _ := os.ReadFile(argsFile)
	got := strings.Fields(string(args))
	want := "--dump-json --flat-playlist --skip-download --playlist-end 50 --cookies-from-browser firefox -- https://www.youtube.com/playlist?list=PLx"
	if strings.Join(got, " ") != want {
		t.Errorf("yt-dlp args = %q", strings.Join(got, " "))
	}
}

func TestFetchPlaylistEmptyAndFailing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := FetchPlaylist("PLx", 5, nil); err == nil {
		t.Error("an empty playlist should be an error")
	}
	os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte("#!/bin/sh\necho 'ERROR: Playlist does not exist' >&2\nexit 1\n"), 0o755)
	if _, err := FetchPlaylist("PLx", 5, nil); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("err = %v", err)
	}
}
