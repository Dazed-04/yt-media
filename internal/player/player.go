// Package player previews a track with a hidden mpv that is controlled over
// mpv's JSON IPC socket, so the TUI can draw the player itself. mpv plays
// either a file that yt-dlp downloaded first (works whenever downloads
// work) or a stream URL that yt-dlp resolved and that is checked to be
// fetchable before mpv gets it (starts sooner, no temporary file). A
// preferred YouTube client is tried first and the default is the fallback.
package player

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Options say what to play.
type Options struct {
	Binary     string        // the player, "mpv"
	YtDlp      string        // "yt-dlp"
	URL        string        // a YouTube link
	Format     string        // yt-dlp format, e.g. "bestaudio/best"
	ExtraArgs  []string      // extra yt-dlp options for the download, e.g. cookie settings
	Volume     int           // 0-100; 0 = 70
	StreamWait time.Duration // how long to wait for a stream URL to start working; 0 = 45s
	StreamArgs []string      // extra yt-dlp options when streaming (cookies are left out on purpose)
	Mode       string        // "download" (default), "stream", or "auto" (stream, then download)
	Client     string        // preferred YouTube client, e.g. "web_safari"; "" = yt-dlp's default
}

// State is a snapshot of the playback.
type State struct {
	Title     string
	Pos, Dur  float64 // seconds; Dur is 0 until known
	Paused    bool
	Buffering bool
	Preparing bool    // nothing is playing yet: fetching or opening the track
	Ended     bool    // finished or stopped
	Failed    string  // why playback failed ("" = fine)
	Volume    float64 // 0-100+
}

// Session is one preview.
type Session struct {
	dir    string // socket folder
	sock   string
	cancel context.CancelFunc

	mu       sync.Mutex
	st       State
	cmd      *exec.Cmd
	out      *tail // mpv's own output, for explaining a failure
	conn     net.Conn
	mediaDir string // where yt-dlp put the file
	closed   bool
	played   bool   // mpv reported a position or length: the track really started
	fileErr  string // mpv's end-file error for the current attempt
	once     sync.Once
}

