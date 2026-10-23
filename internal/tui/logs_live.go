package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
)

const logBufferBytes = 4 * 1024 * 1024
const logBufferLines = 10000

// A bounded queue decouples network readers from terminal rendering. It also
// prevents slow/paused viewports from leaving requests blocked on UI messages.
type logFeed struct {
	sync.Mutex
	lines         []string
	size, dropped int
	done          bool
	err           error
	ready         bool
	notify        chan struct{}
}

func newLogFeed() *logFeed { return &logFeed{notify: make(chan struct{}, 1)} }
func (f *logFeed) opened() { f.Lock(); f.ready = true; f.Unlock(); f.signal() }
func (f *logFeed) signal() {
	select {
	case f.notify <- struct{}{}:
	default:
	}
}
func (f *logFeed) append(line string) {
	f.Lock()
	if len(line) > 1024*1024 {
		line = line[:1024*1024] + " … line truncated"
	}
	line = kube.SafeLogLines(line)
	f.lines = append(f.lines, line)
	f.size += len(line) + 1
	for len(f.lines) > logBufferLines || f.size > logBufferBytes {
		f.size -= len(f.lines[0]) + 1
		f.lines[0] = ""
		f.lines = f.lines[1:]
		f.dropped++
	}
	f.Unlock()
	f.signal()
}
func (f *logFeed) finish(err error) { f.Lock(); f.done, f.err = true, err; f.Unlock(); f.signal() }
func (f *logFeed) drain() ([]string, int, bool, error) {
	f.Lock()
	defer f.Unlock()
	lines, dropped := f.lines, f.dropped
	f.lines = nil
	f.size, f.dropped = 0, 0
	return lines, dropped, f.done, f.err
}

type logPulseMsg struct {
	generation, token uint64
	feed              *logFeed
}

func waitLog(ctx context.Context, g, token uint64, feed *logFeed) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		case <-feed.notify:
			return logPulseMsg{g, token, feed}
		}
	}
}
func (m *model) followLogs() tea.Cmd {
	m.closeLog()
	ctx, cancel := context.WithCancel(m.session.ctx)
	m.logCancel = cancel
	m.logFeed = newLogFeed()
	m.logLines = nil
	m.logSize, m.logDropped = 0, 0
	m.logPaused = false
	m.logTail = m.settings.Config.Logs.Follow
	m.logDone = false
	m.mode = "logs"
	m.resize()
	m.panel.SoftWrap = false
	m.panel.SetXOffset(0)
	m.panel.SoftWrap = m.logWrap
	m.panel.SetContent("Waiting for log lines…")
	m.panel.GotoTop()
	m.logTitle()
	feed, b, target, container, g, token := m.logFeed, m.backend, m.logObject, m.logContainer, m.generation, m.logToken
	options := m.logRequestOptions()
	go func() {
		err := kube.FollowWorkloadWithOptions(ctx, b, target, container, options, feed.append, feed.opened)
		feed.finish(err)
	}()
	m.promoteHistoryExecution("logs", token)
	return waitLog(ctx, g, token, feed)
}
func (m *model) showingLogs() bool {
	return m.mode == "logs" || m.mode == "log-search" || m.mode == "log-save" || m.mode == "log-since" || m.mode == "log-tail"
}
func (m *model) logTitle() {
	m.panelTitle = "Logs · " + m.logObject.GetNamespace() + "/" + m.logObject.GetName() + ":" + m.logContainer
	if m.logPrevious {
		m.panelTitle += " · previous"
	} else if m.logFollow {
		if m.logDone {
			m.panelTitle += " · ended"
		} else {
			m.panelTitle += " · live"
		}
	} else {
		m.panelTitle += " · snapshot"
	}
	if m.logPaused {
		m.panelTitle += " · paused"
	}
	if m.logTail {
		m.panelTitle += " · following"
	} else {
		m.panelTitle += " · follow off"
	}
	if m.logWrap {
		m.panelTitle += " · wrap"
	}
	if !m.settings.Config.Logs.Highlight {
		m.panelTitle += " · plain"
	}
	if since := m.settings.Config.Logs.Since; since != "" && since != "all" {
		m.panelTitle += " · last " + since
	}
	if tail := m.settings.Config.Logs.TailLines; tail != nil {
		if *tail < 0 {
			m.panelTitle += " · all lines"
		} else {
			m.panelTitle += fmt.Sprintf(" · %d lines/source", *tail)
		}
	}
	if m.logSearch != "" {
		m.panelTitle += " · /" + kube.Safe(m.logSearch)
	}
}
func (m *model) logContent() string {
	return m.logContentWith(nil)
}

