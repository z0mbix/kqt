package tui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestInspectorSearchLiveNavigationCancellationAndListIsolation(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 24})
	key(m, "tab")
	selected, filter, history := m.selected, m.filter, len(m.settings.CommandHistory)
	content := ansi.Strip(m.inspectorContent)
	key(m, "/")
	m.Update(tea.PasteMsg{Content: "CNTPRT"})
	if m.mode != "inspector-search" || len(m.inspectorSearch.matches) != 1 || !strings.Contains(m.inspector.View(), "80") {
		t.Fatal("fuzzy search did not find containerPort as it was typed")
	}
	if ansi.Strip(m.inspector.GetContent()) != content || m.inspector.GetContent() == m.inspectorContent {
		t.Fatal("search changed document text or did not highlight the match")
	}
	key(m, "enter")
	if m.mode != "" || m.input.Focused() || !m.inspectorFocused {
		t.Fatal("Enter did not keep the search in the focused inspector")
	}
	key(m, "J")
	if m.inspectorSearch.query != "CNTPRT" || len(m.inspectorSearch.matches) != 1 || !strings.Contains(m.inspector.View(), "80") {
		t.Fatal("JSON switching lost the query or hid its match")
	}
	key(m, "y")
	key(m, "/")
	m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	m.Update(tea.PasteMsg{Content: "name"})
	key(m, "enter")
	count, index := len(m.inspectorSearch.matches), m.inspectorSearch.index
	if count < 2 {
		t.Fatal("expected several name lines for navigation")
	}
	for range count {
		key(m, "n")
	}
	if m.inspectorSearch.index != index {
		t.Fatal("next-match navigation did not wrap")
	}
	key(m, "N")
	if m.inspectorSearch.index != (index-1+count)%count {
		t.Fatal("previous-match navigation did not wrap")
	}
	index, x, y := m.inspectorSearch.index, m.inspector.XOffset(), m.inspector.YOffset()
	key(m, "/")
	m.Update(tea.PasteMsg{Content: "zzzzzzzzzzzz"})
	if len(m.inspectorSearch.matches) != 0 || !strings.Contains(ansi.Strip(m.View().Content), "no matches") || ansi.Strip(m.inspector.GetContent()) != content {
		t.Fatal("no-match search hid the document or lacked feedback")
	}
	key(m, "esc")
	if m.inspectorSearch.query != "name" || m.inspectorSearch.index != index || m.inspector.XOffset() != x || m.inspector.YOffset() != y {
		t.Fatal("cancellation did not restore the previous search and viewport")
	}
	key(m, "esc")
	if m.inspectorSearch.query != "" || !m.inspectorFocused || m.inspector.GetContent() != m.inspectorContent {
		t.Fatal("Escape did not clear search before leaving the inspector")
	}
	key(m, "esc")
	if m.inspectorFocused || m.selected != selected || m.filter != filter || len(m.settings.CommandHistory) != history {
		t.Fatal("inspector search changed list selection, filter, history or focus")
	}
	key(m, "/")
	if m.mode != "filter" {
		t.Fatal("table / no longer filters resources")
	}
}

func TestInspectorSearchWrappedUnicodeAndThemeChanges(t *testing.T) {
	for _, name := range []string{"catppuccin-mocha", "accessible-light", "no-color"} {
		t.Run(name, func(t *testing.T) {
			m := demoModel(t)
			m.Update(tea.WindowSizeMsg{Width: 180, Height: 24})
			m.setTheme(builtThemes()[name])
			key(m, "tab")
			key(m, "/")
			m.inspector.SetWidth(16)
			m.inspector.SetHeight(4)
			plain := strings.Repeat("x", 40) + "\n" + strings.Repeat("x", 16*8) + "東京e\u0301🌲"
			lines := strings.Split(plain, "\n")
			for i, line := range lines {
				lines[i] = m.theme.style("success").Render(line)
			}
			m.inspectorContent = strings.Join(lines, "\n")
			m.Update(tea.PasteMsg{Content: "東京E🌲"})
			if len(m.inspectorSearch.matches) != 1 || !strings.Contains(ansi.Strip(m.inspector.View()), "東京e\u0301🌲") {
				t.Fatal("search did not reveal Unicode in a long wrapped line")
			}
			if ansi.Strip(m.inspector.GetContent()) != plain || ansi.StringWidth(m.inspector.GetContent()) != ansi.StringWidth(plain) {
				t.Fatal("highlighting changed Unicode graphemes or text width")
			}
			if name == "no-color" && hasExplicitColour(m.inspector.GetContent()) {
				t.Fatal("search emitted colours in monochrome mode")
			}
		})
	}
}

func TestInspectorSearchSurvivesRefreshAndClearsOnSelectionOrHiddenPane(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 24})
	key(m, "tab")
	key(m, "/")
	m.Update(tea.PasteMsg{Content: "cntprt"})
	key(m, "enter")
	y := m.inspector.YOffset()
	o, _ := m.current()
	o.SetAnnotations(map[string]string{"new": "an annotation added by a watch event"})
	m.updateInspector()
	m.setTheme(builtThemes()["nord"])
	if m.inspectorSearch.query != "cntprt" || len(m.inspectorSearch.matches) != 1 || m.inspector.YOffset() != y {
		t.Fatal("refresh/theme change lost the search or moved its viewport")
	}
	key(m, "tab")
	key(m, "j")
	if m.inspectorSearch.query != "" || len(m.inspectorSearch.matches) != 0 || m.inspector.YOffset() != 0 {
		t.Fatal("selecting another resource kept the previous search")
	}
	key(m, "tab")
	key(m, "/")
	m.Update(tea.PasteMsg{Content: "cntprt"})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	if m.mode != "" || m.input.Focused() || m.inspectorSearch.query != "" {
		t.Fatal("a disappearing inspector left a hidden search prompt active")
	}
}

func TestInspectorSearchOnlyUsesVisibleSecretValuesAndIsNeverSaved(t *testing.T) {
	m := secretModel(t)
	key(m, "tab")
	key(m, "/")
	m.Update(tea.PasteMsg{Content: "demo-only"})
	if len(m.inspectorSearch.matches) != 0 {
		t.Fatal("search matched hidden Secret values")
	}
	key(m, "enter")
	revealSecret(t, m)
	if len(m.inspectorSearch.matches) != 1 {
		t.Fatal("search did not include an explicitly revealed value")
	}
	key(m, "x")
	assertSecretHidden(t, m)
	if m.inspectorSearch.query != "" || len(m.inspectorSearch.matches) != 0 || strings.Contains(ansi.Strip(m.inspector.GetContent()), "demo-only") {
		t.Fatal("hiding a Secret retained search text or matches")
	}
	data, err := os.ReadFile(m.settings.Path)
	if err != nil || strings.Contains(string(data), "demo-only") || len(m.settings.CommandHistory) != 0 {
		t.Fatal("inspector search leaked into settings or command history", err)
	}
}
