package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
)

func normalizeNamespacePreferences(c *config) error {
	var err error
	if c.Namespace, err = kube.NormalizeNamespaces(c.Namespace); err != nil {
		return fmt.Errorf("invalid saved namespace: %w", err)
	}
	for name, state := range c.Contexts {
		if state.Namespace, err = kube.NormalizeNamespaces(state.Namespace); err != nil {
			return fmt.Errorf("context %s: %w", name, err)
		}
		for group, values := range state.NamespaceGroups {
			if !validName(group) {
				return fmt.Errorf("context %s: invalid namespace group %q", name, group)
			}
			names, err := kube.NamespaceNames(strings.Join(values, ","))
			if err != nil || len(names) == 0 {
				return fmt.Errorf("context %s: namespace group %s requires explicit namespace names", name, group)
			}
			state.NamespaceGroups[group] = names
		}
		c.Contexts[name] = state
	}
	return nil
}

func (m *model) namespaceCommand(arg string) tea.Cmd {
	if arg == "" {
		return m.changeResource("namespaces", true)
	}
	if name, save := strings.CutPrefix(arg, "save "); save {
		m.saveCurrentNamespaceGroup(strings.TrimSpace(name))
		return nil
	}
	if arg == "@" {
		return m.openPicker("namespace-groups")
	}
	if strings.HasPrefix(arg, "@") && strings.Contains(arg, " ") {
		return m.namespaceGroupCommand(arg)
	}
	all := arg == "-A" || arg == "all" || arg == "*"
	if name, group := strings.CutPrefix(arg, "@"); group {
		names, ok := m.settings.Config.Contexts[m.contextName].NamespaceGroups[name]
		if !ok || len(names) == 0 {
			m.notice = "Unknown namespace group: " + kube.Safe(name)
			return nil
		}
		arg = strings.Join(names, ",")
	}
	if all {
		arg = ""
	}
	normalized, err := kube.NormalizeNamespaces(arg)
	if err != nil {
		m.notice = "Invalid namespace selection: " + kube.Safe(err.Error())
		return nil
	}
	m.options.AllNamespaces, m.options.Namespace = all, normalized
	r := m.route
	r.namespace, r.fields, r.labels = normalized, "", ""
	return m.navigate(r, false)
}

func (m *model) namespaceLabel() (string, string) {
	names, _ := kube.NamespaceNames(m.route.namespace)
	if len(names) < 2 {
		return m.route.namespace, "Namespace"
	}
	groups := m.settings.Config.Contexts[m.contextName].NamespaceGroups
	wanted := slices.Sorted(slices.Values(names))
	for _, name := range slices.Sorted(maps.Keys(groups)) {
		if slices.Equal(wanted, slices.Sorted(slices.Values(groups[name]))) {
			return fmt.Sprintf("%s (%d)", name, len(names)), "Namespaces"
		}
	}
	return fmt.Sprintf("%d selected", len(names)), "Namespaces"
}

func (m *model) namespaceCompletions(arg string) []string {
	groups := m.settings.Config.Contexts[m.contextName].NamespaceGroups
	var names []string
	for _, action := range []string{"@save ", "@edit ", "@rename ", "@delete ", "save "} {
		if strings.HasPrefix(arg, action) {
			// The action has been chosen: only its group argument belongs in
			// this cycle, with no empty action-only candidate to stop on.
			for group := range groups {
				names = append(names, action+group)
			}
			return names
		}
	}
	if !strings.Contains(arg, ",") {
		actions := []string{"@save ", "@edit ", "@rename ", "@delete "}
		names = append([]string{"-A", "all", "save "}, actions...)
		// Once @ has been typed, Tab should visibly complete an action/group,
		// rather than selecting the identical manager command first.
		if arg != "@" {
			names = append(names, "@")
		}
		for group := range groups {
			names = append(names, "@"+group)
		}
	}
	if strings.HasPrefix(arg, "@") || strings.HasPrefix(arg, "save ") {
		return names
	}
	known, _ := kube.NamespaceNames(m.route.namespace)
	for _, values := range groups {
		known = append(known, values...)
	}
	for _, row := range m.rows {
		if row.GetKind() == "Namespace" {
			known = append(known, row.GetName())
		} else if row.GetNamespace() != "" {
			known = append(known, row.GetNamespace())
		}
	}
	prefix := ""
	if index := strings.LastIndex(arg, ","); index >= 0 {
		prefix = arg[:index+1]
		if strings.HasPrefix(arg[index+1:], " ") {
			prefix += " "
		}
	}
	for _, name := range known {
		names = append(names, prefix+name)
	}
	return names
}
