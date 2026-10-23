package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextAliasesSaveCompleteDisplayAndUseCanonicalState(t *testing.T) {
	m := demoModel(t)
	m.aliasCommand("prod", false)
	if m.settings.Config.ContextAliases["prod"] != "demo" || !strings.Contains(m.notice, "saved") {
		t.Fatal("alias was not saved", m.notice)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Context: prod") {
		t.Fatal("header did not use alias")
	}
	saved, err := LoadSettings(m.settings.Path)
	if err != nil || saved.Config.ContextAliases["prod"] != "demo" {
		t.Fatal("alias did not persist", err)
	}
	m.startInput("command", "ctx pr")
	m.completeCommand(false)
	if m.input.Value() != "ctx prod" {
		t.Fatal("alias did not complete")
	}
	key(m, "esc")
	m.openPicker("contexts")
	if m.matches[0].name != "prod" || !strings.Contains(m.matches[0].description, "demo") {
		t.Fatal("picker hid canonical context")
	}
	cmd := m.pickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, load := m.Update(cmd())
	m.Update(load())
	if m.contextName != "demo" || m.settings.Config.LastContext != "demo" || m.settings.Config.Contexts["demo"].View == "" {
		t.Fatal("alias created a second context identity")
	}
	m.aliasCommand("prod", true)
	saved, err = LoadSettings(m.settings.Path)
	if err != nil || len(saved.Config.ContextAliases) != 0 || m.contextLabel("demo") != "demo" {
		t.Fatal("removed alias survived restart", err)
	}
}

func aliasLoader(t *testing.T) *kube.Loader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	data := "apiVersion: v1\nkind: Config\ncurrent-context: real-context\nclusters:\n- name: test\n  cluster:\n    server: https://example.invalid\n- name: k3d-cfgerer\n  cluster:\n    server: https://example.invalid\ncontexts:\n- name: real-context\n  context:\n    cluster: test\n- name: another-context\n  context:\n    cluster: test\n- name: k3d-cfgerer\n  context:\n    cluster: k3d-cfgerer\nusers: []\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	loader, err := kube.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return loader
}

func TestContextPickerOmitsRepeatedNamesAndKeepsSearchableDetails(t *testing.T) {
	m := demoModel(t)
	m.options.Demo = false
	m.loader = aliasLoader(t)
	m.openPicker("contexts")
	for _, c := range m.choices {
		want := "cluster: test · namespace: default"
		if c.name == "k3d-cfgerer" {
			want = "namespace: default"
		}
		if c.description != want {
			t.Fatalf("context %q description = %q, want %q", c.name, c.description, want)
		}
	}
	row := "k3d-cfgerer · namespace: default"
	if view := ansi.Strip(m.pickerView(90, 24)); !strings.Contains(view, row) || strings.Contains(view, "k3d-cfgerer · k3d-cfgerer") {
		t.Fatal("context picker still repeats the context/cluster name", view)
	}
	m.settings.Config.ContextAliases = map[string]string{"short": "k3d-cfgerer", "team": "k3d-cfgerer", "another-context": "k3d-cfgerer"}
	m.openPicker("contexts")
	for _, query := range []string{"short", "k3d-cfgerer", "team"} {
		m.input.SetValue(query)
		m.filterChoices()
		var matched *choice
		for i := range m.matches {
			if m.matches[i].name == "short" {
				matched = &m.matches[i]
				break
			}
		}
		if matched == nil {
			t.Fatalf("search %q lost the canonical context or an alias: %v", query, m.matches)
		}
		c := *matched
		if c.description != "context: k3d-cfgerer · namespace: default · aliases: team" {
			t.Fatal("alias description repeats its display name or hides identity", c.description)
		}
		if canonical, err := m.resolveContext(c.name); err != nil || canonical != "k3d-cfgerer" {
			t.Fatal("compact row changed context resolution", canonical, err)
		}
	}
	m.input.SetValue("test")
	m.filterChoices()
	for _, name := range []string{"real-context", "another-context"} {
		found := false
		for _, c := range m.matches {
			found = found || c.name == name
		}
		if !found {
			t.Fatal("distinct cluster names are no longer searchable", name, m.matches)
		}
	}
}

