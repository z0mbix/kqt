package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func selectionLogModel(t *testing.T, width, height int, content string, focus bool) *model {
	t.Helper()
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m.logObject, m.logContainer = m.rows[1], "web"
	m.logFollow = false
	m.loadLogs()
	m.Update(logsMsg{generation: m.generation, token: m.logToken, content: content})
	if focus {
		key(m, "f")
	}
	return m
}

func dragText(m *model, from, to textPoint) tea.Cmd {
	m.Update(tea.MouseClickMsg{X: from.x, Y: from.y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: to.x, Y: to.y, Button: tea.MouseLeft})
	_, cmd := m.Update(tea.MouseReleaseMsg{X: to.x, Y: to.y, Button: tea.MouseLeft})
	return cmd
}

func copiedText(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("selection did not request clipboard copy")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("expected clipboard and flash commands, got %T", cmd())
	}
	// Bubble Tea's clipboard message is a private string type. Read its payload
	// without depending on that type; PTY coverage checks the emitted OSC52 bytes.
	return fmt.Sprint(batch[0]())
}

func TestLogFocusFillsTerminalAndEscapeKeepsStream(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}, {180, 48}} {
		for _, layout := range []string{"classic", "inspector", "bottom"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], layout), func(t *testing.T) {
				m := selectionLogModel(t, size[0], size[1], "INFO ready\nsecond log line", false)
				m.layout = layout
				m.inspector.SetContent("UNDERLYING_INSPECTOR")
				ctx, cancel := context.WithCancel(context.Background())
				m.logCancel, m.logFeed = cancel, newLogFeed()
				feed, token := m.logFeed, m.logToken
				key(m, "f")
				view := ansi.Strip(m.View().Content)
				for _, ui := range []string{"kqt", "Context:", "Namespace:", "UNDERLYING", "╭", "│", "╰", "Esc: close"} {
					if strings.Contains(view, ui) {
						t.Fatalf("focus contains UI %q: %s", ui, view)
					}
				}
				lines := strings.Split(view, "\n")
				if len(lines) != size[1] || m.panel.Width() != size[0] || m.panel.Height() != size[1] || strings.TrimSpace(lines[0]) != "INFO ready" {
					t.Fatal("focus did not fill the terminal with logs")
				}
				for _, line := range lines {
					if ansi.StringWidth(line) != size[0] {
						t.Fatalf("focus width %d, want %d", ansi.StringWidth(line), size[0])
					}
				}
				key(m, "esc")
				if m.logFullScreen || m.mode != "logs" || m.logFeed != feed || m.logToken != token || ctx.Err() != nil || m.panel.Height() != size[1]-5 {
					t.Fatal("leaving focus closed or reset the log session")
				}
				key(m, "esc")
				if m.mode != "" || ctx.Err() == nil {
					t.Fatal("second escape did not close the log session")
				}
			})
		}
	}
}

func TestLogFollowCollectsWithoutMovingViewportAndResumes(t *testing.T) {
	m := selectionLogModel(t, 80, 24, strings.Repeat("INFO old line\n", 40), false)
	m.logFollow, m.logTail = true, true
	m.logFeed = newLogFeed()
	ctx, cancel := context.WithCancel(context.Background())
	m.logCancel = cancel
	feed, token := m.logFeed, m.logToken
	m.renderLogs()
	key(m, "s")
	key(m, "g")
	offset := m.panel.YOffset()
	feed.append("INFO new live line")
	m.logPulse(logPulseMsg{m.generation, token, feed})
	if m.logTail || m.panel.YOffset() != offset || !strings.Contains(m.panel.GetContent(), "new live line") || !strings.Contains(m.panelTitle, "follow off") {
		t.Fatal("follow off stopped collection or moved the viewport")
	}
	key(m, "s")
	if !m.logTail || !m.panel.AtBottom() || !strings.Contains(m.panelTitle, "following") || m.logFeed != feed || m.logToken != token || ctx.Err() != nil {
		t.Fatal("following did not resume on the existing stream")
	}
	key(m, " ")
	frozen := m.panel.GetContent()
	feed.append("INFO paused arrival")
	m.logPulse(logPulseMsg{m.generation, token, feed})
	key(m, "s") // Disabling auto-follow must preserve an explicit pause.
	if !m.logPaused || m.panel.GetContent() != frozen {
		t.Fatal("follow off thawed paused logs")
	}
	key(m, "s")
	if m.logPaused || !m.panel.AtBottom() || !strings.Contains(m.panel.GetContent(), "paused arrival") {
		t.Fatal("follow on did not resume paused logs")
	}
	m.Update(tea.MouseWheelMsg{X: 2, Y: 6, Button: tea.MouseWheelUp})
	if m.logTail || !strings.Contains(m.panelTitle, "follow off") {
		t.Fatal("mouse scroll did not immediately disable following")
	}
}

