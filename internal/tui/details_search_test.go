package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func replaceDocumentQuery(m *model, query string) {
	key(m, "/")
	m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	m.Update(tea.PasteMsg{Content: query})
}

func TestDetailsSearchLiveMatchingNavigationAndFormatSwitches(t *testing.T) {
	for _, open := range []string{"y", "J"} {
		t.Run(open, func(t *testing.T) {
			m := demoModel(t)
			m.selectLayout("classic")
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			selected, filter, history := m.selected, m.filter, len(m.settings.CommandHistory)
			key(m, open)
			plain := ansi.Strip(m.panel.GetContent())
			wantExport, err := m.exportContent(m.manifestFormat(), false)
			if err != nil {
				t.Fatal(err)
			}
			replaceDocumentQuery(m, "CNTPRT")
			if m.mode != "details-search" || len(m.detailsSearch.matches) != 1 || !strings.Contains(m.panel.View(), "80") || !strings.Contains(ansi.Strip(m.View().Content), "1/1 matches") {
				t.Fatal("full resource search did not find and show containerPort live")
			}
			if ansi.Strip(m.panel.GetContent()) != plain || m.panel.GetContent() == m.detailContent {
				t.Fatal("search hid document lines or did not highlight matches")
			}
			key(m, "enter")
			if m.mode != "details" || m.input.Focused() {
				t.Fatal("Enter did not keep search in the full resource view")
			}
			other := "J"
			if open == "J" {
				other = "y"
			}
			key(m, other)
			if m.detailsSearch.query != "CNTPRT" || len(m.detailsSearch.matches) != 1 || !strings.Contains(m.panel.View(), "80") {
				t.Fatal("format switching lost the query or match position")
			}
			key(m, open)
			gotExport, err := m.exportContent(m.manifestFormat(), false)
			if err != nil || gotExport != wantExport || strings.ContainsRune(gotExport, '\x1b') {
				t.Fatal("search changed complete exported resource content", err)
			}
			replaceDocumentQuery(m, "name")
			count, index := len(m.detailsSearch.matches), m.detailsSearch.index
			if count < 2 {
				t.Fatal("expected multiple name lines")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			if m.detailsSearch.index != (index+1)%count {
				t.Fatal("Down did not navigate matches while typing")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
			key(m, "enter")
			for range count {
				key(m, "n")
			}
			if m.detailsSearch.index != index {
				t.Fatal("next-match navigation did not wrap")
			}
			key(m, "N")
			if m.detailsSearch.index != (index-1+count)%count {
				t.Fatal("previous-match navigation did not wrap")
			}
			index, x, y := m.detailsSearch.index, m.panel.XOffset(), m.panel.YOffset()
			m.setTheme(builtThemes()["no-color"])
			if m.detailsSearch.query != "name" || m.detailsSearch.index != index || hasExplicitColour(m.panel.GetContent()) {
				t.Fatal("theme change lost search or emitted explicit colours in monochrome mode")
			}
			replaceDocumentQuery(m, "zzzzzzzzzzzz")
			if len(m.detailsSearch.matches) != 0 || !strings.Contains(ansi.Strip(m.View().Content), "no matches") || ansi.Strip(m.panel.GetContent()) != plain {
				t.Fatal("empty results hid the document or lacked feedback")
			}
			key(m, "esc")
			if m.mode != "details" || m.detailsSearch.query != "name" || m.detailsSearch.index != index || m.panel.XOffset() != x || m.panel.YOffset() != y {
				t.Fatal("cancellation lost the previous search or viewport")
			}
			key(m, "esc")
			if m.mode != "details" || m.detailsSearch.query != "" || m.panel.GetContent() != m.detailContent {
				t.Fatal("Escape did not clear search before closing the resource view")
			}
			key(m, "esc")
			key(m, open)
			if m.detailsSearch.query != "" || strings.Contains(m.detailsSearch.label(), "matches") {
				t.Fatal("reopening retained the previous search")
			}
			if m.selected != selected || m.filter != filter || len(m.settings.CommandHistory) != history {
				t.Fatal("resource search changed list selection, filter or command history")
			}
		})
	}
}

func TestDetailsSearchLongUnicodeLinesRefreshAndCancellation(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	o, _ := m.current()
	annotations := map[string]string{"zz-last": strings.Repeat("x", 220) + "東京🌲"}
	for i := range 40 {
		annotations[fmt.Sprintf("a-%02d", i)] = "padding"
	}
	o.SetAnnotations(annotations)
	m.updateInspector()
	key(m, "J")
	replaceDocumentQuery(m, "東京🌲")
	key(m, "enter")
	if m.panel.XOffset() == 0 || m.panel.YOffset() == 0 || !strings.Contains(ansi.Strip(m.panel.View()), "東京🌲") {
		t.Fatal("full resource search did not reveal a long Unicode value horizontally and vertically")
	}
	x, y := m.panel.XOffset(), m.panel.YOffset()
	o.SetLabels(map[string]string{"new": "watch-update"})
	m.updateInspector()
	if m.detailsSearch.query != "東京🌲" || len(m.detailsSearch.matches) != 1 || m.panel.XOffset() != x || m.panel.YOffset() != y {
		t.Fatal("refresh discarded search or moved the viewport")
	}
	replaceDocumentQuery(m, "kind")
	key(m, "esc")
	if m.detailsSearch.query != "東京🌲" || m.panel.XOffset() != x || m.panel.YOffset() != y {
		t.Fatal("cancellation failed to restore horizontal and vertical position")
	}
	replaceDocumentQuery(m, "東京🌲")
	m.move(1)
	if m.mode != "details" || m.input.Focused() || m.detailsSearch.query != "" || len(m.detailsSearch.matches) != 0 || strings.Contains(m.detailContent, "東京🌲") {
		t.Fatal("resource replacement retained an old query, prompt or document")
	}
}

func TestDetailsAndInspectorKeepSeparateSearches(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 48})
	key(m, "tab")
	replaceDocumentQuery(m, "cntprt")
	key(m, "enter")
	key(m, "tab")
	key(m, "y")
	if m.detailsSearch.query != "" || m.inspectorSearch.query != "cntprt" {
		t.Fatal("opening the full resource view copied or cleared the inspector query")
	}
	replaceDocumentQuery(m, "name")
	key(m, "enter")
	if m.detailsSearch.query != "name" || m.inspectorSearch.query != "cntprt" {
		t.Fatal("resource view search changed the inspector query")
	}
	key(m, "q")
	if m.mode != "" || m.detailsSearch.query != "" {
		t.Fatal("q did not close the resource view and clear its query")
	}
}

