package media

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	for _, b := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skip(b + " not installed")
		}
	}
}

func ff(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("ffmpeg", append([]string{"-y", "-v", "error"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v: %s", args, err, out)
	}
}

// coverStreams counts the attached-picture streams ffprobe sees.
func coverStreams(t *testing.T, path string) int {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v", "-show_entries", "stream=codec_name", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(out)))
}

// makeWithCover builds a one-second file of the given type with a cover and
// tags, the way yt-dlp leaves downloads.
func makeWithCover(t *testing.T, ext string) (path string, cover []byte) {
	t.Helper()
	return makeWithCoverFor(t, ext, 1)
}

// makeWithCoverFor is makeWithCover with a chosen length in seconds.
func makeWithCoverFor(t *testing.T, ext string, secs int) (path string, cover []byte) {
	t.Helper()
	sine := "sine=d=" + strconv.Itoa(secs)
	needFFmpeg(t)
	dir := t.TempDir()
	coverPath := filepath.Join(dir, "cover.jpg")
	ff(t, "-f", "lavfi", "-i", "color=c=red:s=96x64", "-frames:v", "1", coverPath)
	cover, _ = os.ReadFile(coverPath)
	path = filepath.Join(dir, "song"+ext)

	switch ext {
	case ".opus":
		// ffmpeg cannot write a cover into Ogg, so give it as a comment (what yt-dlp does)
		meta := filepath.Join(dir, "m.txt")
		os.WriteFile(meta, []byte(ffmetadata(map[string]string{
			"artist": "Ace Hood", "title": "We Ball", "metadata_block_picture": pictureBlock(cover),
		})), 0o644)
		ff(t, "-f", "lavfi", "-i", sine, "-f", "ffmetadata", "-i", meta, "-map", "0:a", "-map_metadata", "1", "-c:a", "libopus", path)
	default:
		codec := map[string]string{".flac": "flac", ".m4a": "aac", ".mp3": "libmp3lame"}[ext]
		ff(t, "-f", "lavfi", "-i", sine, "-i", coverPath, "-map", "0", "-map", "1", "-c:a", codec, "-c:v", "mjpeg",
			"-disposition:v", "attached_pic", "-metadata", "artist=Ace Hood", "-metadata", "title=We Ball", path)
	}
	if coverStreams(t, path) != 1 {
		t.Fatalf("test file %s has no cover to start with", ext)
	}
	return path, cover
}

func TestWriteTagsWorksForEveryAudioFormat(t *testing.T) {
	for _, ext := range []string{".mp3", ".flac", ".m4a", ".opus"} {
		t.Run(ext, func(t *testing.T) {
			path, cover := makeWithCover(t, ext)

			if err := WriteTags(context.Background(), path, map[string]string{"album": "Body Bag 3"}); err != nil {
				t.Fatal(err)
			}
			in, err := Probe(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if in.Tags["album"] != "Body Bag 3" {
				t.Errorf("album = %q", in.Tags["album"])
			}
			if in.Tags["artist"] != "Ace Hood" || in.Tags["title"] != "We Ball" {
				t.Errorf("other tags lost: %v", in.Tags)
			}
			if coverStreams(t, path) != 1 {
				t.Error("cover art was lost")
			}
			if ext == ".opus" { // the very same picture must come back out
				if got := extractCover(context.Background(), path); got != pictureBlock(cover) {
					t.Error("cover changed")
				}
			}
			if in.Duration < 0.8 {
				t.Errorf("audio damaged: %v s", in.Duration)
			}
		})
	}
}

func TestOpusWithoutCoverStillTags(t *testing.T) {
	needFFmpeg(t)
	path := filepath.Join(t.TempDir(), "n.opus")
	ff(t, "-f", "lavfi", "-i", "sine=d=1", "-c:a", "libopus", "-metadata", "artist=A", path)
	if err := WriteTags(context.Background(), path, map[string]string{"album": "B"}); err != nil {
		t.Fatal(err)
	}
	in, _ := Probe(context.Background(), path)
	if in.Tags["album"] != "B" || in.Tags["artist"] != "A" || coverStreams(t, path) != 0 {
		t.Errorf("tags=%v covers=%d", in.Tags, coverStreams(t, path))
	}
}

func TestFFMetadataEscapesAndPictureBlock(t *testing.T) {
	got := ffmetadata(map[string]string{"title": "a=b;c#d\\e", "x": "line1\nline2"})
	if !strings.HasPrefix(got, ";FFMETADATA1\n") || !strings.Contains(got, `title=a\=b\;c\#d\\e`) || !strings.Contains(got, "x=line1\\\nline2") {
		t.Errorf("escaping wrong:\n%s", got)
	}
	if pictureBlock([]byte("not an image at all")) != "" {
		t.Error("unknown image type accepted")
	}
	if !bytes.Contains([]byte(pictureBlock([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})), []byte("A")) {
		t.Error("empty block")
	}
}
