package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestHistoryLimitConfigDefaultsValidationAndPreservation(t *testing.T) {
	for _, extension := range []string{"yaml", "toml"} {
		t.Run(extension, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config."+extension)
			for _, limit := range []string{"", "1", "500", "0", "-1", "'many'"} {
				body := "version: 1\n# keep my settings\neditor: 'nvim -f'\n"
				if extension == "toml" {
					body = "version=1\neditor='nvim -f'\n"
				}
				if limit != "" {
					separator := ": "
					if extension == "toml" {
						separator = "="
					}
					body += "history_limit" + separator + limit + "\n"
				}
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				s, err := LoadSettings(path)
				invalid := limit == "0" || limit == "-1" || limit == "'many'"
				if invalid {
					if err == nil {
						t.Fatalf("invalid history limit accepted: %s", limit)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				want := 100
				if limit != "" {
					fmt.Sscan(limit, &want)
				}
				if s.Config.HistoryLimit != want || s.Config.Editor != "nvim -f" {
					t.Fatal("limit/default or unrelated config was lost")
				}
				if err := s.savePatch(map[string]any{"history_limit": 0}); err == nil {
					t.Fatal("invalid history limit was written to config")
				}
				after, _ := os.ReadFile(path)
				if string(after) != body {
					t.Fatal("invalid limit changed the config file")
				}
			}
		})
	}
}

func TestConfiguredHistoryLimitControlsPersistenceRecallAndSearch(t *testing.T) {
	for _, limit := range []int{1, 7, 125} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			m := demoModel(t)
			m.settings.Config.HistoryLimit = limit
			if err := m.settings.savePatch(map[string]any{"history_limit": limit}); err != nil {
				t.Fatal(err)
			}
			for i := range limit + 3 {
				submitCommand(m, fmt.Sprintf("sort /metadata/labels/history%d asc", i))
			}
			want := slices.Clone(m.settings.CommandHistory)
			if len(want) != limit || want[0] != "sort /metadata/labels/history3 asc" {
				t.Fatal("configured limit did not retain the newest commands", want)
			}
			saved, err := LoadSettings(m.settings.Path)
			if err != nil {
				t.Fatal(err)
			}
			restarted, err := newModel(context.Background(), saved, Options{Demo: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.close()
			if saved.Config.HistoryLimit != limit || !slices.Equal(saved.CommandHistory, want) {
				t.Fatal("restart did not apply the configured limit", saved.CommandHistory)
			}
			restarted.startInput("command", "draft")
			for range limit + 2 {
				historyKey(restarted, tea.KeyUp)
			}
			if restarted.input.Value() != want[0] {
				t.Fatal("recall did not stop at the oldest retained command")
			}
			ctrlHistory(restarted, 'r')
			typeHistoryQuery(restarted, "history2 asc")
			if restarted.historySearch.index != -1 {
				t.Fatal("search found a command outside the configured limit")
			}
			key(restarted, "esc")
			ctrlHistory(restarted, 'r')
			typeHistoryQuery(restarted, "history3 asc")
			if restarted.historySearch.index != 0 {
				t.Fatal("search could not find the oldest retained command")
			}
			// A smaller setting applies when loading an existing larger cache.
			if limit > 3 {
				saved.Config.HistoryLimit = 3
				saved.loadCommandHistory()
				if !slices.Equal(saved.CommandHistory, want[len(want)-3:]) {
					t.Fatal("reducing the limit retained older commands")
				}
				if err := saved.saveCommandHistory(); err != nil {
					t.Fatal(err)
				}
				data, _ := os.ReadFile(saved.historyPath)
				var stored []string
				if err := json.Unmarshal(data, &stored); err != nil || len(stored) != 3 {
					t.Fatal("saving ignored the reduced limit", err)
				}
			}
		})
	}
}

func TestHistoryMigrationUsesConfiguredLimit(t *testing.T) {
	m := demoModel(t)
	os.WriteFile(m.settings.Path, []byte("version: 1\nhistory_limit: 2\ncommand_history: [themes, layouts, help, pods]\n"), 0600)
	s, err := LoadSettings(m.settings.Path)
	if err != nil {
		t.Fatal(err)
	}
	s.loadCommandHistory()
	if !slices.Equal(s.CommandHistory, []string{"help", "pods"}) || s.historyWarning != "" {
		t.Fatal("migration ignored the configured limit", s.CommandHistory, s.historyWarning)
	}
	data, _ := os.ReadFile(s.Path)
	if strings.Contains(string(data), "command_history") || !strings.Contains(string(data), "history_limit: 2") {
		t.Fatal("migration lost the limit or retained legacy commands")
	}
}