func TestLogWrappingPersistsAcrossSnapshotAndLiveReloads(t *testing.T) {
	m := selectionLogModel(t, 80, 24, "INFO "+strings.Repeat("x", 180), false)
	if !m.logWrap || !m.panel.SoftWrap {
		t.Fatal("logs should wrap initially")
	}
	key(m, "w")
	if m.logWrap || m.panel.SoftWrap || strings.Contains(m.panel.View(), "\nx") {
		t.Fatal("w did not disable wrapping")
	}
	m.panel.SetXOffset(20)
	key(m, "f")
	if m.panel.SoftWrap {
		t.Fatal("focus reset wrapping")
	}
	m.Update(m.loadLogs()())
	if m.panel.SoftWrap || !m.logFullScreen || m.panel.XOffset() != 0 {
		t.Fatal("snapshot reload lost focus/wrap preference or retained an old horizontal offset")
	}
	key(m, "F")
	if !m.logFollow || m.panel.SoftWrap || !m.logFullScreen {
		t.Fatal("live reload lost focus/wrap preference")
	}
	key(m, "w")
	if !m.logWrap || !m.panel.SoftWrap {
		t.Fatal("w did not restore wrapping")
	}
}

func TestLogFocusInputsAndResizeRestoreLogArea(t *testing.T) {
	m := selectionLogModel(t, 80, 24, "INFO ready", true)
	for _, input := range []string{"log-search", "log-save"} {
		m.logsInput(input)
		if m.panel.Height() != 23 || !strings.Contains(ansi.Strip(m.View().Content), m.input.Prompt) {
			t.Fatal("focus input did not reserve its own line")
		}
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
		key(m, "esc")
		if m.mode != "logs" || !m.logFullScreen || m.panel.Width() != 120 || m.panel.Height() != 32 || strings.Contains(ansi.Strip(m.View().Content), "Save to:") {
			t.Fatal("cancelling input did not restore logs-only dimensions")
		}
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	}
}

func TestLogFocusContainerPickerCancellationAndSelection(t *testing.T) {
	m := selectionLogModel(t, 80, 24, "INFO ready", true)
	key(m, "w")
	ctx, cancel := context.WithCancel(context.Background())
	m.logCancel, m.logFeed = cancel, newLogFeed()
	feed := m.logFeed
	key(m, "c")
	if m.mode != "picker" {
		t.Fatal("c did not open container selection")
	}
	key(m, "esc")
	if m.mode != "logs" || !m.logFullScreen || m.panel.Width() != 80 || m.panel.Height() != 24 || m.panel.SoftWrap || m.logFeed != feed || ctx.Err() != nil {
		t.Fatal("cancelling container selection lost focus, wrapping or the stream")
	}
	key(m, "c")
	cmd := m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || m.mode != "logs" || !m.logFullScreen || m.panel.Width() != 80 || m.panel.Height() != 24 || m.panel.SoftWrap {
		t.Fatal("choosing a container lost focus or wrapping")
	}
	m.Update(cmd())
}

