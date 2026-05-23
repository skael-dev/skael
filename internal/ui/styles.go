package ui

import "github.com/charmbracelet/lipgloss"

// Color palette
var (
	colorGreen  = lipgloss.Color("#22c55e")
	colorRed    = lipgloss.Color("#ef4444")
	colorYellow = lipgloss.Color("#f59e0b")
	colorDim    = lipgloss.Color("#666666")
	colorMuted  = lipgloss.Color("#a0a0a0")
	colorWhite  = lipgloss.Color("#ededed")
)

// Base styles
var (
	styleSuccess = lipgloss.NewStyle().Foreground(colorGreen)
	styleError   = lipgloss.NewStyle().Foreground(colorRed)
	styleWarn    = lipgloss.NewStyle().Foreground(colorYellow)
	styleDim     = lipgloss.NewStyle().Foreground(colorDim)
	styleMuted   = lipgloss.NewStyle().Foreground(colorMuted)
	styleWhite   = lipgloss.NewStyle().Foreground(colorWhite)
	styleBold    = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
	styleCode    = lipgloss.NewStyle().Foreground(colorMuted)
	styleAccent  = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	styleFaint   = lipgloss.NewStyle().Foreground(colorDim)
)
