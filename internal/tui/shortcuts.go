package tui

import (
	"strings"

	"github.com/z0mbix/kqt/internal/kube"
)

// Keep shortcut labels separate from resource names and other user text, so
// only actual key hints receive highlighting.
type shortcut struct {
	key, description string
}

func (m *model) shortcuts(separator string, hints ...shortcut) string {
	parts := make([]string, 0, len(hints))
	for _, hint := range hints {
		parts = append(parts, m.theme.style("accent").Bold(true).Render(kube.Safe(hint.key))+
			m.theme.style("muted").Render(": "+kube.Safe(hint.description)))
	}
	return strings.Join(parts, m.theme.style("muted").Render(separator))
}

func (m *model) footerHelp(d dimensions) string {
	if m.mode == "help-search" {
		return " " + m.shortcuts("  ", shortcut{"Tab", "pane"}, shortcut{"↑/↓", "scroll"}, shortcut{"Enter", "keep filter"}, shortcut{"Esc", "cancel search"})
	}
	if m.mode == "help" {
		return " " + m.shortcuts("  ", shortcut{"/", "fuzzy search"}, shortcut{"Tab", "pane"}, shortcut{"j/k", "scroll"}, shortcut{"Esc", "close"})
	}
	if m.showingLogs() && m.logFilterError != "" {
		return m.theme.style("error").Render(" "+m.logFilterError) + " · " + m.shortcuts("", shortcut{"Esc", "cancel"})
	}
	inspectorAction := "show/place"
	if m.inspectorVisible() {
		inspectorAction = "hide/place"
	}
	hints := []shortcut{{"j/k", "move"}, {"[ / ]", "sort column"}, {"i/I", inspectorAction}, {"z", "header"}, {"O", "next sort"}, {"R", "reverse"}, {"S", "status"}, {":", "command"}, {"?", "help"}, {"q", "quit"}}
	if d.width > 0 {
		hints = []shortcut{{"j/k", "move"}, {"Tab", "inspector"}, {"i/I", inspectorAction}, {"z", "header"}, {"O", "next sort"}, {"R", "reverse"}, {"S", "status"}, {":", "command"}, {"?", "help"}}
	}
	if m.inspectorFocused {
		hints = []shortcut{{"j/k", "scroll"}, {"Ctrl+B/F", "page"}, {"g/G", "ends"}, {"i/I", inspectorAction}, {"z", "header"}, {"Tab/Esc", "list"}, {"?", "help"}, {"q", "quit"}}
	}
	if m.mode == "command" {
		hints = []shortcut{{"↑/↓", "history"}, {"Ctrl+R", "search"}, {"Tab/Shift+Tab", "complete"}, {"Enter", "run"}, {"Esc", "cancel"}}
	} else if m.mode == "history-search" {
		return m.historySearchPreview()
	} else if m.mode == "bundle-save" {
		return " " + m.shortcuts("  ", shortcut{"Enter", "save"}, shortcut{"Esc", "cancel"})
	} else if m.mode == "bundle-loading" {
		return " " + m.shortcuts("  ", shortcut{"Esc", "cancel export"})
	} else if m.mode == "debug-image" {
		return " " + m.shortcuts("  ", shortcut{"Enter", "save and continue"}, shortcut{"Esc", "cancel"})
	} else if m.mode == "debug-confirm" {
		return " " + m.shortcuts("  ", shortcut{"Enter", "start"}, shortcut{"c", "container"}, shortcut{"d", "details"}, shortcut{"Esc", "cancel"})
	} else if m.showingDebug() {
		return " " + m.shortcuts("  ", shortcut{"Esc", "cancel waiting"})
	} else if m.mode == "action-confirm" {
		return m.theme.style("muted").Render(" Type the resource name shown  ") + m.shortcuts("  ", shortcut{"Enter", "confirm"}, shortcut{"Esc", "cancel"})
	} else if m.mode == "delete-confirm" {
		return " " + m.shortcuts("  ", shortcut{"←/→ Tab", "No / Yes"}, shortcut{"Enter", "confirm"}, shortcut{"Esc", "cancel"})
	} else if m.mode == "edit-review" {
		return " " + m.shortcuts("  ", shortcut{"Enter", "apply"}, shortcut{"Esc", "discard"}, shortcut{"hjkl", "scroll"})
	} else if m.mode == "" && len(m.resourceForwards()) > 0 {
		hints = []shortcut{{"x", "stop forwards for resource"}, {"F", "manage forwards"}, {":", "command"}, {"?", "help"}, {"q", "quit"}}
	}
	if o, ok := m.current(); ok && o.GetKind() == "Secret" && (m.mode == "" || m.mode == "details") {
		action := "reveal"
		if m.secretTarget != "" {
			action = "hide"
		}
		hints = []shortcut{{"x", action + " values"}, {"y", "YAML"}, {"J", "JSON"}, {"Tab", "inspector"}, {"?", "help"}, {"Esc", "close"}}
		if m.mode == "details" {
			hints = []shortcut{{"x", action + " values"}, {"y", "YAML"}, {"J", "JSON"}, {"hjkl", "scroll"}, {"Esc", "close"}}
		} else if m.inspectorFocused {
			hints = []shortcut{{"x", action + " values"}, {"y", "YAML"}, {"J", "JSON"}, {"j/k", "scroll"}, {"Tab/Esc", "list"}}
		}
	}
	if m.showingLogs() {
		hints = []shortcut{{"/", "filter"}, {"O", "log options"}, {"H", "highlight"}, {"T", "timestamps"}, {"N", "sources"}, {"w", "wrap"}, {"s", "follow"}, {"f", "focus"}, {"z", "header"}, {"o", "save"}, {"Esc", "close"}}
		if m.mode == "log-search" {
			hints = []shortcut{{"Enter", "apply"}, {"Esc", "cancel"}, {"Text / re:regex / !exclude", "filter"}, {"level>=warn / request_id=abc", "fields"}}
		}
	}
	if m.showingEventPanel() {
		hints = []shortcut{{m.resourceTabKeys(), "tabs"}, {"Tab", "cycle"}, {"Space", "pause"}, {"z", "header"}, {"/", "filter"}, {"o", "save"}, {"G", "follow"}, {"Ctrl+R", "reload"}, {"Esc", "close"}}
		if m.resourcePanelTab != resourceStatusTab {
			hints = []shortcut{{m.resourceTabKeys(), "tabs"}, {"Tab/Shift+Tab", "cycle"}, {"j/k", "scroll"}, {"z", "header"}, {"o", "save"}, {"Ctrl+R", "reload"}, {"Esc", "close"}}
		}
	} else if m.eventGlobal && m.mode == "" && !m.inspectorFocused {
		hints = []shortcut{{"Space", "pause"}, {"/", "filter"}, {"z", "header"}, {"o", "save"}, {"G", "follow"}, {"0", "all namespaces"}, {":", "command"}, {"q", "quit"}}
	}
	if m.mode == "overview" {
		hints = []shortcut{{"j/k", "scroll"}, {"G/g", "ends"}, {"Enter", "explore problems"}, {"0", "all namespaces"}, {"n", "namespaces"}, {"Ctrl+R", "reload"}, {"Esc", "close"}}
	}
	if m.mode == "rollout-details" {
		hints = []shortcut{{"d", "compare"}, {"y", "template"}, {"r", "rollback"}, {"Ctrl+R", "reload"}, {"Esc", "revisions"}}
	}
	if m.mode == "" {
		hints = append([]shortcut{{"e", "edit"}}, hints...)
		return m.theme.style("muted").Render(" Explorer · ") + m.shortcuts("  ", hints...)
	}
	return " " + m.shortcuts("  ", hints...)
}
