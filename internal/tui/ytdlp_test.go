package tui

import (
	"errors"
	"testing"
)

func TestParseYtdlpError(t *testing.T) {
	exit1 := errors.New("exit status 1")

	cases := []struct {
		name, stderr, want string
	}{
		{"plain error", "[youtube] abc: Downloading\nERROR: Video unavailable\n", "Video unavailable"},
		{
			"last ERROR wins over trailing warning",
			"ERROR: first\nWARNING: retrying\n", "first",
		},
		{
			"403 is not a network problem",
			"ERROR: unable to download video data: HTTP Error 403: Forbidden\n",
			"unable to download video data: HTTP Error 403: Forbidden",
		},
		{
			"offline",
			"ERROR: [youtube] abc: Unable to download API page: <urlopen error [Errno -3] Temporary failure in name resolution>\n",
			"Network unavailable. Check your internet connection",
		},
		{"no ERROR line falls back", "WARNING: something\n", "exit status 1"},
	}
	for _, c := range cases {
		short, _ := parseYtdlpError(c.stderr, exit1)
		if short != c.want {
			t.Errorf("%s: got %q, want %q", c.name, short, c.want)
		}
	}
}

func TestDetailKeepsTail(t *testing.T) {
	var s string
	for range 30 {
		s += "line\n"
	}
	_, detail := parseYtdlpError(s, errors.New("x"))
	if n := len(splitLines(detail)); n != 12 {
		t.Fatalf("detail has %d lines, want 12", n)
	}
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	return append(out, cur)
}
