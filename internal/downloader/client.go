// Package downloader
package downloader

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

type Video struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Track     string `json:"track"`
	Album     string `json:"album"`
	Uploader  string `json:"uploader"`
	Channel   string `json:"channel"`
	Duration  string `json:"duration_string"`
	Thumbnail string `json:"thumbnail"` // direct URL, used for fast preview fetch
}

func (v Video) GetDisplayString() string {
	if v.Artist != "" && v.Track != "" {
		return fmt.Sprintf("%s - %s", v.Artist, v.Track)
	}
	if v.Artist != "" {
		return fmt.Sprintf("%s - %s", v.Artist, v.Title)
	}
	return v.Title
}

func SearchYoutube(query string, limit int) ([]Video, error) {
	args := []string{
		"--dump-json",
		"--flat-playlist",
		"--skip-download",
		fmt.Sprintf("ytsearch%d:%s", limit, query),
	}
	cmd := exec.Command("yt-dlp", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, cmdError(err)
	}
	var results []Video
	for line := range strings.SplitSeq(string(out), "\n") {
		if line == "" {
			continue
		}
		var v Video
		if err := json.Unmarshal([]byte(line), &v); err == nil {
			if v.Artist == "" {
				v.Artist = v.Uploader
			}
			results = append(results, v)
		}
	}
	return results, nil
}

func (v Video) ThumbnailURL() string {
	if v.Thumbnail != "" {
		return v.Thumbnail
	}
	return fmt.Sprintf("https://i.ytimg.com/vi/%s/maxresdefault.jpg", v.ID)
}

func FetchSingle(idOrURL string) (Video, error) {
	cmd := exec.Command("yt-dlp", "--dump-json", "--skip-download", idOrURL)
	out, err := cmd.Output()
	if err != nil {
		return Video{}, cmdError(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return Video{}, fmt.Errorf("no metadata returned for %s", idOrURL)
	}
	var v Video
	if err := json.Unmarshal([]byte(lines[0]), &v); err != nil {
		return Video{}, err
	}
	if v.Artist == "" {
		v.Artist = v.Uploader
	}
	return v, nil
}

// cmdError replaces "exit status 1" with yt-dlp's own ERROR line.
func cmdError(err error) error {
	var ee *exec.ExitError
	if !errors.As(err, &ee) || len(ee.Stderr) == 0 {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(ee.Stderr)), "\n")
	for _, line := range slices.Backward(lines) {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "ERROR:"); ok {
			return errors.New(strings.TrimSpace(after))
		}
	}
	return errors.New(strings.TrimSpace(lines[len(lines)-1]))
}
