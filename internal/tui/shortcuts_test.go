package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestShortcutColoursFollowThemeWithoutChangingGeometry(t *testing.T) {
	m := demoModel(t)
	for name, palette := range builtThemes() {
		t.Run(name, func(t *testing.T) {
			m.theme = palette
			for _, width := range []int{40, 80, 180} {
				m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
				for _, mode := range []string{"", "command", "history-search", "logs", "events", "rollout-details", "action-confirm", "picker", "help"} {
					m.mode = mode
					m.panelTitle = "Resource"
					if mode == "help" {
						m.panel.SetContent(m.renderHelp())
					}
					view := m.View().Content
					lines := strings.Split(view, "\n")
					if len(lines) != 24 {
						t.Fatalf("%s: height = %d", mode, len(lines))
					}
					for i, line := range lines {
						if ansi.StringWidth(line) != width {
							t.Fatalf("%s: line %d width = %d, want %d: %q", mode, i, ansi.StringWidth(line), width, line)
						}
					}
				}
			}
			m.mode = ""
			help := m.footerHelp(dimensions{})
			if !strings.HasPrefix(ansi.Strip(help), " Explorer · e: edit  j/k: move  [ / ]: sort column") ||
				!strings.Contains(help, palette.style("accent").Bold(true).Render("j/k")) ||
				!strings.Contains(help, palette.style("muted").Render(": move")) {
				t.Fatal("footer lost its key/description colours")
			}
			frame := m.frame("Resource", "", 180, 4, true, shortcut{"Esc", "close"})
			if !strings.Contains(frame, palette.style("accent").Bold(true).Render("Esc")) ||
				!strings.Contains(frame, palette.style("muted").Render(": close")) {
				t.Fatal("frame lost its key/description colours")
			}
		})
	}
}

func TestShortcutTitlesAndHistoryKeepUserTextLiteral(t *testing.T) {
	m := demoModel(t)
	title := "pod-Esc close-界\x1b]52;c;payload\a"
	frame := ansi.Strip(m.frame(title, "", 180, 4, true, shortcut{"Esc", "close"}))
	if strings.ContainsAny(frame, "\x1b\a") || !strings.Contains(frame, "pod-Esc close-界") || !strings.Contains(frame, "Esc: close") {
		t.Fatal("title sanitization or literal resource name changed")
	}
	m.historySearch = &historySearch{entries: []string{"labels Enter run\x1b]52;c;payload\a"}, index: 0}
	preview := ansi.Strip(m.historySearchPreview())
	if strings.ContainsAny(preview, "\x1b\a") || !strings.Contains(preview, "labels Enter run") || !strings.Contains(preview, "Enter: run") {
		t.Fatal("history text was interpreted as a shortcut or terminal controls")
	}
	m.panel.SetWidth(100)
	m.resourcePanelTab = resourceAnnotationsTab
	if tabs := ansi.Strip(m.resourcePanelTabs()); !strings.Contains(tabs, "[3: Annotations]") {
		t.Fatalf("tab label changed: %q", tabs)
	}
}
