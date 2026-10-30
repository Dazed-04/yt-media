package player

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func ytdlpPrinting(t *testing.T, json string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "yt-dlp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat <<'EOF'\n"+json+"\nEOF\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestResolveSplitsVideoAndAudioAndSendsHeaders(t *testing.T) {
	fastWarm(t)
	var ua atomic.Value
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		n.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	bin := ytdlpPrinting(t, `{"title":"T","requested_formats":[
	 {"url":"`+srv.URL+`/a","vcodec":"none","acodec":"opus","http_headers":{"User-Agent":"UA-1"}},
	 {"url":"`+srv.URL+`/v","vcodec":"avc1","acodec":"none","http_headers":{"User-Agent":"UA-1"}}]}`)
	st, err := Resolve(context.Background(), ResolveOptions{YtDlp: bin, URL: "x", Format: "bv+ba"})
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != srv.URL+"/v" || st.AudioURL != srv.URL+"/a" || st.Title != "T" || st.UserAgent != "UA-1" {
		t.Errorf("stream = %+v", st)
	}
	if ua.Load() != "UA-1" || n.Load() != 2 {
		t.Errorf("warm-up: ua=%v requests=%d, want UA-1 and one per stream", ua.Load(), n.Load())
	}
	args := strings.Join(st.MpvArgs(), "\n")
	if !strings.Contains(args, "--audio-file="+srv.URL+"/a") || !strings.HasSuffix(args, "--\n"+srv.URL+"/v") {
		t.Errorf("mpv args = %q", args)
	}
}

func TestResolveErrors(t *testing.T) {
	fastWarm(t)
	if _, err := Resolve(context.Background(), ResolveOptions{YtDlp: "no-such-yt-dlp"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing yt-dlp: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "yt-dlp")
	os.WriteFile(bin, []byte("#!/bin/sh\necho 'ERROR: [youtube] x: Video unavailable' >&2\nexit 1\n"), 0o755)
	if _, err := Resolve(context.Background(), ResolveOptions{YtDlp: bin, URL: "x"}); err == nil || !strings.Contains(err.Error(), "Video unavailable") {
		t.Errorf("yt-dlp failure: %v", err)
	}
	if _, err := Resolve(context.Background(), ResolveOptions{YtDlp: ytdlpPrinting(t, "not json"), URL: "x"}); err == nil {
		t.Error("unreadable output accepted")
	}
	if _, err := Resolve(context.Background(), ResolveOptions{YtDlp: ytdlpPrinting(t, `{"title":"T"}`), URL: "x"}); err == nil || !strings.Contains(err.Error(), "no URL") {
		t.Errorf("no url: %v", err)
	}
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer refuse.Close()
	_, err := Resolve(context.Background(), ResolveOptions{YtDlp: ytdlpPrinting(t, `{"url":"`+refuse.URL+`"}`), URL: "x"})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("a stream that stays refused: %v", err)
	}
}

func TestResolveStopsWaitingWhenCancelled(t *testing.T) {
	warmEvery, warmFor = 10*1000*1000, 30*1000*1000*1000 // 10ms, 30s
	defer func() { warmEvery, warmFor = 1000*1000*1000, 45*1000*1000*1000 }()
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer refuse.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Resolve(ctx, ResolveOptions{YtDlp: ytdlpPrinting(t, `{"url":"`+refuse.URL+`"}`), URL: "x"})
		done <- err
	}()
	cancel()
	if err := <-done; err == nil {
		t.Error("expected an error after cancelling")
	}
}
