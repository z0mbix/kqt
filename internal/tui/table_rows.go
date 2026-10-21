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

type detailDueMsg struct {
	generation, token uint64
	key               string
}
type detailMsg struct {
	generation, token uint64
	key               string
	object            unstructured.Unstructured
	err               error
	prefetch          bool
}
type viewDueMsg struct {
	generation uint64
	key        string
}
type viewPrefetchedMsg struct {
	generation uint64
	key        string
	view       cachedView
}
type cachedView struct {
	route    route
	table    kube.TableMode
	snapshot snapshotMsg
	at       time.Time
}

const (
	detailDelay       = 150 * time.Millisecond
	viewDelay         = 400 * time.Millisecond
	viewCacheLifetime = 2 * time.Minute
	viewCacheSize     = 8
)

func routeKey(r route) string {
	return strings.Join([]string{r.resource.ID(), r.resource.GVR.Version, r.namespace, r.labels, r.fields}, "|")
}

func (m *model) viewSort(r route) string {
	if sort := m.viewFor(r).Sort; sort != "" {
		return sort
	}
	if r.resource.Kind == "Event" {
		return "lastseen"
	}
	return "name"
}

func (m *model) tableMode(r route) kube.TableMode {
	return m.tableModeFor(r, m.sortColumn)
}

func (m *model) serverTables() bool {
	lister, ok := m.backend.(kube.TableLister)
	return ok && lister.ServerTables()
}

func (m *model) tableModeFor(r route, sort string) kube.TableMode {
	if m.settings.Config.FullObjectLists || r.resource.ID() == "" || r.resource.Kind == "Event" {
		return kube.TableOff
	}
	if m.backend != nil && !m.serverTables() {
		return kube.TableOff
	}
	columns := append(m.columnsFor(r), sort)
	for _, c := range columns {
		if !slices.Contains(kube.TableColumns, c) {
			return kube.TableOff
		}
	}
	if r.resource.Namespaced && r.namespace == "" || slices.Contains(columns, "labels") {
		return kube.TableMetadata
	}
	return kube.TableNone
}

func (m *model) clearDetails() {
	m.details = map[string]unstructured.Unstructured{}
	m.detailStale = map[string]bool{}
	m.detailErrors = map[string]string{}
	m.detailLoading, m.detailDue, m.detailPending, m.prefetchLoading = "", "", "", ""
	m.prefetchFailed = map[string]bool{}
	m.detailAction = nil
	m.detailToken++
}

func (m *model) clearViewCache() {
	m.viewCache = map[string]cachedView{}
	m.viewDue, m.viewLoading = "", ""
}

func (m *model) staleDetails() {
	for key := range m.details {
		m.detailStale[key] = true
	}
	clear(m.detailErrors)
}

func (m *model) staleDetail(key string) {
	if _, ok := m.details[key]; ok {
		m.detailStale[key] = true
	}
	delete(m.detailErrors, key)
	delete(m.prefetchFailed, key)
}

func (m *model) awaitingDetail() bool {
	if m.selected < 0 || m.selected >= len(m.visible) || !kube.IsTableRow(m.visible[m.selected]) {
		return false
	}
	_, ok := m.details[m.selectedKey()]
	return !ok
}

func (m *model) afterDetail(action func() tea.Cmd) tea.Cmd {
	key := m.selectedKey()
	m.detailPending, m.detailAction = key, action
	delete(m.detailErrors, key)
	m.notice = "Loading " + kube.Safe(m.visible[m.selected].GetName()) + "…"
	return nil
}

func tableNavigationKey(k string) bool {
	return slices.Contains([]string{"j", "k", "down", "up", "ctrl+d", "ctrl+u", "pgdown", "pgup", "g", "G", "home", "end", "h", "l", "left", "right", ":", "/", "esc", "n", "c", "t", "r", "0", "w", "R", "[", "]", "shift+left", "shift+right", "O", "i", "I", "ctrl+r", "?", "U", "A", "m", "F", "tab", "shift+tab"}, k)
}

