package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseChaptersAndTitles(t *testing.T) {
	got := ParseChapters(`[{"start_time": 0, "end_time": 100.5, "title": "01. Opening"}, {"start_time": 100.5, "end_time": 100.9, "title": "blip"}, {"start_time": 100.9, "end_time": 300, "title": "Boss Battle"}]`)
	if len(got) != 2 || got[1].Title != "Boss Battle" {
		t.Errorf("chapters = %+v (sections under a second must go)", got)
	}
	for _, in := range []string{"", "NA", "null", "{bad"} {
		if ParseChapters(in) != nil {
			t.Errorf("%q should mean no chapters", in)
		}
	}
	for in, want := range map[string]string{
		"01. Opening":       "Opening",
		"3 - Boss Battle":   "Boss Battle",
		"(07) Ending":       "Ending",
		"12:30 Final Boss":  "Final Boss",
		"1:02:03 - Credits": "Credits",
		"2 Fast 2 Furious":  "2 Fast 2 Furious",
		"1999":              "1999",
		"  Plain title  ":   "Plain title",
		"":                  "",
	} {
		if got := ChapterTitle(in); got != want {
			t.Errorf("ChapterTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func probeTag(t *testing.T, path, key string) string {
	t.Helper()
	in, err := Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return in.Tags[key]
}

func TestSplitChaptersForEveryFormat(t *testing.T) {
	for _, ext := range []string{".mp3", ".flac", ".m4a", ".opus"} {
		t.Run(ext, func(t *testing.T) {
			long, cover := makeWithCoverFor(t, ext, 6)
			chs := []Chapter{{0, 2, "01. Intro"}, {2, 4, "Fight: Round 1?"}, {4, 5.9, "Outro"}}
			out := filepath.Join(t.TempDir(), "Album")
			files, err := SplitChapters(context.Background(), long, out, chs, SplitMeta{Artist: "Composer", Album: "Game OST"})
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 3 {
				t.Fatalf("files = %v", files)
			}
			wantNames := []string{"01 - Intro" + ext, "02 - Fight Round 1" + ext, "03 - Outro" + ext}
			for i, f := range files {
				if filepath.Base(f) != wantNames[i] {
					t.Errorf("name %d = %q, want %q", i, filepath.Base(f), wantNames[i])
				}
			}
			if probeTag(t, files[1], "title") != "Fight: Round 1?" && probeTag(t, files[1], "title") != "Fight: Round 1?" {
				t.Errorf("title tag = %q", probeTag(t, files[1], "title"))
			}
			if got := probeTag(t, files[1], "artist"); got != "Composer" {
				t.Errorf("artist = %q", got)
			}
			if got := probeTag(t, files[1], "album"); got != "Game OST" {
				t.Errorf("album = %q", got)
			}
			if got := probeTag(t, files[1], "track"); !strings.HasPrefix(got, "2") {
				t.Errorf("track = %q", got)
			}
			if got := probeTag(t, files[0], "title"); got != "Intro" {
				t.Errorf("title = %q, want the number stripped", got)
			}
			// the parent's own tags must not leak into the pieces
			if got := probeTag(t, files[0], "comment"); got != "" {
				t.Errorf("comment leaked: %q", got)
			}
			in, _ := Probe(context.Background(), files[0])
			if in.Duration < 1.7 || in.Duration > 2.4 {
				t.Errorf("first piece is %.2fs long, want about 2", in.Duration)
			}
			if ext == ".opus" {
				if extractCover(context.Background(), files[0]) != pictureBlock(cover) {
					t.Error("opus cover lost or changed")
				}
			} else if coverStreams(t, files[0]) != 1 {
				t.Errorf("cover streams = %d", coverStreams(t, files[0]))
			}
		})
	}
}

func TestSplitChaptersFailureLeavesNothingBehind(t *testing.T) {
	needFFmpeg(t)
	src, _ := makeWithCover(t, ".mp3")
	out := filepath.Join(t.TempDir(), "Album")
	// a missing source makes the very first cut fail
	if _, err := SplitChapters(context.Background(), src+".missing", out, []Chapter{{0, 1, "a"}, {1, 2, "b"}}, SplitMeta{}); err == nil {
		t.Fatal("expected an error")
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
	if _, err := SplitChapters(context.Background(), src, out, []Chapter{{0, 1, "only one"}}, SplitMeta{}); err == nil {
		t.Error("a single chapter must not be split")
	}
}
