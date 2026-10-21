package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
)

func demoModel(t *testing.T) *model {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s, err := LoadSettings("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := newModel(context.Background(), s, Options{Demo: true, View: "pods"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.close)
	_, cmd := m.Update(m.connect(m.contextName)())
	_, _ = m.Update(cmd())
	return m
}
func key(m *model, s string) {
	var code rune
	switch s {
	case "esc":
		code = tea.KeyEscape
	case "enter":
		code = tea.KeyEnter
	case "tab":
		code = tea.KeyTab
	default:
		code = []rune(s)[0]
	}
	m.Update(tea.KeyPressMsg{Code: code, Text: func() string {
		if len(s) == 1 {
			return s
		}
		return ""
	}()})
}
func TestRejectOldResultsAndRetainStaleSnapshot(t *testing.T) {
	m := demoModel(t)
	if len(m.rows) != 12 {
		t.Fatalf("demo rows: %d", len(m.rows))
	}
	original := m.rows[0].GetName()
	m.Update(snapshotMsg{generation: m.generation - 1, request: m.request, rows: nil})
	if len(m.rows) != 12 {
		t.Fatal("old context changed current rows")
	}
	m.Update(snapshotMsg{generation: m.generation, request: m.request - 1, rows: nil})
	if len(m.rows) != 12 {
		t.Fatal("old route changed current rows")
	}
	m.Update(snapshotMsg{generation: m.generation, request: m.request, err: errors.New("network failed")})
	if len(m.rows) != 12 || m.rows[0].GetName() != original || m.dataErr == "" {
		t.Fatal("refresh failure erased last good snapshot")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "STALE") {
		t.Fatal("stale snapshot lacks visible label")
	}
}
func TestSelectionAndIndependentInspector(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 24})
	m.move(4)
	o, _ := m.current()
	id := kube.Identity(o)
	m.sortColumn = "restarts"
	m.rebuild(id)
	o, _ = m.current()
	if kube.Identity(o) != id {
		t.Fatal("sorting moved selection")
	}
	m.inspectorFocused = true
	key(m, "G")
	o, _ = m.current()
	if kube.Identity(o) != id {
		t.Fatal("inspector changed selected row")
	}
	if m.inspector.YOffset() == 0 {
		t.Fatal("inspector did not scroll")
	}
	key(m, "esc")
	if m.inspectorFocused {
		t.Fatal("escape did not return focus")
	}
	m.startInput("filter", "")
	for _, s := range []string{"h", "j", "k", "l"} {
		key(m, s)
	}
	if m.input.Value() != "hjkl" {
		t.Fatal("navigation stole search input")
	}
	key(m, "esc")
	if m.filter != "" || len(m.visible) != 12 {
		t.Fatal("cancel filter did not restore rows")
	}
}
func TestContextAndRouteChangesClearData(t *testing.T) {
	m := demoModel(t)
	cmd := m.changeResource("nodes", true)
	if len(m.rows) != 0 || m.lastRefresh.IsZero() == false {
		t.Fatal("route showed previous resource data")
	}
	m.Update(cmd())
	if len(m.rows) != 3 {
		t.Fatal("node list failed")
	}
	m.connect("demo")
	if len(m.rows) != 0 || len(m.visible) != 0 || m.backend != nil {
		t.Fatal("context retained previous data")
	}
}

func TestUnknownInitialViewDoesNotFallBack(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s, err := LoadSettings("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := newModel(context.Background(), s, Options{Demo: true, View: "missing"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	_, cmd := m.Update(m.connect(m.contextName)())
	if cmd != nil || m.dataErr == "" || len(m.rows) != 0 {
		t.Fatal("unknown initial resource fell back to another table")
	}
}
func TestThemePreviewCancelAndLogsTokens(t *testing.T) {
	m := demoModel(t)
	before := m.theme.Name
	m.openPicker("themes")
	m.pickerKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.theme.Name == before {
		t.Fatal("theme did not preview")
	}
	key(m, "esc")
	if m.theme.Name != before {
		t.Fatal("theme cancel did not restore")
	}
	cmd := m.openLogs(false)
	if m.mode != "picker" || m.pickerKind != "containers" || cmd == nil {
		t.Fatal("multi-container pod did not offer picker")
	}
	cmd = m.pickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("container selection did not load logs")
	}
	msg := cmd()
	key(m, "esc")
	m.Update(msg)
	if m.mode != "" {
		t.Fatal("late logs reopened panel")
	}
}
func TestFrameDimensionsAndSecretCustomColumns(t *testing.T) {
	m := demoModel(t)
	for _, size := range [][2]int{{80, 24}, {180, 48}, {40, 12}} {
		for _, layout := range []string{"classic", "inspector", "bottom"} {
			m.layout = layout
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			for _, mode := range []string{"", "details", "picker"} {
				if mode == "details" {
					m.openDetails(true, false)
				} else if mode == "picker" {
					m.openPicker("themes")
				} else {
					m.mode = ""
				}
				content := m.View().Content
				lines := strings.Split(content, "\n")
				if len(lines) > size[1] {
					t.Fatalf("%dx%d %s %s overflows vertically: %d", size[0], size[1], layout, mode, len(lines))
				}
				for _, line := range lines {
					if ansi.StringWidth(line) > size[0] {
						t.Fatalf("%dx%d %s %s overflows horizontally", size[0], size[1], layout, mode)
					}
				}
			}
		}
	}
	m.mode = ""
	cmd := m.changeResource("secrets", false)
	m.Update(cmd())
	m.command("columns name,/data/token")
	screen := ansi.Strip(m.View().Content)
	if strings.Contains(screen, "ZGVtby1vbmx5") || !strings.Contains(screen, "redacted") {
		t.Fatal("custom column exposed secret")
	}
}
func TestConfigValidationAndCustomThemes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(SampleConfig), 0600)
	s, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Themes) != 37 {
		t.Fatalf("theme count %d", len(s.Themes))
	}
	os.Mkdir(filepath.Join(dir, "themes"), 0700)
	os.WriteFile(filepath.Join(dir, "themes", "custom.toml"), []byte("version=1\nname='custom'\nextends='nord'\n[colors]\naccent='#123456'\n"), 0600)
	s, err = LoadSettings(path)
	if err != nil || s.Themes["custom"].Colors["accent"] != "#123456" {
		t.Fatalf("custom theme failed: %v", err)
	}
	os.WriteFile(path, []byte("version: 1\ntypo: true\n"), 0600)
	if _, err := LoadSettings(path); err == nil {
		t.Fatal("unknown key accepted")
	}
	os.WriteFile(path, []byte("version: 1\nrefresh_interval: 10ms\n"), 0600)
	if _, err := LoadSettings(path); err == nil {
		t.Fatal("unbounded interval accepted")
	}
	os.WriteFile(path, []byte(SampleConfig), 0600)
	os.WriteFile(filepath.Join(dir, "themes", "custom.toml"), []byte("version=1\nname='custom'\nextends='custom'\n"), 0600)
	if _, err := LoadSettings(path); err == nil {
		t.Fatal("theme cycle accepted")
	}
}
