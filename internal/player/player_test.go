package player

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	var sock string
	for _, a := range os.Args[1:] {
		if v, ok := strings.CutPrefix(a, "--input-ipc-server="); ok {
			sock = v
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

func start(t *testing.T, mode string, o Options) *Session {
	t.Helper()
	t.Setenv("FAKE_MPV", mode)
	o.Binary = os.Args[0]
	s, err := Start(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestPlaybackStateAndControls(t *testing.T) {
	s := start(t, "ok", Options{URL: "https://www.youtube.com/watch?v=aaaaaaaaaaa", Volume: 70})

	waitFor(t, "title and length", func() bool { st := s.State(); return st.Title == "Fake Song" && st.Dur == 200 })
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
	s := start(t, "slow", Options{URL: "x"})
	waitFor(t, "state from a late socket", func() bool { return s.State().Title == "Fake Song" })
}

func TestArgsAreSafeAndComplete(t *testing.T) {
	s := start(t, "log-args", Options{URL: "https://example/x", Format: "bestaudio/best", CookiesFromBrowser: "firefox", Volume: 50})
	var args string
	waitFor(t, "args file", func() bool {
		b, err := os.ReadFile(filepath.Join(s.dir, "args"))
		args = string(b)
		return err == nil && len(b) > 0
	})
	for _, want := range []string{"--no-video", "--no-terminal", "--volume=50", "--ytdl-format=bestaudio/best", "--ytdl-raw-options=cookies-from-browser=firefox"} {
		if !strings.Contains(args, want+"\n") && !strings.HasSuffix(args, want) {
			t.Errorf("mpv args lack %q:\n%s", want, args)
		}
	}
	// the link comes last, after "--", so it can never be taken as an option
	lines := strings.Split(args, "\n")
	if lines[len(lines)-2] != "--" || lines[len(lines)-1] != "https://example/x" {
		t.Errorf("tail of args = %q", lines[len(lines)-2:])
	}
}

func TestOptionLookingCookiesAreNotPassed(t *testing.T) {
	s := start(t, "log-args", Options{URL: "x", CookiesFromBrowser: "--script=evil.lua"})
	var args string
	waitFor(t, "args file", func() bool {
		b, err := os.ReadFile(filepath.Join(s.dir, "args"))
		args = string(b)
		return err == nil && len(b) > 0
	})
	if strings.Contains(args, "evil") {
		t.Errorf("cookie value reached mpv:\n%s", args)
	}
}

func TestCrashIsReported(t *testing.T) {
	s := start(t, "crash", Options{URL: "x"})
	waitFor(t, "failure", func() bool { st := s.State(); return st.Ended && st.Failed != "" })
}

func TestMissingBinary(t *testing.T) {
	if _, err := Start(Options{Binary: "definitely-not-a-real-player", URL: "x"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v", err)
	}
}

func TestCloseStopsMpvAndCleansUp(t *testing.T) {
	s := start(t, "ok", Options{URL: "x"})
	waitFor(t, "connection", func() bool { return s.State().Title != "" })
	dir := s.dir
	s.Close()
	s.Close() // twice is fine
	waitFor(t, "process exit", func() bool { return s.State().Ended })
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("temp dir still there: %v", err)
	}
	s.Seek(5) // controlling a closed session must not panic
}
