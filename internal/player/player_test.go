package player

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain doubles as a fake mpv: when FAKE_MPV is set, this binary is started
// by Start() in mpv's place and speaks enough of its IPC protocol.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_MPV"); mode != "" {
		fakeMPV(mode)
		return
	}
	os.Exit(m.Run())
}

func fakeMPV(mode string) {
	if mode == "crash" {
		os.Exit(2)
	}
	if mode == "stay" { // a window that opens fine and is closed by the user a bit later
		time.Sleep(600 * time.Millisecond)
		os.Exit(0)
	}
	if mode == "explain" {
		fmt.Fprintln(os.Stderr, "[ytdl_hook] ERROR: Sign in to confirm you're not a bot")
		fmt.Fprintln(os.Stderr, "[ytdl_hook] youtube-dl failed: unexpected error occurred")
		fmt.Fprintln(os.Stderr, "Failed to recognize file format.")
		os.Exit(2)
	}
	var sock string
	for _, a := range os.Args[1:] {
		if v, ok := strings.CutPrefix(a, "--input-ipc-server="); ok {
			sock = v
		}
	}
	if mode == "no-stream" { // streaming never works here: any http(s) URL is refused
		for _, a := range os.Args[1:] {
			if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") {
				fmt.Fprintln(os.Stderr, "[ffmpeg] https: HTTP error 403 Forbidden")
				os.Exit(2)
			}
		}
	}
	if mode == "log-args" {
		os.WriteFile(filepath.Join(filepath.Dir(sock), "args"), []byte(strings.Join(os.Args[1:], "\n")), 0o644)
	}
	if mode == "slow" {
		time.Sleep(300 * time.Millisecond) // the socket appears late, as when mpv is resolving a link
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		os.Exit(3)
	}
	conn, err := ln.Accept()
	if err != nil {
		os.Exit(3)
	}
	var mu sync.Mutex
	pos, paused, vol := 0.0, false, 70.0
	emit := func(name string, data any) {
		b, _ := json.Marshal(map[string]any{"event": "property-change", "name": name, "data": data})
		mu.Lock()
		conn.Write(append(b, '\n'))
		mu.Unlock()
	}
	go func() {
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			var c struct {
				Command []any `json:"command"`
			}
			if json.Unmarshal(sc.Bytes(), &c) != nil || len(c.Command) == 0 {
				continue
			}
			switch c.Command[0] {
			case "observe_property":
				emit("duration", 200.0)
				emit("media-title", "Fake Song")
				emit("volume", vol)
				emit("pause", false)
			case "cycle":
				paused = !paused
				emit("pause", paused)
			case "seek":
				pos += c.Command[1].(float64)
				emit("time-pos", pos)
			case "add":
				vol += c.Command[2].(float64)
				emit("volume", vol)
			}
		}
		os.Exit(0)
	}()
	for {
		time.Sleep(30 * time.Millisecond)
		if !paused {
			pos += 0.03
			emit("time-pos", pos)
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// fakeYtDlp writes a stand-in yt-dlp that logs its arguments next to itself
// and, depending on mode, writes media.webm where "-o" says, fails with the
// message yt-dlp would give, or hangs.
func fakeYtDlp(t *testing.T, mode string) (bin, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	bin, argsFile = filepath.Join(dir, "yt-dlp"), filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\n"
	switch mode {
	case "ok":
		// FAKE_REJECT_CLIENT=1 makes it refuse any run that names a client;
		// with -j it describes a stream at $FAKE_STREAM_URL instead of downloading.
		script += `case "$*" in *player_client*) [ -n "$FAKE_REJECT_CLIENT" ] && { echo 'ERROR: client refused' >&2; exit 1; };; esac
case " $* " in *" -j "*) printf '{"title":"Fake Song","url":"%s","http_headers":{"User-Agent":"fake-ua"}}\n' "$FAKE_STREAM_URL"; exit 0;; esac
while [ $# -gt 0 ]; do if [ "$1" = "-o" ]; then out="$2"; fi; shift; done
f=$(printf '%s' "$out" | sed 's/%(ext)s/webm/')
printf 'AUDIO' > "$f"
printf 'junk' > "$f.part"
`
	case "fail":
		script += "echo 'ERROR: [youtube] abc: Sign in to confirm you are not a bot' >&2\nexit 1\n"
	case "empty":
		script += "exit 0\n"
	case "hang":
		script += "sleep 30\n"
	}
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

func start(t *testing.T, mpvMode, ytMode string, o Options) *Session {
	t.Helper()
	t.Setenv("FAKE_MPV", mpvMode)
	o.Binary = os.Args[0]
	o.YtDlp, _ = fakeYtDlp(t, ytMode)
	if o.URL == "" {
		o.URL = "https://www.youtube.com/watch?v=aaaaaaaaaaa"
	}
	s, err := Start(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestPlaybackStateAndControls(t *testing.T) {
	s := start(t, "ok", "ok", Options{Volume: 70})

	if st := s.State(); !st.Preparing {
		t.Errorf("not preparing right after Start: %+v", st)
	}
	waitFor(t, "title and length", func() bool { st := s.State(); return st.Title == "Fake Song" && st.Dur == 200 })
	if s.State().Preparing {
		t.Error("still preparing once playing")
	}
	waitFor(t, "position to advance", func() bool { return s.State().Pos > 0.05 })

	s.TogglePause()
	waitFor(t, "pause", func() bool { return s.State().Paused })
	s.TogglePause()
	waitFor(t, "resume", func() bool { return !s.State().Paused })

	before := s.State().Pos
	s.Seek(30)
	waitFor(t, "seek", func() bool { return s.State().Pos >= before+29 })

	s.AddVolume(10)
	waitFor(t, "volume", func() bool { return s.State().Volume == 80 })
}

func TestSlowSocketStillConnects(t *testing.T) {
	s := start(t, "slow", "ok", Options{})
	waitFor(t, "state from a late socket", func() bool { return s.State().Title == "Fake Song" })
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	var b []byte
	waitFor(t, path, func() bool {
		var err error
		b, err = os.ReadFile(path)
		return err == nil && len(b) > 0
	})
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func TestYtDlpIsGivenTheLinkSafelyAndTheCookieArgs(t *testing.T) {
	t.Setenv("FAKE_MPV", "ok")
	bin, argsFile := fakeYtDlp(t, "ok")
	s, err := Start(Options{Binary: os.Args[0], YtDlp: bin, URL: "https://example/x", Format: "bestaudio/best",
		ExtraArgs: []string{"--cookies-from-browser", "firefox:/home/u/.zen/ab.Default (release)"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	args := readLines(t, argsFile)
	joined := "\n" + strings.Join(args, "\n") + "\n"
	for _, want := range []string{"\n-f\nbestaudio/best\n", "\n--no-playlist\n", "\n--cookies-from-browser\nfirefox:/home/u/.zen/ab.Default (release)\n"} {
		if !strings.Contains(joined, want) {
			t.Errorf("yt-dlp args lack %q:\n%s", want, joined)
		}
	}
	// the link is last, after "--", so it can never be taken as an option
	if n := len(args); args[n-2] != "--" || args[n-1] != "https://example/x" {
		t.Errorf("tail of args = %q", args[n-2:])
	}
	// nothing is added to a download archive
	if strings.Contains(joined, "download-archive") {
		t.Error("preview touches the archive")
	}
}

func TestMpvPlaysTheDownloadedFileNotTheLink(t *testing.T) {
	s := start(t, "log-args", "ok", Options{Format: "bestaudio/best", Volume: 50})
	args := readLines(t, filepath.Join(s.dir, "args"))
	joined := "\n" + strings.Join(args, "\n") + "\n"
	for _, want := range []string{"\n--no-video\n", "\n--no-input-terminal\n", "\n--volume=50\n", "\n--ytdl=no\n"} {
		if !strings.Contains(joined, want) {
			t.Errorf("mpv args lack %q:\n%s", want, joined)
		}
	}
	n := len(args)
	if args[n-2] != "--" || !strings.HasSuffix(args[n-1], "media.webm") {
		t.Errorf("tail of mpv args = %q, want -- <downloaded file>", args[n-2:])
	}
	if strings.Contains(joined, "youtube.com") || strings.Contains(joined, "--ytdl-format") {
		t.Errorf("mpv was given the link:\n%s", joined)
	}
}

func TestFetchFailureShowsYtDlpsOwnMessage(t *testing.T) {
	s := start(t, "ok", "fail", Options{})
	waitFor(t, "failure", func() bool { st := s.State(); return st.Ended && st.Failed != "" })
	st := s.State()
	if !strings.Contains(st.Failed, "Sign in to confirm") || strings.Contains(st.Failed, "ERROR:") {
		t.Errorf("Failed = %q", st.Failed)
	}
	if st.Preparing {
		t.Error("still preparing after failing")
	}
}

func TestFetchThatProducesNothingIsAnError(t *testing.T) {
	s := start(t, "ok", "empty", Options{})
	waitFor(t, "failure", func() bool { return s.State().Failed != "" })
}

func TestCrashIsReported(t *testing.T) {
	s := start(t, "crash", "ok", Options{})
	waitFor(t, "failure", func() bool { st := s.State(); return st.Ended && st.Failed != "" })
}

func playing(s *Session) bool { st := s.State(); return st.Title == "Fake Song" && !st.Ended }

// streamServer is a stand-in for googlevideo: it refuses the first `refuse`
// requests, like a URL that is not valid yet, then serves.
func streamServer(t *testing.T, refuse int32) (url string, hits *atomic.Int32) {
	t.Helper()
	hits = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= refuse {
			http.Error(w, "not yet", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusPartialContent)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/videoplayback", hits
}

func fastWarm(t *testing.T) {
	t.Helper()
	oe, of := warmEvery, warmFor
	warmEvery, warmFor = 10*time.Millisecond, 400*time.Millisecond
	t.Cleanup(func() { warmEvery, warmFor = oe, of })
}

func TestStreamModeWaitsForTheUrlThenPlaysItWithoutYtDlpHook(t *testing.T) {
	fastWarm(t)
	url, hits := streamServer(t, 3)
	t.Setenv("FAKE_STREAM_URL", url)
	s := start(t, "log-args", "ok", Options{Mode: "stream", Format: "bestaudio/best"})
	args := readLines(t, filepath.Join(s.dir, "args"))
	if hits.Load() < 4 {
		t.Errorf("mpv was started after %d requests: it must wait until the URL answers", hits.Load())
	}
	joined := "\n" + strings.Join(args, "\n") + "\n"
	for _, want := range []string{"\n--ytdl=no\n", "\n--force-media-title=Fake Song\n", "\n--user-agent=fake-ua\n"} {
		if !strings.Contains(joined, want) {
			t.Errorf("mpv args lack %q:\n%s", want, joined)
		}
	}
	if n := len(args); args[n-2] != "--" || args[n-1] != url {
		t.Errorf("tail = %q, want -- <resolved url>", args[n-2:])
	}
	waitFor(t, "playing", func() bool { return playing(s) })
}

func TestPreferredClientFallsBackToDefault(t *testing.T) {
	fastWarm(t)
	url, _ := streamServer(t, 0)
	t.Setenv("FAKE_STREAM_URL", url)
	t.Setenv("FAKE_REJECT_CLIENT", "1")
	s := start(t, "ok", "ok", Options{Mode: "stream", Client: "web_safari"})
	waitFor(t, "playback after falling back", func() bool { return playing(s) })
	if f := s.State().Failed; f != "" {
		t.Errorf("Failed = %q", f)
	}
}

func TestStreamThatStaysRefusedIsReported(t *testing.T) {
	fastWarm(t)
	url, _ := streamServer(t, 1<<30)
	t.Setenv("FAKE_STREAM_URL", url)
	s := start(t, "ok", "ok", Options{Mode: "stream"})
	waitFor(t, "failure", func() bool { st := s.State(); return st.Ended && st.Failed != "" })
	if f := s.State().Failed; !strings.Contains(f, "403") {
		t.Errorf("Failed = %q, want the 403", f)
	}
}

func TestAutoModeFallsBackFromStreamToDownload(t *testing.T) {
	fastWarm(t)
	url, _ := streamServer(t, 1<<30) // streaming never becomes possible
	t.Setenv("FAKE_STREAM_URL", url)
	s := start(t, "ok", "ok", Options{Mode: "auto", Client: "web_safari"})
	waitFor(t, "playback from the downloaded file", func() bool { return playing(s) })
	if f := s.State().Failed; f != "" {
		t.Errorf("Failed = %q", f)
	}
}

func TestMpvRefusingTheStreamFallsBackInAutoMode(t *testing.T) {
	fastWarm(t)
	url, _ := streamServer(t, 0)
	t.Setenv("FAKE_STREAM_URL", url)
	s := start(t, "no-stream", "ok", Options{Mode: "auto"})
	waitFor(t, "playback from the downloaded file", func() bool { return playing(s) })
}

func TestPlanOrder(t *testing.T) {
	str := func(p []step) string {
		var b []string
		for _, x := range p {
			k := "dl"
			if x.stream {
				k = "stream"
			}
			b = append(b, k+":"+x.client)
		}
		return strings.Join(b, " ")
	}
	for _, c := range []struct{ mode, client, want string }{
		{"", "", "dl:"},
		{"download", "tv", "dl:tv dl:"},
		{"stream", "", "stream:"},
		{"STREAM", "tv", "stream:tv stream:"},
		{"auto", "tv", "stream:tv stream: dl:tv dl:"},
		{"auto", "", "stream: dl:"},
		{"nonsense", "", "dl:"},
	} {
		if got := str(plan(c.mode, c.client)); got != c.want {
			t.Errorf("plan(%q,%q) = %q, want %q", c.mode, c.client, got, c.want)
		}
	}
}

func TestMpvsOwnExplanationIsShown(t *testing.T) {
	s := start(t, "explain", "ok", Options{})
	waitFor(t, "failure", func() bool { st := s.State(); return st.Ended && st.Failed != "" })
	if got := s.State().Failed; !strings.Contains(got, "Sign in to confirm") || strings.Contains(got, "ytdl_hook") {
		t.Errorf("Failed = %q, want the specific message without its prefix", got)
	}
}

func TestMissingPrograms(t *testing.T) {
	if _, err := Start(Options{Binary: "definitely-not-a-real-player", URL: "x"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing mpv: err = %v", err)
	}
	if _, err := Start(Options{Binary: os.Args[0], YtDlp: "definitely-not-a-real-ytdlp", URL: "x"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing yt-dlp: err = %v", err)
	}
}

func TestCloseStopsMpvAndRemovesEverything(t *testing.T) {
	s := start(t, "ok", "ok", Options{})
	waitFor(t, "connection", func() bool { return s.State().Title != "" })
	dir, media := s.dir, s.mediaDir
	if media == "" {
		t.Fatal("no media folder")
	}
	s.Close()
	s.Close() // twice is fine
	waitFor(t, "process exit", func() bool { return s.State().Ended })
	for _, d := range []string{dir, media} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s still there: %v", d, err)
		}
	}
	s.Seek(5) // controlling a closed session must not panic
}

func TestClosingWhileFetchingStopsYtDlp(t *testing.T) {
	s := start(t, "ok", "hang", Options{})
	began := time.Now()
	s.Close()
	if time.Since(began) > 2*time.Second {
		t.Error("Close waited for the fetch")
	}
	waitFor(t, "ended", func() bool { return s.State().Ended })
	if s.State().Failed != "" {
		t.Errorf("closing is not a failure: %q", s.State().Failed)
	}
}

func TestDownloadReturnsTheMediaFileAndIgnoresLeftovers(t *testing.T) {
	bin, _ := fakeYtDlp(t, "ok")
	dir, file, err := Download(context.Background(), FetchOptions{YtDlp: bin, URL: "x", Format: "bestvideo+bestaudio/best"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if filepath.Base(file) != "media.webm" {
		t.Errorf("file = %s (a .part leftover must be ignored)", file)
	}
}

func TestLaunchWindowReportsAnEarlyFailure(t *testing.T) {
	t.Setenv("FAKE_MPV", "explain")
	done := make(chan struct{})
	err := LaunchWindow(os.Args[0], []string{"--", "x"}, 3*time.Second, func() { close(done) })
	if err == nil || !strings.Contains(err.Error(), "Sign in to confirm") {
		t.Errorf("err = %v, want mpv's own message", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("cleanup callback not called after a failed launch")
	}
}

func TestLaunchWindowLeavesAHealthyWindowAlone(t *testing.T) {
	t.Setenv("FAKE_MPV", "stay")
	done := make(chan struct{})
	if err := LaunchWindow(os.Args[0], []string{"--", "x"}, 150*time.Millisecond, func() { close(done) }); err != nil {
		t.Fatal(err)
	}
	select { // the callback runs when the window is eventually closed
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("cleanup callback not called when the window closed")
	}
}

func TestLaunchWindowMissingPlayer(t *testing.T) {
	if err := LaunchWindow("definitely-not-a-real-player", nil, time.Second, nil); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v", err)
	}
}

func TestTailPrefersTheCauseOverMpvsClosingLines(t *testing.T) {
	tl := &tail{}
	tl.Write([]byte("[ffmpeg] https: HTTP error 403 Forbidden\n" +
		"Failed to open https://rr4---sn.googlevideo.com/videoplayback?expire=1&sig=abc.\n" +
		"No video or audio streams selected.\n" +
		"Exiting... (Errors when loading file)\n"))
	got := tl.last()
	if !strings.Contains(got, "HTTP error 403") {
		t.Errorf("last = %q, want the 403 line", got)
	}

	tl = &tail{}
	tl.Write([]byte("[ffmpeg] https: HTTP error 403 Forbidden\n[ytdl_hook] ERROR: Sign in to confirm\nNo video or audio streams selected.\n"))
	if got := tl.last(); got != "Sign in to confirm" {
		t.Errorf("last = %q, want yt-dlp's message", got)
	}

	tl = &tail{}
	tl.Write([]byte("Failed to open https://x.googlevideo.com/videoplayback?a=b.\n"))
	if got := tl.last(); strings.Contains(got, "googlevideo") {
		t.Errorf("link not cut: %q", got)
	}
}
