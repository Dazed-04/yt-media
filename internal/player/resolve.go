package player

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// Since yt-dlp 2026.10.21 YouTube stream URLs answer 403 for the first few
// seconds ("available_at"). yt-dlp's own downloader waits; mpv's ffmpeg opens
// the URL once and gives up. So for streaming we resolve the URL with yt-dlp,
// wait until it really answers, and only then hand it to mpv (with mpv's own
// yt-dlp hook off).

// Tunables, variables so tests can shorten them.
var (
	warmEvery   = time.Second
	warmFor     = 45 * time.Second
	warmRequest = 10 * time.Second
)

// ResolveOptions say which link to resolve and how.
type ResolveOptions struct {
	YtDlp     string        // "yt-dlp"
	URL       string        // a YouTube link
	Format    string        // yt-dlp -f selector
	Client    string        // preferred YouTube client; "" = default
	ExtraArgs []string      // extra yt-dlp arguments (not cookies: see Options.StreamArgs)
	Wait      time.Duration // how long to wait for the URL to work; 0 = 45s
}

// Stream is what mpv needs to play a resolved link.
type Stream struct {
	Title     string
	URL       string // the video, or the audio for an audio-only format
	AudioURL  string // a separate audio stream, "" if URL has the sound
	UserAgent string
}

// MpvArgs are the mpv options and the file for this stream: everything up to
// and including "--" and the URL. mpv's yt-dlp hook is off, so it plays the
// resolved URLs as they are.
func (st Stream) MpvArgs() []string {
	args := []string{"--ytdl=no"}
	if st.Title != "" {
		args = append(args, "--force-media-title="+st.Title)
	}
	if st.UserAgent != "" {
		args = append(args, "--user-agent="+st.UserAgent)
	}
	if st.AudioURL != "" {
		args = append(args, "--audio-file="+st.AudioURL)
	}
	return append(args, "--", st.URL)
}

type ytFormat struct {
	URL         string            `json:"url"`
	VCodec      string            `json:"vcodec"`
	ACodec      string            `json:"acodec"`
	HTTPHeaders map[string]string `json:"http_headers"`
}

type ytInfo struct {
	Title string `json:"title"`
	ytFormat
	Requested []ytFormat `json:"requested_formats"`
}

// Resolve asks yt-dlp for the stream URL(s) of a link and waits until they
// can actually be fetched. It returns an error if yt-dlp fails or the URL is
// still refused after warmFor.
func Resolve(ctx context.Context, o ResolveOptions) (Stream, error) {
	bin := firstNonEmpty(o.YtDlp, "yt-dlp")
	path, err := exec.LookPath(bin)
	if err != nil {
		return Stream{}, fmt.Errorf("%s not found: install it to preview", bin)
	}
	args := []string{"-j", "--no-playlist", "--no-warnings"}
	if o.Format != "" {
		args = append(args, "-f", o.Format)
	}
	args = append(args, clientArgs(o.Client)...)
	args = append(args, o.ExtraArgs...)
	args = append(args, "--", o.URL)

	cmd := exec.CommandContext(ctx, path, args...)
	errs := &tail{}
	cmd.Stderr = errs
	out, runErr := cmd.Output()
	if runErr != nil {
		if ctx.Err() != nil {
			return Stream{}, ctx.Err()
		}
		if d := errs.last(); d != "" {
			return Stream{}, fmt.Errorf("could not get the stream: %s", d)
		}
		return Stream{}, fmt.Errorf("could not get the stream: %v", runErr)
	}
	var info ytInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return Stream{}, fmt.Errorf("could not get the stream: unreadable yt-dlp output")
	}

	st := Stream{Title: info.Title}
	var warm []ytFormat
	if len(info.Requested) > 0 { // separate video and audio streams
		var v, a *ytFormat
		for i, f := range info.Requested {
			switch {
			case f.VCodec != "" && f.VCodec != "none" && v == nil:
				v = &info.Requested[i]
			case f.ACodec != "" && f.ACodec != "none" && a == nil:
				a = &info.Requested[i]
			}
		}
		switch {
		case v != nil && a != nil:
			st.URL, st.AudioURL = v.URL, a.URL
			warm = []ytFormat{*v, *a}
		case v != nil:
			st.URL, warm = v.URL, []ytFormat{*v}
		case a != nil:
			st.URL, warm = a.URL, []ytFormat{*a}
		}
	}
	if st.URL == "" {
		st.URL, warm = info.URL, []ytFormat{info.ytFormat}
	}
	if st.URL == "" {
		return Stream{}, fmt.Errorf("could not get the stream: yt-dlp gave no URL")
	}
	for _, f := range warm {
		if ua := f.HTTPHeaders["User-Agent"]; ua != "" {
			st.UserAgent = ua
			break
		}
	}
	for _, f := range warm {
		if err := waitUntilFetchable(ctx, f, o.Wait); err != nil {
			return Stream{}, err
		}
	}
	return st, nil
}

// waitUntilFetchable polls with a one-byte range request until the server
// answers 200 or 206.
func waitUntilFetchable(ctx context.Context, f ytFormat, wait time.Duration) error {
	if wait <= 0 {
		wait = warmFor
	}
	client := &http.Client{Timeout: warmRequest}
	deadline := time.Now().Add(wait)
	last := "no answer"
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
		if err != nil {
			return fmt.Errorf("could not get the stream: %v", err)
		}
		for k, v := range f.HTTPHeaders {
			req.Header.Set(k, v)
		}
		req.Header.Set("Range", "bytes=0-1")
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent {
				return nil
			}
			last = resp.Status
		} else if ctx.Err() == nil {
			last = strings.TrimSpace(err.Error())
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().Add(warmEvery).After(deadline) {
			return fmt.Errorf("the stream was still refused after %s (%s)", wait.Round(time.Second), last)
		}
		select {
		case <-time.After(warmEvery):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
