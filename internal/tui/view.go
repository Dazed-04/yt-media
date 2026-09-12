package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func safeTruncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) > maxLen {
		return string(r[:maxLen])
	}
	return s
}

func (m Model) View() tea.View {
	appStyle := lipgloss.NewStyle().Width(m.Width).Height(m.Height).Foreground(lipgloss.Color("#CDD6F4"))

	// --- 1. Header (Search Bar) ---
	cursorChar := "█"
	if !m.Searching || !m.Blink {
		cursorChar = " "
	}
	searchText := m.SearchQuery + cursorChar
	if m.SearchQuery == "" && !m.Searching {
		searchText = "Press '/' to search..."
	}

	searchPrefix := " SEARCH "
	prefixColor := "#89B4FA"
	if m.IsFetching {
		searchPrefix = " FETCHING "
	} else if m.Searching {
		searchPrefix = " INSERT "
	}

	prefixBadge := lipgloss.NewStyle().Foreground(lipgloss.Color("#11111B")).Background(lipgloss.Color(prefixColor)).Bold(true).Render(searchPrefix)

	searchBarContent := fmt.Sprintf("%s  %s", prefixBadge, searchText)
	headerBox := lipgloss.NewStyle().
		Width(m.Width-4).
		Align(lipgloss.Left).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#313244")).
		Margin(1, 2, 0, 2).
		Padding(0, 1).
		Render(searchBarContent)

	imageCols, imageRows := m.previewImageDims()

	// --- 2. Top Block (Theater Art) ---
	var imageBlock string
	if m.IsFetching && len(m.FilteredList) == 0 {
		spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		spinStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#CBA6F7"))
		imageBlock = lipgloss.Place(imageCols, imageRows, lipgloss.Center, lipgloss.Center,
			fmt.Sprintf("%s Fetching YouTube Data...", spinStyle.Render(spinnerFrames[m.SpinnerIdx%len(spinnerFrames)])))
	} else if len(m.FilteredList) > 0 && m.Cursor < len(m.FilteredList) {
		current := m.FilteredList[m.Cursor]
		if pv, ok := m.previewCache[current.ID]; ok {
			imageBlock = pv.data
		} else {
			imageBlock = lipgloss.Place(imageCols, imageRows, lipgloss.Center, lipgloss.Center, "LOADING ART...")
		}
	} else {
		imageBlock = lipgloss.Place(imageCols, imageRows, lipgloss.Center, lipgloss.Center, "NO MEDIA SELECTED")
	}

	artBox := lipgloss.NewStyle().
		Width(m.Width-4).
		Height(imageRows).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#313244")).
		Margin(0, 2, 0, 2). // Zero bottom margin so it attaches cleanly to the bottom boxes
		Align(lipgloss.Center).
		Padding(0).
		Render(imageBlock)

	// --- 3. Bottom Blocks ---
	var bottomSection string
	if m.VisibleListH > 0 && len(m.FilteredList) > 0 {
		artW := m.Width - 4
		infoW := int(float64(artW) * 0.35)
		listW := artW - infoW - 2

		current := m.FilteredList[m.Cursor]

		makeRow := func(label, value string) string {
			lblBox := lipgloss.NewStyle().Width(10).Align(lipgloss.Left).Foreground(lipgloss.Color("#89B4FA")).Bold(true).Render(label)
			valW := infoW - 14
			if valW < 2 {
				valW = 2
			}
			valBox := lipgloss.NewStyle().Width(valW).Align(lipgloss.Right).Foreground(lipgloss.Color("#A6ADC8")).Render(value)
			return lipgloss.JoinHorizontal(lipgloss.Top, lblBox, valBox)
		}

		selectedType := m.ChoiceType
		if t, ok := m.Selected[m.Cursor]; ok {
			selectedType = t
		}

		// Read Async Fetched Size
		cacheKey := current.ID + "-" + selectedType
		sizeStr := "Fetching..."
		if s, ok := m.SizeCache[cacheKey]; ok {
			sizeStr = fmt.Sprintf("%s (%s)", s, selectedType)
		}

		durationStr := fmt.Sprintf("%v", current.Duration)

		infoUI := lipgloss.JoinVertical(lipgloss.Left,
			makeRow("Name:", current.Title),
			makeRow("Artist:", current.Artist),
			makeRow("Channel:", current.Channel),
			makeRow("Size:", sizeStr),
			makeRow("Duration:", durationStr),
		)

		// Fail-safe to physically force the info box content to be 13 lines tall
		infoLines := strings.Split(infoUI, "\n")
		if len(infoLines) < m.VisibleListH {
			infoUI += strings.Repeat("\n", m.VisibleListH-len(infoLines))
		}

		infoBox := lipgloss.NewStyle().
			Width(infoW).
			Height(m.VisibleListH).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#313244")).
			Padding(0, 1).
			Render(infoUI)

		var listBuilder strings.Builder
		endIdx := m.ListOffset + m.VisibleListH
		if endIdx > len(m.FilteredList) {
			endIdx = len(m.FilteredList)
		}

		for i := m.ListOffset; i < endIdx; i++ {
			// (Keep your existing list loop logic here...)
			video := m.FilteredList[i]
			bullet := "○"
			hasCustomBadge := false

			if _, ok := m.Selected[i]; ok {
				bullet = "●"
				hasCustomBadge = true
			} else if i == m.Cursor {
				hasCustomBadge = true
			}

			availableTextW := listW - 10
			if availableTextW < 5 {
				availableTextW = 5
			}

			effectiveWidth := availableTextW
			if hasCustomBadge {
				effectiveWidth -= 4
			}
			if effectiveWidth < 3 {
				effectiveWidth = 3
			}

			var modeLabel string
			if typ, ok := m.Selected[i]; ok {
				modeBadge := lipgloss.NewStyle().Foreground(lipgloss.Color("#11111B")).Background(lipgloss.Color("#CBA6F7")).Padding(0, 1)
				if typ == "Audio" {
					modeLabel = modeBadge.Render("A") + " "
				} else {
					modeLabel = modeBadge.Render("V") + " "
				}
			} else if i == m.Cursor {
				modeLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#585B70")).Render(fmt.Sprintf("[%c] ", m.ChoiceType[0]))
			}

			lineStyle := lipgloss.NewStyle().Width(effectiveWidth).MaxHeight(1)
			if i == m.Cursor {
				bullet = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF")).Render(bullet)
				lineStyle = lineStyle.Foreground(lipgloss.Color("#F9E2AF")).Bold(true)
			} else if _, ok := m.Selected[i]; ok {
				bullet = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1")).Render(bullet)
			}

			titleStr := safeTruncate(video.GetDisplayString(), effectiveWidth)
			fmt.Fprintf(&listBuilder, "%s  %s%s\n", bullet, modeLabel, lineStyle.Render(titleStr))
		}

		listBox := lipgloss.NewStyle().
			Width(listW).
			Height(m.VisibleListH).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#313244")).
			Padding(0, 1).
			Render(strings.TrimSuffix(listBuilder.String(), "\n"))

		bottomSection = lipgloss.NewStyle().
			Margin(0, 2).
			Render(lipgloss.JoinHorizontal(lipgloss.Top,
				lipgloss.NewStyle().MarginRight(2).Render(infoBox),
				listBox,
			))
	}

	// --- 4. Progress Bar ---
	var progressUI string
	if m.DownloadState != StatusIdle {
		barW := m.Width - 4
		completed := 0
		var totalProg float64
		for _, j := range m.Jobs {
			switch j.Status {
			case StatusDone, StatusError:
				completed++
				totalProg += 1.0
			case StatusDownloading:
				totalProg += j.Progress
			}
		}

		var leftStr string
		if m.TotalItems == 0 {
			leftStr = " [ 󰊠 : 󰊠 ] "
		} else {
			leftStr = fmt.Sprintf(" [%d : %d] ", completed, m.TotalItems)
		}

		leftBox := lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA")).Bold(true).Render(leftStr)
		sepBox := lipgloss.NewStyle().Foreground(lipgloss.Color("#313244")).Render(" │ ")

		rightW := barW - lipgloss.Width(leftBox) - lipgloss.Width(sepBox) - 4
		if rightW < 5 {
			rightW = 5
		}

		progRatio := 0.0
		if m.TotalItems > 0 {
			progRatio = totalProg / float64(m.TotalItems)
		}

		filled := int(progRatio * float64(rightW))
		if filled < 0 {
			filled = 0
		} else if filled > rightW {
			filled = rightW
		}

		var barStr string
		switch {
		case filled <= 0:
			barStr = strings.Repeat("·", rightW)
		case filled >= rightW:
			barStr = strings.Repeat("•", rightW-1) + "C"
		default:
			barStr = strings.Repeat("•", filled-1) + "C" + strings.Repeat("·", rightW-filled)
		}

		rightBox := lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1")).Render(barStr)
		progressContent := lipgloss.JoinHorizontal(lipgloss.Center, leftBox, sepBox, rightBox)

		progressUI = lipgloss.NewStyle().
			Width(barW).
			Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#313244")).
			Margin(0, 2, 0, 2).
			Render(progressContent)
	}

	// --- 5. Footer (Hints) ---
	hintStr := fmt.Sprintf("(esc: reset • /: search • t: default %s • space: select • enter: download)", m.ChoiceType)

	// Single line gap specifically placed *between* the bottom boxes (or progress bar) and the hint line
	footer := lipgloss.NewStyle().
		Width(m.Width).
		Align(lipgloss.Center).
		Foreground(lipgloss.Color("#585B70")).
		MarginTop(1).
		Render(hintStr)

	// --- Assembly ---
	var finalBlocks []string
	finalBlocks = append(finalBlocks, headerBox, artBox)
	if m.VisibleListH > 0 && len(m.FilteredList) > 0 {
		finalBlocks = append(finalBlocks, bottomSection)
	}
	if m.DownloadState != StatusIdle {
		finalBlocks = append(finalBlocks, progressUI)
	}
	finalBlocks = append(finalBlocks, footer)

	finalUI := lipgloss.JoinVertical(lipgloss.Left, finalBlocks...)

	v := tea.NewView(appStyle.Render(lipgloss.Place(m.Width, m.Height, lipgloss.Left, lipgloss.Top, finalUI)))
	v.AltScreen = true
	return v
}
