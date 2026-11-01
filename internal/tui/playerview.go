package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
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
	url := v.URL()
	opts := m.Config.DownloadOptions // a copy: the cmd below runs off the UI goroutine

	if m.ChoiceType == "Video" {
		if _, err := exec.LookPath(opts.Player()); err != nil {
			return m.flash(opts.Player()+" not found: install it to preview videos", true)
		}
		fl := m.flash("Opening video in "+opts.Player()+"…", false)
		return tea.Batch(fl, func() tea.Msg { return videoPreviewMsg{err: openVideoPreview(opts, url)} })
	}

	m.closePlayer()
	s, err := player.Start(player.Options{
		Binary:     opts.Player(),
		URL:        url,
		Format:     opts.PreviewAudioSelector(),
		ExtraArgs:  opts.PreviewArgs(),
		StreamArgs: opts.PreviewExtraArgs(),
		StreamWait: opts.PreviewWait(),
		Mode:       opts.PreviewModeOrDefault(),
		Client:     opts.PreviewClientOrEmpty(),
	})
	if err != nil {
		return m.flash(err.Error(), true)
	}
	m.Player, m.PlayerOpen, m.PlayerTitle = s, true, v.GetDisplayString()
	return m.playerTick()
}

// videoPreviewMsg is the outcome of opening a video preview window.
type videoPreviewMsg struct{ err error }

// Timings for a video preview: resolving the link and waiting for YouTube to
// accept the stream URL can take a while; after that mpv opens quickly.
const videoOpenWait = 4 * time.Second

// openVideoPreview has yt-dlp resolve the video's stream URLs, waits until
// YouTube accepts them, and opens them in mpv's own window. Nothing is
// downloaded. The preferred client is tried first, then yt-dlp's default. An
// error means no window opened.
func openVideoPreview(o config.DownloadOptions, url string) error {
	clients := []string{o.PreviewClientOrEmpty()}
	if clients[0] != "" {
		clients = append(clients, "")
	}
	var err error
	for _, c := range clients {
		ctx, cancel := context.WithTimeout(context.Background(), o.PreviewWait()+45*time.Second) // + time for yt-dlp itself
		var st player.Stream
		st, err = player.Resolve(ctx, player.ResolveOptions{
			URL: url, Format: o.PreviewVideoSelector(), Client: c, ExtraArgs: o.PreviewExtraArgs(), Wait: o.PreviewWait(),
		})
		cancel()
		if err != nil {
			continue
		}
		args := append([]string{"--msg-level=all=error"}, st.MpvArgs()...)
		if err = player.LaunchWindow(o.Player(), args, videoOpenWait, nil); err == nil {
			return nil
		}
	}
	return err
}

func (m *Model) closePlayer() {
	if m.Player != nil {
		m.Player.Close()
		m.Player = nil
	}
	m.PlayerOpen = false
	m.playerSeq++ // drop any pending tick
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
	case st.Preparing:
		status = "Fetching"
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
	} else if st.Preparing {
		rows = append(rows, s.row(inner, s.dim.Render("getting the track…")))
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
