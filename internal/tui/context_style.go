package tui

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Keep the header and context picker consistent, including production contexts
// hidden behind friendly aliases and the existing non-/pre-production exclusions.
func (m *model) contextNameStyle(canonical, label string, selected bool) lipgloss.Style {
	token := "accent"
	if productionContext(canonical) || productionContext(label) {
		token = "error"
	}
	style := m.theme.style(token).Bold(true)
	if selected && m.theme.Colors["selection_background"] != "default" {
		fg, bg := m.theme.color(token), m.theme.color("selection_background")
		if labelContrast(fg, bg) < 4.5 || labelContrast(ansi.Convert256(fg), ansi.Convert256(bg)) < 4.5 {
			style = m.theme.style("selection_foreground").Bold(true)
		}
	}
	if selected || token == "error" && m.theme.Name == "no-color" {
		style = style.Underline(true)
	}
	if !selected && token == "error" && m.theme.Name == "no-color" {
		style = style.Reverse(true)
	}
	return style
}
