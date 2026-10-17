package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// MakeMP3 writes a one-second mp3 with a cover picture and optional tags.
func makeMP3(t *testing.T, path string, tags map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	cover := filepath.Join(filepath.Dir(path), "cover.jpg")
	if err := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=64x64", "-frames:v", "1", cover).Run(); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"-y", "-v", "error", "-f", "lavfi", "-i", "sine=d=1", "-i", cover, "-map", "0", "-map", "1",
		"-c:a", "libmp3lame", "-c:v", "mjpeg", "-id3v2_version", "3", "-disposition:v", "attached_pic",
	}
	for k, v := range tags {
		args = append(args, "-metadata", k+"="+v)
	}
	args = append(args, path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestWriteTagsKeepsCoverAndOtherTags(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.mp3")
	makeMP3(t, f, map[string]string{"artist": "Ace Hood", "title": "We Ball"})

	if err := WriteTags(context.Background(), f, map[string]string{"album": "Body Bag 3"}); err != nil {
		t.Fatal(err)
	}
	in, err := Probe(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if in.Tags["album"] != "Body Bag 3" || in.Tags["artist"] != "Ace Hood" || in.Tags["title"] != "We Ball" {
		t.Errorf("tags = %v", in.Tags)
	}
	out, _ := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", f).Output()
	if string(out) != "audio\nvideo\n" {
		t.Errorf("streams after tagging = %q (cover art lost?)", out)
	}
	if in.Duration < 0.5 {
		t.Errorf("duration = %v", in.Duration)
	}
	// no temp files left behind
	entries, _ := os.ReadDir(filepath.Dir(f))
	for _, e := range entries {
		if e.Name() != "a.mp3" && e.Name() != "cover.jpg" {
			t.Errorf("leftover file %s", e.Name())
		}
	}
}

func TestWriteTagsFailureLeavesFileAlone(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bad.mp3")
	if err := os.WriteFile(f, []byte("not audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteTags(context.Background(), f, map[string]string{"album": "X"}); err == nil {
		t.Fatal("expected an error")
	}
	if b, _ := os.ReadFile(f); string(b) != "not audio" {
		t.Error("file was modified")
	}
}

func TestTrackFromFilename(t *testing.T) {
	in := Info{Tags: map[string]string{}, Duration: 10}
	tr := in.Track("/m/The Weeknd - Save Your Tears (Official Video).mp3")
	if tr.Artist != "The Weeknd" || tr.Title != "Save Your Tears" || tr.Duration != 10 {
		t.Errorf("track = %+v", tr)
	}
}
