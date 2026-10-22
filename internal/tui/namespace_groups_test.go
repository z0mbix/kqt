package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func groupControl(m *model, r rune) {
	m.Update(tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl})
}

func TestNamespaceGroupManagementPersistsWithoutChangingScope(t *testing.T) {
	for _, extension := range []string{"yaml", "toml"} {
		t.Run(extension, func(t *testing.T) {
			m := demoModel(t)
			m.settings.Path = filepath.Join(t.TempDir(), "config."+extension)
			applyNamespaceCommand(t, m, "ns default,platform")
			runSubmitted(m, "ns @save development")
			if !slices.Contains(m.settings.CommandHistory, "ns @save development") {
				t.Fatal("successful save did not enter history")
			}
			// Simulate unrelated settings/groups edited while kqt is open.
			if err := m.settings.savePatch(map[string]any{"editor": "vim", "contexts": map[string]any{
				"demo":  map[string]any{"namespace_groups": map[string]any{"external": []string{"platform"}}},
				"other": map[string]any{"namespace_groups": map[string]any{"development": []string{"other-ns"}}},
			}}); err != nil {
				t.Fatal(err)
			}
			runSubmitted(m, "ns @edit development")
			if m.mode != "namespace-group" || m.input.Value() != "default,platform" || !m.input.Focused() || slices.Contains(m.settings.CommandHistory, "ns @edit development") {
				t.Fatal("edit did not prefill membership or was remembered too early")
			}
			before, _ := os.ReadFile(m.settings.Path)
			m.input.SetValue("default,")
			key(m, "enter")
			after, _ := os.ReadFile(m.settings.Path)
			if string(before) != string(after) || m.mode != "namespace-group" || m.namespaceGroup.error == "" {
				t.Fatal("invalid membership changed config or closed the form")
			}
			key(m, "esc")
			if slices.Contains(m.settings.CommandHistory, "ns @edit development") {
				t.Fatal("cancelled edit entered history")
			}
			runSubmitted(m, "ns @edit development")
			m.input.SetValue("default,kube-system,default") // A namespace with no pods must not hide healthy members.
			key(m, "enter")
			if m.mode != "" || m.route.namespace != "default,platform" || len(m.visible) != 26 || !slices.Contains(m.settings.CommandHistory, "ns @edit development") {
				t.Fatal("editing changed the active filter or lost successful history")
			}
			applyNamespaceCommand(t, m, "ns @development")
			if m.route.namespace != "default,kube-system" || len(m.visible) != 12 {
				t.Fatal("edited membership did not apply on selection")
			}
			runSubmitted(m, "namespace @rename development dev-team")
			if !strings.HasPrefix(m.notice, "Renamed namespace group ") || !slices.Contains(m.settings.CommandHistory, "namespace @rename development dev-team") {
				t.Fatal("rename did not complete", m.notice)
			}
			groups := m.settings.Config.Contexts["demo"].NamespaceGroups
			if _, exists := groups["development"]; exists || !slices.Equal(groups["dev-team"], []string{"default", "kube-system"}) {
				t.Fatal("rename lost membership or kept the old name")
			}
			runSubmitted(m, "ns @delete dev-team")
			if m.namespaceGroup.yes || m.input.Focused() {
				t.Fatal("deletion must default to No")
			}
			key(m, "enter")
			if _, exists := groups["dev-team"]; !exists || slices.Contains(m.settings.CommandHistory, "ns @delete dev-team") {
				t.Fatal("default No deleted a group or entered history")
			}
			runSubmitted(m, "ns @delete dev-team")
			key(m, "y")
			if m.mode != "" || m.namespaceGroup != nil || m.route.namespace != "default,kube-system" || len(m.visible) != 12 || !slices.Contains(m.settings.CommandHistory, "ns @delete dev-team") {
				t.Fatal("confirmed deletion changed the filter or lost history")
			}
			saved, err := LoadSettings(m.settings.Path)
			if err != nil {
				t.Fatal(err)
			}
			if len(saved.Config.Contexts["demo"].NamespaceGroups) != 1 || saved.Config.Editor != "vim" || saved.Config.Contexts["other"].NamespaceGroups["development"][0] != "other-ns" || saved.Config.Contexts["demo"].NamespaceGroups["external"][0] != "platform" {
				t.Fatal("group mutations overwrote unrelated config or left deleted groups", saved.Config)
			}
		})
	}
}

