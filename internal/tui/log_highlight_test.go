package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
)

func TestLogHighlightingPreservesMixedFormatsAcrossThemes(t *testing.T) {
	inputs := []string{
		"", "   ", "no error occurred; error_rate=0; INFO_COUNT=1",
		"2026-10-13T15:40:18.123456789Z INFO listening on :8080  ",
		"[cfgerer-system/cfgerer-0003:cfgerer] 2026-10-13T15:40:18Z 2026-10-13T15:40:17.123Z WARN app: retrying",
		`[default/web-0003:web] 2026-10-13T15:40:18+01:00 {"level":"error","msg":"escaped \" quotes, 東京 🌲","id":18446744073709551615,"n":-1.25e+10,"ready":false,"nil":null}  `,
		`{"level":"warn","msg":"ERROR is just part of this message","nested":{"level":"debug"}}`,
		`{"\u006cevel":"er\u0072or","levelObject":{"message":"ERROR"},"severity":"critical"}`,
		`["http://example.com", {"ready":true,"ports":[80,443]}]`,
		`["default/pod:container"]`,
		`INFO: {"ready":true}`, `[warn] {"retry":3}`,
		`time="2026-10-13" level=error msg="connection failed"`,
		`severity='Warning' retrying`, `lvl:debug details`,
		`{"msg":"unfinished`, `{"value":123,}`, `normal message {"some":"JSON"}`,
		`{`, `  "message": "pretty-printed JSON continuation",`,
		"\x1b[32mINFO\x1b[0m ready\x1b]52;c;secret\a\u202e\r\n",
	}
	for name, palette := range builtThemes() {
		t.Run(name, func(t *testing.T) {
			for _, plain := range inputs {
				got := palette.renderLogLine(plain)
				want := kube.SafeLogLines(plain)
				if ansi.Strip(got) != want || ansi.StringWidth(got) != ansi.StringWidth(want) {
					t.Fatalf("log highlighting changed text or width\nwant %q\ngot  %q", want, ansi.Strip(got))
				}
			}
		})
	}
}

func TestLogJSONDetectionAndSeverityColours(t *testing.T) {
	for name, palette := range builtThemes() {
		t.Run(name, func(t *testing.T) {
			got := palette.renderLogLine(`[default/web-0003:web] 2026-10-13T15:40:18.123Z {"level":"ERROR","message":"ERROR","number":18446744073709551615,"ok":true,"nil":null}`)
			for _, want := range []struct{ text, colour string }{
				{"[default/web-0003:web]", "muted"}, {"2026-10-13T15:40:18.123Z", "muted"},
				{`"message"`, "accent"}, {`"ERROR"`, "error"}, {`"ERROR"`, "success"},
				{"18446744073709551615", "warning"}, {"true", "focus"}, {"null", "muted"},
			} {
				if !strings.Contains(got, palette.style(want.colour).Render(want.text)) {
					t.Fatalf("%q did not use %s", want.text, want.colour)
				}
			}
			for _, plain := range []string{`INFO {"message":"ready"}`, `2026-10-13T15:40:18Z [default/web:web] {"message":"ready"}`, `[{"message":"ready"}]`} {
				if !strings.Contains(palette.renderLogLine(plain), palette.style("accent").Render(`"message"`)) {
					t.Fatalf("JSON payload was not detected: %s", plain)
				}
			}
			for _, plain := range []string{`{"message":123,}`, `text {"message":123}`, `  "message": 123,`} {
				key := palette.style("accent").Render(`"message"`)
				if key != `"message"` && strings.Contains(palette.renderLogLine(plain), key) {
					t.Fatalf("incomplete/non-JSON message treated as JSON: %s", plain)
				}
			}
			array := palette.renderLogLine(`["http://example.com"]`)
			if !strings.Contains(array, palette.style("success").Render(`"http://example.com"`)) {
				t.Fatal("JSON array mistaken for a source prefix")
			}
			for _, plain := range []string{"no error occurred", "error_rate=0 INFO_COUNT=1", "level=error_rate", "severity=warning_count"} {
				if palette.renderLogLine(plain) != plain {
					t.Fatalf("ordinary prose/field mistaken for severity: %s", plain)
				}
			}
			for _, want := range []struct{ input, text, colour string }{
				{"INFO ready", "INFO", "success"}, {"app WARN retry", "WARN", "warning"},
				{`level="error" connection failed`, "error", "error"}, {"[warn] retry", "warn", "warning"},
				{"severity=DEBUG details", "DEBUG", "muted"}, {"FATAL: exiting", "FATAL", "error"},
			} {
				if !strings.Contains(palette.renderLogLine(want.input), palette.style(want.colour).Render(want.text)) {
					t.Fatalf("severity not highlighted: %s", want.input)
				}
			}
		})
	}
}

