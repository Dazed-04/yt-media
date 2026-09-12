package downloader

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type (
	DownloadFinishedMsg struct{}
	ErrorMsg            error
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
		return nil, err
	}
	var results []Video
	for _, line := range strings.Split(string(out), "\n") {
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
		return Video{}, err
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
