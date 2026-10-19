package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Rendering, text selection and mouse controls share these exact bounds.
func (m *model) modalBounds() paneBounds {
	w, h := max(2, m.width-4), max(2, m.height-4)
	if m.mode == "picker" {
		w, h = min(90, w), min(24, h)
		if m.pickerKind == "inspector-position" {
			w, h = min(50, w), min(10, h)
		}
	}
	if m.smallDeleteModal() {
		w, h = min(66, w), min(12, h)
	}
	return paneBounds{(m.width - w) / 2, (m.height - h) / 2, w, h}
}
func (m *model) smallDeleteModal() bool {
	return m.actionPlan != nil && m.actionPlan.Action == "delete" && (m.mode == "delete-confirm" || m.mode == "action-running")
}
func (m *model) deleteButtons() string {
	no, yes := "  No  ", "  Yes  "
	if m.deleteYes {
		yes = m.theme.style("focus").Bold(true).Reverse(true).Render(yes)
	} else {
		no = m.theme.style("focus").Bold(true).Reverse(true).Render(no)
	}
	text := no + "  " + yes
	return strings.Repeat(" ", max(0, (m.modalBounds().width-2-ansi.StringWidth(text))/2)) + text
}
func (m *model) deleteMouse(v tea.MouseClickMsg) tea.Cmd {
	if v.Button != tea.MouseLeft || v.Mod != 0 {
		return nil
	}
	b := m.modalBounds()
	left := b.x + 1 + max(0, (b.width-2-15)/2)
	if v.Y != b.y+b.height-2 {
		return nil
	}
	if v.X >= left && v.X < left+6 {
		return m.deleteKey(tea.KeyPressMsg{Code: 'n'})
	}
	if v.X >= left+8 && v.X < left+15 {
		return m.deleteKey(tea.KeyPressMsg{Code: 'y'})
	}
	return nil
}