// Start begins a preview and returns at once: the track is fetched first
// (a few seconds), then played. It fails only if mpv or yt-dlp are missing.
func Start(o Options) (*Session, error) {
	bin := firstNonEmpty(o.Binary, "mpv")
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("%s not found: install it to preview tracks", bin)
	}
	ytdlp := firstNonEmpty(o.YtDlp, "yt-dlp")
	if _, err := exec.LookPath(ytdlp); err != nil {
		return nil, fmt.Errorf("%s not found: install it to preview tracks", ytdlp)
	}
	dir, err := os.MkdirTemp("", "ytm-")
	if err != nil {
		return nil, err
	}
	vol := o.Volume
	if vol <= 0 {
		vol = 70
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Unix socket paths are limited to ~104 bytes on macOS, whose temp folder
	// is long, so the names here are kept short.
	s := &Session{dir: dir, sock: filepath.Join(dir, "s"), cancel: cancel, st: State{Volume: float64(vol), Preparing: true}}
	go s.run(ctx, path, ytdlp, o, vol)
	return s, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// step is one way of getting the track playing.
type step struct {
	stream bool   // mpv resolves the link itself, instead of playing a download
	client string // YouTube client to ask for; "" = the default
}

// plan lists the attempts for a mode, in order. The preferred client always
// falls back to the default client.
func plan(mode, client string) []step {
	var steps []step
	add := func(stream bool) {
		steps = append(steps, step{stream, client})
		if client != "" {
			steps = append(steps, step{stream, ""})
		}
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "stream":
		add(true)
	case "auto":
		add(true)
		add(false)
	default:
		add(false)
	}
	return steps
}

func clientArgs(client string) []string {
	if client == "" {
		return nil
	}
	return []string{"--extractor-args", "youtube:player_client=" + client}
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// run goes through the plan until a track plays or the options run out.
func (s *Session) run(ctx context.Context, mpv, ytdlp string, o Options, vol int) {
	failure := ""
	for _, st := range plan(o.Mode, o.Client) {
		msg, retry := s.attempt(ctx, mpv, ytdlp, o, vol, st)
		if msg == "" {
			return
		}
		failure = msg
		if !retry {
			break
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Preparing, s.st.Ended = false, true
	if !s.closed {
		s.st.Failed = failure
	}
}

// attempt tries one step. It returns "" when the track played (or the
// session was closed), else why it didn't, and whether the next step is
// worth trying.
func (s *Session) attempt(ctx context.Context, mpv, ytdlp string, o Options, vol int, st step) (string, bool) {
	args := []string{
		"--no-video", "--no-input-terminal", "--idle=no", "--force-window=no", "--audio-display=no",
		"--input-ipc-server=" + s.sock,
		fmt.Sprintf("--volume=%d", vol),
	}
	if st.stream {
		stream, err := Resolve(ctx, ResolveOptions{YtDlp: ytdlp, URL: o.URL, Format: o.Format, Client: st.client, ExtraArgs: o.StreamArgs, Wait: o.StreamWait})
		if s.isClosed() {
			s.markEnded()
			return "", false
		}
		if err != nil {
			return err.Error(), true
		}
		args = append(args, "--msg-level=all=error")
		args = append(args, stream.MpvArgs()...)
	} else {
		extra := append(clientArgs(st.client), o.ExtraArgs...)
		mediaDir, file, err := Download(ctx, FetchOptions{YtDlp: ytdlp, URL: o.URL, Format: o.Format, ExtraArgs: extra})
		if s.isClosed() {
			if err == nil {
				os.RemoveAll(mediaDir)
			}
			s.markEnded()
			return "", false
		}
		if err != nil {
			return err.Error(), true
		}
		s.mu.Lock()
		s.mediaDir = mediaDir
		s.mu.Unlock()
		args = append(args, "--msg-level=all=error", "--ytdl=no", "--", file)
	}

	os.Remove(s.sock) // a killed mpv can leave its socket behind
	cmd := exec.Command(mpv, args...)
	out := &tail{}
	cmd.Stdout, cmd.Stderr = out, out
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.markEnded()
		return "", false
	}
	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		return err.Error(), false
	}
	s.cmd, s.out = cmd, out
	s.played, s.fileErr = false, ""
	s.st.Pos, s.st.Dur, s.st.Title, s.st.Buffering = 0, 0, "", false
	s.mu.Unlock()

	exited, connDone := make(chan struct{}), make(chan struct{})
	go func() { s.connect(exited); close(connDone) }()
	err := cmd.Wait()
	close(exited)
	select { // let the reader drain mpv's last events
	case <-connDone:
	case <-time.After(2 * time.Second):
		s.mu.Lock()
		if s.conn != nil {
			s.conn.Close()
		}
		s.mu.Unlock()
		<-connDone
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn = nil
	if s.closed {
		s.st.Preparing, s.st.Ended = false, true
		return "", false
	}
	if s.played {
		s.st.Preparing, s.st.Ended = false, true
		if s.fileErr != "" {
			s.st.Failed = s.fileErr
		}
		return "", false
	}
	msg := "mpv closed before playing anything"
	var ee *exec.ExitError
	switch {
	case s.fileErr != "":
		msg = s.fileErr
	case errors.As(err, &ee):
		msg = "mpv could not play this"
	case err != nil:
		msg = err.Error()
	}
	if detail := out.last(); detail != "" { // mpv says why on its output
		msg += ": " + detail
	}
	return msg, st.stream
}

func (s *Session) markEnded() {
	s.mu.Lock()
	s.st.Preparing, s.st.Ended = false, true
	s.mu.Unlock()
}

// State returns the latest snapshot.
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// TogglePause pauses or resumes.
func (s *Session) TogglePause() { s.send("cycle", "pause") }

// Seek moves by secs (negative = back).
func (s *Session) Seek(secs float64) { s.send("seek", secs, "relative") }

// AddVolume changes the volume by delta percentage points.
func (s *Session) AddVolume(delta float64) {
	// Show the change at once, before sending: mpv's confirmation arrives as
	// a property event and must be able to overwrite this, never the reverse.
	s.mu.Lock()
	s.st.Volume = min(max(s.st.Volume+delta, 0), 130)
	s.mu.Unlock()
	s.send("add", "volume", delta)
}

// Close stops the preview and removes its files. Safe to call more than once.
func (s *Session) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		conn, cmd, mediaDir := s.conn, s.cmd, s.mediaDir
		s.mu.Unlock()
		s.cancel() // stops yt-dlp if it is still fetching
		if conn != nil {
			conn.Close()
		}
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Kill()
		}
		os.RemoveAll(s.dir)
		if mediaDir != "" {
			os.RemoveAll(mediaDir)
		}
	})
}

