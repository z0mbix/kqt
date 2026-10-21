package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
)

type eventPulseMsg struct {
	generation, token uint64
	feed              *eventFeed
}
type eventStatusTickMsg struct{ generation, token uint64 }
type eventStatusMsg struct {
	generation, token uint64
	object            unstructured.Unstructured
	err               error
	replaced          bool
}
type eventSavedMsg struct {
	generation, token uint64
	path              string
	err               error
}

func waitEvent(ctx context.Context, g, token uint64, feed *eventFeed) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		case <-feed.notify:
		}
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return eventPulseMsg{g, token, feed}
		}
	}
}

func (m *model) followEventTable() tea.Cmd {
	m.closeEvents()
	m.eventGlobal, m.eventTail = true, true
	m.eventStore = newEventBuffer()
	m.eventState = "connecting"
	m.busy = true
	return m.startEventWatch()
}

func (m *model) startEventWatch() tea.Cmd {
	ctx, cancel := context.WithCancel(m.session.ctx)
	m.eventContext, m.eventCancel = ctx, cancel
	m.eventFeed = newEventFeed()
	q := kube.Query{Resource: m.route.resource, Namespace: m.route.namespace, Labels: m.route.labels, Fields: m.route.fields}
	if !m.eventGlobal {
		q.Resource, _ = kube.Resolve(kube.Builtins(), "events")
		q.Namespace = m.eventObject.GetNamespace()
		q.Labels = ""
		q.Fields = "involvedObject.uid=" + string(m.eventObject.GetUID())
		m.renderEventPanel()
	}
	feed, backend := m.eventFeed, m.backend
	go func() {
		if q.Fields == "involvedObject.uid=" {
			feed.finish(fmt.Errorf("resource has no UID; refresh before reading its events"))
			return
		}
		watcher, ok := backend.(kube.EventWatcher)
		if !ok {
			feed.finish(fmt.Errorf("this backend does not support live event watches"))
			return
		}
		err := watcher.FollowEvents(ctx, q, feed.append)
		feed.finish(err)
	}()
	g, token := m.generation, m.eventToken
	if m.eventGlobal {
		return waitEvent(ctx, g, token, feed)
	}
	return tea.Batch(waitEvent(ctx, g, token, feed), m.scheduleEventStatus())
}

func (m *model) eventViewState() string {
	state := m.eventState
	if state == "" {
		state = "connecting"
	}
	if m.eventPaused {
		state += " · paused"
	}
	if m.eventGlobal && m.eventTail {
		state += " · following"
	}
	return state
}

func (m *model) eventTitle() {
	if !m.eventGlobal {
		m.panelTitle = m.resourcePanelTab.title() + " · " + m.eventObject.GetName()
		if m.resourcePanelTab == resourceStatusTab {
			m.panelTitle += " · " + m.eventViewState()
			if m.eventSearch != "" {
				m.panelTitle += " · /" + kube.Safe(m.eventSearch)
			}
		} else if m.eventPaused {
			m.panelTitle += " · paused"
		}
	}
}

func (m *model) eventPulse(v eventPulseMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.eventToken || v.feed != m.eventFeed {
		return nil
	}
	batch := v.feed.drain()
	for _, change := range batch.changes {
		m.eventStore.put(change.Object, change.Type == watch.Deleted)
	}
	m.eventDropped += batch.dropped
	m.eventGaps += batch.gaps
	if batch.state != "" {
		m.eventState, m.eventError = batch.state, batch.message
	}
	if batch.synced {
		m.busy = false
		if len(batch.changes) > 0 || batch.state == "live" {
			m.lastRefresh = time.Now()
		}
	}
	if batch.done {
		m.busy = false
		m.eventState = "ended"
		if batch.err != nil && m.session.ctx.Err() == nil {
			m.eventError = kube.Safe(batch.err.Error())
		}
	}
	if !m.eventPaused {
		if m.eventGlobal {
			m.syncEventTable()
		} else {
			m.renderEventPanel()
		}
	} else {
		m.eventTitle()
	}
	if batch.done {
		return nil
	}
	return waitEvent(m.eventContext, v.generation, v.token, v.feed)
}

func (m *model) syncEventTable() {
	key := ""
	if selected := m.selectedKey(); selected != "" {
		key = selected
	}
	m.rows = m.eventStore.objects()
	m.eventRemoved = make(map[string]bool)
	for uid, entry := range m.eventStore.items {
		if entry.deleted {
			m.eventRemoved[uid] = true
		}
	}
	m.rebuild(key)
	if m.eventTail && m.mode == "" {
		m.followLatestEvent()
	}
}
func (m *model) followLatestEvent() {
	for i, o := range m.visible {
		if i == 0 || !kube.EventTime(o).Before(kube.EventTime(m.visible[m.selected])) {
			m.selected = i
		}
	}
	m.updateInspector()
}