func objectCommand(value string) bool {
	word, arg, _ := strings.Cut(strings.TrimSpace(value), " ")
	if word == "forward" && strings.HasPrefix(strings.TrimSpace(arg), "stop") {
		return false
	}
	return slices.Contains([]string{"rollout", "rollouts", "rollback", "scale", "restart", "suspend", "resume", "run", "delete", "copy", "export", "related", "relations", "logs", "events", "status", "bundle", "diagnose", "metadata", "shell", "debug", "forward", "edit", "actions"}, word)
}

func (m *model) syncTableRows() tea.Cmd {
	if m.backend == nil {
		return nil
	}
	if m.mode == "" && !m.busy && !m.lastRefresh.IsZero() && m.route.resource.Kind != "Event" && m.requestedTable != m.tableMode(m.route) {
		return m.refreshResources()
	}
	key := m.selectedKey()
	if m.detailPending != "" && m.detailPending != key {
		m.detailPending, m.detailAction = "", nil
		m.notice = ""
	}
	cmd, waiting := m.selectedDetail(key)
	if m.mode != "" || !m.serverTables() {
		return cmd
	}
	if waiting {
		return tea.Batch(cmd, m.scheduleViewPrefetch())
	}
	return tea.Batch(m.prefetchDetail(), m.scheduleViewPrefetch())
}

func (m *model) selectedDetail(key string) (tea.Cmd, bool) {
	if key == "" || !kube.IsTableRow(m.visible[m.selected]) {
		return nil, false
	}
	if _, ok := m.details[key]; ok && !m.detailStale[key] || m.detailErrors[key] != "" {
		return nil, false
	}
	if m.detailLoading != "" || m.detailDue == key || m.prefetchLoading == key {
		return nil, true
	}
	m.detailDue = key
	g, token := m.generation, m.detailToken
	if m.detailPending == key {
		return func() tea.Msg { return detailDueMsg{g, token, key} }, true
	}
	return tea.Tick(detailDelay, func(time.Time) tea.Msg { return detailDueMsg{g, token, key} }), true
}

func (m *model) getDetail(row unstructured.Unstructured, key string, prefetch bool) tea.Cmd {
	getter, ok := m.backend.(kube.ResourceGetter)
	if !ok {
		return nil
	}
	if prefetch {
		m.prefetchLoading = key
	} else {
		m.detailLoading = key
	}
	g, token, r := m.generation, m.detailToken, m.route.resource
	ctx, cancel := context.WithTimeout(m.session.ctx, 30*time.Second)
	return func() tea.Msg {
		defer cancel()
		o, err := getter.Get(ctx, r, row.GetNamespace(), row.GetName())
		return detailMsg{g, token, key, o, err, prefetch}
	}
}

func (m *model) prefetchDetail() tea.Cmd {
	if m.prefetchLoading != "" {
		return nil
	}
	for _, offset := range []int{1, -1, 2, -2} {
		i := m.selected + offset
		if i < 0 || i >= len(m.visible) || !kube.IsTableRow(m.visible[i]) {
			continue
		}
		key := kube.Identity(m.visible[i])
		if _, ok := m.details[key]; ok || m.prefetchFailed[key] || key == m.detailLoading {
			continue
		}
		return m.getDetail(m.visible[i], key, true)
	}
	return nil
}

func (m *model) detailDueReady(v detailDueMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.detailToken {
		return nil
	}
	if m.detailDue == v.key {
		m.detailDue = ""
	}
	if v.key != m.selectedKey() || m.detailLoading != "" || m.prefetchLoading == v.key {
		return nil
	}
	return m.getDetail(m.visible[m.selected], v.key, false)
}

