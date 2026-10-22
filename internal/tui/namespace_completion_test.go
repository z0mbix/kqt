package tui

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func completeNamespaceInput(m *model, value string, reverse bool) string {
	m.startInput("command", value)
	msg := tea.KeyPressMsg{Code: tea.KeyTab}
	if reverse {
		msg.Mod = tea.ModShift
	}
	m.Update(msg)
	return m.input.Value()
}

func TestNamespaceAtCompletionCyclesActionsAndGroupsSeparately(t *testing.T) {
	m := demoModel(t)
	if got := completeNamespaceInput(m, "ns @", false); got != "ns @delete " {
		t.Fatalf("Tab after @ did not complete an action: %q", got)
	}
	for _, want := range []string{"ns @edit ", "ns @rename ", "ns @save ", "ns @delete "} {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		if m.input.Value() != want {
			t.Fatalf("action cycle = %q, want %q", m.input.Value(), want)
		}
	}
	if got := completeNamespaceInput(m, "ns @", true); got != "ns @save " {
		t.Fatalf("Shift+Tab did not start at the last action: %q", got)
	}
	state := m.settings.Config.Contexts[m.contextName]
	state.NamespaceGroups = map[string][]string{"development": {"default"}, "production": {"platform"}}
	m.settings.Config.Contexts[m.contextName] = state
	if got := completeNamespaceInput(m, "ns @d", false); got != "ns @delete " {
		t.Fatal("action did not complete", got)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "ns @development" {
		t.Fatal("Tab completed arguments before the user chose an action", m.input.Value())
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.input.Value() != "ns @delete " {
		t.Fatal("Shift+Tab did not reverse the same cycle")
	}
	if len(m.settings.CommandHistory) != 0 || m.route.namespace != "default" {
		t.Fatal("completion executed a command")
	}
}

func TestNamespaceAtCompletionFillsActionsThenCurrentContextGroups(t *testing.T) {
	m := demoModel(t)
	state := m.settings.Config.Contexts[m.contextName]
	state.NamespaceGroups = map[string][]string{"development": {"default"}, "production": {"platform"}}
	m.settings.Config.Contexts[m.contextName] = state
	for input, want := range map[string]string{
		"ns @del": "ns @delete ", "ns @ed": "ns @edit ",
		"ns @ren": "ns @rename ", "ns @sav": "ns @save ",
		"ns @dev": "ns @development", "namespace @pro": "namespace @production",
		"ns @delete de": "ns @delete development", "ns @edit de": "ns @edit development",
		"ns @rename de": "ns @rename development", "ns @save de": "ns @save development",
		"ns save de": "ns save development", "namespace @edit pr": "namespace @edit production",
		"ns @rename development new": "ns @rename development new",
		"ns @save new":               "ns @save new", "ns @edit missing": "ns @edit missing",
	} {
		if got := completeNamespaceInput(m, input, false); got != want {
			t.Errorf("Tab %q = %q, want %q", input, got, want)
		}
	}
	// After completing the action, typing a space/name starts a new group cycle.
	m.startInput("command", "ns @e")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "ns @edit production" {
		t.Fatal("could not complete a group after choosing the action", m.input.Value())
	}
	m.contextName = "other"
	if got := completeNamespaceInput(m, "ns @edit de", false); got != "ns @edit de" {
		t.Fatal("completion leaked groups from another context", got)
	}
	if candidates := m.namespaceCompletions("@edit "); slices.Contains(candidates, "@edit default") {
		t.Fatal("literal namespaces became group arguments")
	}
}

func TestNamespaceGroupArgumentCompletionCyclesWithoutEmptyCandidate(t *testing.T) {
	m := demoModel(t)
	state := m.settings.Config.Contexts[m.contextName]
	state.NamespaceGroups = map[string][]string{"development": {"default"}, "production": {"platform"}}
	m.settings.Config.Contexts[m.contextName] = state
	for _, prefix := range []string{"ns @edit ", "namespace @edit ", "ns @delete ", "ns @rename ", "ns @save ", "ns save "} {
		t.Run(prefix, func(t *testing.T) {
			if got := completeNamespaceInput(m, prefix, false); got != prefix+"development" {
				t.Fatalf("first Tab = %q, want the first saved group", got)
			}
			for _, want := range []string{"production", "development", "production"} {
				m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
				if m.input.Value() != prefix+want {
					t.Fatalf("group cycle = %q, want %q", m.input.Value(), prefix+want)
				}
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
			if m.input.Value() != prefix+"development" {
				t.Fatal("Shift+Tab did not reverse group completion", m.input.Value())
			}
			if got := completeNamespaceInput(m, prefix, true); got != prefix+"production" {
				t.Fatal("initial Shift+Tab did not select the last group", got)
			}
		})
	}
	if m.mode != "command" || m.namespaceGroup != nil || len(m.settings.CommandHistory) != 0 {
		t.Fatal("completing a group executed an action or saved history")
	}
}

func TestNamespaceUniqueActionCompletionContinuesIntoGroups(t *testing.T) {
	m := demoModel(t)
	state := m.settings.Config.Contexts[m.contextName]
	state.NamespaceGroups = map[string][]string{"development": {"default"}, "production": {"platform"}}
	m.settings.Config.Contexts[m.contextName] = state
	if got := completeNamespaceInput(m, "ns @ed", false); got != "ns @edit " {
		t.Fatal("action did not complete", got)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "ns @edit development" {
		t.Fatal("the next Tab did not start completing groups", m.input.Value())
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "ns @edit production" {
		t.Fatal("subsequent Tab did not cycle through groups", m.input.Value())
	}
}