func (m *model) renderEventPanel() {
	m.eventTitle()
	if m.eventStore == nil {
		return
	}
	var events []unstructured.Unstructured
	query := strings.ToLower(m.eventSearch)
	for _, o := range m.eventStore.objects() {
		if query == "" || strings.Contains(strings.ToLower(eventLine(o)), query) {
			events = append(events, o)
		}
	}
	var eventError error
	if m.eventError != "" {
		eventError = fmt.Errorf("%s", m.eventError)
	}
	var content string
	if m.resourcePanelTab == resourceStatusTab {
		content = m.renderResourceStatus(m.eventObject, events, eventError)
	} else if m.resourcePanelTab == resourceDiagnosticsTab {
		content = m.renderResourceReport()
	} else {
		content = m.renderResourceMetadata(m.eventObject, m.resourcePanelTab)
	}
	if m.eventStatusError != "" {
		stale := "STALE RESOURCE STATUS"
		if m.resourcePanelTab != resourceStatusTab {
			stale = "STALE RESOURCE METADATA"
		}
		content = m.theme.style("warning").Render(stale+" · "+kube.Safe(m.eventStatusError)) + "\n\n" + content
	}
	if dropped := m.eventDropped + m.eventStore.dropped; dropped > 0 && m.resourcePanelTab == resourceStatusTab {
		content += fmt.Sprintf("\n[kqt] %d older/oversized event records omitted (4 MiB / 2,000 record buffer).", dropped)
	}
	if m.eventGaps > 0 && m.resourcePanelTab == resourceStatusTab {
		content += fmt.Sprintf("\n[kqt] Event history resynced %d time(s); some events may have been missed.", m.eventGaps)
	}
	if query != "" && len(events) == 0 && m.resourcePanelTab == resourceStatusTab {
		content += "\nNo events match this filter. Waiting for new output…"
	}
	offset := m.panel.YOffset()
	m.panel.SetContent(content)
	if m.eventTail && m.resourcePanelTab == resourceStatusTab {
		m.panel.GotoBottom()
	} else {
		m.panel.SetYOffset(offset)
	}
}

func (m *model) scheduleEventStatus() tea.Cmd {
	g, token := m.generation, m.eventToken
	return tea.Tick(m.settings.Interval, func(time.Time) tea.Msg { return eventStatusTickMsg{g, token} })
}
func (m *model) eventStatusTick(v eventStatusTickMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.eventToken || m.eventGlobal || m.eventContext == nil || m.eventContext.Err() != nil {
		return nil
	}
	reader, ok := m.backend.(kube.ResourceGetter)
	if !ok {
		return nil
	}
	target, resource, ctx := m.eventObject, m.route.resource, m.eventContext
	return func() tea.Msg {
		request, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		fresh, err := reader.Get(request, resource, target.GetNamespace(), target.GetName())
		replaced := err == nil && fresh.GetUID() != target.GetUID()
		if replaced {
			err = fmt.Errorf("resource was replaced; close status and refresh")
		}
		return eventStatusMsg{v.generation, v.token, kube.Redacted(fresh), err, replaced || apierrors.IsNotFound(err)}
	}
}
func (m *model) eventStatusResult(v eventStatusMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.eventToken || m.eventGlobal || m.eventFeed == nil {
		return nil
	}
	m.eventStatusError = ""
	if v.err != nil {
		m.eventStatusError = kube.Safe(v.err.Error())
	} else {
		m.eventObject = v.object
	}
	if v.replaced {
		m.eventCancel()
		m.eventFeed.finish(nil)
		m.eventState = "ended"
	}
	if !m.eventPaused {
		m.renderEventPanel()
	}
	if v.replaced {
		return nil
	}
	return tea.Batch(m.scheduleEventStatus(), m.loadResourceReport())
}

