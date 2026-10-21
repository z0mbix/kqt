package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func chooseFirstDebugImage(t *testing.T, m *model) {
	t.Helper()
	runSubmitted(m, "debug")
	if m.mode != "picker" || m.pickerKind != "debug-image-source" {
		t.Fatalf("no first-use chooser: %s %s", m.mode, m.pickerKind)
	}
	m.pickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != "debug-image" {
		t.Fatal("no image input")
	}
}

func TestFirstDebugImageCancellationValidationSaveAndReuse(t *testing.T) {
	m := demoModel(t)
	before, _ := os.ReadFile(m.settings.Path)
	chooseFirstDebugImage(t, m)
	key(m, "q")
	if m.mode != "debug-image" || m.input.Value() != "q" {
		t.Fatal("q in an image name cancelled the prompt")
	}
	key(m, "esc")
	after, _ := os.ReadFile(m.settings.Path)
	if string(after) != string(before) || m.debug != nil || len(m.commandExecutions) != 0 {
		t.Fatal("cancelled setup saved state")
	}
	chooseFirstDebugImage(t, m)
	m.input.SetValue("busybox")
	m.debugKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != "debug-image" || !strings.Contains(m.panel.GetContent(), "fully qualified") {
		t.Fatal("invalid image accepted")
	}
	after, _ = os.ReadFile(m.settings.Path)
	if string(after) != string(before) {
		t.Fatal("invalid image was saved")
	}
	image := "123456789012.dkr.ecr.eu-west-2.amazonaws.com/platform/debug-tools:approved"
	m.input.SetValue(image)
	m.debugKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != "debug-loading" || m.debug.profile.Image != image {
		t.Fatalf("save did not continue to preflight: %s", m.mode)
	}
	loaded, err := LoadSettings(m.settings.Path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Debug.DefaultProfile != "default" || loaded.Config.Debug.Profiles["default"].Image != image {
		t.Fatal("first image not persisted")
	}
	info, err := os.Stat(m.settings.Path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("configuration permissions are not private")
	}
	if len(m.settings.CommandHistory) != 0 {
		t.Fatal("setup counted as a completed debug command")
	}
	key(m, "esc")
	m.settings = loaded
	m.openDebug("")
	if m.mode != "debug-loading" {
		t.Fatal("saved image was not reused")
	}
}

func TestFirstDebugImageBusyBoxIsExplicitAndNonRoot(t *testing.T) {
	m := demoModel(t)
	before, _ := os.ReadFile(m.settings.Path)
	runSubmitted(m, "debug")
	if !strings.Contains(m.debugChoices("debug-image-source")[1].description, "public Docker Hub") {
		t.Fatal("public image choice was not disclosed")
	}
	after, _ := os.ReadFile(m.settings.Path)
	if string(before) != string(after) {
		t.Fatal("public image saved before confirmation")
	}
	m.pickerIndex = 1
	cmd := m.pickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || m.mode != "debug-loading" {
		t.Fatal("BusyBox choice added another prompt")
	}
	profile, err := compileDebugProfile(m.settings.Config.Debug.Profiles["default"])
	if err != nil || profile.SecurityContext.RunAsUser == nil || *profile.SecurityContext.RunAsUser != 1000 || profile.Image != busyBoxDebugImage {
		t.Fatalf("BusyBox non-root profile: %#v %v", profile, err)
	}
}

func TestFirstDebugImageSaveFailureKeepsPromptAndMemory(t *testing.T) {
	m := demoModel(t)
	chooseFirstDebugImage(t, m)
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	m.settings.Path = filepath.Join(parent, "config.yaml")
	m.input.SetValue("registry.example.com/debug:approved")
	m.debugKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != "debug-image" || !strings.Contains(m.panel.GetContent(), "Unable to save") || len(m.settings.Config.Debug.Profiles) != 0 || len(m.settings.CommandHistory) != 0 {
		t.Fatal("failed save published state or continued")
	}
}

func TestSaveDebugImagePreservesExistingProfilesAndConfiguration(t *testing.T) {
	for _, format := range []string{"yaml", "toml"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config."+format)
			text := "# retain this comment\ntheme: nord\neditor: code --wait\ndebug:\n  default_profile: default\n  profiles:\n    default:\n      image: registry.example.com/existing:approved\n"
			if format == "toml" {
				text = "theme = 'nord'\neditor = 'code --wait'\n[debug]\ndefault_profile = 'default'\n[debug.profiles.default]\nimage = 'registry.example.com/existing:approved'\n"
			}
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			settings := &Settings{Path: path, Config: defaultConfig()}
			name, err := settings.saveDebugProfile(debugProfile{Image: "registry.example.com/new:approved"})
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			if name != "default-2" || loaded.Config.Debug.DefaultProfile != "default" || loaded.Config.Debug.Profiles["default"].Image != "registry.example.com/existing:approved" || loaded.Config.Debug.Profiles[name].Image != "registry.example.com/new:approved" || loaded.Config.Theme != "nord" || loaded.Config.Editor != "code --wait" {
				t.Fatal("saving first-use profile overwrote existing settings")
			}
			data, _ := os.ReadFile(path)
			if format == "yaml" && !strings.Contains(string(data), "retain this comment") {
				t.Fatal("YAML comment lost")
			}
		})
	}
}

func TestFirstDebugImagePromptGeometry(t *testing.T) {
	m := demoModel(t)
	chooseFirstDebugImage(t, m)
	m.input.SetValue("registry.example.com/" + strings.Repeat("long-path/", 35) + "debug@sha256:" + strings.Repeat("a", 64))
	for _, size := range [][2]int{{40, 12}, {80, 24}, {180, 48}} {
		m.width, m.height = size[0], size[1]
		m.resize()
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) != m.height {
			t.Fatalf("height %d: %d", m.height, len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != m.width {
				t.Fatalf("width %d: %d", m.width, ansi.StringWidth(line))
			}
		}
	}
}
