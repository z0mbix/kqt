package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"github.com/z0mbix/kqt/internal/kube"
	"maps"
	"slices"
	"strings"
	"unicode"
)

func validateContextAliases(aliases map[string]string) error {
	for name, target := range aliases {
		if !validName(name) {
			return fmt.Errorf("invalid context alias %q", name)
		}
		if strings.TrimSpace(target) == "" || strings.ContainsFunc(target, unicode.IsControl) {
			return fmt.Errorf("context alias %s requires a context name", name)
		}
	}
	return nil
}

func (m *model) contextExists(name string) bool {
	if m.options.Demo {
		return name == "demo"
	}
	if m.loader == nil {
		return false
	}
	for _, context := range m.loader.Contexts() {
		if context.Name == name {
			return true
		}
	}
	return false
}
func (m *model) resolveContext(name string) (string, error) {
	if m.contextExists(name) {
		return name, nil
	}
	if target, ok := m.settings.Config.ContextAliases[name]; ok {
		if m.contextExists(target) {
			return target, nil
		}
		return "", fmt.Errorf("alias %s refers to unavailable context %s", name, target)
	}
	return "", fmt.Errorf("unknown context: %s", name)
}

func (m *model) contextLabel(name string) string {
	aliases := []string{}
	for alias, target := range m.settings.Config.ContextAliases {
		if target == name && !m.contextExists(alias) {
			aliases = append(aliases, alias)
		}
	}
	slices.Sort(aliases)
	if len(aliases) > 0 {
		return aliases[0]
	}
	return name
}

func (m *model) contextChoices() []choice {
	var contexts []kube.Context
	if m.options.Demo {
		contexts = []kube.Context{{Name: "demo", Cluster: "offline sample cluster", Namespace: "default"}}
	}
	if !m.options.Demo && m.loader != nil {
		contexts = m.loader.Contexts()
	}
	var choices []choice
	for _, context := range contexts {
		aliases := []string{}
		for alias, target := range m.settings.Config.ContextAliases {
			if target == context.Name && !m.contextExists(alias) {
				aliases = append(aliases, alias)
			}
		}
		slices.Sort(aliases)
		name := context.Name
		var details []string
		if len(aliases) > 0 {
			name, aliases = aliases[0], aliases[1:]
			details = append(details, "context: "+context.Name)
		}
		if context.Cluster != "" && context.Cluster != context.Name {
			details = append(details, "cluster: "+context.Cluster)
		}
		details = append(details, "namespace: "+context.Namespace)
		if len(aliases) > 0 {
			details = append(details, "aliases: "+strings.Join(aliases, ", "))
		}
		choices = append(choices, choice{name, strings.Join(details, " · ")})
	}
	return choices
}

func (m *model) aliasCommand(arg string, remove bool) tea.Cmd {
	args, err := editorArgs(arg)
	if err != nil && strings.TrimSpace(arg) != "" {
		m.notice = kube.Safe(err.Error())
		return nil
	}
	if len(args) == 0 && !remove {
		names := []string{}
		for name := range m.settings.Config.ContextAliases {
			names = append(names, name)
		}
		slices.Sort(names)
		var lines []string
		for _, name := range names {
			target := m.settings.Config.ContextAliases[name]
			suffix := ""
			if !m.contextExists(target) {
				suffix = " · unavailable"
			}
			lines = append(lines, kube.Safe(name)+" → "+kube.Safe(target)+suffix)
		}
		if len(lines) == 0 {
			lines = []string{"No context aliases.\n\n:alias NAME [CONTEXT] saves an alias (defaults to the current context).\n:ctx NAME switches using an alias; :unalias NAME removes it."}
		}
		m.mode = "help"
		m.resize()
		m.panelTitle = "Context aliases"
		m.panel.SoftWrap = true
		m.panel.SetContent(strings.Join(lines, "\n"))
		m.panel.GotoTop()
		return nil
	}
	if len(args) < 1 || len(args) > 2 || remove && len(args) != 1 || !validName(args[0]) {
		m.notice = "Use :alias NAME [CONTEXT] or :unalias NAME"
		return nil
	}
	aliases := maps.Clone(m.settings.Config.ContextAliases)
	if aliases == nil {
		aliases = map[string]string{}
	}
	name := args[0]
	if remove {
		if _, ok := aliases[name]; !ok {
			m.notice = "Unknown context alias: " + name
			return nil
		}
		delete(aliases, name)
	} else {
		target := m.contextName
		if len(args) == 2 {
			target, err = m.resolveContext(args[1])
			if err != nil {
				m.notice = kube.Safe(err.Error())
				return nil
			}
		}
		if target == "" {
			m.notice = "Choose a context first"
			return nil
		}
		if m.contextExists(name) {
			m.notice = "Alias conflicts with a real context name: " + name
			return nil
		}
		aliases[name] = target
	}
	if err = m.settings.savePatch(map[string]any{"context_aliases": aliases}); err != nil {
		m.notice = "Unable to save context alias: " + kube.Safe(err.Error())
		return nil
	}
	m.settings.Config.ContextAliases = aliases
	if remove {
		m.notice = "Context alias removed: " + name
	} else {
		m.notice = "Context alias saved: " + name + " → " + kube.Safe(aliases[name])
	}
	return nil
}
