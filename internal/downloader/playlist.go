package downloader

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"yt-downloader/internal/songname"
)

var playlistRe = regexp.MustCompile(`(?:music\.)?youtube\.com/playlist\?(?:[^\s]*&)?list=([\w-]+)`)

// PlaylistID extracts the playlist id from a YouTube (or YouTube Music)
// playlist link. A watch link that merely carries a list= parameter is a
// single video, not a playlist, and is not matched.
func PlaylistID(s string) (string, bool) {
	if m := playlistRe.FindStringSubmatch(s); len(m) == 2 {
		return m[1], true
	}
	return "", false
}

type flatEntry struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Uploader      string  `json:"uploader"`
	Channel       string  `json:"channel"`
	Duration      float64 `json:"duration"`
	PlaylistTitle string  `json:"playlist_title"`
	Thumbnails    []struct {
		URL string `json:"url"`
	} `json:"thumbnails"`
}

// FetchPlaylist lists the videos of a playlist, at most limit of them. Only
// the playlist listing is read (one request), so entries carry what the
// listing has: title, channel and length. cookies are extra yt-dlp arguments
// (see config.DownloadOptions.CookieArgs).
func FetchPlaylist(id string, limit int, cookies []string) ([]Video, error) {
	args := []string{"--dump-json", "--flat-playlist", "--skip-download", "--playlist-end", fmt.Sprint(max(limit, 1))}
	args = append(args, cookies...)
	args = append(args, "--", "https://www.youtube.com/playlist?list="+id)

	out, err := exec.Command("yt-dlp", args...).Output()
	if err != nil {
		return nil, cmdError(err)
	}
	vids := parsePlaylist(string(out))
	if len(vids) == 0 {
		return nil, fmt.Errorf("the playlist has no videos")
	}
	return vids, nil
}

func parsePlaylist(out string) []Video {
	var vids []Video
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e flatEntry
		if json.Unmarshal([]byte(line), &e) != nil || e.ID == "" {
			continue
		}
		v := Video{
			ID:       e.ID,
			Title:    e.Title,
			Uploader: e.Uploader,
			Channel:  e.Channel,
			Duration: clock(e.Duration),
		}
		who := songname.FirstNonEmpty(e.Channel, e.Uploader)
		v.Artist = songname.CleanArtist(who) // "Artist - Topic" -> "Artist"
		// YouTube Music album playlists are titled "Album - <name>".
		if name, ok := strings.CutPrefix(e.PlaylistTitle, "Album - "); ok {
			v.Album = strings.TrimSpace(name)
		}
		if n := len(e.Thumbnails); n > 0 {
			v.Thumbnail = e.Thumbnails[n-1].URL
		}
		vids = append(vids, v)
	}
	return vids
}

// clock formats seconds like yt-dlp's duration_string ("3:05", "1:02:03").
func clock(sec float64) string {
	if sec <= 0 {
		return ""
	}
	s := int(sec + 0.5)
	if h := s / 3600; h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
