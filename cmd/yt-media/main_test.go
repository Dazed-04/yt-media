package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sandbox points every per-user folder at a temp dir so tests never touch
// the real config.
func sandbox(t *testing.T) string {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	return home
}

func do(args ...string) (code int, out, errw string) {
	var o, e bytes.Buffer
	code = run(args, &o, &e)
	return code, o.String(), e.String()
}

func TestHelpAndUnknown(t *testing.T) {
	sandbox(t)
	if code, out, _ := do("help"); code != 0 || !strings.Contains(out, "yt-media lyrics") {
		t.Errorf("help: %d %q", code, out)
	}
	code, _, errw := do("lyrcis")
	if code != 2 || !strings.Contains(errw, `unknown command "lyrcis"`) {
		t.Errorf("unknown: %d %q", code, errw)
	}
	if code, _, _ := do("lyrics", "--bogus"); code != 2 {
		t.Errorf("bad flag: %d", code)
	}
	if code, _, errw := do("lyrics", "stray"); code != 2 || !strings.Contains(errw, "unexpected argument") {
		t.Errorf("stray arg: %d %q", code, errw)
	}
}

func TestPaths(t *testing.T) {
	home := sandbox(t)
	_, out, _ := do("paths")
	for _, want := range []string{
		filepath.Join(home, "config", "yt-media", "config.json"),
		filepath.Join(home, "cache", "yt-media", "queue.json"),
		filepath.Join(home, "cache", "yt-media", "debug.log"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("paths output lacks %s:\n%s", want, out)
		}
	}
}

func TestLyricsScanEmptyAndMissingDir(t *testing.T) {
	home := sandbox(t)
	music := filepath.Join(home, "Music")
	os.MkdirAll(music, 0o755)
	code, out, _ := do("lyrics", "--dir", music)
	if code != 0 || !strings.Contains(out, "0 files") || !strings.Contains(out, "Scanning "+music) {
		t.Errorf("empty: %d %q", code, out)
	}
	code, _, errw := do("lyrics", "--dir", filepath.Join(home, "nope"))
	if code != 1 || !strings.Contains(errw, "error:") {
		t.Errorf("missing dir: %d %q", code, errw)
	}
}

func TestLyricsFileAlreadyDone(t *testing.T) {
	home := sandbox(t)
	music := filepath.Join(home, "Music")
	os.MkdirAll(filepath.Join(music, "lyrics"), 0o755)
	song := filepath.Join(music, "a.mp3")
	os.WriteFile(song, []byte("x"), 0o644)
	os.WriteFile(filepath.Join(music, "lyrics", "a.lrc"), []byte("[00:01.00] hi"), 0o644)

	code, out, _ := do("lyrics", "--dir", music, "--file", song)
	if code != 0 || !strings.Contains(out, "already have lyrics") {
		t.Errorf("%d %q", code, out)
	}
	// a scan skips it without any network access
	code, out, _ = do("lyrics", "--dir", music)
	if code != 0 || !strings.Contains(out, "1 files: 0 saved") || !strings.Contains(out, "1 already had lyrics") {
		t.Errorf("scan: %d %q", code, out)
	}
}

func TestAlbumFileWithAlbumTag(t *testing.T) {
	home := sandbox(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	song := filepath.Join(home, "a.mp3")
	if b, err := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "lavfi", "-i", "sine=d=1", "-metadata", "album=Known", song).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	code, out, _ := do("album", "--file", song)
	if code != 0 || !strings.Contains(out, `already has an album: "Known"`) {
		t.Errorf("%d %q", code, out)
	}
	code, out, _ = do("album", "--dir", home)
	if code != 0 || !strings.Contains(out, "1 already had an album") {
		t.Errorf("scan: %d %q", code, out)
	}
}

func TestVersionFlagIsInjectable(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go tool")
	}
	bin := filepath.Join(t.TempDir(), "yt-media")
	build := exec.Command("go", "build", "-ldflags", "-X main.version=9.9.9", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("can't build here: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "yt-media 9.9.9" {
		t.Errorf("version output = %q, %v", out, err)
	}
}
