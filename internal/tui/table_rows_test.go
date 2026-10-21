package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
)

type tableServer struct {
	mu       sync.Mutex
	includes []string
	gets     []string
}

func (s *tableServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query()
	switch r.URL.Path {
	case "/api":
		fmt.Fprint(w, `{"kind":"APIVersions","versions":["v1"]}`)
	case "/apis":
		fmt.Fprint(w, `{"kind":"APIGroupList","groups":[]}`)
	case "/api/v1":
		fmt.Fprint(w, `{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"pods","kind":"Pod","namespaced":true,"verbs":["get","list","watch"]},{"name":"namespaces","kind":"Namespace","namespaced":false,"verbs":["get","list","watch"]}]}`)
	case "/api/v1/namespaces":
		if q.Get("watch") == "true" {
			<-r.Context().Done()
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"kind": "Table", "apiVersion": "meta.k8s.io/v1", "metadata": map[string]interface{}{"resourceVersion": "10"},
			"columnDefinitions": []map[string]interface{}{{"name": "Name", "type": "string", "format": "name"}, {"name": "Status", "type": "string"}, {"name": "Age", "type": "string"}},
			"rows":              []interface{}{map[string]interface{}{"cells": []interface{}{"apps", "Active", "9d"}}}})
	case "/api/v1/namespaces/apps/pods":
		if q.Get("watch") == "true" {
			<-r.Context().Done()
			return
		}
		s.mu.Lock()
		s.includes = append(s.includes, q.Get("includeObject"))
		s.mu.Unlock()
		row := func(name, ready string) map[string]interface{} {
			row := map[string]interface{}{"cells": []interface{}{name, ready, "Running", "0", "3d"}}
			if q.Get("includeObject") == "Metadata" {
				row["object"] = map[string]interface{}{"kind": "PartialObjectMetadata", "apiVersion": "meta.k8s.io/v1", "metadata": map[string]interface{}{"name": name, "namespace": "apps", "uid": name + "-uid", "labels": map[string]interface{}{"app": "web"}}}
			}
			return row
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"kind": "Table", "apiVersion": "meta.k8s.io/v1", "metadata": map[string]interface{}{"resourceVersion": "10"},
			"columnDefinitions": []map[string]interface{}{{"name": "Name", "type": "string", "format": "name"}, {"name": "Ready", "type": "string"}, {"name": "Status", "type": "string"}, {"name": "Restarts", "type": "string"}, {"name": "Age", "type": "string"}},
			"rows":              []interface{}{row("web-1", "1/1"), row("web-2", "0/1")}})
	case "/api/v1/namespaces/apps/pods/web-1", "/api/v1/namespaces/apps/pods/web-2":
		name := filepath.Base(r.URL.Path)
		s.mu.Lock()
		s.gets = append(s.gets, name)
		s.mu.Unlock()
		fmt.Fprintf(w, `{"apiVersion":"v1","kind":"Pod","metadata":{"name":%q,"namespace":"apps","uid":"%s-uid","resourceVersion":"9","creationTimestamp":"2026-10-16T00:00:00Z"},"spec":{"nodeName":"node-a","containers":[{"name":"web","image":"nginx"}]},"status":{"phase":"Running"}}`, name, name)
	case "/version":
		fmt.Fprint(w, `{"major":"1","minor":"36"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
	}
}

func tableModel(t *testing.T) (*model, *tableServer) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	backend := &tableServer{}
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "kubeconfig")
	config := fmt.Sprintf("apiVersion: v1\nkind: Config\ncurrent-context: test\nclusters:\n- name: test\n  cluster:\n    server: %s\ncontexts:\n- name: test\n  context:\n    cluster: test\n    namespace: apps\nusers: []\n", server.URL)
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	loader, err := kube.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := newModel(context.Background(), s, Options{Context: "test", Namespace: "apps", View: "pods", ScopeExplicit: true}, loader)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.close)
	_, cmd := m.Update(m.connect(m.contextName)())
	m.Update(cmd())
	return m, backend
}

func loadSelectedDetail(t *testing.T, m *model) tea.Cmd {
	t.Helper()
	cmd := m.detailDueReady(detailDueMsg{m.generation, m.detailToken, m.selectedKey()})
	if cmd == nil {
		t.Fatal("selected table row did not request its full object")
	}
	_, next := m.Update(cmd())
	return next
}

func TestServerTableRowsLoadSelectedObjectsOnDemand(t *testing.T) {
	m, server := tableModel(t)
	if m.requestedTable != kube.TableNone || m.rowsTable != kube.TableNone || len(m.rows) != 2 || !kube.IsTableRow(m.rows[0]) {
		t.Fatalf("server-side table not used: requested=%q rows=%d", m.requestedTable, len(m.rows))
	}
	if m.column(m.visible[1], "ready") != "0/1" || kube.Status(m.visible[1]) != "NotReady" {
		t.Fatal("table cells were not rendered")
	}
	if !strings.Contains(m.inspectorContent, "Loading full resource") {
		t.Fatalf("inspector rendered a partial row as a manifest: %q", m.inspectorContent)
	}
	key(m, "d")
	if m.mode != "" || m.detailPending != "apps/web-1" {
		t.Fatal("describe opened before the full object loaded")
	}
	loadSelectedDetail(t, m)
	current, _ := m.current()
	if m.mode != "details" || kube.Text(current, "spec", "nodeName") != "node-a" || m.selectedKey() != "apps/web-1" {
		t.Fatalf("deferred describe did not use the full object: mode=%q", m.mode)
	}
	key(m, "esc")
	key(m, "j")
	if m.awaitingDetail() == false || m.selectedKey() != "apps/web-2" {
		t.Fatal("moving selection reused another row's details")
	}
	m.staleDetail("apps/web-1")
	key(m, "k")
	if current, _ := m.current(); kube.Text(current, "spec", "nodeName") != "node-a" {
		t.Fatal("stale details were discarded before a refresh arrived")
	}
	if len(server.gets) != 1 || server.gets[0] != "web-1" {
		t.Fatalf("unexpected object reads: %v", server.gets)
	}
}

func TestServerTableModeFollowsColumnsAndConfiguration(t *testing.T) {
	m, server := tableModel(t)
	key(m, "w")
	if m.requestedTable != kube.TableMetadata || !m.busy {
		t.Fatalf("labels column did not request table metadata: %q", m.requestedTable)
	}
	m.settings.Config.FullObjectLists = true
	if m.tableMode(m.route) != kube.TableOff {
		t.Fatal("full_object_lists did not disable server-side tables")
	}
	m.settings.Config.FullObjectLists = false
	m.wide = false
	m.setOrder("/spec/nodeName", false)
	if m.tableMode(m.route) != kube.TableOff {
		t.Fatal("pointer columns cannot be served by a table")
	}
	if server.includes[0] != "None" {
		t.Fatalf("initial namespaced list: %v", server.includes)
	}
}

func TestDemoBackendKeepsFullObjects(t *testing.T) {
	m := demoModel(t)
	if m.tableMode(m.route) != kube.TableOff || kube.IsTableRow(m.rows[0]) {
		t.Fatal("demo backend used server-side tables")
	}
}

func collect(cmd tea.Cmd, wait time.Duration) []tea.Msg {
	messages := make(chan tea.Msg, 64)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range batch {
					run(sub)
				}
				return
			}
			if msg != nil {
				messages <- msg
			}
		}()
	}
	run(cmd)
	var result []tea.Msg
	timeout := time.After(wait)
	for {
		select {
		case msg := <-messages:
			result = append(result, msg)
		case <-timeout:
			return result
		}
	}
}

func deliver[T tea.Msg](m *model, messages []tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, msg := range messages {
		if _, ok := msg.(T); ok {
			_, cmd := m.Update(msg)
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func TestNeighbouringRowsArePrefetched(t *testing.T) {
	m, server := tableModel(t)
	next := loadSelectedDetail(t, m)
	if m.prefetchLoading != "apps/web-2" {
		t.Fatalf("next row was not prefetched: %q", m.prefetchLoading)
	}
	deliver[detailMsg](m, collect(next, 500*time.Millisecond))
	key(m, "j")
	if m.awaitingDetail() || strings.Contains(m.inspectorContent, "Loading full resource") {
		t.Fatal("moving to a prefetched row still waited for its object")
	}
	if len(server.gets) != 2 {
		t.Fatalf("object reads: %v", server.gets)
	}
}

func TestDrillDownTargetIsPrefetched(t *testing.T) {
	m, server := tableModel(t)
	_, cmd := m.Update(m.changeResource("namespaces", false)())
	if len(m.rows) != 1 || m.visible[0].GetName() != "apps" {
		t.Fatalf("namespaces: %v", m.rows)
	}
	messages := collect(cmd, time.Second)
	_, load := m.Update(messages[slices.IndexFunc(messages, func(msg tea.Msg) bool { _, ok := msg.(viewDueMsg); return ok })])
	deliver[viewPrefetchedMsg](m, collect(load, time.Second))
	if _, ok := m.viewCache[routeKey(route{resource: m.resources[slices.IndexFunc(m.resources, func(r kube.Resource) bool { return r.Kind == "Pod" })], namespace: "apps"})]; !ok {
		t.Fatalf("pods in the selected namespace were not prefetched: %v", m.viewCache)
	}
	lists := len(server.includes)
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.awaitingDetail() || m.route.resource.Kind != "Pod" || m.route.namespace != "apps" {
		t.Fatal("namespace drill-down waited for the namespace object")
	}
	m.Update(cmd())
	if len(m.rows) != 2 || len(server.includes) != lists {
		t.Fatalf("drill-down did not use the prefetched view: rows=%d lists=%d->%d", len(m.rows), lists, len(server.includes))
	}
}