func TestContextAliasCLIResolutionCollisionsAndStaleTargets(t *testing.T) {
	m := demoModel(t)
	m.settings.Config.ContextAliases = map[string]string{"prod": "real-context", "another-context": "real-context", "stale": "gone"}
	loader := aliasLoader(t)
	configured, err := newModel(context.Background(), m.settings, Options{Context: "prod"}, loader)
	if err != nil {
		t.Fatal(err)
	}
	defer configured.close()
	if configured.contextName != "real-context" || configured.contextLabel("real-context") != "prod" {
		t.Fatal("CLI alias did not resolve to canonical context")
	}
	if name, err := configured.resolveContext("another-context"); err != nil || name != "another-context" {
		t.Fatal("alias shadowed real context")
	}
	if _, err := configured.resolveContext("stale"); err == nil {
		t.Fatal("stale alias silently switched context")
	}
	configured.aliasCommand("another-context real-context", false)
	if !strings.Contains(configured.notice, "conflicts") {
		t.Fatal("saved alias over real context name")
	}
	before := configured.settings.Config.ContextAliases["prod"]
	configured.aliasCommand("prod gone", false)
	if configured.settings.Config.ContextAliases["prod"] != before {
		t.Fatal("unknown context replaced a valid alias")
	}
}

func TestContextAliasesYAMLAndTOMLRemovalAndValidation(t *testing.T) {
	for _, suffix := range []string{"yaml", "toml"} {
		t.Run(suffix, func(t *testing.T) {
			m := demoModel(t)
			m.settings.Path = filepath.Join(t.TempDir(), "config."+suffix)
			m.aliasCommand("prod demo", false)
			m.aliasCommand("dev demo", false)
			m.aliasCommand("prod", true)
			saved, err := LoadSettings(m.settings.Path)
			if err != nil || len(saved.Config.ContextAliases) != 1 || saved.Config.ContextAliases["dev"] != "demo" {
				t.Fatal("alias removal lost other aliases or retained removed entry", err)
			}
			if err := m.settings.savePatch(map[string]any{"context_aliases": map[string]string{"bad alias": "demo"}}); err == nil {
				t.Fatal("invalid alias was written")
			}
		})
	}
}

func TestContextPickerHighlightsNamesWithThemeAndSelectionContrast(t *testing.T) {
	m := demoModel(t)
	m.options.Demo = false
	m.loader = aliasLoader(t)
	m.settings.Config.ContextAliases = map[string]string{"prod": "k3d-cfgerer"}
	for _, themeName := range []string{"catppuccin-mocha", "nord", "accessible-dark", "accessible-light", "no-color"} {
		m.setTheme(builtThemes()[themeName])
		m.openPicker("contexts")
		for _, selected := range []bool{false, true} {
			m.pickerIndex = 0
			if selected {
				m.pickerIndex = 1 // prod, whose canonical context is k3d-cfgerer.
			}
			style := m.contextNameStyle("k3d-cfgerer", "prod", selected)
			view := m.pickerView(90, 24)
			if !strings.Contains(view, style.Render("prod")) || !strings.Contains(ansi.Strip(view), "prod · context: k3d-cfgerer · namespace: default") {
				t.Fatalf("%s selected=%v: picker lost name highlighting or context details", themeName, selected)
			}
			if themeName == "no-color" {
				if hasExplicitColour(view) || !style.GetUnderline() {
					t.Fatal("monochrome context warning lost emphasis or emitted colours")
				}
			} else if selected {
				fg, bg := style.GetForeground(), m.theme.color("selection_background")
				if themeContrast(fg, bg) < 4.5 || themeContrast(ansi.Convert256(fg), ansi.Convert256(bg)) < 4.5 {
					t.Fatal("selected context name has poor contrast", themeName)
				}
			}
		}
	}
	m.setTheme(builtThemes()["catppuccin-mocha"])
	for _, tc := range []struct{ canonical, label, token string }{
		{"eks-production", "friendly", "error"}, {"eks-nonprod", "friendly", "accent"},
		{"eks-pre-production", "friendly", "accent"}, {"demo", "prod", "error"},
	} {
		got := m.contextNameStyle(tc.canonical, tc.label, false).GetForeground()
		if got != m.theme.color(tc.token) {
			t.Fatal("picker and header production highlighting differ", tc)
		}
	}
}
