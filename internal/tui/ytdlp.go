package tui

import (
	"slices"
	"strings"
)

// parseYtdlpError turns yt-dlp's stderr into a one-line message for the UI
// and a longer tail for the error window's detail view. fallback is used when
// yt-dlp printed no "ERROR:" line (usually just "exit status 1").
func parseYtdlpError(stderr string, fallback error) (short, detail string) {
	var lines []string
	for l := range strings.SplitSeq(stderr, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}

	short = fallback.Error()
	for _, line := range slices.Backward(lines) { // the last ERROR:, not the last line (a WARNING can trail it)
		if after, ok := strings.CutPrefix(line, "ERROR:"); ok {
			short = strings.TrimSpace(after)
			break
		}
	}

	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	return friendlyError(short), strings.Join(lines, "\n")
}

// Signs that the machine, not YouTube, is the problem. These are deliberately
// specific: a generic "unable to download" also appears on HTTP 403 and
// similar errors that have nothing to do with connectivity.
var networkErrMarkers = []string{
	"temporary failure in name resolution",
	"name or service not known",
	"failed to resolve",
	"getaddrinfo failed",
	"network is unreachable",
	"no route to host",
	"connection refused",
	"connection timed out",
	"timed out",
}

// friendlyError replaces raw network errors with a readable message and
// leaves everything else untouched.
func friendlyError(msg string) string {
	low := strings.ToLower(msg)
	for _, k := range networkErrMarkers {
		if strings.Contains(low, k) {
			return "Network unavailable. Check your internet connection"
		}
	}
	return msg
}