func (m *model) detailReady(v detailMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.detailToken {
		return nil
	}
	if v.prefetch {
		m.prefetchLoading = ""
	} else {
		m.detailLoading = ""
	}
	if v.err != nil && v.prefetch {
		m.prefetchFailed[v.key] = true
	} else if v.err != nil {
		m.detailErrors[v.key] = kube.Safe(v.err.Error())
	} else {
		o := kube.Redacted(v.object)
		m.hideChangedSecret(o)
		if len(m.details) >= 128 {
			for key := range m.details {
				if key != m.selectedKey() {
					delete(m.details, key)
					delete(m.detailStale, key)
				}
			}
		}
		m.details[v.key] = o
		delete(m.detailStale, v.key)
	}
	if v.key == m.selectedKey() {
		m.updateInspector()
	}
	if m.detailPending != v.key || m.detailAction == nil || v.err != nil && v.prefetch {
		return nil
	}
	action := m.detailAction
	m.detailPending, m.detailAction = "", nil
	m.notice = ""
	if v.err != nil {
		m.notice = "Unable to load resource: " + kube.Safe(v.err.Error())
		return nil
	}
	return action()
}

func (m *model) describeLoading(o unstructured.Unstructured) string {
	note := "Loading full resource…"
	if err := m.detailErrors[kube.Identity(o)]; err != "" {
		note = "Unable to load resource: " + err
	}
	return fmt.Sprintf("%s  %s\nNamespace  %s\nStatus     %s\nAge        %s\n\n%s\n", kube.Safe(o.GetKind()), kube.Safe(o.GetName()), kube.Safe(o.GetNamespace()), kube.Safe(kube.Status(o)), kube.Age(o), m.theme.style("muted").Render(note))
}

func (m *model) scheduleViewPrefetch() tea.Cmd {
	o, ok := m.current()
	if !ok || kube.IsTableRow(o) && o.GetKind() != "Namespace" && o.GetKind() != "Node" {
		return nil
	}
	target, ok := m.drillTarget(o)
	if !ok {
		return nil
	}
	key := routeKey(target)
	if cached, ok := m.viewCache[key]; ok && time.Since(cached.at) < viewCacheLifetime/2 || m.viewLoading != "" || m.viewDue == key || key == routeKey(m.route) {
		return nil
	}
	m.viewDue = key
	g := m.generation
	return tea.Tick(viewDelay, func(time.Time) tea.Msg { return viewDueMsg{g, key} })
}

func (m *model) viewDueReady(v viewDueMsg) tea.Cmd {
	if v.generation != m.generation {
		return nil
	}
	if m.viewDue == v.key {
		m.viewDue = ""
	}
	o, ok := m.current()
	if !ok || m.viewLoading != "" || m.mode != "" {
		return nil
	}
	target, ok := m.drillTarget(o)
	if !ok || routeKey(target) != v.key {
		return nil
	}
	table := m.tableModeFor(target, m.viewSort(target))
	m.viewLoading = v.key
	g, b := m.generation, m.backend
	ctx, cancel := context.WithTimeout(m.session.ctx, time.Minute)
	return func() tea.Msg {
		defer cancel()
		snapshot := loadSnapshot(ctx, b, target, false, table)
		return viewPrefetchedMsg{g, v.key, cachedView{target, table, snapshot, time.Now()}}
	}
}

func (m *model) viewPrefetched(v viewPrefetchedMsg) {
	if v.generation != m.generation {
		return
	}
	if m.viewLoading == v.key {
		m.viewLoading = ""
	}
	if v.view.snapshot.err != nil {
		return
	}
	if len(m.viewCache) >= viewCacheSize {
		oldest := ""
		for key, cached := range m.viewCache {
			if oldest == "" || cached.at.Before(m.viewCache[oldest].at) {
				oldest = key
			}
		}
		delete(m.viewCache, oldest)
	}
	m.viewCache[v.key] = v.view
}

func (m *model) cachedSnapshot() tea.Cmd {
	key := routeKey(m.route)
	cached, ok := m.viewCache[key]
	delete(m.viewCache, key)
	if !ok || time.Since(cached.at) > viewCacheLifetime || cached.table != m.tableMode(m.route) || m.route.resource.Kind == "Event" {
		return nil
	}
	m.busy = true
	m.request++
	m.requestedTable = cached.table
	snapshot := cached.snapshot
	snapshot.generation, snapshot.request = m.generation, m.request
	return func() tea.Msg { return snapshot }
}