func TestLogFocusAndResizePreserveLoadingErrorsAndPausedContent(t *testing.T) {
	m := selectionLogModel(t, 80, 24, "INFO old buffer", false)
	for _, content := range []string{"Waiting for log lines…", "Unable to read logs: permission denied", "Frozen display"} {
		m.panel.SetContent(content)
		key(m, "f")
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
		if ansi.Strip(m.panel.GetContent()) != content {
			t.Fatal("focus or resize replaced loading/error/paused output with an old buffer")
		}
		if got := copiedText(t, dragText(m, textPoint{0, 0}, textPoint{6, 0})); got != content[:7] || ansi.Strip(m.panel.GetContent()) != content {
			t.Fatal("copying loading/error/paused output replaced it with an old buffer")
		}
		key(m, "f")
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	}
}

func TestDragCopyUsesPlainTextAndOriginalLineBreaks(t *testing.T) {
	for _, focus := range []bool{false, true} {
		m := selectionLogModel(t, 40, 12, "INFO "+strings.Repeat("ab", 30)+"\nWARN next", focus)
		bounds := m.logTextBounds()
		// A long physical log line occupies two screen rows; copy it once, then
		// the next original line. Padding, styles and frame borders are excluded.
		cmd := dragText(m, textPoint{bounds.x, bounds.y}, textPoint{bounds.x + 8, bounds.y + 2})
		if got, want := copiedText(t, cmd), "INFO "+strings.Repeat("ab", 30)+"\nWARN next"; got != want {
			t.Fatalf("focus=%t copied %q, want %q", focus, got, want)
		}
		if m.selection != nil || m.copyFlash != "Copied" {
			t.Fatal("release did not clear highlight and acknowledge copy")
		}
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		if !strings.HasSuffix(lines[m.height-1], " Copied ") {
			t.Fatal("copy confirmation is not in the bottom-right corner")
		}
		if len(lines) != m.height || ansi.StringWidth(lines[m.height-1]) != m.width {
			t.Fatal("copy confirmation changed terminal dimensions")
		}
	}
}

