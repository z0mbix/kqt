package tui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type colouredLogBackend struct{ *kube.Demo }

func (b *colouredLogBackend) StreamLogs(context.Context, string, string, string, *time.Time, ...kube.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("2026-10-03T20:59:23Z \x1b[32mINFO\x1b[0m \x1b[2mapp\x1b[0m: ready\n")), nil
}

func TestStreamColoursAreSanitizedAtUIBoundary(t *testing.T) {
	b := &colouredLogBackend{kube.NewDemo()}
	r, _ := kube.Resolve(kube.Builtins(), "pods")
	pod, err := b.Get(context.Background(), r, "default", "web-7c9f6d8b5-0001")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed := newLogFeed()
	done := make(chan error, 1)
	go func() { done <- kube.FollowWorkload(ctx, b, pod, "web", feed.append) }()
	select {
	case <-feed.notify:
	case <-time.After(2 * time.Second):
		t.Fatal("coloured log stream produced no output")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("log stream did not stop")
	}
	lines, _, _, _ := feed.drain()
	if len(lines) != 1 || lines[0] != "2026-10-03T20:59:23Z INFO app: ready" {
		t.Fatalf("stream still exposes escaped colour fragments: %q", lines)
	}
}

func TestLogColourCodesAreCleanedForLiveSnapshotSearchAndExport(t *testing.T) {
	m := demoModel(t)
	m.logObject, m.logContainer = m.rows[1], "web"
	m.logFollow = false
	m.loadLogs()
	raw := "2026-10-03T20:59:23Z \x1b[32mINFO\x1b[0m \x1b[2mapp\x1b[0m: ready revision=3"
	want := "2026-10-03T20:59:23Z INFO app: ready revision=3"
	m.Update(logsMsg{generation: m.generation, token: m.logToken, content: raw})
	if got := ansi.Strip(m.panel.GetContent()); got != want {
		t.Fatalf("snapshot retained colour fragments: %q", got)
	}
	m.logLines, m.logSize = nil, 0
	m.logFollow = true
	m.logFeed = newLogFeed()
	m.logFeed.append(raw)
	m.logPulse(logPulseMsg{m.generation, m.logToken, m.logFeed})
	m.logSearch = "INFO app"
	m.renderLogs()
	if got := ansi.Strip(m.panel.GetContent()); got != want {
		t.Fatalf("live search retained colour fragments: %q", got)
	}
	path := filepath.Join(t.TempDir(), "logs.txt")
	m.mode = "log-save"
	m.input.SetValue(path)
	message := m.logsInputKey(tea.KeyPressMsg{Code: tea.KeyEnter})().(logSavedMsg)
	if message.err != nil {
		t.Fatal(message.err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want+"\n" {
		t.Fatalf("export is not the cleaned displayed text: %q %v", data, err)
	}
}

func TestLogsFillMainPaneWithoutInspectorShowingThrough(t *testing.T) {
	m := demoModel(t)
	for _, size := range [][2]int{{80, 24}, {180, 48}, {240, 76}, {40, 12}} {
		for _, layout := range []string{"classic", "inspector", "bottom"} {
			m.layout = layout
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.logObject, m.logContainer = m.rows[1], "web"
			m.logFollow = false
			m.loadLogs()
			m.Update(logsMsg{generation: m.generation, token: m.logToken, content: "INFO " + strings.Repeat("界e\u0301 ", 100)})
			m.inspector.SetContent(strings.Repeat(strings.Repeat("¤", size[0])+"\n", size[1]))
			for _, mode := range []string{"logs", "log-search", "log-save", "log-since", "log-tail"} {
				m.mode = mode
				m.resize()
				view := m.View().Content
				if strings.Contains(view, "¤") {
					t.Fatalf("%dx%d %s %s shows the inspector behind logs", size[0], size[1], layout, mode)
				}
				lines := strings.Split(view, "\n")
				if len(lines) != size[1] {
					t.Fatalf("%dx%d %s %s height is %d", size[0], size[1], layout, mode, len(lines))
				}
				for _, line := range lines {
					if ansi.StringWidth(line) != size[0] {
						t.Fatalf("%dx%d %s %s line width is %d", size[0], size[1], layout, mode, ansi.StringWidth(line))
					}
				}
				if size[0] >= 80 && !strings.Contains(ansi.Strip(lines[0]), "connected") {
					t.Fatal("log view hid the cluster connection header")
				}
			}
			m.mode = "logs"
			key(m, "esc")
			m.openDetails(true, false)
			if m.panel.Width() != size[0]-6 || m.panel.Height() != size[1]-6 {
				t.Fatal("closing logs did not restore detail viewport dimensions")
			}
		}
	}
}

func TestLiveLogPauseSearchBoundsAndStaleMessages(t *testing.T) {
	m := demoModel(t)
	m.mode = "logs"
	m.logFollow = true
	m.logFeed = newLogFeed()
	m.logPaused = true
	m.panel.SetContent("frozen")
	m.logFeed.append("hello \x1b[31m red")
	m.logPulse(logPulseMsg{m.generation, m.logToken, m.logFeed})
	if m.panel.GetContent() != "frozen" {
		t.Fatal("pause moved the viewport")
	}
	m.logPaused = false
	m.logSearch = "hello"
	m.renderLogs()
	if !strings.Contains(m.panel.GetContent(), "hello") {
		t.Fatal("search hid a match")
	}
	token := m.logToken
	feed := m.logFeed
	m.closeLog()
	feed.append("stale data")
	m.logPulse(logPulseMsg{m.generation, token, feed})
	if strings.Contains(m.panel.GetContent(), "stale data") {
		t.Fatal("closed log stream updated panel")
	}
	f := newLogFeed()
	for i := 0; i < logBufferLines+20; i++ {
		f.append(strings.Repeat("x", 512))
	}
	lines, dropped, _, _ := f.drain()
	size := 0
	for _, line := range lines {
		size += len(line) + 1
	}
	if size > logBufferBytes || dropped == 0 || len(lines) > logBufferLines {
		t.Fatalf("unbounded log queue: %d %d %d", size, len(lines), dropped)
	}
}

func TestMetricsSortByActualUsageAndLeaveManifestUntouched(t *testing.T) {
	m := demoModel(t)
	m.metricsEnabled = true
	m.sortColumn = "memory"
	first, second := m.rows[0], m.rows[1]
	before := kube.Manifest(first, true)
	m.usage = map[string]kube.Usage{kube.UsageKey(first): {CPUMilli: 100, MemoryBytes: 1024 * 1024 * 1024}, kube.UsageKey(second): {CPUMilli: 20, MemoryBytes: 128 * 1024 * 1024}}
	m.rebuild("")
	if m.visible[0].GetUID() != second.GetUID() || m.column(first, "memory") != "1.0 GiB" {
		t.Fatal("metrics sort used display strings")
	}
	if kube.Manifest(first, true) != before {
		t.Fatal("metrics polluted original manifest")
	}
	m.descending = true
	m.rebuild("")
	if m.visible[0].GetUID() != first.GetUID() {
		t.Fatal("missing usage sorted ahead of known usage")
	}
}

func TestForwardParsingAndSessionCancellation(t *testing.T) {
	for _, value := range []string{"8080", "-1:80", "65536:80", "0:0", "0:70000", "0:80;cmd"} {
		if _, _, err := parsePorts(value); err == nil {
			t.Fatalf("accepted invalid ports: %s", value)
		}
	}
	local, remote, err := parsePorts("0:8080")
	if err != nil || local != 0 || remote != 8080 {
		t.Fatal("random local port unavailable")
	}
	m := demoModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	m.forwards = map[uint64]*forwardSession{1: {id: 1, state: "active", cancel: cancel}}
	m.connect("demo")
	if ctx.Err() == nil || m.forwards != nil {
		t.Fatal("context switch left forward running")
	}
	m.forwardUpdate(forwardMsg{generation: m.generation - 1, id: 1, local: 8080})
	if len(m.forwards) != 0 {
		t.Fatal("old-context forward leaked into new session")
	}
}

func TestScopedEventsRejectOldResponse(t *testing.T) {
	m := demoModel(t)
	cmd := m.openEvents()
	msg := cmd()
	m.closeEvents()
	m.mode = ""
	before := m.panel.GetContent()
	m.Update(msg)
	if m.panel.GetContent() != before {
		t.Fatal("closed events request updated panel")
	}
}

func TestLogExportDoesNotOverwriteExistingFile(t *testing.T) {
	m := demoModel(t)
	path := filepath.Join(t.TempDir(), "logs.txt")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	m.mode = "log-save"
	m.input.SetValue(path)
	m.logLines = []string{"replacement"}
	cmd := m.logsInputKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	message := cmd().(logSavedMsg)
	if message.err == nil {
		t.Fatal("overwrote an existing file")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "keep" {
		t.Fatal("existing export changed")
	}
}

func TestShellRequiresTTYAndRunningContainer(t *testing.T) {
	adapter := &shellCommand{ctx: context.Background(), in: strings.NewReader(""), target: unstructured.Unstructured{}}
	if err := adapter.Run(); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatal("accepted nonterminal shell")
	}
	m := demoModel(t)
	before := m.descending
	key(m, "s")
	if m.descending != before || !strings.Contains(m.notice, "live Kubernetes") {
		t.Fatal("shell key still changed sort")
	}
	key(m, "R")
	if m.descending == before {
		t.Fatal("reverse sort shortcut lost")
	}
}

func TestShellEscapeDisconnectsWithoutSendingFollowingInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := shellInput{reader: strings.NewReader("echo\x1dnot-remote-input"), disconnect: cancel}
	data := make([]byte, 64)
	n, err := input.Read(data)
	if err != nil || string(data[:n]) != "echo" || ctx.Err() == nil {
		t.Fatalf("disconnect was sent to the remote shell: %q %v", data[:n], err)
	}
}

func TestShellQueueConsumesDisconnectBeforeRemoteStartsReading(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := &shellQueue{ctx: ctx, notify: make(chan struct{}, 1)}
	if err := queue.pump(shellInput{reader: strings.NewReader("typed-before-connection\x1d"), disconnect: cancel}); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("startup disconnect was not consumed")
	}
	data := make([]byte, 64)
	n, err := queue.Read(data)
	if n != 0 || err == nil {
		t.Fatal("canceled input reached the remote session")
	}
}
