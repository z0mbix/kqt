package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestBundleDownloadPromptAndPrivateArchive(t *testing.T) {
	m := demoModel(t)
	dir := t.TempDir()
	m.settings.Config.DownloadDir = dir
	m.options.ReadOnly = true
	cmd := m.executeSubmittedCommand("bundle")
	if cmd == nil || m.mode != "bundle-save" || !strings.HasPrefix(m.input.Value(), dir) {
		t.Fatal("missing default download prompt")
	}
	path := m.input.Value()
	cmd = m.bundleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || m.mode != "bundle-loading" {
		t.Fatal("export did not start")
	}
	m.Update(cmd())
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 || m.mode != "" || !strings.Contains(m.notice, "saved to") {
		t.Fatal("bundle did not save privately")
	}
	if len(m.settings.CommandHistory) != 1 || m.settings.CommandHistory[0] != "bundle" {
		t.Fatal("successful export missing history")
	}
	cmd = m.executeSubmittedCommand("bundle " + path)
	m.Update(cmd())
	if !strings.Contains(m.notice, "Unable to save bundle") {
		t.Fatal("overwrote existing bundle")
	}
}
func TestBundleCancelAndFailedHistory(t *testing.T) {
	m := demoModel(t)
	m.settings.Config.DownloadDir = t.TempDir()
	m.executeSubmittedCommand("bundle --logs")
	m.bundleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.mode != "" || len(m.commandExecutions) != 0 || len(m.settings.CommandHistory) != 0 {
		t.Fatal("cancelled bundle saved history")
	}
	m.executeSubmittedCommand("bundle --typo")
	if len(m.settings.CommandHistory) != 0 {
		t.Fatal("invalid bundle saved history")
	}
	cmd := m.executeSubmittedCommand("bundle cancelled.tar.gz")
	token := m.bundleToken
	m.bundleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(cmd())
	if _, err := os.Stat(filepath.Join(m.settings.Config.DownloadDir, "cancelled.tar.gz")); !os.IsNotExist(err) {
		t.Fatal("cancelled capture wrote a file")
	}
	m.Update(bundleSavedMsg{generation: m.generation, token: token, path: "late.tar.gz"})
	if m.mode != "" || m.notice != "Bundle export cancelled" {
		t.Fatal("stale completion changed UI")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := saveDownloadContext(ctx, m.settings.Config.DownloadDir, "cancelled", []byte("data")); err == nil {
		t.Fatal("cancelled writer saved data")
	}
}
