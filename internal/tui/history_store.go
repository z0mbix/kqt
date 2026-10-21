package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/z0mbix/kqt/internal/kube"
)

func commandHistoryPath() (string, error) {
	root := os.Getenv("XDG_CACHE_HOME")
	if root != "" && !filepath.IsAbs(root) {
		return "", fmt.Errorf("XDG_CACHE_HOME must be an absolute path")
	}
	if root == "" {
		var err error
		if runtime.GOOS == "darwin" {
			root, err = os.UserHomeDir()
			root = filepath.Join(root, ".local", "cache")
		} else {
			root, err = os.UserCacheDir()
		}
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(root, "kqt", "history.json"), nil
}

func readCommandHistory(path string, historyLimit int) ([]string, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	const limit = 4 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("history file exceeds 4 MiB: %s", path)
	}
	var entries []string
	if err = json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("invalid history file %s: %w", path, err)
	}
	return boundedCommandHistory(entries, historyLimit), nil
}

// Avoid duplicating history if copying succeeded but config cleanup previously
// failed. Cached commands are newer than entries in the legacy config.
func mergeLegacyHistory(legacy, cached []string, limit int) []string {
	for overlap := min(len(legacy), len(cached)); overlap >= 0; overlap-- {
		if slices.Equal(legacy[len(legacy)-overlap:], cached[:overlap]) {
			return boundedCommandHistory(append(slices.Clone(legacy[:len(legacy)-overlap]), cached...), limit)
		}
	}
	return nil
}

// Cache failures never prevent startup, config validation or command execution.
// Load only when starting the TUI; `kqt config --validate` remains read-only.
func (s *Settings) loadCommandHistory() {
	s.historyWarning = ""
	legacy := boundedCommandHistory(s.Config.LegacyHistory, s.Config.HistoryLimit)
	path, err := commandHistoryPath()
	s.historyPath = path
	if err == nil {
		s.CommandHistory, err = readCommandHistory(path, s.Config.HistoryLimit)
	}
	s.CommandHistory = mergeLegacyHistory(legacy, s.CommandHistory, s.Config.HistoryLimit)
	if err != nil {
		s.historyWarning = "Unable to load command history: " + kube.Safe(err.Error())
		return
	}
	if s.Config.LegacyHistory != nil {
		if err = s.saveCommandHistory(); err != nil {
			s.historyWarning = "Unable to migrate command history: " + kube.Safe(err.Error())
		}
	}
}

func (s *Settings) saveCommandHistory() error {
	if s.historyPath == "" {
		var err error
		if s.historyPath, err = commandHistoryPath(); err != nil {
			return err
		}
	}
	entries := boundedCommandHistory(s.CommandHistory, s.Config.HistoryLimit)
	if entries == nil {
		entries = []string{}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.historyPath), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.historyPath), ".kqt-history-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(append(data, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, s.historyPath)
	}
	if err != nil {
		return err
	}
	// Never remove the old copy before the cache has been written successfully.
	if s.Config.LegacyHistory != nil {
		if err = s.savePatch(map[string]any{"command_history": nil}); err != nil {
			return fmt.Errorf("history saved; unable to remove legacy config history: %w", err)
		}
		s.Config.LegacyHistory = nil
	}
	s.historyWarning = ""
	return nil
}
