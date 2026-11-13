package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
)

const standardNodeView = "standard"

type nodeViewEditor struct {
	name, command string
	sequence      uint64
	view          viewConfig
}

func validateNodeViews(contexts map[string]contextState) error {
	for context, state := range contexts {
		for name := range state.NodeViews {
			if !validName(name) || name == standardNodeView {
				return fmt.Errorf("context %s: invalid node view name %q", context, name)
			}
		}
		if err := validateViews(state.NodeViews); err != nil {
			return fmt.Errorf("context %s: node %w", context, err)
		}
		if _, ok := state.NodeViews[state.DefaultNodeView]; state.DefaultNodeView != "" && !ok {
			return fmt.Errorf("context %s: unknown default node view %q", context, state.DefaultNodeView)
		}
	}
	return nil
}

func (m *model) activeNodeView() string {
	if m.route.resource.ID() != "nodes" {
		return ""
	}
	switch m.nodeView {
	case standardNodeView:
		return ""
	case "":
		return m.settings.Config.Contexts[m.contextName].DefaultNodeView
	default:
		return m.nodeView
	}
}

func (m *model) nodeViewCommand(arg string) tea.Cmd {
	if m.route.resource.ID() != "nodes" || m.contextName == "" {
		m.notice = "Open :nodes to manage node views"
		return nil
	}
	if arg == "" || arg == "@" {
		return m.openPicker("node-views")
	}
	args := strings.Fields(arg)
	if len(args) == 1 {
		if name, ok := strings.CutPrefix(args[0], "@"); ok && m.hasNodeView(name) {
			m.selectNodeView(name)
			return nil
		}
		m.notice = "Unknown node view: " + kube.Safe(arg)
		return nil
	}
	if len(args) == 2 {
		name := args[1]
		switch args[0] {
		case "@save", "@edit":
			return m.editNodeView(name, args[0] == "@edit")
		case "@default":
			m.setDefaultNodeView(name)
			return nil
		case "@delete":
			if name == standardNodeView || !m.hasNodeView(name) {
				m.notice = "Unknown saved node view: " + kube.Safe(name)
			} else if m.writeNodeView(name, nil) {
				if m.nodeView == name {
					m.nodeView = ""
				}
				m.selectNodeView(m.nodeView)
				m.notice = "Deleted node view " + name
			}
			return nil
		}
	}
	m.notice = "Use :nodeviews @NAME, @save NAME, @edit NAME, @default NAME or @delete NAME"
	return nil
}

func (m *model) hasNodeView(name string) bool {
	_, exists := m.settings.Config.Contexts[m.contextName].NodeViews[name]
	return name == standardNodeView || exists
}

func (m *model) selectNodeView(name string) {
	m.nodeView = name
	key := ""
	if row, ok := m.current(); ok {
		key = kube.Identity(row)
	}
	m.horizontal = 0
	m.applyView()
	m.rebuild(key)
}

func (m *model) setDefaultNodeView(name string) bool {
	if !m.hasNodeView(name) {
		m.notice = "Unknown node view: " + kube.Safe(name)
		return false
	}
	var value any
	state := m.settings.Config.Contexts[m.contextName]
	state.DefaultNodeView = ""
	if name != standardNodeView {
		state.DefaultNodeView, value = name, name
	}
	if !m.saveNodeViewPreferences(map[string]any{"default_node_view": value}) {
		return false
	}
	m.settings.Config.Contexts[m.contextName] = state
	m.selectNodeView("")
	return true
}

func (m *model) saveNodeViewPreferences(changes map[string]any) bool {
	patch := map[string]any{"contexts": map[string]any{m.contextName: changes}}
	if err := m.settings.savePatch(patch); err != nil {
		m.commandConfigError = true
		m.notice = "Unable to save node views: " + kube.Safe(err.Error())
		return false
	}
	m.notice = ""
	return true
}

func (m *model) writeNodeView(name string, view *viewConfig) bool {
	state := m.settings.Config.Contexts[m.contextName]
	var value any
	if view != nil {
		value = viewPreferences(*view)
	}
	changes := map[string]any{"node_views": map[string]any{name: value}}
	if view == nil && state.DefaultNodeView == name {
		changes["default_node_view"] = nil
		state.DefaultNodeView = ""
	}
	if !m.saveNodeViewPreferences(changes) {
		return false
	}
	if view == nil {
		delete(state.NodeViews, name)
	} else {
		if state.NodeViews == nil {
			state.NodeViews = map[string]viewConfig{}
		}
		state.NodeViews[name] = *view
	}
	m.settings.Config.Contexts[m.contextName] = state
	return true
}