func TestDragCopyHandlesReverseUnicodeAndHorizontalScroll(t *testing.T) {
	m := selectionLogModel(t, 80, 24, "INFO 界e\u0301👩‍💻 end\nlast line", true)
	before := m.View().Content
	m.Update(tea.MouseClickMsg{X: 10, Y: 0, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	if m.View().Content == before || ansi.Strip(m.View().Content) != ansi.Strip(before) {
		t.Fatal("drag did not highlight without changing displayed text")
	}
	_, cmd := m.Update(tea.MouseReleaseMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	if got := copiedText(t, cmd); got != "界e\u0301👩‍💻 " {
		t.Fatalf("reverse Unicode copy split a grapheme: %q", got)
	}
	m.Update(copyFlashMsg{m.copyFlashToken})
	m.Update(tea.MouseClickMsg{X: 6, Y: 0, Button: tea.MouseLeft}) // Second cell of 界.
	m.Update(tea.MouseMotionMsg{X: 7, Y: 0, Button: tea.MouseLeft})
	if ansi.Strip(m.View().Content) != ansi.Strip(before) {
		t.Fatal("selecting part of a wide glyph replaced it with padding")
	}
	_, cmd = m.Update(tea.MouseReleaseMsg{X: 7, Y: 0, Button: tea.MouseLeft})
	if got := copiedText(t, cmd); got != "界e\u0301" {
		t.Fatalf("partial wide-glyph selection: %q", got)
	}
	cmd = dragText(m, textPoint{3, 1}, textPoint{7, 0})
	if got := copiedText(t, cmd); got != "e\u0301👩‍💻 end\nlast" {
		t.Fatalf("reverse multiline copy: %q", got)
	}
	key(m, "w")
	m.logLines = []string{"first", strings.Repeat("0123456789", 12), "last"}
	m.renderLogs()
	m.panel.SetHeight(1)
	m.panel.SetYOffset(1)
	m.panel.SetXOffset(8)
	cmd = dragText(m, textPoint{0, 0}, textPoint{4, 0})
	if got := copiedText(t, cmd); got != "89012" {
		t.Fatalf("copy ignored horizontal or vertical scrolling: %q", got)
	}
}

func TestDragCopyStartsInsideWrappedUnicodeLine(t *testing.T) {
	line := strings.Repeat("a", 39) + "界e\u0301👩‍💻" + strings.Repeat("b", 50)
	m := selectionLogModel(t, 40, 12, line+"\nlast", true)
	m.panel.SetHeight(2)
	m.panel.SetYOffset(1)
	cmd := dragText(m, textPoint{0, 0}, textPoint{39, 1})
	if got := copiedText(t, cmd); got != "界e\u0301👩‍💻"+strings.Repeat("b", 50) {
		t.Fatalf("wrapped/scrolled Unicode line gained breaks or duplicated glyphs: %q", got)
	}
}

func TestLogDragFreezesOnlyRenderingAndPreservesPause(t *testing.T) {
	for _, paused := range []bool{false, true} {
		m := selectionLogModel(t, 80, 24, "INFO old line", true)
		m.logFollow, m.logPaused = true, paused
		m.logFeed = newLogFeed()
		m.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
		m.Update(tea.MouseMotionMsg{X: 3, Y: 0, Button: tea.MouseLeft})
		frozen := m.panel.GetContent()
		m.logFeed.append("WARN arrival during selection")
		m.logPulse(logPulseMsg{m.generation, m.logToken, m.logFeed})
		if m.panel.GetContent() != frozen || len(m.logLines) != 2 {
			t.Fatal("selection moved or blocked log collection")
		}
		_, cmd := m.Update(tea.MouseReleaseMsg{X: 3, Y: 0, Button: tea.MouseLeft})
		if got := copiedText(t, cmd); got != "INFO" || m.logPaused != paused {
			t.Fatal("release copied arriving text or changed pause")
		}
		if strings.Contains(m.panel.GetContent(), "arrival") == paused {
			t.Fatal("release did not respect the explicit pause state")
		}
	}
}

func TestTextSelectionRespectsPanesAndRejectsChangedContent(t *testing.T) {
	for _, layout := range []string{"inspector", "bottom"} {
		m := demoModel(t)
		m.layout = layout
		m.Update(tea.WindowSizeMsg{Width: 180, Height: 48})
		b := m.inspectorBounds()
		m.inspector.SetContent("INFO selected inspector\nsecond line")
		cmd := dragText(m, textPoint{b.x + 1, b.y + 1}, textPoint{b.x + 4, b.y + 1})
		if got := copiedText(t, cmd); got != "INFO" || !m.inspectorFocused {
			t.Fatal("drag copied the wrong pane or lost inspector focus")
		}
	}
	m := selectionLogModel(t, 80, 24, "INFO visible log", false)
	y := m.bodyBounds().y
	for _, press := range []tea.MouseClickMsg{
		{X: 0, Y: y + 2, Button: tea.MouseLeft}, // Border.
		{X: 2, Y: y, Button: tea.MouseLeft},     // Title.
		{X: 2, Y: y + 2, Button: tea.MouseRight},
		{X: 2, Y: y + 2, Button: tea.MouseLeft, Mod: tea.ModShift},
	} {
		m.Update(press)
		if m.selection != nil {
			t.Fatal("border, title or modified click started selection")
		}
	}
	b := m.logTextBounds()
	m.Update(tea.MouseClickMsg{X: b.x, Y: b.y, Button: tea.MouseLeft})
	_, cmd := m.Update(tea.MouseReleaseMsg{X: b.x, Y: b.y, Button: tea.MouseLeft})
	if cmd != nil || m.copyFlash != "" {
		t.Fatal("single click copied text")
	}
	for _, change := range []func(){
		func() { m.panel.SetContent("REPLACED") },
		func() { m.generation++ },
		func() { m.mode = "log-search" },
	} {
		m.mode = "logs"
		m.panel.SetContent("INFO visible log")
		m.Update(tea.MouseClickMsg{X: b.x, Y: b.y, Button: tea.MouseLeft})
		m.Update(tea.MouseMotionMsg{X: b.x + 3, Y: b.y, Button: tea.MouseLeft})
		change()
		_, cmd = m.Update(tea.MouseReleaseMsg{X: b.x + 3, Y: b.y, Button: tea.MouseLeft})
		if cmd != nil || m.selection != nil {
			t.Fatal("changed view copied a stale selection")
		}
	}
}

func TestDragCopyInReadableModalsAndResourceTabs(t *testing.T) {
	for _, mode := range []string{"details", "help", "access", "overview", "forward-details", "edit-error", "operation-error", "events"} {
		m := demoModel(t)
		m.mode = mode
		m.resize()
		m.panel.SetContent("INFO visible panel\nsecond line")
		point := textPoint{3, 3}
		if mode == "events" {
			point = textPoint{1, m.bodyBounds().y + 2} // Below the pinned resource tabs.
		}
		if mode == "help" {
			point = textPoint{3, 4} // Below the help pane headings.
		}
		row := strings.Split(ansi.Strip(m.View().Content), "\n")[point.y]
		if !strings.HasPrefix(ansi.Cut(row, point.x, m.width), "INFO visible panel") {
			t.Fatalf("%s selection coordinates do not match the rendered panel", mode)
		}
		if got := copiedText(t, dragText(m, point, textPoint{point.x + 3, point.y})); got != "INFO" {
			t.Fatalf("%s copied pane chrome or incorrect content: %q", mode, got)
		}
	}
}

func TestSelectionCancellationAndFlashExpiry(t *testing.T) {
	m := selectionLogModel(t, 80, 24, "INFO line", true)
	for _, event := range []tea.Msg{
		tea.KeyPressMsg{Code: 'w', Text: "w"},
		tea.WindowSizeMsg{Width: 120, Height: 32},
		tea.MouseWheelMsg{X: 2, Y: 2, Button: tea.MouseWheelDown},
	} {
		m.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
		m.Update(event)
		if m.selection != nil {
			t.Fatal("key, resize or scroll retained selection")
		}
	}
	m.flashCopy("Copied")
	old := m.copyFlashToken
	m.flashCopy("Copied")
	m.Update(copyFlashMsg{old})
	if m.copyFlash == "" {
		t.Fatal("old timer cleared a newer confirmation")
	}
	m.Update(copyFlashMsg{m.copyFlashToken})
	if m.copyFlash != "" || strings.Contains(ansi.Strip(m.View().Content), "Copied") {
		t.Fatal("copy confirmation did not expire")
	}
}

func TestSelectionCannotCopyHiddenSecretValues(t *testing.T) {
	m := secretModel(t)
	b := m.inspectorBounds()
	cmd := dragText(m, textPoint{b.x + 1, b.y + 1}, textPoint{b.x + b.width - 2, b.y + b.height - 2})
	if got := copiedText(t, cmd); strings.Contains(got, "demo-only") || !strings.Contains(got, "<redacted>") {
		t.Fatalf("selection exposed hidden Secret data: %q", got)
	}
	revealSecret(t, m)
	m.Update(tea.MouseClickMsg{X: b.x + 1, Y: b.y + 1, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: b.x + b.width - 2, Y: b.y + b.height - 2, Button: tea.MouseLeft})
	m.clearSecret()
	m.updateInspector()
	_, cmd = m.Update(tea.MouseReleaseMsg{X: b.x + b.width - 2, Y: b.y + b.height - 2, Button: tea.MouseLeft})
	if cmd != nil {
		t.Fatal("hiding Secret values allowed copying a stale revealed selection")
	}
}
