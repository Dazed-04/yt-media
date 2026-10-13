package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The bottom section (info box + result list) needs at least this much width.
const minBottomWidth = 60

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// View draws the screen top to bottom: search bar, image, info box + list,
// progress bar, footer. A popup window, when open, is composited on top.
func (m *Model) View() tea.View {
	lay := m.layout()
	artW := m.Width - 4
	showBottom := lay.listH > 0 && len(m.FilteredList) > 0 && artW >= minBottomWidth

	blocks := []string{m.renderSearchBar(), m.renderArt(lay)}
	if showBottom {
		blocks = append(blocks, m.renderBottom(lay.listH, artW))
	} else if v, ok := m.current(); ok {
		hint := fmt.Sprintf("%s  (resize terminal for details)", safeTruncate(v.GetDisplayString(), m.Width-30))
		blocks = append(blocks, lipgloss.NewStyle().Width(m.Width-4).Margin(0, 2).Foreground(colDim).Render(hint))
	}
	if m.DownloadState != StatusIdle {
		blocks = append(blocks, m.renderProgress())
	}
	blocks = append(blocks, m.renderFooter())

	ui := lipgloss.JoinVertical(lipgloss.Left, blocks...)
	appStyle := lipgloss.NewStyle().Width(m.Width).Height(m.Height).Foreground(colText)
	content := appStyle.Render(lipgloss.Place(m.Width, m.Height, lipgloss.Left, lipgloss.Top, ui))

	if m.overlayOpen() {
		content = m.withOverlay(content)
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.ReportFocus = true
	return v
}

// withOverlay draws the open popup window centered over content.
func (m *Model) withOverlay(content string) string {
	box := m.renderQueueOverlay()
	if m.ErrorsOpen {
		box = m.renderErrorsOverlay()
	}
	x := max(0, (m.Width-lipgloss.Width(box))/2)
	y := max(0, (m.Height-lipgloss.Height(box))/2)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(content),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
}

// ---------- 1. search bar ----------

func (m *Model) renderSearchBar() string {
	cursor := "█"
	if !m.Searching || !m.Blink {
		cursor = " "
	}
	text := m.SearchQuery + cursor
	if m.SearchQuery == "" && !m.Searching {
		text = "Press '/' to search..."
	}

	label := " SEARCH "
	if m.IsFetching {
		label = " FETCHING "
	} else if m.Searching {
		label = " INSERT "
	}
	badge := lipgloss.NewStyle().Foreground(colCrust).Background(colBlue).Bold(true).Render(label)

	return lipgloss.NewStyle().
		Width(m.Width-4).
		Align(lipgloss.Left).
		Border(lipgloss.NormalBorder()).
		BorderForeground(colBorder).
		Margin(1, 2, 0, 2).
		Padding(0, 1).
		Render(fmt.Sprintf("%s  %s", badge, text))
}

// ---------- 2. image ----------

func (m *Model) renderArt(lay layout) string {
	var image string
	cur, hasCur := m.current()

	switch {
	case m.IsFetching && len(m.FilteredList) == 0:
		spin := lipgloss.NewStyle().Foreground(colMauve).Render(spinnerFrames[m.SpinnerIdx%len(spinnerFrames)])
		image = lipgloss.Place(lay.cols, lay.artRows, lipgloss.Center, lipgloss.Center, spin+" Fetching YouTube Data...")
	case hasCur:
		if pv, ok := m.previewCache[cur.ID]; ok {
			image = pv.data
		} else {
			image = lipgloss.Place(lay.cols, lay.artRows, lipgloss.Center, lipgloss.Center, "LOADING ART...")
		}
	default:
		image = lipgloss.Place(lay.cols, lay.artRows, lipgloss.Center, lipgloss.Center, "NO MEDIA SELECTED")
	}

	return lipgloss.NewStyle().
		Width(m.Width-4).
		Height(lay.artRows).
		Border(lipgloss.NormalBorder()).
		BorderForeground(colBorder).
		Margin(0, 2, 0, 2). // no bottom margin, so it attaches to the boxes below
		Align(lipgloss.Center).
		Padding(0).
		Render(image)
}

// ---------- 3. info box + result list ----------

func (m *Model) renderBottom(listH, artW int) string {
	infoW := int(float64(artW) * 0.35)
	listW := artW - infoW - 2

	info := m.renderInfoBox(infoW, listH)
	list := m.renderListBox(listW, listH)

	return lipgloss.NewStyle().Margin(0, 2).Render(
		lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().MarginRight(2).Render(info),
			list,
		))
}

func (m *Model) renderInfoBox(infoW, listH int) string {
	cur, _ := m.current()
	valW := max(infoW-14, 2)

	labelStyle := lipgloss.NewStyle().Width(10).Align(lipgloss.Left).Foreground(colBlue).Bold(true)
	valueStyle := lipgloss.NewStyle().Foreground(colSubtext)

	row := func(label, value string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			labelStyle.Render(label),
			valueStyle.Width(valW).Align(lipgloss.Right).Render(safeTruncate(value, valW)),
		)
	}

	// The name wraps over several lines (capped); the other rows are single-line.
	var nameRows []string
	for i, ln := range wrapLines(cur.Title, valW, maxNameLines) {
		label := lipgloss.NewStyle().Width(10).Render("")
		if i == 0 {
			label = labelStyle.Render("Name:")
		}
		nameRows = append(nameRows, lipgloss.JoinHorizontal(lipgloss.Top,
			label, valueStyle.Width(valW).Align(lipgloss.Left).Render(ln)))
	}

	selectedType := m.ChoiceType
	if qi := m.queueIndex(cur.ID); qi >= 0 {
		selectedType = m.Queue[qi].Type
	}
	sizeStr := "Fetching..."
	if s, ok := m.SizeCache[cur.ID+"-"+selectedType]; ok {
		sizeStr = fmt.Sprintf("%s (%s)", s, selectedType)
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		strings.Join(nameRows, "\n"),
		row("Artist:", cur.Artist),
		row("Channel:", cur.Channel),
		row("Size:", sizeStr),
		row("Duration:", fmt.Sprintf("%v", cur.Duration)),
	)
	body = padLines(body, listH) // keep the box a fixed height

	return lipgloss.NewStyle().
		Width(infoW).
		Height(listH).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		Padding(0, 1).
		Render(body)
}

