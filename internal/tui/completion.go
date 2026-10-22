package tui

import (
	"slices"
	"strings"

	"github.com/z0mbix/kqt/internal/kube"
)

type completionState struct {
	value   string
	matches []string
	index   int
}

var commandNames = []string{"header", "diagnose", "bundle", "debug", "alias", "unalias", "rollout", "rollouts", "rollback", "overview", "access", "can-i", "permissions", "actions", "api", "delete", "restart", "resume", "run", "scale", "suspend", "columns", "copy", "context", "contexts", "ctx", "download-dir", "edit", "events", "export", "forward", "forwards", "help", "inspector", "label", "labels", "layout", "layouts", "logs", "metadata", "metrics", "namespace", "ns", "q", "quit", "refresh", "related", "relations", "resources", "shell", "sort", "status", "theme", "themes", "write"}

func (m *model) completeCommand(reverse bool) {
	value := m.input.Value()
	if m.input.Position() != len([]rune(value)) {
		m.completion = completionState{}
		return
	}
	c := &m.completion
	if len(c.matches) == 0 || c.value != value {
		*c = completionState{}
		var candidates []string
		parts := strings.SplitN(value, " ", 2)
		if len(parts) == 1 {
			candidates = append(candidates, commandNames...)
			for _, r := range m.resources {
				candidates = append(candidates, r.ID(), r.GVR.Resource, strings.ToLower(r.Kind))
				candidates = append(candidates, r.Aliases...)
			}
		} else {
			prefix := parts[0] + " "
			var names []string
			switch parts[0] {
			case "bundle":
				names = []string{"--logs"}
			case "debug":
				names = []string{"attach"}
				for name := range m.settings.Config.Debug.Profiles {
					names = append(names, name)
				}
				if pod, ok := m.current(); ok {
					for _, container := range kube.DebugContainers(pod) {
						names = append(names, "attach "+container.Name)
					}
				}
			case "unalias":
				for alias := range m.settings.Config.ContextAliases {
					names = append(names, alias)
				}
			case "rollout":
				names = []string{"history"}
			case "theme", "themes":
				names = themeNames(m.settings.Themes)
			case "layout", "layouts":
				for name := range m.settings.Layouts {
					names = append(names, name)
				}
			case "ctx", "context", "contexts":
				for alias := range m.settings.Config.ContextAliases {
					if _, err := m.resolveContext(alias); err == nil {
						names = append(names, alias)
					}
				}
				if m.options.Demo {
					names = append(names, "demo")
				} else {
					for _, ctx := range m.loader.Contexts() {
						names = append(names, ctx.Name)
					}
				}
			case "ns", "namespace":
				names = m.namespaceCompletions(parts[1])
			case "can-i":
				names = []string{"get", "list", "watch", "patch", "delete", "create", "get log", "get exec", "create exec", "get portforward", "create portforward"}
			case "copy", "export":
				names = []string{"yaml", "json", "clean", "yaml clean", "json clean"}
			case "header":
				names = []string{"on", "off", "toggle"}
			case "inspector":
				names = []string{"yaml", "json", "position", "right", "bottom", "on", "off", "toggle", "none"}
			case "sort":
				columns := append(m.columns(), "name", "age", "status", "restarts")
				for _, column := range columns {
					names = append(names, column, column+" asc", column+" desc")
				}
			case "logs":
				names = []string{"previous"}
			case "events":
				names = []string{"selected"}
			case "metadata":
				names = []string{"labels", "annotations", "finalizers", "status"}
			case "download-dir":
				names = []string{"clear"}
			case "metrics":
				names = []string{"on", "off"}
			case "forward":
				names = []string{"stop all"}
			}
			for _, name := range names {
				candidates = append(candidates, prefix+name)
			}
		}
		slices.Sort(candidates)
		candidates = slices.Compact(candidates)
		for _, candidate := range candidates {
			if strings.HasPrefix(candidate, value) {
				c.matches = append(c.matches, candidate)
			}
		}
		if len(c.matches) == 0 {
			return
		}
		if reverse {
			c.index = len(c.matches) - 1
		}
	} else {
		direction := 1
		if reverse {
			direction = -1
		}
		c.index = (c.index + direction + len(c.matches)) % len(c.matches)
	}
	c.value = c.matches[c.index]
	m.input.SetValue(c.value)
	m.input.CursorEnd()
	// A unique namespace action is complete. The next Tab starts a new cycle
	// for its group argument; ambiguous action/group cycles stay reversible.
	if len(c.matches) == 1 {
		word, arg, _ := strings.Cut(c.value, " ")
		if (word == "ns" || word == "namespace") && slices.Contains([]string{"@save ", "@edit ", "@rename ", "@delete ", "save "}, arg) {
			*c = completionState{}
		}
	}
}
