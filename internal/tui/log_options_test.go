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

func TestLogPreferencesPreserveDefaultsAndRoundTripYAMLAndTOML(t *testing.T) {
	for _, extension := range []string{"yaml", "toml"} {
		t.Run(extension, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			path := filepath.Join(t.TempDir(), "config."+extension)
			text := "version: 1\n# keep me\nlogs:\n  timestamps: false\n"
			if extension == "toml" {
				text = "version=1\n[logs]\ntimestamps=false\n"
			}
			os.WriteFile(path, []byte(text), 0600)
			settings, err := LoadSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			if settings.Config.Logs.Timestamps || !settings.Config.Logs.Highlight || !settings.Config.Logs.Wrap || !settings.Config.Logs.Follow || settings.Config.Logs.Prefixes != "auto" || settings.Config.Logs.TailLines != nil {
				t.Fatalf("partial config lost defaults: %+v", settings.Config.Logs)
			}
			if err := settings.savePatch(map[string]any{"logs": map[string]any{"since": "10m", "tail_lines": int64(1000), "prefixes": "off", "highlight": false, "wrap": false, "follow": false}}); err != nil {
				t.Fatal(err)
			}
			saved, err := LoadSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			p := saved.Config.Logs
			if p.Since != "10m" || p.TailLines == nil || *p.TailLines != 1000 || p.Prefixes != "off" || p.Timestamps || p.Highlight || p.Wrap || p.Follow {
				t.Fatalf("preferences lost: %+v", p)
			}
			before, _ := os.ReadFile(path)
			if extension == "yaml" && !strings.Contains(string(before), "# keep me") {
				t.Fatal("comment lost")
			}
			if err := saved.savePatch(map[string]any{"logs": map[string]any{"tail_lines": -2}}); err == nil {
				t.Fatal("saved invalid line count")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("invalid preference replaced config")
			}
			if err := saved.savePatch(map[string]any{"logs": map[string]any{"tail_lines": nil}}); err != nil {
				t.Fatal(err)
			}
			saved, err = LoadSettings(path)
			if err != nil || saved.Config.Logs.TailLines != nil {
				t.Fatalf("reset to original defaults failed: %v", err)
			}
			m, err := newModel(context.Background(), saved, Options{Demo: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.close()
			if m.logWrap {
				t.Fatal("saved wrapping default not applied")
			}
			limits := m.logRequestOptions()
			if limits.SinceTime == nil || time.Since(*limits.SinceTime) < 9*time.Minute || time.Since(*limits.SinceTime) > 11*time.Minute {
				t.Fatal("saved time window not applied")
			}
		})
	}
	for _, p := range []logPreferences{{Since: "yesterday", Prefixes: "auto"}, {Since: "-1m", Prefixes: "auto"}, {Prefixes: "invalid"}} {
		if validateLogPreferences(p) == nil {
			t.Fatalf("accepted invalid preferences: %+v", p)
		}
	}
}

func TestLogDisplayTogglesKeepStreamAndFrozenOutput(t *testing.T) {
	m := demoModel(t)
	m.logObject, m.logContainer = m.rows[0], "web"
	m.mode = "logs"
	m.logTail = true
	m.logLines = []string{`2026-10-15T12:00:00Z {"level":"warn","message":"old","time":"application-time"}`}
	m.renderLogs()
	token := m.logToken
	key(m, " ")
	m.logLines = append(m.logLines, `2026-10-15T12:00:01Z {"level":"info","message":"new"}`)
	key(m, "T")
	key(m, "N")
	if m.logToken != token || !m.logPaused {
		t.Fatal("display controls restarted or resumed the stream")
	}
	shown := ansi.Strip(m.panel.GetContent())
	if strings.Contains(shown, "2026-") || !strings.HasPrefix(shown, "[default/") || !strings.Contains(shown, "application-time") || strings.Contains(shown, "new") {
		t.Fatalf("incorrect frozen display: %s", shown)
	}
	key(m, "N")
	if strings.Contains(m.logContent(), "[default/") || strings.ContainsRune(m.logContent(), '\x1b') {
		t.Fatal("export did not respect display preferences")
	}
	key(m, " ")
	if !strings.Contains(ansi.Strip(m.panel.GetContent()), "new") {
		t.Fatal("resuming lost arrivals")
	}
	key(m, "w")
	key(m, "s")
	saved, err := LoadSettings(m.settings.Path)
	if err != nil || saved.Config.Logs.Timestamps || saved.Config.Logs.Prefixes != "off" || saved.Config.Logs.Wrap || saved.Config.Logs.Follow {
		t.Fatalf("display/follow defaults not saved: %+v %v", saved, err)
	}
}

func TestLogOptionsValidateInputPreserveQueryAndCancelOldRequests(t *testing.T) {
	m := demoModel(t)
	m.logObject, m.logContainer, m.logFollow = m.rows[0], "web", false
	m.loadLogs()
	m.logSearch = "level=warn"
	m.logFullScreen = true
	m.openPicker("log-options")
	m.chooseLogOption("since custom")
	m.input.SetValue("yesterday")
	m.logOptionsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != "log-since" || !strings.Contains(ansi.Strip(m.View().Content), "Invalid value") {
		t.Fatal("invalid custom window had no visible error in focus mode")
	}
	token := m.logToken
	m.input.SetValue("10m")
	cmd := m.logOptionsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || m.mode != "logs" || m.logToken <= token || !m.logFullScreen || m.logSearch != "level=warn" {
		t.Fatal("window did not reload logs while preserving focus and filter")
	}
	m.Update(cmd())
	if !strings.Contains(m.logContent(), "WARN slow") || strings.Contains(m.logContent(), "configuration") {
		t.Fatal("reload lost the filter")
	}
	m.openPicker("log-options")
	m.chooseLogOption("tail custom")
	m.input.SetValue("-2")
	m.logOptionsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != "log-tail" {
		t.Fatal("invalid tail closed prompt")
	}
	m.logOptionsKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.mode != "logs" || m.logToken != token+1 {
		t.Fatal("cancelled options restarted logs")
	}
	m.openPicker("log-options")
	m.pickerKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.mode != "logs" || !m.logFullScreen {
		t.Fatal("picker cancellation lost focus mode")
	}
}

func TestLogSnapshotCanFinishWhileOptionsOrFilterIsOpen(t *testing.T) {
	for _, mode := range []string{"log-search", "log-since", "log-tail", "picker"} {
		t.Run(mode, func(t *testing.T) {
			m := demoModel(t)
			m.logObject, m.logContainer = m.rows[0], "web"
			cmd := m.loadLogs()
			m.mode = mode
			if mode == "picker" {
				m.pickerKind, m.pickerReturn = "log-options", "logs"
			}
			m.Update(cmd())
			if m.mode != mode || !strings.Contains(m.logContent(), "configuration loaded") {
				t.Fatal("snapshot was discarded or closed the active prompt")
			}
		})
	}
}
