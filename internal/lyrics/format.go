package lyrics

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	stampRe      = regexp.MustCompile(`^\[(\d+):(\d+(?:\.\d+)?)\]`)
	inlineTimeRe = regexp.MustCompile(`<\d+:\d+(?:\.\d+)?>`) // enhanced-LRC word timing
)

func stampSeconds(m []string) float64 {
	min, _ := strconv.ParseFloat(m[1], 64)
	sec, _ := strconv.ParseFloat(m[2], 64)
	return min*60 + sec
}

// CleanLRC keeps only the timestamped lines of raw (dropping any 00:00.00
// placeholder), adds artist/title tags, and puts a "🎵" at 00:00 when the
// first line starts more than 5 seconds in,
// so players show something during the intro. It returns "" if raw has no
// timestamped lines.
func CleanLRC(raw, artist, title string) string {
	var lines []string
	first := -1.0
	for _, ln := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		ln = strings.TrimRight(ln, " \t\r")
		m := stampRe.FindStringSubmatch(ln)
		if m == nil || strings.HasPrefix(ln, "[00:00.00]") {
			continue // not a lyric line, or the service's own 00:00 placeholder
		}
		if first < 0 {
			first = stampSeconds(m)
		}
		lines = append(lines, ln)
	}
	if len(lines) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[ar:%s]\n[ti:%s]\n", artist, title)
	if first > 5 {
		b.WriteString("[00:00.00] 🎵\n")
	}
	for _, ln := range lines {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	return b.String()
}

type cue struct {
	at   float64
	text string
}

// Display limits for subtitle cues, in seconds.
const (
	maxCue  = 6.0 // a line never stays up longer than this, even over a long instrumental gap
	lastCue = 4.0 // how long the final line stays up
	minCue  = 1.5 // used only when two lines share a timestamp
)

// LRCToSRT converts synced lyrics to SubRip subtitles. Each line is shown
// until the next one starts. It returns "" if raw has no timestamped lines.
func LRCToSRT(raw string) string {
	var cues []cue
	for _, ln := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		var stamps []float64
		rest := strings.TrimSpace(ln)
		for {
			m := stampRe.FindStringSubmatch(rest)
			if m == nil {
				break
			}
			stamps = append(stamps, stampSeconds(m))
			rest = rest[len(m[0]):]
		}
		text := strings.TrimSpace(inlineTimeRe.ReplaceAllString(rest, ""))
		for _, at := range stamps {
			cues = append(cues, cue{at: at, text: text})
		}
	}
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].at < cues[j].at })

	var b strings.Builder
	n := 0
	for i, c := range cues {
		if c.text == "" { // an empty line only marks the end of the previous one
			continue
		}
		end := c.at + lastCue
		if i+1 < len(cues) {
			end = cues[i+1].at
		}
		end = math.Min(end, c.at+maxCue)
		if end <= c.at {
			end = c.at + minCue
		}
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, srtTime(c.at), srtTime(end), c.text)
	}
	return b.String()
}

func srtTime(sec float64) string {
	ms := int(math.Round(sec * 1000))
	h, ms := ms/3600000, ms%3600000
	m, ms := ms/60000, ms%60000
	s, ms := ms/1000, ms%1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// ParseClock turns "3:45" or "1:02:03" (yt-dlp's duration_string) into seconds.
// It returns 0 for anything it can't read.
func ParseClock(s string) float64 {
	total := 0.0
	for _, part := range strings.Split(strings.TrimSpace(s), ":") {
		v, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0
		}
		total = total*60 + v
	}
	return total
}
