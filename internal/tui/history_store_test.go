package tui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestHistoryCachePathHonoursXDGAndPlatformDefault(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	if path, err := commandHistoryPath(); err != nil || path != filepath.Join(root, "kqt", "history.json") {
		t.Fatal("XDG cache location was ignored", path, err)
	}
	t.Setenv("XDG_CACHE_HOME", "relative/cache")
	if _, err := commandHistoryPath(); err == nil {
		t.Fatal("relative XDG cache location accepted")
	}
	t.Setenv("XDG_CACHE_HOME", "")
	root, err := os.UserCacheDir()
	if runtime.GOOS == "darwin" {
		root, err = os.UserHomeDir()
		root = filepath.Join(root, ".local", "cache")
	}
	if err != nil {
		t.Fatal(err)
	}
	if path, err := commandHistoryPath(); err != nil || path != filepath.Join(root, "kqt", "history.json") {
		t.Fatal("wrong default history path", path, err)
	}
}

func TestHistoryMigrationStartsWithTUIAndDeletingCacheClearsHistory(t *testing.T) {
	for _, extension := range []string{"yaml", "toml"} {
		t.Run(extension, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			path := filepath.Join(t.TempDir(), "config."+extension)
			body := "version: 1\n# keep this comment\neditor: 'nvim -f'\ncommand_history: [themes, themes, layouts]\n"
			if extension == "toml" {
				body = "version=1\neditor='nvim -f'\ncommand_history=['themes','themes','layouts']\n"
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := LoadSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			historyPath, _ := commandHistoryPath()
			if _, err := os.Stat(historyPath); !os.IsNotExist(err) {
				t.Fatal("config validation read/wrote the history cache", err)
			}
			unchanged, _ := os.ReadFile(path)
			if string(unchanged) != body {
				t.Fatal("config loading migrated history before TUI startup")
			}
			m, err := newModel(context.Background(), s, Options{Demo: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.close()
			entries, err := readCommandHistory(historyPath, s.Config.HistoryLimit)
			if err != nil || !slices.Equal(entries, []string{"themes", "themes", "layouts"}) || s.historyWarning != "" {
				t.Fatal("migration lost commands, including intentional repeats", entries, err, s.historyWarning)
			}
			for location, mode := range map[string]os.FileMode{historyPath: 0600, filepath.Dir(historyPath): 0700} {
				info, err := os.Stat(location)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatal("history cache is not private", location, err)
				}
			}
			migrated, _ := os.ReadFile(path)
			if strings.Contains(string(migrated), "command_history") || !strings.Contains(string(migrated), "nvim -f") || extension == "yaml" && !strings.Contains(string(migrated), "# keep this comment") {
				t.Fatal("migration failed to preserve config or remove legacy history")
			}
			submitCommand(m, "help")
			after, _ := os.ReadFile(path)
			if string(after) != string(migrated) || !slices.Contains(s.CommandHistory, "help") {
				t.Fatal("a history-only command rewrote the config")
			}
			os.Remove(historyPath)
			reloaded, err := LoadSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			reloaded.loadCommandHistory()
			if len(reloaded.CommandHistory) != 0 || reloaded.historyWarning != "" {
				t.Fatal("deleted cache resurrected migrated history")
			}
		})
	}
}

func TestHistoryMigrationRetainsConfigOnCacheFailureAndRetries(t *testing.T) {
	m := demoModel(t)
	path := m.settings.Path
	body := "version: 1\ncommand_history: [themes, layouts]\n"
	os.WriteFile(path, []byte(body), 0600)
	s, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), "blocked")
	os.WriteFile(blocked, []byte("not a directory"), 0600)
	t.Setenv("XDG_CACHE_HOME", blocked)
	s.loadCommandHistory()
	before, _ := os.ReadFile(path)
	if string(before) != body || !slices.Equal(s.CommandHistory, []string{"themes", "layouts"}) || s.historyWarning == "" {
		t.Fatal("failed migration lost old history/config or hid the failure")
	}
	m.settings = s
	submitCommand(m, "help")
	if m.mode != "help" || !slices.Contains(s.CommandHistory, "help") {
		t.Fatal("unavailable cache prevented execution or in-memory history")
	}
	// Make the same cache path usable; the next successful command retries.
	os.Remove(blocked)
	os.Mkdir(blocked, 0700)
	key(m, "esc")
	submitCommand(m, "help")
	entries, err := readCommandHistory(s.historyPath, s.Config.HistoryLimit)
	after, _ := os.ReadFile(path)
	if err != nil || len(entries) != 4 || strings.Contains(string(after), "command_history") || s.historyWarning != "" {
		t.Fatal("history migration did not recover", entries, err, s.historyWarning)
	}
}

func TestHistoryMigrationCleanupFailureDoesNotDuplicateOnRetry(t *testing.T) {
	m := demoModel(t)
	os.WriteFile(m.settings.Path, []byte("version: 1\ncommand_history: [themes, layouts]\n"), 0600)
	s, err := LoadSettings(m.settings.Path)
	if err != nil {
		t.Fatal(err)
	}
	// The cache write succeeds, but config cleanup cannot replace a directory.
	os.Remove(s.Path)
	os.Mkdir(s.Path, 0700)
	s.loadCommandHistory()
	if s.historyWarning == "" || s.Config.LegacyHistory == nil {
		t.Fatal("failed cleanup was hidden or discarded the migration source")
	}
	s.loadCommandHistory()
	if !slices.Equal(s.CommandHistory, []string{"themes", "layouts"}) {
		t.Fatal("retry duplicated migrated history", s.CommandHistory)
	}
	os.Remove(s.Path)
	os.WriteFile(s.Path, []byte("version: 1\ncommand_history: [themes, layouts]\n"), 0600)
	s.loadCommandHistory()
	if s.historyWarning != "" || s.Config.LegacyHistory != nil {
		t.Fatal("migration cleanup did not recover", s.historyWarning)
	}
}

func TestHistoryCorruptCacheDoesNotBreakConfigValidationOrTUI(t *testing.T) {
	m := demoModel(t)
	cache, _ := commandHistoryPath()
	os.MkdirAll(filepath.Dir(cache), 0700)
	os.WriteFile(cache, []byte("invalid JSON"), 0600)
	s, err := LoadSettings(m.settings.Path)
	if err != nil {
		t.Fatal("cache corruption broke config validation", err)
	}
	s.loadCommandHistory()
	if s.historyWarning == "" || len(s.CommandHistory) != 0 {
		t.Fatal("corrupt cache was not handled as a non-fatal failure")
	}
	m.settings = s
	submitCommand(m, "help")
	entries, err := readCommandHistory(cache, s.Config.HistoryLimit)
	if m.mode != "help" || err != nil || !slices.Equal(entries, []string{"help"}) || s.historyWarning != "" {
		t.Fatal("cache could not recover after a successful command", entries, err)
	}
}
