package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
)

// A form captures its context and membership; background refreshes cannot
// retarget a save or change the namespace selection underneath it.
type namespaceGroupForm struct {
	action, name, context, query string
	members                      []string
	generation                   uint64
	manager, yes                 bool
	error                        string
	command                      string
	sequence                     uint64
}

func (m *model) namespaceGroupCommand(arg string) tea.Cmd {
	args := strings.Fields(arg)
	if len(args) == 2 {
		switch args[0] {
		case "@save":
			m.saveCurrentNamespaceGroup(args[1])
			return nil
		case "@edit":
			return m.startNamespaceGroupForm("edit", args[1], false)
		case "@delete":
			return m.startNamespaceGroupForm("delete", args[1], false)
		}
	}
	if len(args) == 3 && args[0] == "@rename" {
		m.renameNamespaceGroup(args[1], args[2])
		return nil
	}
	m.notice = "Use :ns @save NAME, @edit NAME, @rename OLD NEW or @delete NAME"
	return nil
}

func (m *model) saveCurrentNamespaceGroup(name string) {
	names, err := kube.NamespaceNames(m.route.namespace)
	if !validName(name) || err != nil || len(names) == 0 || m.contextName == "" {
		m.notice = "Use :ns @save NAME after selecting explicit namespaces"
		return
	}
	m.writeNamespaceGroup(map[string]any{name: names}, "Saved namespace group "+name)
}

func (m *model) renameNamespaceGroup(old, name string) bool {
	groups := m.settings.Config.Contexts[m.contextName].NamespaceGroups
	names, ok := groups[old]
	if !ok || len(names) == 0 {
		m.notice = "Unknown namespace group: " + kube.Safe(old)
		return false
	}
	if !validName(name) {
		m.notice = "Group names use 1–64 letters, digits, dots, underscores or hyphens; start with a letter or digit"
		return false
	}
	if _, exists := groups[name]; exists {
		m.notice = "Namespace group already exists: " + name
		return false
	}
	return m.writeNamespaceGroup(map[string]any{old: nil, name: names}, "Renamed namespace group "+old+" to "+name)
}

// A narrow patch preserves other groups, context preferences and YAML comments.
// Update memory only after the atomic config write has succeeded.
func (m *model) writeNamespaceGroup(changes map[string]any, notice string) bool {
	patch := map[string]any{"contexts": map[string]any{m.contextName: map[string]any{"namespace_groups": changes}}}
	if err := m.settings.savePatch(patch); err != nil {
		m.commandConfigError = true
		m.notice = "Unable to save namespace group: " + kube.Safe(err.Error())
		return false
	}
	state := m.settings.Config.Contexts[m.contextName]
	state.NamespaceGroups = maps.Clone(state.NamespaceGroups)
	if state.NamespaceGroups == nil {
		state.NamespaceGroups = map[string][]string{}
	}
	for name, value := range changes {
		if value == nil {
			delete(state.NamespaceGroups, name)
		} else {
			state.NamespaceGroups[name] = slices.Clone(value.([]string))
		}
	}
	m.settings.Config.Contexts[m.contextName] = state
	m.notice = notice
	return true
}

func (m *model) namespaceGroupChoices() []choice {
	groups := m.settings.Config.Contexts[m.contextName].NamespaceGroups
	current, _ := kube.NamespaceNames(m.route.namespace)
	current = slices.Sorted(slices.Values(current))
	var choices []choice
	for _, name := range slices.Sorted(maps.Keys(groups)) {
		names := groups[name]
		description := fmt.Sprintf("%d namespaces · %s", len(names), strings.Join(names, ", "))
		if slices.Equal(current, slices.Sorted(slices.Values(names))) {
			description = "active · " + description
		}
		choices = append(choices, choice{name, description})
	}
	return choices
}

func (m *model) namespaceGroupPickerKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	action := map[string]string{"ctrl+n": "save", "ctrl+e": "edit", "ctrl+r": "rename", "ctrl+d": "delete"}[msg.String()]
	if action == "" {
		return nil, false
	}
	name := ""
	if action != "save" {
		if len(m.matches) == 0 {
			return nil, true
		}
		name = m.matches[m.pickerIndex].name
	}
	return m.startNamespaceGroupForm(action, name, true), true
}

func (m *model) startNamespaceGroupForm(action, name string, manager bool) tea.Cmd {
	if m.contextName == "" {
		m.notice = "Choose a context first"
		return nil
	}
	var names []string
	if action == "save" {
		names, _ = kube.NamespaceNames(m.route.namespace)
		if len(names) == 0 {
			m.notice = "Select explicit namespaces before saving a group"
			return nil
		}
	} else {
		names = m.settings.Config.Contexts[m.contextName].NamespaceGroups[name]
		if len(names) == 0 {
			m.notice = "Unknown namespace group: " + kube.Safe(name)
			return nil
		}
	}
	f := &namespaceGroupForm{action: action, name: name, context: m.contextName, members: slices.Clone(names), generation: m.generation, manager: manager}
	if manager {
		f.query = m.input.Value()
	}
	m.namespaceGroup = f
	m.mode = "namespace-group"
	m.notice = ""
	m.input.Prompt, m.input.CharLimit = "Name: ", 64
	value := ""
	if action == "edit" {
		m.input.Prompt, m.input.CharLimit = "Namespaces: ", 8192
		value = strings.Join(names, ",")
	} else if action == "rename" {
		value = name
	}
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.resize()
	if action == "delete" {
		m.input.Blur()
		return nil
	}
	return m.input.Focus()
}

