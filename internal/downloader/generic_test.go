package downloader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVideoURL(t *testing.T) {
	yt := Video{ID: "dQw4w9WgXcQ"}
	if yt.URL() != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" || !yt.IsYouTube() {
		t.Errorf("youtube: %s", yt.URL())
	}
	bc := Video{ID: "42", WebpageURL: "https://artist.bandcamp.com/track/song"}
	if bc.URL() != "https://artist.bandcamp.com/track/song" || bc.IsYouTube() {
		t.Errorf("bandcamp: %s", bc.URL())
	}
	if (Video{ID: "x", WebpageURL: "javascript:alert(1)"}).URL() != "https://www.youtube.com/watch?v=x" {
		t.Error("a non-http page url must not be used")
	}
	ytWeb := Video{ID: "a", WebpageURL: "https://www.youtube.com/watch?v=a"}
	if !ytWeb.IsYouTube() {
		t.Error("youtube page url not recognised")
	}
}

func TestParseGeneric(t *testing.T) {
	single := `{"id":"1234","title":"Song","uploader":"Some Artist","duration_string":"3:05","thumbnail":"https://i/x.jpg","webpage_url":"https://soundcloud.com/a/song"}`
	vs := parseGeneric(single, "https://soundcloud.com/a/song")
	if len(vs) != 1 || vs[0].URL() != "https://soundcloud.com/a/song" || vs[0].Artist != "Some Artist" || vs[0].Duration != "3:05" {
		t.Errorf("single = %+v", vs)
	}

	album := `{"id":"1","title":"Track One","url":"https://a.bandcamp.com/track/one","duration":185,"uploader":"The Band - Topic","thumbnails":[{"url":"https://i/small.jpg"},{"url":"https://i/big.jpg"}]}
{"title":"No Id","url":"https://a.bandcamp.com/track/two"}

{"id":"3","title":"","url":"not-a-link"}
not json`
	vs = parseGeneric(album, "https://a.bandcamp.com/album/x")
	if len(vs) != 3 {
		t.Fatalf("entries = %+v", vs)
	}
	if vs[0].URL() != "https://a.bandcamp.com/track/one" || vs[0].Duration != "3:05" || vs[0].Thumbnail != "https://i/big.jpg" || vs[0].Artist != "The Band" {
		t.Errorf("first = %+v", vs[0])
	}
	if vs[1].ID == "" || vs[1].ID == vs[2].ID {
		t.Errorf("ids must be filled in and differ: %q %q", vs[1].ID, vs[2].ID)
	}
	if vs[2].URL() != "https://a.bandcamp.com/album/x" || vs[2].Title == "" {
		t.Errorf("an entry without a usable link falls back to the pasted one: %+v", vs[2])
	}
}

func TestFetchURLAsksYtDlpSafely(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + log + "\necho '{\"id\":\"1\",\"title\":\"T\",\"webpage_url\":\"https://x.example/1\"}'\n"
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	vs, err := FetchURL("https://x.example/1", 50, []string{"--cookies-from-browser", "firefox"})
	if err != nil || len(vs) != 1 {
		t.Fatalf("%v %v", vs, err)
	}
	b, _ := os.ReadFile(log)
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	if n := len(args); args[n-2] != "--" || args[n-1] != "https://x.example/1" {
		t.Errorf("the link must come after --: %v", args)
	}
	if !strings.Contains(string(b), "--playlist-end\n50") || !strings.Contains(string(b), "--cookies-from-browser\nfirefox") {
		t.Errorf("args: %v", args)
	}
}

func TestFetchURLReportsAnUnsupportedLink(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte("#!/bin/sh\necho 'ERROR: Unsupported URL: https://x.example' >&2\nexit 1\n"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := FetchURL("https://x.example", 10, nil); err == nil || !strings.Contains(err.Error(), "Unsupported URL") {
		t.Errorf("err = %v", err)
	}
}
