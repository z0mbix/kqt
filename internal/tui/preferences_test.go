package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSavedStateLayoutsAndFlagPrecedence(t *testing.T) {
	m := demoModel(t)
	m.command("ns platform")
	m.changeResource("statefulsets", false)
	m.command("columns name,ready,status,age")
	m.command("layout bottom")
	m.command("write investigation")
	m.setInspectorFormat("json")
	saved, err := LoadSettings(m.settings.Path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Config.View != "statefulsets.apps" || saved.Config.Namespace != "platform" || saved.Config.Layout != "investigation" || saved.Config.InspectorFormat != "json" {
		t.Fatalf("state not saved: %+v", saved.Config)
	}
	info, _ := os.Stat(saved.Path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("config permissions are not private")
	}
	restarted, err := newModel(context.Background(), saved, Options{Demo: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.close()
	if restarted.route.resource.Kind != "StatefulSet" || restarted.route.namespace != "platform" || restarted.layout != "investigation" || restarted.inspectorFormat != "json" {
		t.Fatal("restart did not restore state")
	}
	if strings.Join(restarted.columns(), ",") != "name,ready,status,age" {
		t.Fatal("saved layout columns lost")
	}
	overridden, err := newModel(context.Background(), saved, Options{Demo: true, View: "pods", Namespace: "default", ScopeExplicit: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer overridden.close()
	if overridden.route.resource.Kind != "Pod" || overridden.route.namespace != "default" {
		t.Fatal("flags did not override saved state")
	}
	m.command("ns -A")
	m.changeResource("nodes", false)
	saved, err = LoadSettings(m.settings.Path)
	if err != nil || !saved.Config.AllNamespaces {
		t.Fatalf("aggregate scope lost on cluster resource: %v", err)
	}
	m.openPicker("layouts")
	found := false
	for _, choice := range m.choices {
		if choice.name == "investigation" {
			found = true
		}
	}
	if !found {
		t.Fatal("layouts picker did not include saved layout")
	}
}
func TestConfigUpdatesPreserveExternalSettingsAndComments(t *testing.T) {
	m := demoModel(t)
	path := m.settings.Path
	text := strings.Replace(SampleConfig, "refresh_interval: 2s", "refresh_interval: 3s", 1) + "\neditor: 'custom-editor --wait' # preserve this\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	m.command("ns platform")
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "custom-editor --wait") || !strings.Contains(string(data), "# preserve this") || !strings.Contains(string(data), "refresh_interval: 3s") {
		t.Fatalf("unrelated settings overwritten: %s", data)
	}
	broken := []byte("version: [\n")
	os.WriteFile(path, broken, 0600)
	m.command("ns default")
	after, _ := os.ReadFile(path)
	if string(after) != string(broken) || !strings.Contains(m.notice, "Unable to save") {
		t.Fatal("invalid config overwritten or failure hidden")
	}
}
func TestLegacyConfigMigratesWithoutChangingOriginal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	dir := filepath.Join(root, "kqt")
	os.MkdirAll(dir, 0700)
	legacy := filepath.Join(dir, "config.toml")
	source := "version=1\ntheme='nord'\nlayout='classic'\nview='nodes'\nnamespace='apps'\neditor='nvim -f'\n"
	os.WriteFile(legacy, []byte(source), 0600)
	s, err := LoadSettings("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(s.Path, "config.yaml") || s.Config.Theme != "nord" {
		t.Fatal("legacy defaults not loaded")
	}
	if err = s.savePatch(map[string]any{"layout": "bottom"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(legacy)
	if string(after) != source {
		t.Fatal("legacy file changed")
	}
	saved, err := LoadSettings("")
	if err != nil || saved.Config.Editor != "nvim -f" || saved.Config.View != "nodes" || saved.Config.Layout != "bottom" {
		t.Fatalf("migration lost settings: %v", err)
	}
}
func TestLayoutsRejectCyclesAndKeepOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, body := range []string{"layouts:\n  classic:\n    inspector: right\n", "layouts:\n  one:\n    extends: two\n  two:\n    extends: one\n", "layouts:\n  custom:\n    inspector_percent: 90\n", "layouts:\n  custom:\n    extends: missing\n"} {
		os.WriteFile(path, []byte("version: 1\n"+body), 0600)
		if _, err := LoadSettings(path); err == nil {
			t.Fatalf("invalid layout accepted: %s", body)
		}
	}
}
func TestCompletionFuzzySearchAndStableConnectionHeader(t *testing.T) {
	m := demoModel(t)
	m.startInput("command", "the")
	m.key(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "theme" {
		t.Fatal("command Tab completion failed")
	}
	m.completeCommand(false)
	if m.input.Value() != "themes" {
		t.Fatal("completion cycling failed")
	}
	m.completeCommand(true)
	if m.input.Value() != "theme" {
		t.Fatal("reverse completion failed")
	}
	original := m.theme.Name
	m.startInput("command", "themes nord")
	m.completeCommand(false)
	if m.input.Value() != "themes nord" || m.theme.Name != original {
		t.Fatal("completion executed a theme")
	}
	m.startInput("command", "layouts in")
	m.completeCommand(false)
	if m.input.Value() != "layouts inspector" {
		t.Fatal("layout argument completion failed")
	}
	m.mode = ""
	m.filter = "web0003"
	m.rebuild("")
	if len(m.visible) != 1 || m.visible[0].GetName() != "web-7c9f6d8b5-0003" {
		t.Fatalf("fuzzy pod search: %v", m.visible)
	}
	m.filter = ""
	m.rebuild("")
	m.busy = true
	line := strings.Split(ansi.Strip(m.View().Content), "\n")[0]
	if strings.Contains(line, "loading") || !strings.Contains(line, "connected") {
		t.Fatalf("background refresh flickers: %s", line)
	}
	m.busy = false
	m.lastRefresh = time.Time{}
	m.busy = true
	line = strings.Split(ansi.Strip(m.View().Content), "\n")[0]
	if !strings.Contains(line, "loading") {
		t.Fatal("initial loading state disappeared")
	}
}
func TestThemeNameExpiresAndPreviewDoesNotPersist(t *testing.T) {
	m := demoModel(t)
	original := m.settings.Config.Theme
	m.openPicker("themes")
	m.pickerKey(tea.KeyPressMsg{Code: tea.KeyDown})
	saved, _ := LoadSettings(m.settings.Path)
	if saved.Config.Theme != original {
		t.Fatal("preview persisted before selection")
	}
	m.pickerKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	before := time.Now()
	m.selectTheme(m.settings.Themes["nord"])
	if m.themeUntil.Sub(before) < 10*time.Second || m.themeUntil.Sub(before) > 11*time.Second {
		t.Fatal("theme timeout is not ten seconds")
	}
	token := m.themeTimer
	line := strings.Split(ansi.Strip(m.View().Content), "\n")[0]
	if !strings.Contains(line, "nord") {
		t.Fatalf("new theme not announced: %s", line)
	}
	m.Update(themeHiddenMsg{token - 1})
	if m.themeUntil.IsZero() {
		t.Fatal("old timer hid a new theme")
	}
	m.Update(themeHiddenMsg{token})
	line = strings.Split(ansi.Strip(m.View().Content), "\n")[0]
	if strings.Contains(line, "nord") {
		t.Fatal("expired theme still displayed")
	}
	saved, _ = LoadSettings(m.settings.Path)
	if saved.Config.Theme != "nord" {
		t.Fatal("selected theme was not persisted")
	}
}
func TestInspectorJSONKeysAndModalHighlighting(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 24})
	m.inspectorFocused = true
	key(m, "J")
	if m.mode != "" || m.inspectorFormat != "json" || !strings.Contains(m.inspectorContent, m.theme.style("accent").Render(`"apiVersion"`)) {
		t.Fatal("focused inspector did not switch to JSON")
	}
	key(m, "y")
	if m.inspectorFormat != "yaml" || !strings.Contains(m.inspectorContent, "apiVersion") {
		t.Fatal("inspector did not return to YAML")
	}
	m.inspectorFocused = false
	key(m, "J")
	if m.mode != "details" || !strings.HasPrefix(m.panelTitle, "JSON") {
		t.Fatal("table J did not open JSON")
	}
	key(m, "y")
	if !strings.HasPrefix(m.panelTitle, "YAML") {
		t.Fatal("JSON panel y did not switch back")
	}
}