func TestNamespaceGroupNamesRemainDistinctFromLiteralNamespaces(t *testing.T) {
	m := demoModel(t)
	for _, name := range []string{"groups", "save", "edit", "rename", "delete", "all", "platform"} {
		applyNamespaceCommand(t, m, "ns default,kube-system")
		runSubmitted(m, "ns @save "+name)
		applyNamespaceCommand(t, m, "ns @"+name)
		if m.route.namespace != "default,kube-system" || len(m.visible) != 12 {
			t.Fatal("group name was treated as a management verb", name)
		}
		if name == "all" {
			applyNamespaceCommand(t, m, "ns all")
			if !m.options.AllNamespaces {
				t.Fatal("group captured all namespaces shortcut")
			}
		} else {
			applyNamespaceCommand(t, m, "ns "+name)
			if m.route.namespace != name || m.options.AllNamespaces {
				t.Fatal("group name captured a literal namespace", name)
			}
		}
	}
	for input, want := range map[string]string{
		"ns @ed": "ns @edit", "ns @edit pl": "ns @edit platform",
		"namespace @delete pl": "namespace @delete platform", "ns @rename pl": "ns @rename platform",
		"ns @save pl": "ns @save platform", "ns save pl": "ns save platform",
	} {
		m.startInput("command", input)
		m.completeCommand(false)
		if m.input.Value() != want {
			t.Fatalf("completion %q = %q, want %q", input, m.input.Value(), want)
		}
	}
}

func TestNamespaceGroupManagerSearchActionsAndCancellation(t *testing.T) {
	m := demoModel(t)
	runSubmitted(m, "ns @")
	if m.mode != "picker" || m.pickerKind != "namespace-groups" || !strings.Contains(ansi.Strip(m.View().Content), "No saved groups") {
		t.Fatal("empty manager did not open")
	}
	groupControl(m, 'n')
	if m.mode != "namespace-group" || m.namespaceGroup.action != "save" {
		t.Fatal("manager could not create the first group")
	}
	m.Update(tea.PasteMsg{Content: "development"})
	key(m, "enter")
	if m.mode != "picker" || len(m.choices) != 1 {
		t.Fatal("save did not return to refreshed manager")
	}
	m.Update(tea.PasteMsg{Content: "dvlpmnt"})
	if len(m.matches) != 1 || m.matches[0].name != "development" {
		t.Fatal("pasted query did not fuzzy search groups")
	}
	groupControl(m, 'e')
	if m.input.Value() != "default" {
		t.Fatal("manager edit did not prefill the selected group")
	}
	m.input.SetValue("platform")
	key(m, "esc")
	if m.mode != "picker" || m.input.Value() != "dvlpmnt" || m.settings.Config.Contexts["demo"].NamespaceGroups["development"][0] != "default" {
		t.Fatal("Escape did not restore manager search without saving")
	}
	groupControl(m, 'r')
	if m.input.Value() != "development" {
		t.Fatal("rename did not prefill the group name")
	}
	m.input.SetValue("dev")
	key(m, "enter")
	if m.mode != "picker" || len(m.matches) != 1 || m.matches[0].name != "dev" {
		t.Fatal("rename did not refresh and select renamed group")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter did not select group")
	}
	m.Update(cmd())
	if m.mode != "" || m.route.namespace != "default" {
		t.Fatal("manager selection failed")
	}
	runSubmitted(m, "ns @")
	groupControl(m, 'd')
	key(m, "y")
	if m.mode != "picker" || len(m.choices) != 0 || len(m.visible) != 12 {
		t.Fatal("manager deletion did not refresh or changed active resources")
	}
}

