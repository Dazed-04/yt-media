package tui

import (
	"fmt"
	"log"
	"os"
	"os/user"

	"yt-downloader/internal/config"
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
	Config     config.Config
	ChoiceType string
	ChoiceMode string

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

	// Download queue (persisted to disk between runs)
	Queue       []DownloadReq
	QueueOpen   bool
	QueueCursor int

	Focused bool // terminal focus, from tea.FocusMsg / tea.BlurMsg

	Flash    string // transient footer message
	FlashErr bool
	flashSeq int

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

	Width           int
	Height          int
	fontShrinkDelta float64

	ready         bool
	Err           error
	ErrorExpanded bool
	ErrorsOpen    bool
	ErrorCursor   int
	Failed        []FailedItem
}

func InitialModel(cfg config.Config) *Model {
	currentUser, err := user.Current()
	if err != nil {
		log.Fatal("Could not get current user")
	}
	queue, failed := loadQueue()
	return &Model{
		Config:          cfg,
		ChoiceType:      cfg.DefaultDownloadType,
		ChoiceMode:      "Single",
		Searching:       true,
		Blink:           true,
		Focused:         true,
		Queue:           queue,
		Failed:          failed,
		FilteredList:    []downloader.Video{},
		SizeCache:       make(map[string]string),
		SizeInFlight:    make(map[string]bool),
		User:            currentUser.HomeDir,
		MaxWorkers:      max(cfg.MaxWorkers, 1),
		DownloadState:   StatusIdle,
		fontShrinkDelta: cfg.FontShrinkDelta,
	}
}

func (m *Model) Init() tea.Cmd {
	log.Println("[SYS] Initializing TUI Session")
	shrinkKittyFont(m.fontShrinkDelta)
	cmds := []tea.Cmd{func() tea.Msg { return blinkMsg{} }}
	if n := len(m.Queue) + len(m.Failed); n > 0 {
		cmds = append(cmds, m.flash(fmt.Sprintf("Restored %d item(s) - w: queue, e: errors", n), false))
	}
	return tea.Batch(cmds...)
}

func (m *Model) Cleanup() {
	m.persistQueue()
	restoreKittyFont(m.fontShrinkDelta)
	deleteAllKittyImages()
	dir := thumbCacheDir()
	if err := os.RemoveAll(dir); err != nil {
		log.Printf("[CLEANUP] failed to remove thumb cache: %v", err)
	}
}

func (m *Model) moveCursor(delta int) {
	n := len(m.FilteredList)
	if n == 0 {
		return
	}
	m.Cursor = ((m.Cursor+delta)%n + n) % n

	visible := min(m.VisibleListH, n)
	visibleIndex := ((m.Cursor-m.ListOffset)%n + n) % n
	if visibleIndex >= visible {
		if delta > 0 {
			m.ListOffset = (m.ListOffset + 1) % n
		} else {
			m.ListOffset = (m.ListOffset - 1 + n) % n
		}
	}
}

func (m *Model) previewImageDims() (cols, rows int) {
	cols = max(m.Width-8, 4)

	headerH := 4 // Header margin + border + content
	footerH := 2 // Hint line margin + content
	progressH := 0
	if m.DownloadState != StatusIdle {
		progressH = 3 // Progress margin + border + content
	}
	artBorders := 2 // Top and bottom borders of the art box
	bottomBordersH := 2

	const idealListH = 13
	const minListH = 4
	const minArtRows = 4

	fixedOverhead := headerH + artBorders + footerH + progressH

	m.VisibleListH = idealListH
	for m.VisibleListH > 0 {
		rows = m.Height - fixedOverhead - (m.VisibleListH + bottomBordersH)
		if rows >= minArtRows {
			break
		}
		m.VisibleListH--
	}

	if m.VisibleListH < minListH {
		m.VisibleListH = 0
		rows = m.Height - fixedOverhead
	}

	if rows < minArtRows {
		rows = minArtRows
	}
	return
}

func (m *Model) resetInFlight() {
	m.inFlight = nil
}

func (m *Model) purgePreviewCache() {
	for _, pv := range m.previewCache {
		if pv.kittyID != 0 {
			deleteKittyImage(pv.kittyID)
		}
	}
	m.previewCache = nil
	m.inFlight = nil
}
