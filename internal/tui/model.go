package tui

import (
	"fmt"
	"log"
	"os"
	"os/user"
	"time"

	"yt-downloader/internal/config"
	"yt-downloader/internal/downloader"
	"yt-downloader/internal/player"

	tea "charm.land/bubbletea/v2"
)

// Model is the whole application state. It is only ever touched from
// Update/View. Anything run in a tea.Cmd goroutine must work on copies taken
// before the command is returned (see downloadItem, doSearch).
type Model struct {
	Config     config.Config
	ChoiceType string // "Audio" | "Video": default type for new downloads
	ChoiceMode string // "Single" | other: where downloads are filed
	User       string // home directory

	// Search + results
	Searching    bool
	IsFetching   bool
	SpinnerIdx   int
	Blink        bool
	MusicList    []downloader.Video
	FilteredList []downloader.Video
	SearchQuery  string
	Cursor       int
	ListOffset   int
	SkippedItems []string

	// Previews and sizes
	previewCache map[string]renderedPreview
	inFlight     map[string]bool
	prefetchList []downloader.Video
	prefetchIdx  int
	prefetchGen  int
	SizeCache    map[string]string
	SizeInFlight map[string]bool

	// Download queue + failures (persisted to disk between runs)
	Queue         []DownloadReq
	Failed        []FailedItem
	QueueOpen     bool
	QueueCursor   int // one cursor over failed items, then staged items
	ErrorsOpen    bool
	ErrorCursor   int
	ErrorExpanded bool

	// Audio preview (p): a hidden mpv shown as a small player window
	Player      *player.Session
	PlayerOpen  bool
	PlayerTitle string
	playerSeq   int

	// Active batch
	DownloadState   DownloadStatus
	Jobs            []DownloadJob
	ActiveWorkers   int
	MaxWorkers      int
	ProgressChan    chan JobProgressMsg
	TotalItems      int
	TotalDownloaded int

	// Terminal
	Width           int
	Height          int
	Focused         bool // from tea.FocusMsg / tea.BlurMsg
	fontShrinkDelta float64
	ready           bool

	// Terminal escape sequences waiting to be sent through Bubble Tea's
	// output buffer (see Update). Never write them to os.Stdout directly.
	rawOut []string

	// Transient footer message
	Flash    string
	FlashErr bool
	flashSeq int

	Err error
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

// Cleanup restores the terminal and removes temporary files. Call it before
// quitting.
func (m *Model) Cleanup() {
	m.closePlayer()
	m.persistQueue()
	restoreKittyFont(m.fontShrinkDelta)
	deleteAllKittyImages()
	if err := os.RemoveAll(thumbCacheDir()); err != nil {
		log.Printf("[CLEANUP] failed to remove thumb cache: %v", err)
	}
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	m.Cleanup()
	return m, tea.Quit
}

// ---------- selection ----------

// current returns the highlighted result, if there is one.
func (m *Model) current() (downloader.Video, bool) {
	if m.Cursor < 0 || m.Cursor >= len(m.FilteredList) {
		return downloader.Video{}, false
	}
	return m.FilteredList[m.Cursor], true
}

// moveCursor moves the highlight by delta, wrapping at both ends, and keeps
// the rolling list window around it.
func (m *Model) moveCursor(delta int) {
	n := len(m.FilteredList)
	if n == 0 {
		return
	}
	m.Cursor = ((m.Cursor+delta)%n + n) % n

	visible := min(m.listHeight(), n)
	visibleIndex := ((m.Cursor-m.ListOffset)%n + n) % n
	if visibleIndex >= visible {
		if delta > 0 {
			m.ListOffset = (m.ListOffset + 1) % n
		} else {
			m.ListOffset = (m.ListOffset - 1 + n) % n
		}
	}
}

// ---------- footer flash messages ----------

// flash shows text in the footer for a few seconds.
func (m *Model) flash(text string, isErr bool) tea.Cmd {
	m.Flash, m.FlashErr = text, isErr
	m.flashSeq++
	seq := m.flashSeq
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg {
		return flashClearMsg{seq: seq}
	})
}
