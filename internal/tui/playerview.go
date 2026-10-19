package tui

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"yt-downloader/internal/config"
	"yt-downloader/internal/downloader"
	"yt-downloader/internal/player"
)

// Preview (p): audio plays in a hidden mpv that the TUI controls and draws a
// small player for; video opens mpv's own window, which handles picture and
// sound itself.

type playerTickMsg struct{ seq int }

const playerTickEvery = 250 * time.Millisecond

func (m *Model) playerTick() tea.Cmd {
	m.playerSeq++
	seq := m.playerSeq
	return tea.Tick(playerTickEvery, func(time.Time) tea.Msg { return playerTickMsg{seq: seq} })
}

// onPlayerTick keeps the player window fresh while it's open and playing.
func (m *Model) onPlayerTick(msg playerTickMsg) tea.Cmd {
	if msg.seq != m.playerSeq || !m.PlayerOpen || m.Player == nil {
		return nil
	}
	if m.Player.State().Ended {
		return nil // nothing more will change
	}
	return m.playerTick()
}

// startPreview handles the p key for video v.
func (m *Model) startPreview(v downloader.Video) tea.Cmd {
	if v.ID == "" {
		return nil
	}
	url := "https://www.youtube.com/watch?v=" + v.ID
	opts := m.Config.DownloadOptions

	if m.ChoiceType == "Video" {
		if err := launchVideoPreview(opts, url); err != nil {
			return m.flash(err.Error(), true)
		}
		return m.flash("Opened in "+opts.Player(), false)
	}

	m.closePlayer()
	s, err := player.Start(player.Options{
		Binary: opts.Player(),
		URL:    url,
		Format: "bestaudio/best",
	})
	if err != nil {
		return m.flash(err.Error(), true)
	}
	m.Player, m.PlayerOpen, m.PlayerTitle = s, true, v.GetDisplayString()
	return m.playerTick()
}

func (m *Model) closePlayer() {
	if m.Player != nil {
		m.Player.Close()
		m.Player = nil
	}
	m.PlayerOpen = false
	m.playerSeq++ // drop any pending tick
}

// videoPreviewArgs builds the mpv command line for a video preview window.
func videoPreviewArgs(o config.DownloadOptions, url string) []string {
	// No cookies for previews: YouTube gives logged-in requests stream URLs
	// that mpv can't fetch (HTTP 403). Downloads use cookies_from_browser;
	// previews stay logged out, and any cookies line in the user's mpv.conf
	// is dropped.
	args := []string{"--ytdl-format=" + o.VideoSelector()}
	args = append(args, player.YtdlCookieArgs("")...)
	return append(args, "--", url)
}

// launchVideoPreview starts mpv in its own window, detached from the TUI so
// it keeps playing however the TUI is used (or closed) afterwards.
func launchVideoPreview(o config.DownloadOptions, url string) error {
	bin := o.Player()
	path, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("%s not found: install it to preview videos", bin)
	}
	cmd := exec.Command(path, videoPreviewArgs(o, url)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var out lockedBuf
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { // reap it when the window closes; a failure goes to the log
		if err := cmd.Wait(); err != nil {
			log.Printf("[PREVIEW] %s exited: %v: %s", bin, err, strings.TrimSpace(out.String()))
		}
	}()
	return nil
}

// ---------- keys ----------

func (m *Model) handlePlayerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.Player == nil {
		m.closePlayer()
		return m, nil
	}
	switch msg.String() {
	case "esc", "q", "p":
		m.closePlayer()
	case "space":
		m.Player.TogglePause()
	case "left", "h":
		m.Player.Seek(-5)
	case "right", "l":
		m.Player.Seek(5)
	case "H":
		m.Player.Seek(-30)
	case "L":
		m.Player.Seek(30)
	case "up", "k", "+", "=":
		m.Player.AddVolume(5)
	case "down", "j", "-":
		m.Player.AddVolume(-5)
	}
	return m, nil
}

// ---------- rendering ----------

func fmtClock(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	t := int(sec)
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, t%3600/60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}

// progressBar draws pos/dur across width cells; an unknown length is empty.
func progressBar(pos, dur float64, width int) (filled, empty string) {
	if width < 1 {
		return "", ""
	}
	n := 0
	if dur > 0 {
		n = int(float64(width) * min(max(pos/dur, 0), 1))
	}
	return strings.Repeat("━", n), strings.Repeat("━", width-n)
}

func (m *Model) renderPlayerOverlay() string {
	s := newWinStyles()
	w := m.overlayWidth()
	inner := w - 4
	st := player.State{}
	if m.Player != nil {
		st = m.Player.State()
	}

	status := "Playing"
	switch {
	case st.Failed != "":
		status = "Error"
	case st.Ended:
		status = "Finished"
	case st.Buffering:
		status = "Buffering"
	case st.Paused:
		status = "Paused"
	case st.Dur == 0:
		status = "Loading"
	}
	header := s.header(inner, "Preview", status)

	title := m.PlayerTitle
	if title == "" {
		title = st.Title
	}
	rows := []string{s.row(inner, s.accent.Render(safeTruncate(title, inner)))}

	if st.Failed != "" {
		for _, ln := range wrapLines(st.Failed, inner, 3) {
			rows = append(rows, s.row(inner, s.red.Render(strings.TrimRight(ln, " "))))
		}
	} else {
		left, right := fmtClock(st.Pos), "--:--"
		if st.Dur > 0 {
			right = fmtClock(st.Dur)
		}
		barW := max(4, inner-len(left)-len(right)-2)
		on, off := progressBar(st.Pos, st.Dur, barW)
		rows = append(rows,
			s.row(inner, s.dim.Render(left+" "), s.green.Render(on), s.dim.Render(off), s.dim.Render(" "+right)),
			s.row(inner, s.dim.Render(fmt.Sprintf("volume %d%%", int(st.Volume+0.5)))),
		)
	}

	hints := [][]string{{"space: pause", "←/→: seek 5s", "↑/↓: volume", "esc: close"}}
	return windowBox(s, w, header, [][]string{rows}, hints)
}

// lockedBuf collects a detached process's output for the log.
type lockedBuf struct {
	mu sync.Mutex
	b  []byte
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.b) < 8192 {
		l.b = append(l.b, p...)
	}
	return len(p), nil
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.b)
}
