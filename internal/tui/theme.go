package tui

import "charm.land/lipgloss/v2"

// One palette for the whole UI (Catppuccin Mocha).
var (
	colText    = lipgloss.Color("#CDD6F4")
	colSubtext = lipgloss.Color("#A6ADC8")
	colDim     = lipgloss.Color("#585B70")
	colBorder  = lipgloss.Color("#313244")
	colCrust   = lipgloss.Color("#11111B")
	colBlue    = lipgloss.Color("#89B4FA")
	colGold    = lipgloss.Color("#F9E2AF")
	colGreen   = lipgloss.Color("#A6E3A1")
	colRed     = lipgloss.Color("#F38BA8")
	colMauve   = lipgloss.Color("#CBA6F7")

	// Background of the popup windows (see overlay.go).
	colWinBG = lipgloss.Color("#181825")
	// colWinFrame is the overlay frame. It is drawn with half-block glyphs that sit
	// on the outer edge of the cell, so the window ends exactly at its border.
	colWinFrame = lipgloss.Color("#6c7a9c")
)
