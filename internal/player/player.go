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
	dir, err := os.MkdirTemp("", "yt-media-mpv-")
	if err != nil {
		return nil, err
	}
	vol := o.Volume
	if vol <= 0 {
		vol = 70
	}
	s := &Session{dir: dir, sock: filepath.Join(dir, "ipc.sock"), st: State{Volume: float64(vol)}}

	args := []string{
		"--no-video", "--no-terminal", "--idle=no", "--force-window=no", "--audio-display=no",
		"--input-ipc-server=" + s.sock,
		fmt.Sprintf("--volume=%d", vol),
	}
	if o.Format != "" {
		args = append(args, "--ytdl-format="+o.Format)
	}
	if c := strings.TrimSpace(o.CookiesFromBrowser); c != "" && !strings.ContainsAny(c, ",") && !strings.HasPrefix(c, "-") {
		args = append(args, "--ytdl-raw-options=cookies-from-browser="+c)
	}
	args = append(args, "--", o.URL)

	s.cmd = exec.Command(path, args...)
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
	if err != nil && !s.closed && s.st.Failed == "" {
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			s.st.Failed = "mpv could not play this (is yt-dlp installed?)"
		} else {
			s.st.Failed = err.Error()
		}
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
