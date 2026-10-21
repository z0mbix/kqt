package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
)

var podColumns = []map[string]interface{}{
	{"name": "Name", "type": "string", "format": "name"},
	{"name": "Ready", "type": "string"},
	{"name": "Status", "type": "string"},
	{"name": "Restarts", "type": "string"},
	{"name": "Age", "type": "string"},
	{"name": "IP", "type": "string", "priority": 1},
	{"name": "Node", "type": "string", "priority": 1},
}

func podTable(version, next string, columns bool, rows ...[]interface{}) map[string]interface{} {
	table := map[string]interface{}{"kind": "Table", "apiVersion": "meta.k8s.io/v1", "metadata": map[string]interface{}{"resourceVersion": version, "continue": next}}
	if columns {
		table["columnDefinitions"] = podColumns
	}
	var encoded []interface{}
	for _, cells := range rows {
		row := map[string]interface{}{"cells": cells}
		if metadata, ok := cells[len(cells)-1].(map[string]interface{}); ok {
			row["cells"] = cells[:len(cells)-1]
			row["object"] = map[string]interface{}{"kind": "PartialObjectMetadata", "apiVersion": "meta.k8s.io/v1", "metadata": metadata}
		}
		encoded = append(encoded, row)
	}
	table["rows"] = encoded
	return table
}

func TestTableListBuildsCompactRows(t *testing.T) {
	var accepts, includes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accepts = append(accepts, r.Header.Get("Accept"))
		includes = append(includes, r.URL.Query().Get("includeObject"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/namespaces/apps/pods" || r.URL.Query().Get("labelSelector") != "app=web" || r.URL.Query().Get("limit") != "1000" {
			t.Errorf("wrong table request: %s", r.URL)
		}
		if r.URL.Query().Get("continue") == "" {
			json.NewEncoder(w).Encode(podTable("10", "page2", true, []interface{}{"web-1", "1/2", "Running", "3 (5m ago)", "2d4h", "10.0.0.1", "node-a"}))
			return
		}
		json.NewEncoder(w).Encode(podTable("10", "", true, []interface{}{"web-2", "1/1", "Completed", "0", "45s", "<none>", "<none>"}))
	}))
	defer server.Close()
	c, err := NewClient(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, _ := Resolve(Builtins(), "pods")
	rows, version, err := c.ListVersion(context.Background(), Query{Resource: r, Namespace: "apps", Labels: "app=web", Table: TableNone})
	if err != nil || version != "10" || len(rows) != 2 {
		t.Fatalf("rows=%d version=%q err=%v", len(rows), version, err)
	}
	if !strings.Contains(accepts[0], "as=Table") || includes[0] != "None" {
		t.Fatalf("server-side table not requested: %q %q", accepts[0], includes[0])
	}
	web := rows[0]
	if !IsTableRow(web) || web.GetName() != "web-1" || web.GetNamespace() != "apps" || web.GetKind() != "Pod" || web.GetAPIVersion() != "v1" || Identity(web) != "apps/web-1" {
		t.Fatalf("stub identity: %v", web.Object)
	}
	if Status(web) != "NotReady" || Column(web, "ready") != "1/2" || Column(web, "restarts") != "3" || Column(web, "node") != "node-a" || Column(web, "ip") != "10.0.0.1" || Age(web) != "2d" {
		t.Fatalf("stub columns: status=%s ready=%s restarts=%s node=%s ip=%s age=%s", Status(web), Column(web, "ready"), Column(web, "restarts"), Column(web, "node"), Column(web, "ip"), Age(web))
	}
	if Status(rows[1]) != "Succeeded" || Column(rows[1], "node") != "" || Column(rows[1], "/spec/nodeName") != "—" {
		t.Fatal("completed stub or missing cells were misreported")
	}
	if strings.Contains(Manifest(web, false), "kqt.table") {
		t.Fatal("table cells leaked into manifests")
	}
}

