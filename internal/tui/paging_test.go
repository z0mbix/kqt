package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func pageKey(m *model, code rune) {
	m.Update(tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl})
}

func TestResourcePagingUsesVisibleRows(t *testing.T) {
	for _, tc := range []struct {
		layout        string
		width, height int
		page          int
	}{
		{"classic", 80, 24, 18},
		{"inspector", 180, 48, 42},
		{"bottom", 80, 40, 20},
		{"inspector", 80, 24, 11}, // Right inspector falls back to bottom.
		{"classic", 40, 12, 6},
	} {
		t.Run(fmt.Sprintf("%s-%dx%d", tc.layout, tc.width, tc.height), func(t *testing.T) {
			m := demoModel(t)
			m.selectLayout(tc.layout)
			m.Update(tea.WindowSizeMsg{Width: tc.width, Height: tc.height})
			for len(m.visible) < 200 {
				m.visible = append(m.visible, m.visible[0])
			}
			m.move(50)
			for _, pair := range []struct{ down, up tea.KeyPressMsg }{
				{tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}},
				{tea.KeyPressMsg{Code: tea.KeyPgDown}, tea.KeyPressMsg{Code: tea.KeyPgUp}},
			} {
				m.Update(pair.down)
				if m.selected != 50+tc.page {
					t.Fatalf("page down selected %d, want %d", m.selected, 50+tc.page)
				}
				m.Update(pair.up)
				if m.selected != 50 {
					t.Fatalf("page up selected %d, want 50", m.selected)
				}
			}
			pageKey(m, 'd')
			if m.selected != 50+max(1, tc.page/2) {
				t.Fatal("Ctrl+D did not move half a visible page")
			}
			pageKey(m, 'u')
			if m.selected != 50 {
				t.Fatal("Ctrl+U did not return half a page")
			}
			key(m, "G")
			pageKey(m, 'f')
			if m.selected != 199 {
				t.Fatal("paging crossed the last row")
			}
			key(m, "g")
			pageKey(m, 'b')
			if m.selected != 0 {
				t.Fatal("paging crossed the first row")
			}
			m.visible = nil
			pageKey(m, 'f')
			pageKey(m, 'b')
			if m.selected != 0 {
				t.Fatal("empty table paging changed selection")
			}
		})
	}
}

func TestVimPagingScrollsOnlyTheFocusedView(t *testing.T) {
	for _, mode := range []string{"inspector", "details", "logs", "events", "help", "help-notes", "edit-review", "overview", "debug-confirm", "debug-loading", "debug-starting"} {
		t.Run(mode, func(t *testing.T) {
			m := demoModel(t)
			m.selectLayout("inspector")
			m.Update(tea.WindowSizeMsg{Width: 180, Height: 24})
			m.move(3)
			pane := &m.panel
			m.mode = mode
			m.logTail = true
			switch mode {
			case "inspector":
				m.mode, m.inspectorFocused = "", true
				pane = &m.inspector
			case "help", "help-notes":
				m.openHelp()
				m.helpNotesFocused = mode == "help-notes"
				pane = m.helpActivePane()
			}
			pane.SetWidth(40)
			pane.SetHeight(6)
			pane.SetContent(strings.Repeat("line\n", 100))
			pageKey(m, 'f')
			if pane.YOffset() != 6 || m.selected != 3 {
				t.Fatalf("page down: offset %d, selection %d", pane.YOffset(), m.selected)
			}
			pageKey(m, 'b')
			if pane.YOffset() != 0 || m.selected != 3 {
				t.Fatalf("page up: offset %d, selection %d", pane.YOffset(), m.selected)
			}
			if mode == "logs" && m.logTail {
				t.Fatal("scrolling logs up retained automatic following")
			}
			if mode == "help-notes" && m.panel.YOffset() != 0 || mode == "help" && m.helpNotes.YOffset() != 0 {
				t.Fatal("paging moved the inactive help pane")
			}
		})
	}
}

func TestVimPagingPreservesInputCursorBindings(t *testing.T) {
	for _, mode := range []string{"command", "filter", "picker", "help-search", "debug-image"} {
		t.Run(mode, func(t *testing.T) {
			m := demoModel(t)
			m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
			switch mode {
			case "picker":
				m.openPicker("themes")
			case "help-search":
				m.openHelp()
				m.startHelpSearch()
			default:
				m.startInput(mode, "")
			}
			m.input.SetValue("themes")
			m.input.CursorEnd()
			pageKey(m, 'b')
			if m.input.Position() != 5 {
				t.Fatalf("Ctrl+B cursor = %d, want 5", m.input.Position())
			}
			pageKey(m, 'f')
			if m.input.Position() != 6 || m.input.Value() != "themes" || m.selected != 0 || m.panel.YOffset() != 0 {
				t.Fatal("Ctrl+F did not preserve input editing and resource selection")
			}
		})
	}
}
