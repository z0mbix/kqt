package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
)

func scopedPod(namespace, name, rv string) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"namespace": namespace, "name": name, "uid": namespace + "/" + name, "resourceVersion": rv}}}
}

func TestNamespaceSelectionValidation(t *testing.T) {
	for _, value := range []string{"a,", ",a", "a,,b", "a,*", "a,-A", "a,UPPER", " ", "a/other", strings.Repeat("a", 64)} {
		if _, err := NamespaceNames(value); err == nil {
			t.Errorf("invalid selection accepted: %q", value)
		}
		if NamespaceIncluded(value, "unselected") {
			t.Fatal("invalid selection broadened the scope")
		}
	}
	if value, err := NormalizeNamespaces("prod-dep, prod-ccp,prod-dep,prod-global,prod-system"); err != nil || value != "prod-dep,prod-ccp,prod-global,prod-system" {
		t.Fatal("namespace normalization failed", value, err)
	}
	if !NamespaceIncluded("", "anything") || !NamespaceIncluded("a,b", "b") || NamespaceIncluded("a,b", "c") {
		t.Fatal("namespace membership does not respect explicit scope")
	}
}

func TestNamespaceListUsesOnlySelectedEndpointsAndKeepsPartialResults(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		ns := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/"), "/pods")
		if ns != "prod-dep" && ns != "prod-ccp" && ns != "denied" {
			t.Errorf("unexpected endpoint, including a cluster-wide request: %s", r.URL)
		}
		if ns == "denied" {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`)
			return
		}
		if r.URL.Query().Get("labelSelector") != "app=web" || r.URL.Query().Get("fieldSelector") != "status.phase=Running" {
			t.Error("resource selectors were not preserved per namespace")
		}
		rows := []unstructured.Unstructured{scopedPod(ns, "web", "10"), scopedPod("unselected", "leak", "10")}
		meta := map[string]any{"resourceVersion": "10"}
		if ns == "prod-dep" && r.URL.Query().Get("continue") == "" {
			meta["continue"] = "next"
		} else if ns == "prod-dep" {
			rows = []unstructured.Unstructured{scopedPod(ns, "second", "10")}
		}
		json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": meta, "items": rows})
	}))
	defer server.Close()
	client, err := NewClient(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	pods, _ := Resolve(Builtins(), "pods")
	rows, err := client.List(context.Background(), Query{Resource: pods, Namespace: "prod-dep,prod-ccp,prod-dep,denied", Labels: "app=web", Fields: "status.phase=Running"})
	var partial *NamespaceErrors
	if !errors.As(err, &partial) || !PartialNamespaces(err) || partial.Available != 2 || partial.Total != 3 || len(rows) != 3 {
		t.Fatalf("partial result lost: rows=%d err=%v", len(rows), err)
	}
	if rows[0].GetNamespace() != "prod-dep" || rows[2].GetNamespace() != "prod-ccp" || rows[0].GetName() != rows[2].GetName() {
		t.Fatal("same-name resources from different namespaces were lost")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 || requests["/api/v1/namespaces/prod-dep/pods"] != 2 || requests["/api/v1/namespaces/prod-ccp/pods"] != 1 {
		t.Fatalf("unexpected pagination or duplicate requests: %v", requests)
	}
}

func TestNamespaceWatchCheckpointsResetsPartialFailureAndCancellation(t *testing.T) {
	var mu sync.Mutex
	lists := map[string]int{}
	bReady := make(chan struct{})
	var bOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ns := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/"), "/pods")
		if !slices.Contains([]string{"a", "b", "denied"}, ns) {
			t.Errorf("unselected endpoint: %s", r.URL)
			w.WriteHeader(500)
			return
		}
		if ns == "denied" {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`)
			return
		}
		if r.URL.Query().Get("watch") == "true" {
			rv := r.URL.Query().Get("resourceVersion")
			if ns == "b" {
				if rv != "20" {
					t.Errorf("b lost its checkpoint: %s", rv)
				}
				bOnce.Do(func() { close(bReady) })
				writeWatch(w, watch.Modified, scopedPod(ns, "web", "21"))
			} else if rv == "10" {
				select {
				case <-bReady:
				case <-r.Context().Done():
					return
				}
				writeWatch(w, watch.Error, map[string]any{"kind": "Status", "apiVersion": "v1", "reason": "Expired", "code": 410})
			} else {
				if rv != "30" {
					t.Errorf("a lost its relist checkpoint: %s", rv)
				}
				writeWatch(w, watch.Modified, scopedPod(ns, "replacement", "31"))
			}
			<-r.Context().Done()
			return
		}
		mu.Lock()
		lists[ns]++
		count := lists[ns]
		mu.Unlock()
		rv, name := "10", "web"
		if ns == "b" {
			rv = "20"
		} else if count > 1 {
			rv, name = "30", "replacement"
		}
		json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": rv}, "items": []unstructured.Unstructured{scopedPod(ns, name, rv)}})
	}))
	defer server.Close()
	client, err := NewClient(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pods, _ := Resolve(Builtins(), "pods")
	resets := map[string]int{}
	partial, fromB, replacement := false, false, false
	err = client.FollowResources(ctx, Query{Resource: pods, Namespace: "a,b,denied"}, func(update EventUpdate) {
		if update.Reset {
			resets[update.Namespace]++
		}
		if update.ScopeStatus && update.State == "partial" && strings.Contains(update.Message, "denied") {
			partial = true
		}
		if update.Type == watch.Modified && update.Object.GetNamespace() == "b" {
			fromB = true
		}
		if update.Type == watch.Modified && update.Object.GetName() == "replacement" {
			replacement = true
		}
		if partial && fromB && replacement {
			cancel()
		}
	})
	if err != nil || !partial || !fromB || !replacement || resets["a"] != 2 || resets["b"] != 1 || resets[""] != 0 {
		t.Fatalf("combined watch failed: partial=%v b=%v replacement=%v resets=%v err=%v", partial, fromB, replacement, resets, err)
	}
}