func TestNamespaceGroupErrorsDoNotMutateConfigOrHistory(t *testing.T) {
	m := demoModel(t)
	runSubmitted(m, "ns @save dev")
	runSubmitted(m, "ns @save taken")
	for _, command := range []string{"ns @rename dev taken", "ns @rename missing new", "ns @rename dev invalid/name", "ns @edit missing", "ns @delete missing", "ns @edit dev extra", "ns @save invalid/name", "ns @rename dev"} {
		before, _ := os.ReadFile(m.settings.Path)
		history := slices.Clone(m.settings.CommandHistory)
		runSubmitted(m, command)
		after, _ := os.ReadFile(m.settings.Path)
		if m.notice == "" || string(before) != string(after) || !slices.Equal(history, m.settings.CommandHistory) {
			t.Fatal("failed command changed config/history", command)
		}
	}
	applyNamespaceCommand(t, m, "ns all")
	runSubmitted(m, "ns @save everything")
	if _, exists := m.settings.Config.Contexts["demo"].NamespaceGroups["everything"]; exists || slices.Contains(m.settings.CommandHistory, "ns @save everything") {
		t.Fatal("all namespaces saved as an implicit group")
	}
	runSubmitted(m, "ns @edit dev")
	m.input.SetValue("platform")
	// A config path that cannot be written must retain both the form and data.
	m.settings.Path = t.TempDir()
	key(m, "enter")
	if m.mode != "namespace-group" || m.namespaceGroup.error == "" || m.settings.Config.Contexts["demo"].NamespaceGroups["dev"][0] != "default" || slices.Contains(m.settings.CommandHistory, "ns @edit dev") {
		t.Fatal("failed save mutated runtime membership or history")
	}
	key(m, "esc")
	runSubmitted(m, "ns @delete dev")
	m.generation++
	key(m, "y")
	if m.mode != "" || m.namespaceGroup != nil || m.settings.Config.Contexts["demo"].NamespaceGroups["dev"][0] != "default" {
		t.Fatal("old context confirmation removed a group")
	}
}

func TestNamespaceGroupModalGeometryMouseAndHelp(t *testing.T) {
	m := demoModel(t)
	runSubmitted(m, "ns @save dev")
	for _, size := range [][2]int{{40, 12}, {80, 24}, {180, 48}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, action := range []string{"edit", "rename", "delete"} {
			m.startNamespaceGroupForm(action, "dev", false)
			m.namespaceGroup.error = "Invalid namespace selection"
			bounds := m.modalBounds()
			if bounds.x != (m.width-bounds.width)/2 || bounds.y != (m.height-bounds.height)/2 || bounds.width > 76 || bounds.height > 12 {
				t.Fatal("group modal is not compact and centred", bounds)
			}
			text := m.View().Content
			lines := strings.Split(text, "\n")
			if len(lines) != m.height || !strings.Contains(ansi.Strip(text), "Invalid namespace selection") {
				t.Fatal("modal lost validation message or overflowed vertically")
			}
			for _, line := range lines {
				if ansi.StringWidth(line) != m.width {
					t.Fatal("modal overflowed horizontally")
				}
			}
			key(m, "esc")
		}
	}
	runSubmitted(m, "ns @delete dev")
	b := m.modalBounds()
	left := b.x + 1 + (b.width-2-15)/2
	m.Update(tea.MouseClickMsg{X: left + 9, Y: b.y + b.height - 3, Button: tea.MouseLeft})
	if m.mode != "namespace-group" {
		t.Fatal("click outside button row deleted group")
	}
	m.Update(tea.MouseClickMsg{X: left + 9, Y: b.y + b.height - 2, Button: tea.MouseLeft})
	if _, exists := m.settings.Config.Contexts["demo"].NamespaceGroups["dev"]; exists || m.mode != "" {
		t.Fatal("visible Yes button did not delete group")
	}
	m.openHelp()
	m.helpQuery = "namespace groups"
	text := ansi.Strip(m.renderHelp() + m.renderHelpNotes())
	if !strings.Contains(text, ":ns @") || !strings.Contains(text, "Ctrl+N") {
		t.Fatal("group management is not discoverable in fuzzy help", text)
	}
}