func TestMixedLogDisplaySearchExportAndThemeChanges(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 48})
	m.logObject, m.logContainer = m.rows[1], "web"
	m.logFollow = false
	m.loadLogs()
	jsonLine := `[default/web-0003:web] 2026-10-13T15:40:18Z {"level":"warn","message":"connection retry","number":18446744073709551615}`
	textLine := "2026-10-13T15:40:19Z INFO connection ready"
	plain := jsonLine + "\n" + textLine
	m.Update(logsMsg{generation: m.generation, token: m.logToken, content: plain})
	if got := m.panel.GetContent(); got == plain || ansi.Strip(got) != plain {
		t.Fatal("snapshot did not highlight both formats without changing the text")
	}
	if !strings.Contains(m.panel.View(), m.theme.style("accent").Render(`"message"`)) {
		t.Fatal("viewport lost JSON key highlighting")
	}
	m.logLines, m.logSize = nil, 0
	m.logFollow = true
	m.logFeed = newLogFeed()
	m.logFeed.append(jsonLine)
	m.logFeed.append(textLine)
	m.logPulse(logPulseMsg{m.generation, m.logToken, m.logFeed})
	m.logSearch = `"warn","message":"CONNECTION`
	m.renderLogs()
	if ansi.Strip(m.panel.GetContent()) != jsonLine || m.logContent() != jsonLine {
		t.Fatal("search across coloured tokens did not use original case-insensitive text")
	}
	before := m.panel.GetContent()
	m.setTheme(builtThemes()["nord"])
	if m.panel.GetContent() == before || ansi.Strip(m.panel.GetContent()) != jsonLine {
		t.Fatal("theme change did not refresh cached colours in place")
	}
	path := filepath.Join(t.TempDir(), "logs.txt")
	m.mode = "log-save"
	m.input.SetValue(path)
	message := m.logsInputKey(tea.KeyPressMsg{Code: tea.KeyEnter})().(logSavedMsg)
	if message.err != nil {
		t.Fatal(message.err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != jsonLine+"\n" {
		t.Fatalf("export changed text or retained styling: %q %v", data, err)
	}
}

func TestLogHighlightCacheFollowsBufferLifetime(t *testing.T) {
	m := demoModel(t)
	m.mode = "logs"
	m.logSearch = "!missing"
	m.logFeed = newLogFeed()
	for i := 0; i < logBufferLines; i++ {
		m.logFeed.append(fmt.Sprintf("INFO line %d", i))
	}
	m.logPulse(logPulseMsg{m.generation, m.logToken, m.logFeed})
	for i := logBufferLines; i < logBufferLines+20; i++ {
		m.logFeed.append(fmt.Sprintf("INFO line %d", i))
	}
	m.logPulse(logPulseMsg{m.generation, m.logToken, m.logFeed})
	if len(m.logColours) > logBufferLines || len(m.logFilter.cache) > logBufferLines || len(m.logLines) > logBufferLines || m.logDropped != 20 {
		t.Fatal("highlight cache outlived retained lines")
	}
	if _, found := m.logColours["INFO line 0"]; found {
		t.Fatal("discarded line remained in the highlight cache")
	}
	if _, found := m.logFilter.cache["INFO line 0"]; found {
		t.Fatal("discarded line remained in the filter cache")
	}
	m.closeLog()
	if m.logColours != nil || m.logFilter != nil {
		t.Fatal("closing logs retained the highlight cache")
	}
}

func TestLogHighlightTogglePreservesFrozenViewportAndStream(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.mode = "logs"
	m.logObject, m.logContainer = m.rows[0], "web"
	m.logFollow, m.logTail = true, false
	m.logFeed = newLogFeed()
	for i := range 60 {
		m.logFeed.append(fmt.Sprintf(`{"level":"warn","message":"retry %d"}`, i))
	}
	m.logFeed.append("\x1b[31mERROR\x1b[0m retry\x1b]52;c;secret\a")
	m.logPulse(logPulseMsg{m.generation, m.logToken, m.logFeed})
	key(m, " ")
	m.panel.SetYOffset(4)
	before, offset := m.panel.GetContent(), m.panel.YOffset()
	token, feed := m.logToken, m.logFeed
	m.logFeed.append("INFO retry arrived while paused")
	m.logPulse(logPulseMsg{m.generation, m.logToken, m.logFeed})
	key(m, "H")
	if m.settings.Config.Logs.Highlight || !strings.Contains(m.panelTitle, " · plain") {
		t.Fatal("H did not disable highlighting")
	}
	if got := m.panel.GetContent(); got != ansi.Strip(before) || strings.Contains(got, "\x1b") || strings.Contains(got, "arrived") {
		t.Fatalf("plain logs changed frozen text or retained escape sequences: %q", got)
	}
	if !m.logPaused || m.logTail || m.logToken != token || m.logFeed != feed || m.panel.YOffset() != offset {
		t.Fatal("highlight toggle changed pause, scroll, follow or stream state")
	}
	saved, err := LoadSettings(m.settings.Path)
	if err != nil || saved.Config.Logs.Highlight {
		t.Fatalf("highlight preference not saved: %v", err)
	}
	key(m, "H")
	if m.panel.GetContent() != before || m.panel.YOffset() != offset || strings.Contains(m.panelTitle, " · plain") {
		t.Fatal("re-enabling highlighting did not restore the frozen display")
	}
	key(m, "H")
	key(m, " ")
	if got := m.panel.GetContent(); !strings.Contains(got, "arrived") || got != ansi.Strip(got) {
		t.Fatal("resuming plain logs lost arrivals or restored syntax colours")
	}

	key(m, "f")
	m.logSearch = "include:retry"
	checkSearch := func() {
		t.Helper()
		plain := m.logContent()
		var want []string
		for _, line := range strings.Split(plain, "\n") {
			want = append(want, m.theme.highlightLogMatches(line, m.logFilter.spans(line)))
		}
		got := m.panel.GetContent()
		if got == plain || got != strings.Join(want, "\n") || ansi.Strip(got) != plain {
			t.Fatal("plain mode lost search matches or restored syntax colours")
		}
	}
	m.renderLogs()
	checkSearch()
	m.setTheme(builtThemes()["nord"])
	checkSearch()
	m.logFeed.append(`{"level":"error","message":"retry live"}`)
	m.logPulse(logPulseMsg{m.generation, m.logToken, m.logFeed})
	checkSearch()
	m.openPicker("log-options")
	m.chooseLogOption("highlight")
	if !m.settings.Config.Logs.Highlight || m.mode != "logs" || !m.logFullScreen || m.logToken != token || m.logSearch != "include:retry" {
		t.Fatal("log options did not restore highlighting while preserving focus, stream and search")
	}
	if !strings.Contains(m.panel.GetContent(), m.theme.style("accent").Render(`"message"`)) {
		t.Fatal("re-enabling highlighting retained cached plain text")
	}
}

func BenchmarkLogHighlighting(b *testing.B) {
	palette := builtThemes()["nord"]
	line := `[default/web-0003:web] 2026-10-13T15:40:18Z {"level":"info","message":"request completed","status":200,"ready":true,"nil":null}`
	for b.Loop() {
		palette.renderLogLine(line)
	}
}