func (m *model) finishNamespaceGroupForm(saved bool) tea.Cmd {
	f := m.namespaceGroup
	m.namespaceGroup = nil
	m.mode = ""
	m.input.Blur()
	m.resize()
	if saved && f.command != "" {
		m.rememberCommandAt(f.command, f.sequence)
	}
	if !f.manager {
		return nil
	}
	cmd := m.openPicker("namespace-groups")
	if !saved {
		m.input.SetValue(f.query)
		m.filterChoices()
	}
	for i, c := range m.matches {
		if c.name == f.name {
			m.pickerIndex = i
			break
		}
	}
	return cmd
}

func (m *model) namespaceGroupKey(msg tea.KeyPressMsg) tea.Cmd {
	f := m.namespaceGroup
	if f.context != m.contextName || f.generation != m.generation {
		f.manager = false
		m.notice = "Context changed; namespace group action cancelled"
		return m.finishNamespaceGroupForm(false)
	}
	k := msg.String()
	if k == "esc" || f.action == "delete" && (k == "n" || k == "N" || k == "q") {
		m.notice = "Namespace group action cancelled"
		return m.finishNamespaceGroupForm(false)
	}
	if f.action == "delete" {
		switch k {
		case "left", "right", "tab", "shift+tab":
			f.yes = !f.yes
			return nil
		case "y", "Y":
			f.yes = true
			k = "enter"
		}
		if k != "enter" {
			return nil
		}
		if !f.yes {
			return m.finishNamespaceGroupForm(false)
		}
	}
	if k == "enter" {
		var saved bool
		value := strings.TrimSpace(m.input.Value())
		switch f.action {
		case "save":
			if !validName(value) {
				m.notice = "Use a 1–64 character name: letters, digits, dots, underscores or hyphens"
			} else {
				saved = m.writeNamespaceGroup(map[string]any{value: f.members}, "Saved namespace group "+value)
				if saved {
					f.name = value
				}
			}
		case "edit":
			names, err := kube.NamespaceNames(value)
			if err != nil || len(names) == 0 {
				m.notice = "Enter explicit comma-separated namespace names (no empty members or wildcards)"
			} else {
				saved = m.writeNamespaceGroup(map[string]any{f.name: names}, "Saved namespace group "+f.name)
			}
		case "rename":
			saved = m.renameNamespaceGroup(f.name, value)
			if saved {
				f.name = value
			}
		case "delete":
			saved = m.writeNamespaceGroup(map[string]any{f.name: nil}, "Deleted namespace group "+f.name)
		}
		if saved {
			return m.finishNamespaceGroupForm(true)
		}
		f.error = m.notice
		return nil
	}
	f.error = ""
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *model) namespaceGroupView(width, height int) string {
	f := m.namespaceGroup
	title := map[string]string{"save": "Save namespace group", "edit": "Edit namespace group", "rename": "Rename namespace group", "delete": "Delete namespace group"}[f.action]
	if f.name != "" {
		title += " · @" + f.name
	}
	lines := make([]string, height-2)
	lines[0] = fit("Context: "+kube.Safe(f.context), width-2)
	description := "Enter comma-separated namespaces. Reselect the group to apply changes."
	if f.action == "delete" {
		description = "Remove this saved group? Current selection stays unchanged."
	} else if f.action == "save" {
		description = "Save current selection. An existing name replaces that group."
	} else if f.action == "rename" {
		description = "Choose a new name. Existing groups will not be overwritten."
	}
	wrapped := strings.Split(ansi.Wrap(description, width-2, ""), "\n")
	footerRow := len(lines) - 1
	if f.action == "delete" {
		footerRow--
	}
	descriptionRows := min(len(wrapped), max(1, footerRow-3))
	copy(lines[1:], wrapped[:descriptionRows])
	row := 1 + descriptionRows
	if f.action != "delete" {
		lines[row] = m.input.View()
	} else {
		lines[row] = fit(strings.Join(f.members, ", "), width-2)
	}
	if f.error != "" {
		lines[row+1] = m.theme.style("error").Render(fit(f.error, width-2))
	}
	hints := []shortcut{{"Enter", "save"}, {"Esc", "cancel"}}
	if f.action == "delete" {
		hints = []shortcut{{"←/→", "choose"}, {"Enter", "confirm"}, {"Esc", "cancel"}}
		lines[len(lines)-1] = m.confirmationButtons(f.yes)
	}
	lines[footerRow] = m.shortcuts(" · ", hints...)
	return m.frame(title, strings.Join(lines, "\n"), width, height, true)
}

func (m *model) namespaceGroupMouse(v tea.MouseClickMsg) tea.Cmd {
	if m.namespaceGroup.action != "delete" || v.Button != tea.MouseLeft || v.Mod != 0 {
		return nil
	}
	b := m.modalBounds()
	left := b.x + 1 + max(0, (b.width-2-15)/2)
	if v.Y == b.y+b.height-2 {
		if v.X >= left && v.X < left+6 {
			return m.namespaceGroupKey(tea.KeyPressMsg{Code: 'n'})
		}
		if v.X >= left+8 && v.X < left+15 {
			return m.namespaceGroupKey(tea.KeyPressMsg{Code: 'y'})
		}
	}
	return nil
}
