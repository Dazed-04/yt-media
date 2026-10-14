package lyrics

import (
	"regexp"
	"strings"
)

// Track is what we know about a song.
type Track struct {
	Artist   string
	Title    string
	AltTitle string // another title for the same song (e.g. the video title when Title is clean music metadata)
	Album    string
	Duration float64 // seconds; 0 = unknown
}

// Query is one search to try against the lyrics service. An empty Artist means
// a free-text search on the title alone.
type Query struct {
	Artist, Title string
}

// maxQueries caps the searches tried for one song, so a song with no lyrics
// costs a handful of requests rather than dozens.
const maxQueries = 8

var (
	spaceRe = regexp.MustCompile(`\s+`)

	// "(Official Video)", "[Lyrics]", "【MV】" ...
	bracketNoiseRe = regexp.MustCompile(`(?i)[(\[{【「][^)\]}】」]*\b(?:official|lyrics?|video|audio|visuali[sz]er|hd|hq|4k|1080p|mv|explicit)\b[^)\]}】」]*[)\]}】」]`)
	// " - Official Video", " | Lyric Video" ... up to the end
	trailNoiseRe = regexp.MustCompile(`(?i)\s*[|\-–—]\s*(?:official\b|lyrics?\b|music video\b|video\b|audio\b|visuali[sz]er\b|hd\b|hq\b|4k\b).*$`)

	// "(feat. X)", "[ft. X]", "(with X)", "(prod. X)"
	bracketFeatRe = regexp.MustCompile(`(?i)[(\[{][^)\]}]*\b(?:feat|ft|featuring|with|prod)\b[^)\]}]*[)\]}]`)
	// "feat. X" / "ft. X" up to the end
	trailFeatRe = regexp.MustCompile(`(?i)\s+(?:feat\.?|ft\.?|featuring)\s+.*$`)

	artistTopicRe = regexp.MustCompile(`(?i)\s*-\s*topic$`)
	artistVevoRe  = regexp.MustCompile(`(?i)\s*vevo$`)
	artistSplitRe = regexp.MustCompile(`(?i)\s*(?:,|&|;|\bfeat\.?|\bft\.?|\bfeaturing\b|\bx\b)\s*`)

	titleSplitRe = regexp.MustCompile(`\s+[-–—]\s+`)
)

func tidy(s string) string {
	s = spaceRe.ReplaceAllString(s, " ")
	return strings.Trim(strings.TrimSpace(s), "-–—|:, ")
}

// stripNoise removes "(Official Video)"-style decoration.
func stripNoise(title string) string {
	title = bracketNoiseRe.ReplaceAllString(title, " ")
	title = trailNoiseRe.ReplaceAllString(title, "")
	return tidy(title)
}

// stripFeat removes "(feat. X)" and "feat. X".
func stripFeat(title string) string {
	title = bracketFeatRe.ReplaceAllString(title, " ")
	title = trailFeatRe.ReplaceAllString(title, "")
	return tidy(title)
}

// cleanArtist drops channel decoration like " - Topic" and "VEVO".
func cleanArtist(a string) string {
	a = artistTopicRe.ReplaceAllString(a, "")
	a = artistVevoRe.ReplaceAllString(a, "")
	return tidy(a)
}

// primaryArtist is the first artist of "A, B & C" / "A feat. B".
func primaryArtist(a string) string {
	for _, part := range artistSplitRe.Split(a, -1) {
		if p := tidy(part); p != "" {
			return p
		}
	}
	return a
}

// splitArtistTitle splits "Artist - Title" at its first dash.
func splitArtistTitle(s string) (artist, title string, ok bool) {
	loc := titleSplitRe.FindStringIndex(s)
	if loc == nil {
		return "", "", false
	}
	artist, title = tidy(s[:loc[0]]), tidy(s[loc[1]:])
	return artist, title, artist != "" && title != ""
}

// titleVariants lists every spelling of the title worth matching against.
func titleVariants(t Track) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s = tidy(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, title := range []string{t.Title, t.AltTitle} {
		add(title)
		add(stripNoise(title))
		add(stripFeat(stripNoise(title)))
		if _, right, ok := splitArtistTitle(stripNoise(title)); ok {
			add(right)
			add(stripFeat(right))
		}
	}
	return out
}

// Candidates returns the searches to try, best first.
//
// Songs are listed inconsistently: some have "feat. X" in the title, others
// don't, and YouTube titles carry "(Official Video)" and often "Artist - Title".
// So the exact spelling comes first, then progressively cleaner ones.
func Candidates(t Track) []Query {
	artist := cleanArtist(t.Artist)

	var out []Query
	seen := map[Query]bool{}
	add := func(a, title string) {
		q := Query{tidy(a), tidy(title)}
		if q.Title != "" && !seen[q] {
			seen[q] = true
			out = append(out, q)
		}
	}
	variants := func(a, title string) {
		titles := []string{title, stripNoise(title), stripFeat(stripNoise(title))}
		for _, tv := range titles {
			for _, av := range []string{a, primaryArtist(a)} {
				add(av, tv)
			}
		}
	}

	for _, title := range []string{t.Title, t.AltTitle} {
		if title == "" {
			continue
		}
		if left, right, ok := splitArtistTitle(stripNoise(title)); ok {
			variants(cleanArtist(left), right)
		}
		variants(artist, title)
	}

	if len(out) > maxQueries-1 {
		out = out[:maxQueries-1]
	}
	// Last resort: free-text search on the cleanest title.
	last := stripFeat(stripNoise(firstNonEmpty(t.Title, t.AltTitle)))
	if _, right, ok := splitArtistTitle(last); ok {
		last = right
	}
	add("", last)
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
