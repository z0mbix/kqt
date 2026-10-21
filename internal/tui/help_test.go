package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func typeHelpQuery(m *model, query string) {
	for _, r := range query {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestHelpFuzzySearchMatchesKeysDescriptionsSectionsAndNotes(t *testing.T) {
	m := demoModel(t)
	m.openHelp()
	for _, tc := range []struct{ query, want string }{
		{"rllts", "H / :rollouts"},
		{"clpbrd", "clipboard"},
		{"appearance", ":layouts"},
		{"osc52", "OSC52"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			m.helpQuery = tc.query
			if text := ansi.Strip(m.renderHelp() + "\n" + m.renderHelpNotes()); !strings.Contains(text, tc.want) {
				t.Fatalf("missing %q in fuzzy results: %s", tc.want, text)
			}
		})
	}
	m.helpQuery = "rollouts"
	text := m.renderHelp()
	if !strings.Contains(text, m.theme.style("accent").Bold(true).Render("H / :rollouts")) || strings.Contains(ansi.Strip(text), "Move through resources") {
		t.Fatal("filter lost key styling or retained unrelated shortcuts")
	}
}

func TestHelpSearchIsLiveAndKeepsResourceAndHistoryState(t *testing.T) {
	m := demoModel(t)
	m.filter = "web"
	m.selected = 3
	selected, namespace, history := m.selected, m.route.namespace, len(m.settings.CommandHistory)
	key(m, "?")
	key(m, "/")
	typeHelpQuery(m, "rllts")
	if m.mode != "help-search" || m.helpQuery != "rllts" || !strings.Contains(ansi.Strip(m.panel.GetContent()), "Deployment revisions") || !strings.Contains(m.panelTitle, "matches") {
		t.Fatal("search did not update live")
	}
	key(m, "enter")
	if m.mode != "help" || m.input.Focused() || !strings.Contains(m.panelTitle, "/rllts") {
		t.Fatal("Enter did not keep the filtered results")
	}
	key(m, "esc")
	if m.mode != "" || m.filter != "web" || m.selected != selected || m.route.namespace != namespace || len(m.settings.CommandHistory) != history {
		t.Fatal("help search changed resource scope, selection, filter or history")
	}
	m.command("help")
	if m.helpQuery != "" || !strings.Contains(ansi.Strip(m.panel.GetContent()), "Navigation") {
		t.Fatal(":help retained the previous search")
	}
}

func TestHelpSearchPasteCancellationAndNoMatches(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.openHelp()
	m.panel.GotoBottom()
	offset := m.panel.YOffset()
	key(m, "/")
	m.Update(tea.PasteMsg{Content: "quit"})
	if m.helpQuery != "quit" || !strings.Contains(ansi.Strip(m.panel.GetContent()), "Quit") || m.panel.YOffset() != 0 {
		t.Fatal("paste did not update search or typed q closed help")
	}
	key(m, "esc")
	if m.helpQuery != "" || m.mode != "help" || m.panel.YOffset() != offset {
		t.Fatal("cancel did not restore the prior query and scroll position")
	}
	key(m, "/")
	typeHelpQuery(m, "rllts")
	key(m, "enter")
	key(m, "/")
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	typeHelpQuery(m, "zzzzzzzzzzzzzzzz")
	if !strings.Contains(ansi.Strip(m.panel.GetContent()), "No help matches") {
		t.Fatal("missing no-results state")
	}
	key(m, "esc")
	if m.helpQuery != "rllts" || !strings.Contains(ansi.Strip(m.panel.GetContent()), "Deployment revisions") {
		t.Fatal("cancel lost the retained search")
	}
	key(m, "/")
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m.helpQuery != "" || !strings.Contains(ansi.Strip(m.panel.GetContent()), "Navigation") {
		t.Fatal("clearing search did not restore full help")
	}
	typeHelpQuery(m, "zzzzzzzzzzzzzzzz")
	key(m, "enter")
	if m.mode != "help" || !strings.Contains(m.panel.GetContent(), "Press / to change") {
		t.Fatal("retained empty results kept editing instructions")
	}
}

func TestHelpSearchScrollResizeAndThemeChanges(t *testing.T) {
	m := demoModel(t)
	for _, size := range [][2]int{{40, 12}, {80, 24}, {180, 48}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.openHelp()
		key(m, "/")
		typeHelpQuery(m, "e")
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		if m.input.Value() != "e" || m.panel.YOffset() == 0 {
			t.Fatal("arrow navigation edited the query or did not scroll")
		}
		for _, name := range []string{"nord", "no-color"} {
			m.setTheme(builtThemes()[name])
			view := m.View().Content
			if m.helpQuery != "e" || !strings.Contains(m.panelTitle, "/e") || m.panel.YOffset() == 0 {
				t.Fatal("theme change reset search or scrolling")
			}
			lines := strings.Split(view, "\n")
			if len(lines) != size[1] {
				t.Fatalf("help search height %d, want %d", len(lines), size[1])
			}
			for _, line := range lines {
				if ansi.StringWidth(line) != size[0] {
					t.Fatal("help search exceeded terminal width")
				}
			}
			if name == "no-color" && hasExplicitColour(view) {
				t.Fatal("no-color search retained colours")
			}
		}
	}
}