func (s *Session) send(args ...any) {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return
	}
	b, _ := json.Marshal(map[string]any{"command": args})
	conn.SetWriteDeadline(time.Now().Add(time.Second))
	conn.Write(append(b, '\n'))
}

// connect waits for mpv's socket, subscribes to the properties we show, and
// reads events until mpv goes away (exited is closed).
func (s *Session) connect(exited <-chan struct{}) {
	var conn net.Conn
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(40 * time.Millisecond) {
		select {
		case <-exited:
			return
		default:
		}
		if s.isClosed() {
			return
		}
		c, err := net.Dial("unix", s.sock)
		if err == nil {
			conn = c
			break
		}
	}
	if conn == nil {
		return // attempt() reports why mpv never came up
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.Close()
		return
	}
	s.conn = conn
	s.mu.Unlock()

	for i, p := range []string{"time-pos", "duration", "pause", "volume", "media-title", "paused-for-cache"} {
		s.send("observe_property", i+1, p)
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		s.handle(sc.Bytes())
	}
}

type event struct {
	Event     string          `json:"event"`
	Name      string          `json:"name"`
	Data      json.RawMessage `json:"data"`
	Reason    string          `json:"reason"`
	FileError string          `json:"file_error"`
}

func (s *Session) handle(line []byte) {
	var ev event
	if json.Unmarshal(line, &ev) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch ev.Event {
	case "property-change":
		var f float64
		var b bool
		var str string
		switch ev.Name {
		case "time-pos":
			if json.Unmarshal(ev.Data, &f) == nil {
				s.st.Pos = f
				s.started()
			}
		case "duration":
			if json.Unmarshal(ev.Data, &f) == nil {
				s.st.Dur = f
				s.started()
			}
		case "volume":
			if json.Unmarshal(ev.Data, &f) == nil {
				s.st.Volume = f
			}
		case "pause":
			if json.Unmarshal(ev.Data, &b) == nil {
				s.st.Paused = b
			}
		case "paused-for-cache":
			if json.Unmarshal(ev.Data, &b) == nil {
				s.st.Buffering = b
			}
		case "media-title":
			if json.Unmarshal(ev.Data, &str) == nil {
				s.st.Title = str
			}
		}
	case "end-file":
		// attempt() decides what this means once mpv has exited: an error
		// before anything played may mean "try the next step".
		if ev.Reason == "error" {
			s.fileErr = "could not play this track"
			if ev.FileError != "" {
				s.fileErr += " (" + ev.FileError + ")"
			}
		}
	}
}

// started records that the track really began (call with s.mu held).
func (s *Session) started() {
	s.played = true
	s.st.Preparing = false
}

// tail keeps the last lines mpv wrote, so a failure can say why.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 64*1024 { // stream links are long: keep enough for the real cause
		t.buf = t.buf[len(t.buf)-64*1024:]
	}
	return len(p), nil
}

// last returns the most useful line of output. yt-dlp's own "ERROR:" message
// wins, then an HTTP failure from the stream fetch, then any other line that
// looks like an error; mpv's closing lines ("No video or audio streams
// selected", "Exiting...") are only a last resort because they come after
// the real cause. URLs are cut so a long stream link can't bury the reason.
func (t *tail) last() string {
	t.mu.Lock()
	lines := strings.Split(strings.ReplaceAll(string(t.buf), "\r", "\n"), "\n")
	t.mu.Unlock()

	rank := func(l string) int {
		low := strings.ToLower(l)
		switch {
		case strings.Contains(l, "ERROR:"):
			return 4
		case strings.Contains(low, "http error"):
			return 3
		case strings.Contains(low, "error") || strings.Contains(low, "failed"):
			if strings.Contains(low, "errors when loading") {
				return 1 // mpv's closing line
			}
			return 2
		}
		return 0
	}
	best, bestRank := "", -1
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if r := rank(l); r >= bestRank { // later lines win ties
			best, bestRank = l, r
		}
	}
	if i := strings.Index(best, "ERROR:"); i >= 0 { // drop "[ytdl_hook] ERROR:"
		best = strings.TrimSpace(best[i+len("ERROR:"):])
	}
	best = urlRe.ReplaceAllString(best, "<link>")
	if len(best) > 300 {
		best = best[:300] + "…"
	}
	return best
}

var urlRe = regexp.MustCompile(`https?://\S+`)
