package player

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// FetchOptions say what yt-dlp should fetch for a preview.
type FetchOptions struct {
	YtDlp     string   // "yt-dlp"
	URL       string   // a YouTube link
	Format    string   // yt-dlp -f selector
	ExtraArgs []string // e.g. cookie settings
}

// Download fetches one video's media into a fresh temporary folder, the way a
// normal download does, and returns the folder and the file in it. The
// caller removes the folder. Nothing is added to the download archive.
func Download(ctx context.Context, o FetchOptions) (dir, file string, err error) {
	bin := firstNonEmpty(o.YtDlp, "yt-dlp")
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", "", fmt.Errorf("%s not found: install it to preview tracks", bin)
	}
	dir, err = os.MkdirTemp("", "ytm-")
	if err != nil {
		return "", "", err
	}
	args := []string{"--no-playlist", "--no-warnings", "--newline", "-o", filepath.Join(dir, "media.%(ext)s")}
	if o.Format != "" {
		args = append(args, "-f", o.Format)
	}
	args = append(args, o.ExtraArgs...)
	args = append(args, "--", o.URL) // after "--" so a link can never be read as an option

	cmd := exec.CommandContext(ctx, path, args...)
	out := &tail{}
	cmd.Stdout, cmd.Stderr = out, out
	if runErr := cmd.Run(); runErr != nil {
		os.RemoveAll(dir)
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		if d := out.last(); d != "" {
			return "", "", fmt.Errorf("could not fetch this track: %s", d)
		}
		return "", "", fmt.Errorf("could not fetch this track: %v", runErr)
	}

	// the file is media.<ext>; ignore yt-dlp's leftovers
	entries, _ := os.ReadDir(dir)
	var best string
	var bestSize int64
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, "media.") || strings.HasSuffix(n, ".part") || strings.HasSuffix(n, ".ytdl") || strings.HasSuffix(n, ".temp") {
			continue
		}
		if info, err := e.Info(); err == nil && info.Size() >= bestSize {
			best, bestSize = filepath.Join(dir, n), info.Size()
		}
	}
	if best == "" {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("could not fetch this track: yt-dlp produced no file")
	}
	return dir, best, nil
}

// LaunchWindow starts a player (mpv) in its own window, detached from the
// TUI so it outlives it, and waits briefly to see whether it opened. If it
// exits with an error inside that time the error is returned, with the
// player's own message; otherwise it is left running, and done is called
// when it eventually exits (use it to delete temporary files).
func LaunchWindow(bin string, args []string, within time.Duration, done func()) error {
	bin = firstNonEmpty(bin, "mpv")
	path, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("%s not found: install it to preview videos", bin)
	}
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out := &tail{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if done != nil {
			done()
		}
		exited <- err
	}()
	select {
	case err := <-exited:
		if err != nil {
			if d := out.last(); d != "" {
				return fmt.Errorf("%s could not open this: %s", bin, d)
			}
			return fmt.Errorf("%s could not open this: %v", bin, err)
		}
	case <-time.After(within):
	}
	return nil
}
