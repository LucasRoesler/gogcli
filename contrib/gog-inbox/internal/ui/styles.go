package ui

import "charm.land/lipgloss/v2"

// Basic ANSI colors, so the UI follows the terminal and zellij theme.
var (
	colAccent = lipgloss.Color("4")
	colNow    = lipgloss.Color("1")
	colWarn   = lipgloss.Color("3")
	colOK     = lipgloss.Color("2")

	stBold      = lipgloss.NewStyle().Bold(true)
	stDim       = lipgloss.NewStyle().Faint(true)
	stTab       = lipgloss.NewStyle().Bold(true).Reverse(true)
	stSel       = lipgloss.NewStyle().Reverse(true)
	stNow       = lipgloss.NewStyle().Foreground(colNow).Bold(true)
	stLive      = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	stWarn      = lipgloss.NewStyle().Foreground(colWarn)
	stOK        = lipgloss.NewStyle().Foreground(colOK)
	stLabel     = lipgloss.NewStyle().Faint(true).Bold(true)
	stStrike    = lipgloss.NewStyle().Strikethrough(true).Faint(true)
	stLinkBase  = lipgloss.NewStyle().Foreground(colAccent).Underline(true).Bold(true)
	stTitleFoc  = lipgloss.NewStyle().Bold(true).Reverse(true)
	stTitleBlur = lipgloss.NewStyle().Bold(true).Faint(true)
)

// link renders text as an OSC 8 hyperlink, so a modifier-click in ghostty opens
// it even when the app holds the mouse.
func link(text, url string) string {
	return stLinkBase.Hyperlink(url).Render(text)
}
