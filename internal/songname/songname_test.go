package songname

import "testing"

func TestCleaning(t *testing.T) {
	cases := []struct{ in, noise, feat string }{
		{"Song (Official Video)", "Song", "Song"},
		{"Song [Lyrics]", "Song", "Song"},
		{"Song (feat. Someone) (Official Music Video)", "Song (feat. Someone)", "Song"},
		{"Song ft. Someone", "Song ft. Someone", "Song"},
		{"Song - Official Lyric Video", "Song", "Song"},
		{"Song (Live)", "Song (Live)", "Song (Live)"}, // a different version: keep
		{"Stay (With You)", "Stay (With You)", "Stay"},
	}
	for _, c := range cases {
		if got := StripNoise(c.in); got != c.noise {
			t.Errorf("StripNoise(%q) = %q, want %q", c.in, got, c.noise)
		}
		if got := StripFeat(StripNoise(c.in)); got != c.feat {
			t.Errorf("StripFeat(StripNoise(%q)) = %q, want %q", c.in, got, c.feat)
		}
	}
	if got := CleanArtist("The Weeknd - Topic"); got != "The Weeknd" {
		t.Errorf("cleanArtist = %q", got)
	}
	if got := CleanArtist("TaylorSwiftVEVO"); got != "TaylorSwift" {
		t.Errorf("cleanArtist = %q", got)
	}
	if got := PrimaryArtist("A, B & C"); got != "A" {
		t.Errorf("primaryArtist = %q", got)
	}
}

func TestCandidates(t *testing.T) {
	qs := Candidates(Track{Artist: "Taylor Swift", Title: "Taylor Swift - Love Story (feat. X) (Official Video)"})
	has := func(a, title string) bool {
		for _, q := range qs {
			if q.Artist == a && q.Title == title {
				return true
			}
		}
		return false
	}
	if !has("Taylor Swift", "Love Story") {
		t.Errorf("missing split artist/title query: %v", qs)
	}
	if !has("Taylor Swift", "Love Story (feat. X)") {
		t.Errorf("missing with-feat query (some songs keep it): %v", qs)
	}
	if last := qs[len(qs)-1]; last.Artist != "" || last.Title != "Love Story" {
		t.Errorf("last query should be a free-text title search, got %+v", last)
	}
	if len(qs) > MaxQueries {
		t.Errorf("%d queries, max is %d", len(qs), MaxQueries)
	}
	// exact spelling is tried before cleaned ones
	plain := Candidates(Track{Artist: "A", Title: "Song (feat. B)"})
	if plain[0].Title != "Song (feat. B)" || plain[1].Title != "Song" {
		t.Errorf("order wrong: %v", plain)
	}
}

func TestUnsanitizeAndBrackets(t *testing.T) {
	if got := Unsanitize("Spider-Man： Across ｜ ＂Calling＂ ⧸ x"); got != `Spider-Man: Across | "Calling" / x` {
		t.Errorf("Unsanitize = %q", got)
	}
	for in, want := range map[string]string{
		"Annihilate (Spider-Man: Across the Spider-Verse)": "Annihilate",
		"Heathens [From Suicide Squad]":                    "Heathens",
		"Song (Live)":                                      "Song (Live)", // another version: kept
		"Song (Remix) (From The Film)":                     "Song (Remix)",
		"No Brackets":                                      "No Brackets",
	} {
		if got := StripBrackets(in); got != want {
			t.Errorf("StripBrackets(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestColonSplitAndNames(t *testing.T) {
	a, ti, ok := SplitArtistTitle(StripNoise("twenty one pilots： Heathens (from Suicide Squad： The Album) [OFFICIAL VIDEO]"))
	if !ok || a != "twenty one pilots" || ti != "Heathens (from Suicide Squad: The Album)" {
		t.Errorf("split = %q, %q, %v", a, ti, ok)
	}
	// a dash wins over a colon
	if a, ti, _ := SplitArtistTitle("A - B: C"); a != "A" || ti != "B: C" {
		t.Errorf("dash split = %q, %q", a, ti)
	}
	// a title that merely contains a colon inside brackets is not split
	if _, _, ok := SplitArtistTitle("Heathens (from Suicide Squad: The Album)"); ok {
		t.Error("split on a colon inside brackets")
	}
	variants := TitleVariants(Track{Title: "Annihilate (Spider-Man： Across the Spider-Verse)"})
	found := false
	for _, v := range variants {
		if v == "Annihilate" {
			found = true
		}
	}
	if !found {
		t.Errorf("no bracket-free variant: %v", variants)
	}
}

func TestCleanestFirst(t *testing.T) {
	qs := CleanestFirst(Track{Artist: "Metro Boomin", Title: "Annihilate (Spider-Man: Across the Spider-Verse) [Official Video]"})
	if qs[0].Title != "Annihilate" {
		t.Errorf("first query = %+v, want the bare title", qs[0])
	}
	if len(qs) > MaxQueries {
		t.Errorf("%d queries", len(qs))
	}
	if last := qs[len(qs)-1]; last.Artist != "" {
		t.Errorf("last = %+v, want the free-text search", last)
	}
}

func TestRealWorldYouTubeNames(t *testing.T) {
	cases := []struct {
		name, artist, tagTitle string // as read from the file
		wantTitle              string // a title variant that must exist
		wantArtist             string // a known artist that must exist
	}{
		{"Coldplay- Sparks (Visualiser)", "", "", "Sparks", "Coldplay"},
		{"Dominic Fike ＂Babydoll＂ (Official Audio)", "", "", "Babydoll", "Dominic Fike"},
		{"Strawberry Skies ~ Kid Travis (Lyric Video)", "Kid Travis", "", "Strawberry Skies", "Kid Travis"},
		{"Denzel Curry, Gizzle, Bren Joy - Dynasties & Dystopia ｜ Arcane League of Legends ｜ Riot Games Music", "", "", "Dynasties & Dystopia", "Denzel Curry"},
		{"Metro Boomin & Swae Lee, Lil Wayne, Offset “Annihilate – Spider-Man： Across the Spider-Verse”", "", "", "Annihilate", "Metro Boomin"},
		{"Paint The Town Blue (from the series Arcane League of Legends)", "Arcane", "", "Paint The Town Blue", "Arcane"},
	}
	for _, c := range cases {
		tr := Track{Artist: c.artist, Title: StripNoise(Unsanitize(c.name))}
		hasT, hasA := false, false
		for _, v := range TitleVariants(tr) {
			hasT = hasT || v == c.wantTitle
		}
		for _, a := range KnownArtists(tr) {
			hasA = hasA || a == c.wantArtist
		}
		if !hasT || !hasA {
			t.Errorf("%q: title %q found=%v, artist %q found=%v\nvariants %q\nartists %q", c.name, c.wantTitle, hasT, c.wantArtist, hasA, TitleVariants(tr), KnownArtists(tr))
		}
		// and a query for it is actually made
		found := false
		for _, q := range CleanestFirst(tr) {
			found = found || q.Title == c.wantTitle
		}
		if !found {
			t.Errorf("%q: no query for title %q: %v", c.name, c.wantTitle, CleanestFirst(tr))
		}
	}
}
