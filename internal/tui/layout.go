package tui

// layout is the vertical budget of the main screen. It is computed from the
// terminal size and download state on demand and never stored, so it can't go
// stale and is safe to call from anywhere.
type layout struct {
	cols    int // image width in cells
	artRows int // image height in cells
	listH   int // visible rows of the result list (0 = bottom section hidden)
}

func (m *Model) layout() layout {
	const (
		headerH        = 4 // margin + border + content
		footerH        = 2 // hint line margin + content
		artBorders     = 2
		bottomBordersH = 2
		idealListH     = 13
		minListH       = 4
		minArtRows     = 4
	)

	progressH := 0
	if m.DownloadState != StatusIdle {
		progressH = 3 // margin + border + content
	}
	fixed := headerH + artBorders + footerH + progressH

	listH := idealListH
	rows := 0
	for listH > 0 {
		rows = m.Height - fixed - (listH + bottomBordersH)
		if rows >= minArtRows {
			break
		}
		listH--
	}
	if listH < minListH {
		listH = 0
		rows = m.Height - fixed
	}

	return layout{
		cols:    max(m.Width-8, 4),
		artRows: max(rows, minArtRows),
		listH:   listH,
	}
}

// previewImageDims returns the image size in terminal cells.
func (m *Model) previewImageDims() (cols, rows int) {
	l := m.layout()
	return l.cols, l.artRows
}

// listHeight returns how many result rows fit on screen.
func (m *Model) listHeight() int {
	return m.layout().listH
}