func (m *model) nodeViewChoices() []choice {
	state := m.settings.Config.Contexts[m.contextName]
	active := m.activeNodeView()
	standard := "Use the current layout"
	if state.DefaultNodeView == "" {
		standard = "default · " + standard
	}
	if active == "" {
		standard = "active · " + standard
	}
	choices := []choice{{standardNodeView, standard}}
	for _, name := range slices.Sorted(maps.Keys(state.NodeViews)) {
		description := strings.Join(state.NodeViews[name].Columns, ", ")
		if state.DefaultNodeView == name {
			description = "default · " + description
		}
		if active == name {
			description = "active · " + description
		}
		choices = append(choices, choice{name, description})
	}
	return choices
}

func (m *model) editNodeView(name string, edit bool) tea.Cmd {
	if !validName(name) || name == standardNodeView {
		m.notice = "Choose a node view name using letters, digits, dots, underscores or hyphens"
		return nil
	}
	view, exists := m.settings.Config.Contexts[m.contextName].NodeViews[name]
	if edit && !exists {
		m.notice = "Unknown saved node view: " + name
		return nil
	}
	if !edit && exists {
		m.notice = "Node view already exists · use :nodeviews @edit " + name
		return nil
	}
	if !edit {
		view = m.currentViewConfig()
		view.Sort, view.Descending = m.sortColumn, m.descending
		if len(view.Columns) == 0 {
			view.Columns = kube.DefaultColumns("Node")
		}
		view.Columns = slices.DeleteFunc(slices.Clone(view.Columns), func(column string) bool { return column == "labels" })
		if len(view.Columns) == 0 {
			view.Columns = kube.DefaultColumns("Node")
		}
	} else {
		view.Columns = slices.Clone(view.Columns)
	}
	m.nodeViewEditor = &nodeViewEditor{name: name, view: view}
	return m.openPicker("node-view-labels")
}

func (m *model) nodeLabelChoices() []choice {
	keys := map[string]bool{}
	for _, row := range m.rows {
		for key := range row.GetLabels() {
			keys[key] = true
		}
	}
	for _, column := range m.nodeViewEditor.view.Columns {
		if key, ok := strings.CutPrefix(column, kube.LabelColumnPrefix); ok {
			keys[key] = true
		} else if pointer, ok := strings.CutPrefix(column, "/metadata/labels/"); ok && !strings.Contains(pointer, "/") {
			keys[strings.ReplaceAll(strings.ReplaceAll(pointer, "~1", "/"), "~0", "~")] = true
		}
	}
	var choices []choice
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		state := "[ ]"
		if slices.IndexFunc(m.nodeViewEditor.view.Columns, func(column string) bool { return nodeLabelColumn(column, key) }) >= 0 {
			state = "[x]"
		}
		choices = append(choices, choice{key, state})
	}
	return choices
}

func nodeLabelColumn(column, key string) bool {
	return column == kube.LabelColumnPrefix+key || column == "/metadata/labels/"+strings.ReplaceAll(key, "/", "~1")
}

func (m *model) nodeViewPickerKey(key string) (tea.Cmd, bool) {
	if m.pickerKind == "node-views" {
		if key == "ctrl+n" {
			return m.startInput("command", "nodeviews @save "), true
		}
		if len(m.matches) == 0 {
			return nil, false
		}
		name := m.matches[m.pickerIndex].name
		switch key {
		case "ctrl+e":
			if name != standardNodeView {
				return m.editNodeView(name, true), true
			}
		case "ctrl+t":
			if m.setDefaultNodeView(name) {
				m.choices = m.nodeViewChoices()
				m.filterChoices()
			}
			return nil, true
		case "ctrl+d":
			if name != standardNodeView {
				return m.startInput("command", "nodeviews @delete "+name), true
			}
		}
		return nil, false
	}

	editor := m.nodeViewEditor
	switch key {
	case "esc":
		m.nodeViewEditor = nil
		return m.openPicker("node-views"), true
	case "enter":
		if len(m.matches) > 0 {
			label := m.matches[m.pickerIndex].name
			columns := editor.view.Columns
			matches := func(column string) bool { return nodeLabelColumn(column, label) }
			if slices.IndexFunc(columns, matches) >= 0 {
				editor.view.Columns = slices.DeleteFunc(columns, matches)
			} else if len(columns) < 32 {
				editor.view.Columns = append(columns, kube.LabelColumnPrefix+label)
			} else {
				m.notice = "Node views support up to 32 columns"
				return nil, true
			}
			m.notice = ""
			m.choices = m.nodeLabelChoices()
			m.filterChoices()
		}
		return nil, true
	case "ctrl+s":
		if len(editor.view.Columns) == 0 {
			m.notice = "Keep at least one column in the node view"
			return nil, true
		}
		if !slices.Contains(editor.view.Columns, editor.view.Sort) {
			editor.view.Sort, editor.view.Descending = "name", false
		}
		if m.writeNodeView(editor.name, &editor.view) {
			m.selectNodeView(editor.name)
			m.nodeViewEditor = nil
			if editor.command != "" {
				m.rememberCommandAt(editor.command, editor.sequence)
			}
			m.notice = "Saved node view " + editor.name
			return m.openPicker("node-views"), true
		}
		return nil, true
	}
	return nil, false
}
