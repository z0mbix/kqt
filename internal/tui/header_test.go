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
	"github.com/charmbracelet/x/ansi"
)

func TestHeaderToggleReclaimsRowsAndPreservesLayout(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}, {180, 48}} {
		for _, layout := range []string{"classic", "inspector", "bottom"} {
			t.Run(fmt.Sprintf("%s-%dx%d", layout, size[0], size[1]), func(t *testing.T) {
				m := demoModel(t)
				m.selectLayout(layout)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.move(3)
				columns, selected := slices.Clone(m.columns()), m.selected
				before := m.bodyBounds().height
				for _, visible := range []bool{false, true} {
					key(m, "z")
					view := ansi.Strip(m.View().Content)
					rows := strings.Split(view, "\n")
					if len(rows) != size[1] {
						t.Fatalf("height = %d, want %d", len(rows), size[1])
					}
					for _, row := range rows {
						if ansi.StringWidth(row) != size[0] {
							t.Fatal("header toggle broke row width")
						}
					}
					if m.headerVisible() != visible || strings.Contains(view, "Context:") != visible {
						t.Fatal("header visibility does not match the rendered view")
					}
					want := before
					if !visible {
						want += 4
						if !strings.HasPrefix(rows[0], "╭") || m.tableBounds().y != 0 {
							t.Fatal("hidden header left unused space above the table")
						}
					}
					if m.bodyBounds().height != want || m.layout != layout || m.selected != selected || !slices.Equal(m.columns(), columns) {
						t.Fatal("toggle failed to reclaim rows or changed the active view")
					}
					body, table, inspector := m.bodyBounds(), m.tableBounds(), m.inspectorDimensions()
					if inspector.bottom && table.height+inspector.height != body.height || !inspector.bottom && table.height != body.height {
						t.Fatal("table and inspector did not fill the available body")
					}
				}
			})
		}
	}
}

func TestHeaderPreferencePersistsWithoutReplacingOtherSettings(t *testing.T) {
	for _, ext := range []string{"yaml", "toml"} {
		t.Run(ext, func(t *testing.T) {
			m := demoModel(t)
			path := filepath.Join(t.TempDir(), "config."+ext)
			config := "# keep this comment\nversion: 1\ntheme: nord\neditor: vim\n"
			if ext == "toml" {
				config = "version = 1\ntheme = 'nord'\neditor = 'vim'\n"
			}
			if err := os.WriteFile(path, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			settings, err := LoadSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			m.settings = settings
			if !m.headerVisible() {
				t.Fatal("existing config without header_visible hid the header")
			}
			runSubmitted(m, "header off")
			saved, err := LoadSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Config.HeaderVisible == nil || *saved.Config.HeaderVisible || saved.Config.Editor != "vim" || saved.Config.Theme != "nord" {
				t.Fatal("header preference was not saved independently")
			}
			data, _ := os.ReadFile(path)
			if ext == "yaml" && !strings.Contains(string(data), "# keep this comment") {
				t.Fatal("header toggle removed YAML comments")
			}
			restarted, err := newModel(context.Background(), saved, Options{Demo: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.close()
			if restarted.headerVisible() {
				t.Fatal("restart lost hidden-header preference")
			}
			key(restarted, "z")
			saved, err = LoadSettings(path)
			if err != nil || saved.Config.HeaderVisible == nil || !*saved.Config.HeaderVisible {
				t.Fatal("restoring the header was not saved", err)
			}
		})
	}
}

func TestHeaderCommandsHistoryCompletionAndTextInput(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, command := range []string{"header off", "header on", "header toggle", "header"} {
		before := len(m.settings.Config.CommandHistory)
		runSubmitted(m, command)
		if len(m.settings.Config.CommandHistory) != before+1 {
			t.Fatal("successful header command was not remembered")
		}
	}
	before := m.headerVisible()
	runSubmitted(m, "header typo")
	if m.headerVisible() != before || slices.Contains(m.settings.Config.CommandHistory, "header typo") {
		t.Fatal("invalid header command changed preferences or entered history")
	}
	m.startInput("command", "header of")
	m.completeCommand(false)
	if m.input.Value() != "header off" || m.headerVisible() != before {
		t.Fatal("header completion failed or executed the command")
	}
	key(m, "esc")
	for _, mode := range []string{"command", "filter", "picker"} {
		if mode == "picker" {
			m.openPicker("themes")
		} else {
			m.startInput(mode, "")
		}
		key(m, "z")
		if m.input.Value() != "z" || m.headerVisible() != before {
			t.Fatal("header shortcut consumed text input")
		}
		key(m, "esc")
	}
}

func TestHiddenHeaderMouseAndPagingGeometry(t *testing.T) {
	m := demoModel(t)
	m.selectLayout("classic")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	key(m, "z")
	m.Update(tea.MouseClickMsg{X: 3, Y: 4, Button: tea.MouseLeft})
	if m.selected != 2 {
		t.Fatal("row click used the old header offset")
	}
	row := strings.Split(ansi.Strip(m.View().Content), "\n")[1]
	m.Update(tea.MouseClickMsg{X: strings.Index(row, "NAME") + 1, Y: 1, Button: tea.MouseLeft})
	if m.sortColumn != "name" || !m.descending {
		t.Fatal("column click used the old header offset")
	}
	for len(m.visible) < 100 {
		m.visible = append(m.visible, m.visible[0])
	}
	m.selected = 0
	pageKey(m, 'f')
	if m.selected != 19 {
		t.Fatal("paging did not include the four reclaimed rows")
	}
	m.placeInspector("right")
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 24})
	b := m.inspectorBounds()
	m.Update(tea.MouseClickMsg{X: b.x + 2, Y: 2, Button: tea.MouseLeft})
	if !m.inspectorFocused || b.y != 0 {
		t.Fatal("inspector focus used the old header offset")
	}
}

func TestHeaderTogglePreservesLogsAndCopyCoordinates(t *testing.T) {
	m := selectionLogModel(t, 80, 24, strings.Repeat("HEADER\n", 60), false)
	m.logTail = true
	before, token, feed := m.panel.Height(), m.logToken, m.logFeed
	key(m, "z")
	if m.panel.Height() != before+4 || m.logToken != token || m.logFeed != feed || !m.panel.AtBottom() {
		t.Fatal("hiding header did not expand logs while retaining following")
	}
	m.panel.GotoTop()
	if got := copiedText(t, dragText(m, textPoint{1, 1}, textPoint{6, 1})); got != "HEADER" {
		t.Fatalf("headerless log selection copied %q", got)
	}
	key(m, "z")
	if m.panel.Height() != before {
		t.Fatal("restoring header did not resize logs")
	}
	m.mode = "events"
	m.resize()
	m.panel.SetContent("EVENTROW\nsecond")
	before = m.panel.Height()
	key(m, "z")
	if m.panel.Height() != before+4 {
		t.Fatal("events did not gain four rows")
	}
	if got := copiedText(t, dragText(m, textPoint{1, 2}, textPoint{8, 2})); got != "EVENTROW" {
		t.Fatalf("headerless event selection copied %q", got)
	}
}
