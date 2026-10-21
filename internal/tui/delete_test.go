package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestDeleteNoByDefaultAndCompactCentred(t *testing.T) {
	m := demoModel(t)
	for _, size := range [][2]int{{40, 12}, {80, 24}, {180, 48}} {
		m.width, m.height = size[0], size[1]
		m.actionCommand("delete", "")
		b := m.modalBounds()
		if b.x != (m.width-b.width)/2 || b.y != (m.height-b.height)/2 || b.width > 66 || b.height > 12 {
			t.Fatalf("bad modal bounds %#v", b)
		}
		if m.mode != "delete-confirm" || m.deleteYes || m.input.Focused() {
			t.Fatal("delete is not a default-No button dialog")
		}
		view := ansi.Strip(m.View().Content)
		if !strings.Contains(view, "No") || !strings.Contains(view, "Yes") || strings.Contains(view, "Confirm:") {
			t.Fatal("missing Yes/No buttons")
		}
		key(m, "enter")
		if m.mode != "" || m.actionPlan != nil {
			t.Fatal("Enter on default No did not cancel")
		}
		if len(m.rows) != 12 {
			t.Fatal("No deleted a resource")
		}
	}
}

func TestDeleteYesUsesExistingActionAndRecordsOnlySuccess(t *testing.T) {
	m := demoModel(t)
	runSubmitted(m, "delete")
	if len(m.settings.CommandHistory) != 0 {
		t.Fatal("unconfirmed delete entered history")
	}
	m.deleteKey(tea.KeyPressMsg{Code: tea.KeyRight})
	cmd := m.deleteKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Yes did not delete")
	}
	_, refresh := m.Update(cmd())
	m.Update(refresh())
	if len(m.rows) != 11 || len(m.settings.CommandHistory) != 1 || m.settings.CommandHistory[0] != "delete" {
		t.Fatal("confirmed deletion or history failed")
	}
}

func TestDeleteMouseUsesVisibleButtonBounds(t *testing.T) {
	m := demoModel(t)
	m.width, m.height = 180, 48
	m.actionCommand("delete", "")
	b := m.modalBounds()
	left := b.x + 1 + (b.width-2-15)/2
	if m.deleteMouse(tea.MouseClickMsg{X: left + 9, Y: b.y + b.height - 3, Button: tea.MouseLeft}) != nil {
		t.Fatal("click outside button row deleted")
	}
	if cmd := m.deleteMouse(tea.MouseClickMsg{X: left + 9, Y: b.y + b.height - 2, Button: tea.MouseLeft}); cmd == nil || m.mode != "action-running" {
		t.Fatal("Yes button did not use the visible bounds")
	}
}
