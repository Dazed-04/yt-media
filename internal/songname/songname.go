// Package songname cleans and compares song titles and artist names.
//
// Song metadata is messy: YouTube titles carry "(Official Video)" and often
// "Artist - Title", channel names end in "- Topic" or "VEVO", and some
// databases keep "feat." credits in the title while others drop them. Both
// the lyrics and the album lookups need to see through that, so it lives here.
package songname

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Track is what we know about a song.
type Track struct {
	Artist   string
	Title    string
	AltTitle string // another title for the same song (e.g. the video title when Title is clean music metadata)
	Album    string
	Duration float64 // seconds; 0 = unknown
}

// Query is one search to try against an online service. An empty Artist means
// a free-text search on the title alone.
type Query struct {
	Artist, Title string
}

// MaxQueries caps the searches Candidates returns, so a song nobody knows
// costs a handful of requests rather than dozens.
const MaxQueries = 10

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

	titleSplitRe = regexp.MustCompile(`\s*[-–—]\s+`)
	// `Dominic Fike "Babydoll"`: artist followed by the title in quotes
	quotedSplitRe = regexp.MustCompile(`^(.+?)\s+["“«](.+?)["”»]$`)
	// "Song | Channel", "Song – Film", "Song ~ Artist": what follows is not the title
	tailRe = regexp.MustCompile(`\s+(?:[|~–—]|-)\s+.*$`)
	// "twenty one pilots: Heathens": a short left side, no brackets
	colonSplitRe = regexp.MustCompile(`^([^()\[\]:：]{1,40}?)[:：]\s+(.+)$`)
)

func tidy(s string) string {
	s = spaceRe.ReplaceAllString(s, " ")
	return strings.Trim(strings.TrimSpace(s), "-–—|:, ")
}

// Unsanitize undoes yt-dlp's file-name sanitising: it swaps ":" "|" "?" '"' "*"
// for their full-width look-alikes (and "/" for "⧸"), which would otherwise
// make "Artist： Title" or "Song ｜ Channel" look like different text.
func Unsanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0xFF01 && r <= 0xFF5E:
			return r - 0xFEE0
		case r == '⧸':
			return '/'
		case r == '\u3000':
			return ' '
		}
		return r
	}, s)
}

var (
	anyBracketRe = regexp.MustCompile(`\s*[(\[][^)\]]*[)\]]`)
	// Brackets that name another version of the song: these are never dropped,
	// because the live or remixed recording is a different one.
	versionRe = regexp.MustCompile(`(?i)\b(?:live|remix|acoustic|instrumental|demo|version|mix|edit|cover|sped|slowed|reverb|karaoke|session|unplugged|reprise|intro|outro|skit)\b`)
)

// TrimTail cuts a title at the first " | ", " – ", " - " or " ~ ": uploads
// append the channel, the film or the artist there.
func TrimTail(title string) string {
	return tidy(tailRe.ReplaceAllString(title, ""))
}

// StripBrackets removes every "(...)" and "[...]" group except ones that name
// another version ("(Live)", "(Remix)"). Streaming databases usually keep
// "(From the Motion Picture ...)" out of the song's title.
func StripBrackets(title string) string {
	title = anyBracketRe.ReplaceAllStringFunc(title, func(m string) string {
		if versionRe.MatchString(m) {
			return m
		}
		return ""
	})
	return tidy(title)
}

// StripNoise removes "(Official Video)"-style decoration.
func StripNoise(title string) string {
	title = Unsanitize(title)
	title = bracketNoiseRe.ReplaceAllString(title, " ")
	title = trailNoiseRe.ReplaceAllString(title, "")
	return tidy(title)
}

// StripFeat removes "(feat. X)" and "feat. X".
func StripFeat(title string) string {
	title = bracketFeatRe.ReplaceAllString(title, " ")
	title = trailFeatRe.ReplaceAllString(title, "")
	return tidy(title)
}

// CleanArtist drops channel decoration like " - Topic" and "VEVO".
func CleanArtist(a string) string {
	a = artistTopicRe.ReplaceAllString(a, "")
	a = artistVevoRe.ReplaceAllString(a, "")
	return tidy(a)
}

// PrimaryArtist is the first artist of "A, B & C" / "A feat. B".
func PrimaryArtist(a string) string {
	for _, part := range artistSplitRe.Split(a, -1) {
		if p := tidy(part); p != "" {
			return p
		}
	}
	return a
}

// SplitArtistTitle splits "Artist - Title" at its first dash.
func SplitArtistTitle(s string) (artist, title string, ok bool) {
	// `Artist "Title"` first: the title may itself contain dashes.
	if m := quotedSplitRe.FindStringSubmatch(s); m != nil {
		artist, title = tidy(m[1]), tidy(m[2])
		return artist, title, artist != "" && title != ""
	}
	if loc := titleSplitRe.FindStringIndex(s); loc != nil {
		artist, title = tidy(s[:loc[0]]), tidy(s[loc[1]:])
		return artist, title, artist != "" && title != ""
	}
	// "Artist: Title" (a dash is what most uploads use, so it wins)
	if m := colonSplitRe.FindStringSubmatch(s); m != nil {
		artist, title = tidy(m[1]), tidy(m[2])
		return artist, title, artist != "" && title != ""
	}
	return "", "", false
}

