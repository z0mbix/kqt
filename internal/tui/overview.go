package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type overviewMsg struct {
	generation, token uint64
	snapshot          kube.Overview
	elapsed           time.Duration
}
type overviewTickMsg struct{ generation, token uint64 }

func (m *model) cancelOverview() {
	m.overviewToken++
	if m.overviewCancel != nil {
		m.overviewCancel()
		m.overviewCancel = nil
	}
	m.overviewContext = nil
}
func (m *model) openOverview() tea.Cmd {
	if m.backend == nil {
		m.notice = "Connect to a cluster first"
		return nil
	}
	m.cancelOverview()
	ctx, cancel := context.WithCancel(m.session.ctx)
	m.overviewContext = ctx
	m.overviewCancel = cancel
	m.mode = "overview"
	m.resize()
	m.panel.SoftWrap = true
	m.panel.GotoTop()
	m.panelTitle = "Cluster overview"
	m.panel.SetContent("Loading cluster health and capacity…")
	return m.fetchOverview()
}
func (m *model) fetchOverview() tea.Cmd {
	if m.overviewContext == nil {
		return nil
	}
	g, token, b, ns, parent := m.generation, m.overviewToken, m.backend, m.route.namespace, m.overviewContext
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, time.Minute)
		defer cancel()
		started := time.Now()
		snapshot := kube.ReadOverview(ctx, b, ns)
		return overviewMsg{g, token, snapshot, time.Since(started)}
	}
}
func (m *model) overviewReady(v overviewMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.overviewToken || m.mode != "overview" {
		return nil
	}
	m.overview = v.snapshot
	m.renderOverview()
	delay := max(5*time.Second, m.settings.Interval, 2*v.elapsed)
	return tea.Tick(delay, func(time.Time) tea.Msg { return overviewTickMsg{v.generation, v.token} })
}
func (m *model) renderOverview() {
	o := m.overview
	w := &statusWriter{theme: m.theme}
	w.heading("Cluster overview · " + kube.Safe(m.contextName))
	w.field("Workload scope", o.Scope(), "foreground")
	w.field("Updated", o.Updated.Format("15:04:05"), "muted")
	w.WriteByte('\n')
	w.heading("Health")
	for _, name := range []string{"nodes", "pods", "deployments", "statefulsets", "daemonsets", "jobs"} {
		colour := "success"
		healthy, total := o.Health(name)
		if o.Errors[name] != "" {
			colour = "muted"
		} else if healthy < total {
			colour = "warning"
		}
		w.field(strings.Title(name), o.Summary(name), colour)
	}
	w.WriteByte('\n')
	w.heading("Node capacity and pressure (cluster)")
	if o.Errors["nodes"] != "" {
		w.note("Node capacity unavailable: " + o.Errors["nodes"])
	} else {
		cpu, memory, reported := o.Capacity()
		if reported == 0 {
			w.note("Allocatable capacity is not reported by these nodes.")
		} else {
			w.field("Allocatable", fmt.Sprintf("%.2f CPU · %s memory · %d/%d nodes reporting", float64(cpu)/1000, formatOverviewBytes(memory), reported, len(o.Objects["nodes"])), "foreground")
		}
		for _, node := range o.Objects["nodes"] {
			var pressure []string
			for _, value := range kube.Slice(node, "status", "conditions") {
				c, _ := value.(map[string]interface{})
				kind, _ := c["type"].(string)
				if c["status"] == "True" && slices.Contains([]string{"MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable"}, kind) {
					pressure = append(pressure, kind)
				}
			}
			if cordoned, _, _ := unstructured.NestedBool(node.Object, "spec", "unschedulable"); cordoned {
				pressure = append(pressure, "Cordoned")
			}
			if len(pressure) > 0 {
				w.field(node.GetName(), strings.Join(pressure, ", "), "warning")
			}
		}
	}
	w.WriteByte('\n')
	w.heading("Usage")
	for _, nodes := range []bool{true, false} {
		kind, label := "Pod metrics", "Pods ("+o.Scope()+")"
		objects := "pods"
		if nodes {
			kind, label, objects = "Node metrics", "Nodes (cluster)", "nodes"
		}
		usage, count := o.UsageTotals(nodes)
		if err := o.Errors[kind]; err != "" {
			w.field(label, "Unavailable: "+err, "muted")
		} else if count == 0 {
			w.field(label, "Waiting for samples", "muted")
		} else {
			w.field(label, fmt.Sprintf("%.2f CPU · %s memory · %d/%d samples", float64(usage.CPUMilli)/1000, formatOverviewBytes(usage.MemoryBytes), count, len(o.Objects[objects])), "foreground")
		}
	}
	w.WriteByte('\n')
	w.heading("Unhealthy resources · Enter to explore")
	problems := o.Problems()
	if len(problems) == 0 {
		w.note("No unhealthy resources in the available sections.")
	} else {
		for _, problem := range problems[:min(15, len(problems))] {
			w.field(problem.Title(), problem.Reason, "warning")
		}
		if len(problems) > 15 {
			w.note(fmt.Sprintf("%d more; Enter opens the full list.", len(problems)-15))
		}
	}
	w.WriteByte('\n')
	w.heading("Recent warning events")
	if o.Errors["events"] != "" {
		w.note("Unavailable: " + o.Errors["events"])
	} else {
		events := o.Warnings()
		if len(events) == 0 {
			w.note("No warning events in this scope.")
		}
		for _, event := range events {
			w.note(eventLine(event))
		}
	}
	offset := m.panel.YOffset()
	m.panel.SetContent(w.String())
	m.panel.SetYOffset(offset)
}
func formatOverviewBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	n := float64(value) / 1024
	unit := 0
	for n >= 1024 && unit < len(units)-1 {
		n /= 1024
		unit++
	}
	return fmt.Sprintf("%.1f %s", n, units[unit])
}
func (m *model) overviewProblems() tea.Cmd {
	links := m.overview.Problems()
	if len(links) == 0 {
		m.notice = "No unhealthy resources in the available overview sections"
		return nil
	}
	m.cancelOverview()
	m.relations = links
	return m.openPicker("relations")
}