// Filtering and export always use plain text; styling happens only for display.
func (m *model) logContentWith(render func(string) string) string {
	var lines []string
	source, dropped := m.logLines, m.logDropped
	if m.logPaused {
		source, dropped = m.logFrozen, m.logFrozenDropped
	}
	if dropped > 0 {
		lines = append(lines, fmt.Sprintf("[kqt] %d older lines discarded (4 MiB / 10,000 line buffer)", dropped))
	}
	if m.logFilter == nil || m.logFilter.query != m.logSearch {
		filter, err := compileLogFilter(m.logSearch)
		if err != nil {
			return "Invalid log filter: " + kube.Safe(err.Error())
		}
		m.logFilter = filter
	}
	for _, line := range source {
		if m.logFilter.matches(line) {
			if render != nil {
				line = render(line)
			} else {
				line = m.presentLogLine(line)
			}
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		if m.logFollow && !m.logPrevious && !m.logDone {
			return "No log lines match. Waiting for new output…"
		}
		return "No log lines match this filter."
	}
	return strings.Join(lines, "\n")
}
func (m *model) renderLogs() {
	m.logTitle()
	offset := m.panel.YOffset()
	if m.logColours == nil {
		m.logColours = make(map[string]string)
	}
	m.panel.SetContent(m.logContentWith(func(line string) string {
		shown := m.presentLogLine(line)
		styled, ok := m.logColours[line]
		if !ok {
			if m.settings.Config.Logs.Highlight {
				styled = m.theme.renderLogLine(shown)
			} else {
				styled = kube.SafeLogLines(shown)
			}
			m.logColours[line] = styled
		}
		return m.theme.highlightLogMatches(styled, m.logFilter.spans(shown))
	}))
	if m.logTail {
		m.panel.GotoBottom()
	} else {
		m.panel.SetYOffset(offset)
	}
}
func (m *model) logPulse(v logPulseMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.logToken || v.feed != m.logFeed {
		return nil
	}
	lines, dropped, done, err := v.feed.drain()
	m.logDropped += dropped
	for _, line := range lines {
		m.logLines = append(m.logLines, line)
		m.logSize += len(line) + 1
	}
	for len(m.logLines) > logBufferLines || m.logSize > logBufferBytes {
		m.logSize -= len(m.logLines[0]) + 1
		delete(m.logColours, m.logLines[0])
		if m.logFilter != nil {
			delete(m.logFilter.cache, m.logLines[0])
		}
		m.logLines[0] = ""
		m.logLines = m.logLines[1:]
		m.logDropped++
	}
	m.logDone = done
	if err != nil && m.session.ctx.Err() == nil {
		m.logLines = append(m.logLines, "[kqt] Live logs ended: "+kube.Safe(err.Error()))
	}
	if !m.logPaused && !m.selectingLogs() {
		m.renderLogs()
	} else {
		if m.selectingLogs() {
			m.selection.logPending = true
		}
		m.logTitle()
	}
	if done {
		return nil
	}
	return waitLog(m.session.ctx, v.generation, v.token, v.feed)
}
func (m *model) logsInput(mode string) tea.Cmd {
	m.input.CharLimit = 256
	m.logFilterError = ""
	m.input.Prompt = "/ "
	value := m.logSearch
	if mode == "log-search" {
		m.input.CharLimit = 4096
	}
	if mode == "log-save" {
		container := m.logContainer
		if container == "*" {
			container = "all-containers"
		}
		var err error
		value, err = downloadPath(m.settings.Config.DownloadDir, m.logObject.GetName()+"-"+container+".log")
		if err != nil {
			m.notice = "Unable to prepare download: " + kube.Safe(err.Error())
			return nil
		}
		m.input.Prompt = "Save to: "
		m.input.CharLimit = 4096
	}
	m.mode = mode
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.resize()
	return m.input.Focus()
}
func (m *model) logsInputKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "esc" {
		m.logFilterError = ""
		m.mode = "logs"
		m.input.Blur()
		m.input.CharLimit = 256
		m.resize()
		return nil
	}
	if msg.String() == "enter" {
		mode, value := m.mode, m.input.Value()
		if mode == "log-search" {
			filter, err := compileLogFilter(value)
			if err != nil {
				m.logFilterError = kube.Safe(err.Error())
				m.input.Prompt = "Invalid filter (Esc cancels): "
				return nil
			}
			m.logFilter = filter
			m.logFilterError = ""
		}
		m.mode = "logs"
		m.input.Blur()
		m.input.CharLimit = 256
		m.resize()
		if mode == "log-search" {
			m.logSearch = value
			m.renderLogs()
			return nil
		}
		content := m.logContent() + "\n"
		directory := m.settings.Config.DownloadDir
		// Save exactly what is displayed, privately, without replacing existing files.
		return func() tea.Msg {
			path, err := saveDownload(directory, value, content)
			return logSavedMsg{path: path, err: err}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

type logSavedMsg struct {
	path string
	err  error
}
