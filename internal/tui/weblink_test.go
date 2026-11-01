package tui

import (
	"strings"
	"testing"

	"yt-downloader/internal/downloader"
)

func TestIsWebLink(t *testing.T) {
	for in, want := range map[string]bool{
		"https://artist.bandcamp.com/album/x": true,
		"  http://example.com/a  ":            true,
		"https://music.youtube.com/watch?v=a": true,
		"hollow knight ost":                   false,
		"https://x.example/a b":               false,
		"ftp://x.example/a":                   false,
		"":                                    false,
	} {
		if got := isWebLink(in); got != want {
			t.Errorf("isWebLink(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestThumbnailForOtherSites(t *testing.T) {
	bc := downloader.Video{ID: "a/b:c", WebpageURL: "https://x.bandcamp.com/track/t", Thumbnail: "https://f4.bcbits.com/img/a1.jpg"}
	if got := thumbURL(bc); got != bc.Thumbnail {
		t.Errorf("thumbURL = %q, want the site's own", got)
	}
	if got := thumbURL(downloader.Video{ID: "a", WebpageURL: "https://x.bandcamp.com/t"}); got != "" {
		t.Errorf("no thumbnail must stay empty, not become a youtube link: %q", got)
	}
	if got := thumbURL(downloader.Video{ID: "dQw4w9WgXcQ"}); !strings.Contains(got, "ytimg.com/vi/dQw4w9WgXcQ") {
		t.Errorf("youtube fallback lost: %q", got)
	}
	p := thumbCachePath("a/b:c")
	if strings.Contains(strings.TrimPrefix(p, thumbCacheDir()), ":") || strings.Count(strings.TrimPrefix(p, thumbCacheDir()), "/") != 1 {
		t.Errorf("cache path escapes its folder: %s", p)
	}
	if thumbCachePath("dQw4w9WgXcQ") != thumbCacheDir()+"/dQw4w9WgXcQ.jpg" {
		t.Error("youtube ids must keep their file name")
	}
}