func TestDetailsSearchOnlyMatchesVisibleSecretsAndPurgesCachedValues(t *testing.T) {
	for _, open := range []string{"y", "J"} {
		t.Run(open, func(t *testing.T) {
			m := secretModel(t)
			key(m, open)
			replaceDocumentQuery(m, "demo-only")
			if len(m.detailsSearch.matches) != 0 {
				t.Fatal("full resource search matched hidden Secret values")
			}
			key(m, "enter")
			revealSecret(t, m)
			if len(m.detailsSearch.matches) != 1 {
				t.Fatal("search did not include explicitly revealed values")
			}
			key(m, "x")
			assertSecretHidden(t, m)
			if m.detailsSearch.query != "" || len(m.detailsSearch.matches) != 0 || strings.Contains(m.detailContent, "demo-only") {
				t.Fatal("hiding a Secret retained cached values or matches")
			}
			revealSecret(t, m)
			replaceDocumentQuery(m, "demo-only")
			m.clearSecret() // A watch refresh can hide changed values during typing.
			m.updateInspector()
			if m.mode != "details" || m.input.Focused() || m.input.Value() != "" || strings.Contains(m.detailContent+m.panel.GetContent(), "demo-only") {
				t.Fatal("hiding values while typing retained a secret query or document")
			}
			data, err := os.ReadFile(m.settings.Path)
			if err != nil || strings.Contains(string(data), "demo-only") || len(m.settings.CommandHistory) != 0 {
				t.Fatal("resource search leaked into config or command history", err)
			}
		})
	}
}
