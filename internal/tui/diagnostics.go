package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type eventsMsg struct {
	generation, token uint64
	object            unstructured.Unstructured
	events            []unstructured.Unstructured
	eventsError       error
	err               error
}

func (m *model) openEvents() tea.Cmd {
	return m.openResourcePanel(resourceStatusTab)
}
func (m *model) openResourcePanel(tab resourcePanelTab) tea.Cmd {
	target, ok := m.current()
	if !ok || m.backend == nil {
		return nil
	}
	m.clearSecret()
	m.updateInspector()
	m.eventObject = target
	m.resourcePanelTab = tab
	return m.loadEvents()
}
func (m *model) loadEvents() tea.Cmd {
	tab := m.resourcePanelTab
	m.closeEvents()
	m.resourcePanelTab = tab
	m.eventStore = newEventBuffer()
	m.eventState = "connecting"
	m.mode = "events"
	m.resize()
	m.panel.SoftWrap = true
	m.eventTitle()
	m.panel.SetContent("Loading resource…")
	m.panel.GotoTop()
	ctx, cancel := context.WithTimeout(m.session.ctx, 15*time.Second)
	m.eventCancel = cancel
	g, token, b, o, r := m.generation, m.eventToken, m.backend, m.eventObject, m.route.resource
	return func() tea.Msg {
		defer cancel()
		if reader, ok := b.(kube.ResourceGetter); ok {
			fresh, err := reader.Get(ctx, r, o.GetNamespace(), o.GetName())
			if err != nil {
				return eventsMsg{generation: g, token: token, err: err}
			}
			if fresh.GetUID() != o.GetUID() {
				return eventsMsg{generation: g, token: token, err: fmt.Errorf("resource was replaced; close status and refresh")}
			}
			o = fresh
		}
		events, err := kube.EventsForResource(ctx, b, o)
		return eventsMsg{generation: g, token: token, object: kube.Redacted(o), events: events, eventsError: err}
	}
}
func (m *model) closeEvents() {
	m.closeResourceReport()
	m.eventToken++
	if m.eventCancel != nil {
		m.eventCancel()
		m.eventCancel = nil
	}
	m.eventContext = nil
	m.eventFeed = nil
	m.eventStore = nil
	m.eventRemoved = nil
	m.eventGlobal, m.eventPaused, m.eventTail = false, false, false
	m.eventState, m.eventError, m.eventStatusError, m.eventSearch = "", "", "", ""
	m.eventDropped, m.eventGaps = 0, 0
	m.resourcePanelTab = resourceStatusTab
}
func (m *model) toggleMetrics() tea.Cmd {
	m.stopResourceWatch()
	m.notice = ""
	m.metricsEnabled = !m.metricsEnabled
	m.metricsWarning = ""
	m.usage = nil
	if m.requestCancel != nil {
		m.requestCancel()
	}
	m.request++
	m.busy = false
	key := ""
	if selected := m.selectedKey(); selected != "" {
		key = selected
	}
	m.rebuild(key)
	return m.fetch()
}
func metricColumn(c string) bool {
	return slices.Contains([]string{"cpu", "memory", "cpu%", "memory%"}, c)
}
func (m *model) hasMetrics() bool {
	if m.metricsEnabled {
		return true
	}
	for _, c := range m.columnsWithoutMetrics() {
		if metricColumn(c) {
			return true
		}
	}
	return metricColumn(m.sortColumn)
}
func (m *model) columnsWithoutMetrics() []string {
	v, ok := m.views[m.route.resource.ID()]
	if !ok {
		v = m.views[m.route.resource.GVR.Resource]
	}
	return v.Columns
}
func (m *model) metricValue(o unstructured.Unstructured, c string) (float64, bool) {
	usage, ok := m.usage[kube.UsageKey(o)]
	if !ok {
		return 0, false
	}
	switch c {
	case "cpu":
		return float64(usage.CPUMilli), true
	case "memory":
		return float64(usage.MemoryBytes), true
	case "cpu%", "memory%":
		var capacity int64
		if o.GetKind() == "Node" {
			path := "cpu"
			if c == "memory%" {
				path = "memory"
			}
			capacity = resourceQuantity(kube.Text(o, "status", "allocatable", path), c == "cpu%")
		} else {
			for _, value := range kube.Slice(o, "spec", "containers") {
				container, _ := value.(map[string]interface{})
				requests, _ := container["resources"].(map[string]interface{})
				limits, _ := requests["limits"].(map[string]interface{})
				name := "cpu"
				if c == "memory%" {
					name = "memory"
				}
				q, ok := limits[name]
				if !ok {
					return 0, false
				}
				capacity += resourceQuantity(fmt.Sprint(q), c == "cpu%")
			}
		}
		if capacity <= 0 {
			return 0, false
		}
		current := usage.CPUMilli
		if c == "memory%" {
			current = usage.MemoryBytes
		}
		return float64(current) * 100 / float64(capacity), true
	}
	return 0, false
}
func (m *model) column(o unstructured.Unstructured, c string) string {
	if !metricColumn(c) {
		if c == "type" && o.GetKind() == "Event" && m.eventStore != nil {
			removed := m.eventRemoved[kube.Identity(o)]
			if !m.eventGlobal {
				entry := m.eventStore.items[kube.Identity(o)]
				removed = entry != nil && entry.deleted
			}
			if removed {
				return kube.Column(o, c) + " (removed)"
			}
		}
		return kube.Column(o, c)
	}
	value, ok := m.metricValue(o, c)
	if !ok {
		return "—"
	}
	switch c {
	case "cpu":
		return strconv.FormatInt(int64(value), 10) + "m"
	case "memory":
		return humanBytes(int64(value))
	default:
		return fmt.Sprintf("%.0f%%", value)
	}
}
func humanBytes(n int64) string {
	for _, unit := range []struct {
		value int64
		name  string
	}{{1 << 40, "TiB"}, {1 << 30, "GiB"}, {1 << 20, "MiB"}, {1 << 10, "KiB"}} {
		if n >= unit.value {
			return fmt.Sprintf("%.1f %s", float64(n)/float64(unit.value), unit.name)
		}
	}
	return fmt.Sprintf("%d B", n)
}
func resourceQuantity(value string, cpu bool) int64 {
	q, err := resource.ParseQuantity(value)
	if err != nil {
		return 0
	}
	if cpu {
		return q.MilliValue()
	}
	return q.Value()
}