func TestNamespaceMetricsFallbackAndClusterScopedRequests(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/apis/metrics.k8s.io/v1/namespaces/a/pods":
			w.WriteHeader(404)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`)
		case "/apis/metrics.k8s.io/v1beta1/namespaces/a/pods", "/apis/metrics.k8s.io/v1/namespaces/b/pods":
			ns := "a"
			if strings.Contains(r.URL.Path, "/namespaces/b/") {
				ns = "b"
			}
			object := scopedPod(ns, "same-name", "1")
			object.SetKind("PodMetrics")
			object.Object["containers"] = []any{map[string]any{"name": "web", "usage": map[string]any{"cpu": "25m", "memory": "64Mi"}}}
			json.NewEncoder(w).Encode(map[string]any{"apiVersion": "metrics.k8s.io/v1", "kind": "PodMetricsList", "items": []any{object.Object}})
		case "/apis/metrics.k8s.io/v1/namespaces/denied/pods":
			w.WriteHeader(403)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"Forbidden","code":403}`)
		case "/api/v1/nodes":
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"NodeList","items":[{"metadata":{"name":"worker"}}]}`)
		default:
			t.Errorf("unselected or cluster-wide namespaced endpoint: %s", r.URL)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client, err := NewClient(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	usage, err := client.Metrics(context.Background(), "Pod", "a,b,denied,a")
	if !PartialNamespaces(err) || len(usage) != 2 || usage["a/same-name"].CPUMilli != 25 || usage["b/same-name"].MemoryBytes != 64*1024*1024 {
		t.Fatalf("namespace metrics/fallback lost usable results: usage=%v err=%v", usage, err)
	}
	nodes, _ := Resolve(Builtins(), "nodes")
	rows, err := client.List(context.Background(), Query{Resource: nodes, Namespace: "a,b,denied"})
	if err != nil || len(rows) != 1 {
		t.Fatal("cluster-scoped resources were changed by a namespace selection", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests["/api/v1/nodes"] != 1 || len(requests) != 5 {
		t.Fatal("duplicate or unexpected scoped requests", requests)
	}
}