func TestTableMetadataRowsAndPlainListFallback(t *testing.T) {
	plain := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if plain {
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"7"},"items":[{"metadata":{"name":"full","namespace":"apps","uid":"full-uid"},"spec":{"nodeName":"node-b"}}]}`)
			return
		}
		metadata := map[string]interface{}{"name": "web", "namespace": "apps", "uid": "web-uid", "resourceVersion": "5", "creationTimestamp": "2026-10-01T00:00:00Z", "labels": map[string]interface{}{"app": "web"}, "managedFields": []interface{}{map[string]interface{}{"manager": "kubectl"}}}
		json.NewEncoder(w).Encode(podTable("6", "", true, []interface{}{"web", "1/1", "Running", "0", "18d", "", "node-a", metadata}))
	}))
	defer server.Close()
	c, _ := NewClient(&rest.Config{Host: server.URL})
	defer c.Close()
	r, _ := Resolve(Builtins(), "pods")
	rows, _, err := c.ListVersion(context.Background(), Query{Resource: r, Table: TableMetadata})
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	web := rows[0]
	if Identity(web) != "web-uid" || web.GetResourceVersion() != "5" || web.GetLabels()["app"] != "web" || web.GetManagedFields() != nil || web.GetCreationTimestamp().UTC().Format(time.RFC3339) != "2026-10-01T00:00:00Z" || Status(web) != "Running" {
		t.Fatalf("metadata stub: %v", web.Object)
	}
	plain = true
	rows, version, err := c.ListVersion(context.Background(), Query{Resource: r, Namespace: "apps", Table: TableNone})
	if err != nil || version != "7" || len(rows) != 1 || IsTableRow(rows[0]) || rows[0].GetKind() != "Pod" || Column(rows[0], "node") != "node-b" {
		t.Fatalf("plain list fallback: %v %v", rows, err)
	}
}

func TestTableWatchKeepsColumnsAcrossEventsAndRelistsOnExpiry(t *testing.T) {
	var lists, watches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/version" {
			return
		}
		if r.URL.Query().Get("includeObject") != "None" || !strings.Contains(r.Header.Get("Accept"), "as=Table") {
			t.Errorf("watch lost table format: %s", r.URL)
		}
		if r.URL.Query().Get("watch") != "true" {
			lists.Add(1)
			json.NewEncoder(w).Encode(podTable("20", "", true, []interface{}{"web-2", "1/1", "Running", "0", "1m", "", ""}))
			return
		}
		if watches.Add(1) == 1 {
			if r.URL.Query().Get("resourceVersion") != "10" {
				t.Errorf("watch ignored checkpoint: %s", r.URL)
			}
			writeWatch(w, watch.Modified, podTable("11", "", true, []interface{}{"web-1", "0/1", "CrashLoopBackOff", "4 (1m ago)", "2d", "", ""}))
			writeWatch(w, watch.Bookmark, podTable("12", "", false))
			writeWatch(w, watch.Deleted, podTable("13", "", false, []interface{}{"web-1", "0/1", "Terminating", "4", "2d", "", ""}))
			writeWatch(w, watch.Error, map[string]interface{}{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "Expired", "code": 410})
			return
		}
		if r.URL.Query().Get("resourceVersion") != "20" {
			t.Errorf("relist checkpoint lost: %s", r.URL)
		}
		writeWatch(w, watch.Added, podTable("21", "", true, []interface{}{"web-3", "1/1", "Running", "0", "5s", "", ""}))
		<-r.Context().Done()
	}))
	defer server.Close()
	c, _ := NewClient(&rest.Config{Host: server.URL})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	r, _ := Resolve(Builtins(), "pods")
	var updates []EventUpdate
	err := c.FollowResources(ctx, Query{Resource: r, Namespace: "apps", ResourceVersion: "10", Table: TableNone}, func(v EventUpdate) {
		if v.Type != "" {
			updates = append(updates, v)
		}
		if v.Object.GetName() == "web-3" {
			cancel()
		}
	})
	if err != nil || lists.Load() != 1 {
		t.Fatalf("lists=%d err=%v", lists.Load(), err)
	}
	var summary []string
	for _, u := range updates {
		summary = append(summary, fmt.Sprintf("%s %s %s %s", u.Type, u.Object.GetName(), u.Object.GetResourceVersion(), Status(u.Object)))
	}
	want := "MODIFIED web-1 11 CrashLoopBackOff|DELETED web-1 13 Terminating|ADDED web-2  Running|ADDED web-3 21 Running"
	if strings.Join(summary, "|") != want {
		t.Fatalf("updates:\n%s\nwant:\n%s", strings.Join(summary, "|"), want)
	}
	if !IsTableRow(updates[0].Object) || Identity(updates[0].Object) != "apps/web-1" || Column(updates[0].Object, "restarts") != "4" {
		t.Fatal("watch rows lost table identity or cells")
	}
}

func TestTableStatusForWorkloads(t *testing.T) {
	row := func(kind string, cells map[string]interface{}) unstructured.Unstructured {
		return unstructured.Unstructured{Object: map[string]interface{}{"kind": kind, "metadata": map[string]interface{}{"name": "x"}, tableCells: cells}}
	}
	for _, tc := range []struct {
		row        unstructured.Unstructured
		status     string
		ready      string
		deleteMeta bool
	}{
		{row("Deployment", map[string]interface{}{"ready": "2/3"}), "Progressing", "2/3", false},
		{row("StatefulSet", map[string]interface{}{"ready": "3/3"}), "Ready", "3/3", false},
		{row("DaemonSet", map[string]interface{}{"desired": "4", "ready": "4"}), "Ready", "4/4", false},
		{row("ReplicaSet", map[string]interface{}{"desired": "2", "ready": "1"}), "Progressing", "1/2", false},
		{row("Node", map[string]interface{}{"status": "Ready,SchedulingDisabled"}), "Ready", "—", false},
		{row("Node", map[string]interface{}{"status": "Unknown"}), "NotReady", "—", false},
		{row("Job", map[string]interface{}{"status": "Complete"}), "Succeeded", "—", false},
		{row("Job", map[string]interface{}{"completions": "0/1"}), "Running", "—", false},
		{row("Certificate", map[string]interface{}{"ready": "True"}), "Ready", "True", false},
		{row("ConfigMap", map[string]interface{}{"data": "3"}), "—", "—", false},
		{row("Deployment", map[string]interface{}{"ready": "3/3"}), "Terminating", "3/3", true},
	} {
		if tc.deleteMeta {
			unstructured.SetNestedField(tc.row.Object, "2026-10-19T00:00:00Z", "metadata", "deletionTimestamp")
		}
		if Status(tc.row) != tc.status || Column(tc.row, "ready") != tc.ready {
			t.Errorf("%s %v: status=%s ready=%s", tc.row.GetKind(), tc.row.Object[tableCells], Status(tc.row), Column(tc.row, "ready"))
		}
	}
}

func TestParseServerAge(t *testing.T) {
	for value, want := range map[string]time.Duration{"45s": 45 * time.Second, "5m30s": 330 * time.Second, "3h": 3 * time.Hour, "2d4h": 52 * time.Hour, "2y30d": (730 + 30) * 24 * time.Hour} {
		if got, ok := parseAge(value); !ok || got != want {
			t.Errorf("%s: %v %v", value, got, ok)
		}
	}
	for _, value := range []string{"", "<invalid>", "2026-10-01T00:00:00Z", "5 m"} {
		if _, ok := parseAge(value); ok {
			t.Errorf("%q parsed as an age", value)
		}
	}
}
