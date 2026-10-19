// Package player plays a track with mpv, hidden, and reports its state.
//
// mpv runs with no window and is controlled over its JSON IPC socket, so the
// TUI can draw the player itself. mpv uses yt-dlp to stream YouTube links.
package player

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Options say what to play.
type Options struct {
	Binary             string // "mpv"
	URL                string
	Format             string // yt-dlp format, e.g. "bestaudio/best"; "" = mpv's default
	CookiesFromBrowser string // passed to yt-dlp through mpv; "" = none
	Volume             int    // 0-100; 0 = 70
}

// YtdlCookieArgs makes the browser-cookies setting come from this app's config
// alone. A user's mpv.conf may carry its own cookies-from-browser (for a
// browser they don't use, say) and would break every preview, so that one key
// is removed from mpv's ytdl-raw-options first; the rest of the list (other
// yt-dlp options in mpv.conf) is kept. Command-line options are applied after
// mpv.conf, so this wins without touching the user's files.
func YtdlCookieArgs(cookies string) []string {
	args := []string{"--ytdl-raw-options-remove=cookies-from-browser"}
	c := strings.TrimSpace(cookies)
	if c == "" || strings.HasPrefix(c, "-") {
		return args
	}
	// %N%value gives mpv the value's byte length, so spaces, commas and
	// colons in a profile path can't be misread as list syntax.
	return append(args, fmt.Sprintf("--ytdl-raw-options-add=cookies-from-browser=%%%d%%%s", len(c), c))
}

// State is a snapshot of the playback.
type State struct {
	Title     string
	Pos, Dur  float64 // seconds; Dur is 0 until known
	Paused    bool
	Buffering bool
	Ended     bool    // finished or stopped
	Failed    string  // why playback failed ("" = fine)
	Volume    float64 // 0-100+
}

// Session is one running mpv.
type Session struct {
	cmd  *exec.Cmd
	out  *tail // mpv's own error output, for explaining a failure
	dir  string
	sock string

	mu     sync.Mutex
	st     State
	conn   net.Conn
	closed bool
	once   sync.Once
}

// Start launches mpv and returns at once; state fills in as mpv resolves the
// link, which can take a few seconds. It fails only if mpv can't be started.
func Start(o Options) (*Session, error) {
	bin := o.Binary
	if bin == "" {
		bin = "mpv"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("%s not found: install it to preview tracks", bin)
	}
	dir, err := os.MkdirTemp("", "ytm-")
	if err != nil {
		return nil, err
	}
	vol := o.Volume
	if vol <= 0 {
		vol = 70
	}
	// Unix socket paths are limited to ~104 bytes on macOS, whose temp folder
	// is long, so the names here are kept short.
	s := &Session{dir: dir, sock: filepath.Join(dir, "s"), st: State{Volume: float64(vol)}}

	args := []string{
		"--no-video", "--no-input-terminal", "--msg-level=all=error", "--idle=no", "--force-window=no", "--audio-display=no",
		"--input-ipc-server=" + s.sock,
		fmt.Sprintf("--volume=%d", vol),
	}
	if o.Format != "" {
		args = append(args, "--ytdl-format="+o.Format)
	}
	args = append(args, YtdlCookieArgs(o.CookiesFromBrowser)...)
	args = append(args, "--", o.URL)

	s.cmd = exec.Command(path, args...)
	s.out = &tail{}
	s.cmd.Stdout, s.cmd.Stderr = s.out, s.out
	if err := s.cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	go s.wait()
	go s.connect()
	return s, nil
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

// Close stops mpv and removes its socket. It is safe to call more than once.
func (s *Session) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		conn := s.conn
		s.mu.Unlock()
		if conn != nil {
			conn.Close()
		}
		if s.cmd.Process != nil {
			s.cmd.Process.Kill()
		}
		os.RemoveAll(s.dir)
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

// wait notes when mpv exits.
func (s *Session) wait() {
	err := s.cmd.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Ended = true
	if s.closed {
		return
	}
	if err != nil && s.st.Failed == "" {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			s.st.Failed = "mpv could not play this"
		} else {
			s.st.Failed = err.Error()
		}
	}
	// mpv says why on its output (often yt-dlp's message): show it.
	if detail := s.out.last(); s.st.Failed != "" && detail != "" {
		s.st.Failed += ": " + detail
	}
}

// connect waits for mpv's socket, subscribes to the properties we show, and
// reads events until mpv goes away.
func (s *Session) connect() {
	var conn net.Conn
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(40 * time.Millisecond) {
		s.mu.Lock()
		done := s.closed || s.st.Ended
		s.mu.Unlock()
		if done {
			return
		}
		c, err := net.Dial("unix", s.sock)
		if err == nil {
			conn = c
			break
		}
	}
	if conn == nil {
		s.mu.Lock()
		if s.st.Failed == "" {
			s.st.Failed = "mpv did not start"
		}
		s.mu.Unlock()
		return
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
			}
		case "duration":
			if json.Unmarshal(ev.Data, &f) == nil {
				s.st.Dur = f
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
		s.st.Ended = true
		if ev.Reason == "error" {
			s.st.Failed = "could not play this track"
			if ev.FileError != "" {
				s.st.Failed += " (" + ev.FileError + ")"
			}
		}
	}
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
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

// last returns the most useful line of output: yt-dlp's own "ERROR:" message
// if there is one (the last one), else the last non-empty line.
func (t *tail) last() string {
	t.mu.Lock()
	lines := strings.Split(strings.ReplaceAll(string(t.buf), "\r", "\n"), "\n")
	t.mu.Unlock()
	best, fallback := "", ""
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		fallback = l
		if strings.Contains(l, "ERROR:") {
			best = l
		}
	}
	if best == "" {
		best = fallback
	}
	if i := strings.Index(best, "ERROR:"); i >= 0 { // drop "[ytdl_hook] ERROR:"
		best = strings.TrimSpace(best[i+len("ERROR:"):])
	}
	if len(best) > 300 {
		best = best[:300] + "…"
	}
	return best
}