func (m *Model) renderListBox(listW, listH int) string {
	var b strings.Builder
	n := len(m.FilteredList)

	for row := range min(listH, n) {
		i := (m.ListOffset + row) % n
		b.WriteString(m.renderListRow(i, listW))
		b.WriteByte('\n')
	}
	body := padLines(strings.TrimSuffix(b.String(), "\n"), listH)

	return lipgloss.NewStyle().
		Width(listW).
		Height(listH).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		Padding(0, 1).
		Render(body)
}

// renderListRow draws result i: bullet, mode badge (queued or highlighted),
// and the title cut to fit.
func (m *Model) renderListRow(i, listW int) string {
	video := m.FilteredList[i]
	selected := i == m.Cursor

	queuedType, isQueued := "", false
	if qi := m.queueIndex(video.ID); qi >= 0 {
		queuedType, isQueued = m.Queue[qi].Type, true
	}

	bullet := "○"
	if isQueued {
		bullet = "●"
	}

	textW := max(listW-10, 5)
	if isQueued || selected { // room for the badge
		textW -= 4
	}
	textW = max(textW, 3)

	var badge string
	switch {
	case isQueued:
		style := lipgloss.NewStyle().Foreground(colCrust).Background(colMauve).Padding(0, 1)
		if queuedType == "Audio" {
			badge = style.Render("A") + " "
		} else {
			badge = style.Render("V") + " "
		}
	case selected:
		badge = lipgloss.NewStyle().Foreground(colDim).Render(fmt.Sprintf("[%c] ", m.ChoiceType[0]))
	}

	line := lipgloss.NewStyle().Width(textW).MaxHeight(1)
	switch {
	case selected:
		bullet = lipgloss.NewStyle().Foreground(colGold).Render(bullet)
		line = line.Foreground(colGold).Bold(true)
	case isQueued:
		bullet = lipgloss.NewStyle().Foreground(colGreen).Render(bullet)
	}

	return fmt.Sprintf("%s  %s%s", bullet, badge, line.Render(safeTruncate(video.GetDisplayString(), textW)))
}

// padLines adds blank lines until s has at least n lines.
func padLines(s string, n int) string {
	if have := strings.Count(s, "\n") + 1; have < n {
		s += strings.Repeat("\n", n-have)
	}
	return s
}

// ---------- 4. progress bar ----------

func (m *Model) renderProgress() string {
	st := m.batchStats()
	barW := m.Width - 4

	counts := " [ 󰊠 : 󰊠 ] "
	if st.total > 0 {
		counts = fmt.Sprintf(" [%d : %d] ", st.done, st.goal())
	}
	left := lipgloss.NewStyle().Foreground(colBlue).Bold(true).Render(counts)

	// Failures are shown next to the counter, not folded into the bar.
	failed := ""
	if st.failed > 0 {
		failed = lipgloss.NewStyle().Foreground(colRed).Bold(true).Render(fmt.Sprintf("✗ %d", st.failed))
	}
	sep := lipgloss.NewStyle().Foreground(colBorder).Render(" │ ")

	rightW := max(barW-lipgloss.Width(left)-lipgloss.Width(failed)-lipgloss.Width(sep)-4, 5)

	ratio := 0.0
	if goal := st.goal(); goal > 0 {
		ratio = st.progress / float64(goal)
	}
	filled := min(max(int(ratio*float64(rightW)), 0), rightW)

	var bar string
	switch {
	case filled <= 0:
		bar = strings.Repeat("·", rightW)
	case filled >= rightW:
		bar = strings.Repeat("•", rightW-1) + "C"
	default:
		bar = strings.Repeat("•", filled-1) + "C" + strings.Repeat("·", rightW-filled)
	}
	right := lipgloss.NewStyle().Foreground(colGreen).Render(bar)

	return lipgloss.NewStyle().
		Width(barW).
		Border(lipgloss.NormalBorder()).BorderForeground(colBorder).
		Margin(0, 2, 0, 2).
		Render(lipgloss.JoinHorizontal(lipgloss.Center, left, failed, sep, right))
}

// ---------- 5. footer ----------

func (m *Model) renderFooter() string {
	text := fmt.Sprintf("(esc: reset • /: search • tab: default %s • space: queue • w: queue [%d] • e: errors [%d] • enter: download • r: retry)",
		m.ChoiceType, len(m.Queue), len(m.Failed))
	color := colDim

	if m.Flash != "" {
		text, color = m.Flash, colGreen
		if m.FlashErr {
			text, color = "⚠ "+m.Flash, colRed
		}
	}

	return lipgloss.NewStyle().
		Width(m.Width).
		Align(lipgloss.Center).
		Foreground(color).
		MarginTop(1).
		Render(safeTruncate(text, m.Width-2))
}
