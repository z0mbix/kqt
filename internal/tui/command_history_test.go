package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func submitCommand(m *model, value string) {
	m.startInput("command", value)
	m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
}

func historyKey(m *model, code rune) { m.key(tea.KeyPressMsg{Code: code}) }

func TestCommandHistoryRecallsWithoutExecutingAndRestoresDraft(t *testing.T) {
	m := demoModel(t)
	submitCommand(m, "themes")
	key(m, "esc")
	submitCommand(m, "layouts")
	key(m, "esc")
	submitCommand(m, "theme nord")
	m.startInput("command", "theme dracula")
	m.input.SetCursor(5)
	for _, want := range []string{"theme nord", "layouts", "themes", "themes"} {
		historyKey(m, tea.KeyUp)
		if m.input.Value() != want || m.mode != "command" || m.theme.Name != "nord" {
			t.Fatalf("history executed or recalled wrong command: %q, %s, %s", m.input.Value(), m.mode, m.theme.Name)
		}
	}
	for _, want := range []string{"layouts", "theme nord", "theme dracula", "theme dracula"} {
		historyKey(m, tea.KeyDown)
		if m.input.Value() != want {
			t.Fatalf("down recalled %q, want %q", m.input.Value(), want)
		}
	}
	if m.input.Position() != 5 {
		t.Fatal("returning to draft moved its cursor")
	}
	historyKey(m, tea.KeyUp)
	m.input.SetValue("theme dracula")
	historyKey(m, tea.KeyEnter)
	if m.theme.Name != "dracula" || len(m.settings.CommandHistory) != 4 {
		t.Fatal("edited recalled command was not submitted")
	}
	m.startInput("command", "")
	historyKey(m, tea.KeyUp)
	if m.input.Value() != "theme dracula" {
		t.Fatal("new prompt did not start at newest command")
	}
}

func TestCommandHistoryBoundPersistenceAndCancelledInputs(t *testing.T) {
	m := demoModel(t)
	for i := range defaultHistoryLimit + 3 {
		submitCommand(m, fmt.Sprintf("sort /metadata/labels/history%d asc", i))
	}
	submitCommand(m, "   ")
	m.startInput("command", "cancelled")
	key(m, "esc")
	m.startInput("filter", "web")
	historyKey(m, tea.KeyUp)
	historyKey(m, tea.KeyEnter)
	history := m.settings.CommandHistory
	if len(history) != defaultHistoryLimit || history[0] != "sort /metadata/labels/history3 asc" || history[defaultHistoryLimit-1] != fmt.Sprintf("sort /metadata/labels/history%d asc", defaultHistoryLimit+2) {
		t.Fatalf("history did not retain the default number of successful commands: %v", history)
	}
	saved, err := LoadSettings(m.settings.Path)
	if err != nil {
		t.Fatal(err)
	}
	saved.loadCommandHistory()
	if !slices.Equal(saved.CommandHistory, history) {
		t.Fatalf("history did not persist: %v", err)
	}
	info, err := os.Stat(saved.historyPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("saved history cache is not private")
	}
	restarted, err := newModel(context.Background(), saved, Options{Demo: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.close()
	restarted.startInput("command", "")
	for i := len(history) - 1; i >= 0; i-- {
		historyKey(restarted, tea.KeyUp)
		if restarted.input.Value() != history[i] {
			t.Fatalf("restarted history entry %d: %q", i, restarted.input.Value())
		}
	}
}

func TestCommandHistoryAndCompletionRemainIndependent(t *testing.T) {
	m := demoModel(t)
	m.startInput("command", "draft")
	historyKey(m, tea.KeyUp)
	historyKey(m, tea.KeyDown)
	if m.input.Value() != "draft" {
		t.Fatal("empty history changed draft")
	}
	submitCommand(m, "theme")
	key(m, "esc")
	m.startInput("command", "lay")
	m.completeCommand(false)
	historyKey(m, tea.KeyUp)
	if len(m.completion.matches) != 0 {
		t.Fatal("history retained completion cycle from draft")
	}
	m.completeCommand(false)
	if m.input.Value() != "theme" {
		t.Fatal("completion did not use recalled command")
	}
	m.completeCommand(false)
	if m.input.Value() != "themes" {
		t.Fatal("completion stopped cycling after history recall")
	}
	key(m, "esc")
	m.startInput("filter", "web0003")
	historyKey(m, tea.KeyUp)
	if m.input.Value() != "web0003" {
		t.Fatal("command history stole filter navigation")
	}
}

func TestCommandHistoryConfigFormatsPreserveOtherSettings(t *testing.T) {
	for _, format := range []string{"yaml", "toml"} {
		t.Run(format, func(t *testing.T) {
			m := demoModel(t)
			m.settings.Path = filepath.Join(t.TempDir(), "config."+format)
			body := "version: 1\neditor: 'nvim -f' # keep editor\ncommand_history: [themes, '', layouts]\n"
			if format == "toml" {
				body = "version=1\neditor='nvim -f'\ncommand_history=['themes', '', 'layouts']\n"
			}
			if err := os.WriteFile(m.settings.Path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := LoadSettings(m.settings.Path)
			if err != nil {
				t.Fatal(err)
			}
			s.loadCommandHistory()
			m.settings = s
			submitCommand(m, "  theme nord  ")
			if m.theme.Name != "nord" {
				t.Fatal("surrounding whitespace prevented command execution")
			}
			saved, err := LoadSettings(s.Path)
			if err != nil {
				t.Fatal(err)
			}
			saved.loadCommandHistory()
			if !slices.Equal(saved.CommandHistory, []string{"themes", "layouts", "theme nord"}) || saved.Config.Editor != "nvim -f" {
				t.Fatalf("history migration lost commands or settings: %v, %v", saved.CommandHistory, err)
			}
			data, _ := os.ReadFile(s.Path)
			if strings.Contains(string(data), "command_history") {
				t.Fatal("migrated history remains in config")
			}
			if format == "yaml" && !strings.Contains(string(data), "# keep editor") {
				t.Fatal("history patch lost YAML comment")
			}
		})
	}
}
