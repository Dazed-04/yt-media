package tui

import "yt-downloader/internal/downloader"

// ---------- download domain types ----------

type DownloadStatus int

const (
	StatusIdle DownloadStatus = iota
	StatusPending
	StatusDownloading
	StatusDone
	StatusError
)

type DownloadReq struct {
	Video downloader.Video
	Type  string
}

type DownloadJob struct {
	Req      DownloadReq
	Status   DownloadStatus
	Progress float64
	Err      error
}

// ---------- messages ----------

type (
	// UI ticks.
	blinkMsg       struct{}
	spinnerTickMsg struct{}
	flashClearMsg  struct{ seq int }

	// Search.
	searchResultsMsg []downloader.Video
	errorMsg         error
	sizeFetchedMsg   struct{ videoID, format, sizeStr string }

	// Previews. gen ties a prefetch message to the search that started it, so
	// a stale chain from an older search can't advance the current one.
	thumbnailReadyMsg struct {
		videoID string
		gen     int
	}
	previewRenderedMsg struct {
		videoID string
		preview renderedPreview
		err     error
	}

	// Downloads.
	checkLibraryMsg struct {
		ToDownload []DownloadReq
		Skipped    []string
	}
	JobProgressMsg struct {
		Index    int
		Progress float64
	}
	downloadFinishedMsg struct {
		Index  int
		Err    error
		Detail string // tail of yt-dlp's stderr, shown in the error window
	}
)