func (m *model) eventsKey(k string) tea.Cmd {
	if !m.eventGlobal {
		count := resourcePanelTabCount - 1
		if m.hasResourceDiagnostics() {
			count++
		}
		tab := m.resourcePanelTab
		switch k {
		case "1", "S", "E":
			tab = resourceStatusTab
		case "2", "M":
			tab = resourceLabelsTab
		case "3":
			tab = resourceAnnotationsTab
		case "4":
			tab = resourceFinalizersTab
		case "5":
			if m.hasResourceDiagnostics() {
				tab = resourceDiagnosticsTab
			} else {
				tab = resourcePanelTabCount
			}
		case "tab":
			tab = (tab + 1) % count
		case "shift+tab":
			tab = (tab + count - 1) % count
		default:
			tab = resourcePanelTabCount
		}
		if tab != resourcePanelTabCount {
			m.resourcePanelTab = tab
			m.eventTail = false
			m.panel.GotoTop()
			m.renderEventPanel()
			return m.loadResourceReport()
		}
	}
	switch k {
	case "space", " ":
		m.eventPaused = !m.eventPaused
		if !m.eventPaused {
			if m.eventGlobal {
				m.syncEventTable()
			} else {
				m.renderEventPanel()
			}
		}
		m.eventTitle()
	case "G", "end":
		if m.eventGlobal && m.inspectorFocused {
			scroll(&m.inspector, k)
			return nil
		}
		m.eventTail = m.eventGlobal || m.resourcePanelTab == resourceStatusTab
		if m.eventGlobal {
			m.followLatestEvent()
		} else {
			m.panel.GotoBottom()
		}
	case "j", "down", "k", "up", "g", "home", "ctrl+u", "ctrl+d", "pgup", "pgdown", "ctrl+b", "ctrl+f":
		if !m.eventGlobal || !m.inspectorFocused {
			m.eventTail = false
		}
	case "o":
		return m.eventsInput("event-save")
	case "/":
		if !m.eventGlobal && m.resourcePanelTab == resourceStatusTab {
			return m.eventsInput("event-search")
		}
	case "ctrl+r":
		if m.eventGlobal {
			return m.followEventTable()
		}
		return m.loadEvents()
	}
	return nil
}
func (m *model) eventsInput(mode string) tea.Cmd {
	m.notice = ""
	m.input.CharLimit = 256
	m.input.Prompt = "/ "
	value := m.eventSearch
	if mode == "event-save" {
		m.input.Prompt = "Save to: "
		value = "kqt-events.log"
		if !m.eventGlobal && m.resourcePanelTab != resourceStatusTab {
			value = "kqt-" + strings.ToLower(m.resourcePanelTab.title()) + ".txt"
		}
		var err error
		value, err = downloadPath(m.settings.Config.DownloadDir, value)
		if err != nil {
			m.notice = "Unable to prepare download: " + kube.Safe(err.Error())
			return nil
		}
		m.input.CharLimit = 4096
	}
	m.mode = mode
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.resize()
	return m.input.Focus()
}
func (m *model) eventsInputKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "esc" {
		m.mode = m.eventReturnMode()
		m.input.Blur()
		m.input.CharLimit = 256
		return nil
	}
	if msg.String() == "enter" {
		mode, value := m.mode, m.input.Value()
		m.mode = m.eventReturnMode()
		m.input.Blur()
		m.input.CharLimit = 256
		if mode == "event-search" {
			m.eventSearch = value
			m.renderEventPanel()
			return nil
		}
		content := m.eventExport()
		g, token := m.generation, m.eventToken
		directory := m.settings.Config.DownloadDir
		return func() tea.Msg {
			path, err := saveDownload(directory, value, content)
			return eventSavedMsg{g, token, path, err}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}
func (m *model) eventReturnMode() string {
	if m.eventGlobal {
		return ""
	}
	return "events"
}
func (m *model) showingEventPanel() bool {
	return !m.eventGlobal && (m.mode == "events" || m.mode == "event-search" || m.mode == "event-save")
}
func (m *model) eventExport() string {
	if !m.eventGlobal {
		return ansi.Strip(m.panel.GetContent()) + "\n"
	}
	var text strings.Builder
	for _, o := range m.visible {
		text.WriteString(eventLine(o) + "\n")
	}
	if m.eventGaps > 0 {
		fmt.Fprintf(&text, "[kqt] History resynced %d time(s); some events may have been missed.\n", m.eventGaps)
	}
	if dropped := m.eventDropped + m.eventStore.dropped; dropped > 0 {
		fmt.Fprintf(&text, "[kqt] %d older/oversized event records omitted.\n", dropped)
	}
	return text.String()
}
func eventLine(o unstructured.Unstructured) string {
	timestamp := "time unknown"
	if observed := kube.EventTime(o); !observed.IsZero() {
		timestamp = observed.UTC().Format(time.RFC3339Nano)
	}
	return kube.SafeLogLines(fmt.Sprintf("%s %s [%s/%s/%s] %s ×%d %s", timestamp, kube.Text(o, "type"), o.GetNamespace(), kube.EventSubject(o, "kind"), kube.EventSubject(o, "name"), kube.Text(o, "reason"), kube.EventCount(o), kube.EventMessage(o)))
}
