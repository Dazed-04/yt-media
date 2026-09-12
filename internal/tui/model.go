package tui

import (
	"log"
	"os"
	"os/user"

	"yt-downloader/internal/downloader"

	tea "charm.land/bubbletea/v2"
)

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

type JobProgressMsg struct {
	Index    int
	Progress float64
}

type sizeFetchedMsg struct {
	videoID string
	format  string
	sizeStr string
}

type Model struct {
	ChoiceType string
	ChoiceMode string

	Selected   map[int]string
	Searching  bool
	IsFetching bool
	SpinnerIdx int
	Blink      bool
	User       string

	MusicList    []downloader.Video
	FilteredList []downloader.Video
	SearchQuery  string
	Cursor       int
	ListOffset   int
	VisibleListH int
	SkippedItems []string
	previewCache map[string]renderedPreview
	inFlight     map[string]bool

	// Caching for Async Size Fetching
	SizeCache    map[string]string
	SizeInFlight map[string]bool

	prefetchList []downloader.Video
	prefetchIdx  int

	DownloadState   DownloadStatus
	Jobs            []DownloadJob
	ActiveWorkers   int
	MaxWorkers      int
	ProgressChan    chan JobProgressMsg
	TotalItems      int
	TotalDownloaded int

	Width  int
	Height int

	ready        bool
	Err          error
	ErrorMessage string
}

func InitialModel() *Model {
	currentUser, err := user.Current()
	if err != nil {
		log.Fatal("Could not get current user")
	}
	return &Model{
		ChoiceType:    "Audio",
		ChoiceMode:    "Single",
		Searching:     true,
		Blink:         true,
		Selected:      make(map[int]string),
		FilteredList:  []downloader.Video{},
		SizeCache:     make(map[string]string),
		SizeInFlight:  make(map[string]bool),
		User:          currentUser.HomeDir,
		MaxWorkers:    3,
		DownloadState: StatusIdle,
	}
}

func (m *Model) Init() tea.Cmd {
	log.Println("[SYS] Initializing TUI Session")
	return func() tea.Msg {
		return blinkMsg{}
	}
}

func (m *Model) Cleanup() {
	dir := thumbCacheDir()
	if err := os.RemoveAll(dir); err != nil {
		log.Printf("[CLEANUP] failed to remove thumb cache: %v", err)
	}
}

func (m *Model) previewImageDims() (cols, rows int) {
	cols = m.Width - 8
	if cols < 4 {
		cols = 4
	}

	headerH := 4 // Header margin + border + content
	footerH := 2 // Hint line margin + content
	progressH := 0
	if m.DownloadState != StatusIdle {
		progressH = 3 // Progress margin + border + content
	}

	bottomH := 15       // Info & List boxes (13 content lines + 2 borders)
	m.VisibleListH = 13 // Inner content capacity
	artBorders := 2     // Top and bottom borders of the art box

	// Exactly accounts for every vertical line on the screen
	fixedOverhead := headerH + artBorders + bottomH + progressH + footerH

	rows = m.Height - fixedOverhead
	if rows < 4 {
		rows = 4
	}
	return
}

func (m *Model) resetInFlight() {
	m.inFlight = nil
}

func (m *Model) purgePreviewCache() {
	m.previewCache = nil
	m.inFlight = nil
}