// TitleVariants lists every spelling of the title worth matching against.
func TitleVariants(t Track) []string {
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
		add(StripNoise(title))
		add(StripFeat(StripNoise(title)))
		add(StripBrackets(StripFeat(StripNoise(title))))
		add(TrimTail(StripBrackets(StripFeat(StripNoise(title)))))
		if _, right, ok := SplitArtistTitle(StripNoise(title)); ok {
			add(right)
			add(StripFeat(right))
			add(StripBrackets(StripFeat(right)))
			add(TrimTail(StripBrackets(StripFeat(right))))
		}
	}
	return out
}

// KnownArtists lists the artists the track might be credited to: its artist
// field, and the left side of "Artist - Title" titles.
func KnownArtists(t Track) []string {
	var known []string
	if a := PrimaryArtist(CleanArtist(t.Artist)); a != "" {
		known = append(known, a)
	}
	for _, title := range []string{t.Title, t.AltTitle} {
		if left, _, ok := SplitArtistTitle(StripNoise(title)); ok {
			known = append(known, PrimaryArtist(CleanArtist(left)))
		}
	}
	return known
}

// Candidates returns the searches to try, best first.
//
// Songs are listed inconsistently: some have "feat. X" in the title, others
// don't, and YouTube titles carry "(Official Video)" and often "Artist - Title".
// So the exact spelling comes first, then progressively cleaner ones.
func Candidates(t Track) []Query { return candidates(t, false) }

// CleanestFirst is Candidates with the most thoroughly cleaned titles first.
// Databases that keep titles tidy (MusicBrainz) match those best, and a song
// they don't know then costs few requests.
func CleanestFirst(t Track) []Query { return candidates(t, true) }

func candidates(t Track, cleanFirst bool) []Query {
	artist := CleanArtist(t.Artist)

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
		clean := StripBrackets(StripFeat(StripNoise(title)))
		titles := []string{title, StripNoise(title), StripFeat(StripNoise(title)), clean, TrimTail(clean)}
		for _, tv := range titles {
			for _, av := range []string{a, PrimaryArtist(a)} {
				add(av, tv)
			}
		}
	}

	for _, title := range []string{t.Title, t.AltTitle} {
		if title == "" {
			continue
		}
		if left, right, ok := SplitArtistTitle(StripNoise(title)); ok {
			variants(CleanArtist(left), right)
		}
		variants(artist, title)
	}

	if cleanFirst {
		sort.SliceStable(out, func(i, j int) bool { return len([]rune(out[i].Title)) < len([]rune(out[j].Title)) })
	}
	if len(out) > MaxQueries-1 {
		out = out[:MaxQueries-1]
	}
	// Last resort: free-text search on the cleanest title.
	last := StripBrackets(StripFeat(StripNoise(FirstNonEmpty(t.Title, t.AltTitle))))
	if _, right, ok := SplitArtistTitle(last); ok {
		last = right
	}
	add("", last)
	return out
}

// Merge fills the gaps of hint (what the caller knows, e.g. from YouTube)
// from fromFile (the file's own tags). The caller's values win, except the
// length: the file's own is the most accurate.
func Merge(hint, fromFile Track) Track {
	dur := hint.Duration
	if fromFile.Duration > 0 {
		dur = fromFile.Duration
	}
	return Track{
		Artist:   FirstNonEmpty(hint.Artist, fromFile.Artist),
		Title:    FirstNonEmpty(hint.Title, fromFile.Title),
		AltTitle: hint.AltTitle,
		Album:    FirstNonEmpty(hint.Album, fromFile.Album),
		Duration: dur,
	}
}

// ---------- comparing ----------

// Norm lowercases s and keeps only letters and digits.
func Norm(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// AnyArtistMatches is true when no artist is known, or one of them matches.
func AnyArtistMatches(wants []string, got string) bool {
	if len(wants) == 0 {
		return true
	}
	for _, w := range wants {
		if ArtistMatches(w, got) {
			return true
		}
	}
	return false
}

// ArtistMatches reports whether got (a credited artist) is want.
func ArtistMatches(want, got string) bool {
	if want == "" {
		return true
	}
	nw, ng := Norm(want), Norm(got)
	if nw == "" || ng == "" {
		return false
	}
	if strings.Contains(ng, nw) || strings.Contains(nw, ng) {
		return true
	}
	for _, w := range strings.Fields(strings.ToLower(want)) {
		if nwd := Norm(w); len(nwd) >= 3 && strings.Contains(ng, nwd) {
			return true
		}
	}
	return false
}

// TitleMatches reports whether got is (or contains) one of the wanted titles.
func TitleMatches(wants []string, got string) bool {
	ng := Norm(got)
	if ng == "" {
		return false
	}
	for _, w := range wants {
		nw := Norm(w)
		switch {
		case nw == "":
		case nw == ng:
			return true
		case len(nw) >= 4 && len(ng) >= 4 && (strings.Contains(ng, nw) || strings.Contains(nw, ng)):
			return true
		}
	}
	return false
}

// TitleEquals is the strict version of TitleMatches: the titles must be the
// same once punctuation and case are ignored, so "Song (Remix)" is not "Song".
func TitleEquals(wants []string, got string) bool {
	ng := Norm(got)
	if ng == "" {
		return false
	}
	for _, w := range wants {
		if Norm(w) == ng {
			return true
		}
	}
	return false
}

// ---------- small helpers ----------

// ParseClock turns "3:45" or "1:02:03" (yt-dlp's duration_string) into
// seconds. It returns 0 for anything it can't read.
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

// FirstNonEmpty returns the first argument that isn't "".
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
