package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
)

func TestGenericResourceWatchScopeChangesExpiryAndCancellation(t *testing.T) {
	var lists, watches atomic.Int32
	object := func(name, rv string) unstructured.Unstructured {
		return unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]interface{}{"name": name, "namespace": "apps", "uid": name, "resourceVersion": rv, "labels": map[string]interface{}{"app": "web"}}}}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/version" {
			return
		}
		if r.URL.Path != "/api/v1/namespaces/apps/pods" || r.URL.Query().Get("labelSelector") != "app=web" {
			t.Errorf("watch scope lost: %s", r.URL)
		}
		if r.URL.Query().Get("watch") == "true" {
			n := watches.Add(1)
			if n == 1 {
				if r.URL.Query().Get("resourceVersion") != "10" {
					t.Error("initial list checkpoint lost")
				}
				writeWatch(w, watch.Modified, object("one", "11"))
				writeWatch(w, watch.Deleted, object("one", "12"))
				unrelated := object("unrelated", "13")
				unrelated.SetNamespace("other")
				writeWatch(w, watch.Added, unrelated)
				writeWatch(w, watch.Error, map[string]interface{}{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "Expired", "code": 410})
			} else {
				if r.URL.Query().Get("resourceVersion") != "20" {
					t.Error("relist checkpoint lost")
				}
				writeWatch(w, watch.Modified, object("two", "21"))
			}
			<-r.Context().Done()
			return
		}
		n := lists.Add(1)
		rv, name := "10", "one"
		if n > 1 {
			rv, name = "20", "two"
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]interface{}{"resourceVersion": rv}, "items": []unstructured.Unstructured{object(name, rv)}})
	}))
	defer server.Close()
	c, e := NewClient(&rest.Config{Host: server.URL})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	r, _ := Resolve(Builtins(), "pods")
	resets, modified, deleted := 0, 0, 0
	e = c.FollowResources(ctx, Query{Resource: r, Namespace: "apps", Labels: "app=web"}, func(v EventUpdate) {
		if v.Reset {
			resets++
		}
		if v.Type == watch.Modified {
			modified++
			if v.Object.GetName() == "two" {
				cancel()
			}
		}
		if v.Type == watch.Deleted {
			deleted++
		}
		if v.Object.GetName() == "unrelated" {
			t.Error("unrelated object leaked")
		}
	})
	if e != nil || resets != 2 || modified != 2 || deleted != 1 {
		t.Fatalf("watch lifecycle failed: reset=%d modified=%d deleted=%d err=%v", resets, modified, deleted, e)
	}
}
func TestGenericClusterScopedWatchUsesRootAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/nodes" {
			t.Errorf("cluster watch used namespace: %s", r.URL.Path)
		}
		if r.URL.Query().Get("watch") == "true" {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Forbidden","code":403}`)
			return
		}
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"NodeList","metadata":{"resourceVersion":"1"},"items":[]}`)
	}))
	defer server.Close()
	c, _ := NewClient(&rest.Config{Host: server.URL})
	defer c.Close()
	r, _ := Resolve(Builtins(), "nodes")
	if e := c.FollowResources(context.Background(), Query{Resource: r, Namespace: "ignored"}, func(EventUpdate) {}); e == nil {
		t.Fatal("watch permission denial was hidden")
	}
}
func TestGenericResourceWatchResumesFromCallerList(t *testing.T) {
	var lists, warms atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/version" {
			warms.Add(1)
			fmt.Fprint(w, `{"major":"1","minor":"36"}`)
			return
		}
		if r.URL.Query().Get("watch") != "true" {
			lists.Add(1)
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"99"},"items":[]}`)
			return
		}
		if r.URL.Query().Get("resourceVersion") != "42" {
			t.Errorf("watch ignored caller checkpoint: %s", r.URL)
		}
		pod := unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]interface{}{"name": "web", "namespace": "apps", "uid": "web", "resourceVersion": "43"}}}
		writeWatch(w, watch.Modified, pod)
		<-r.Context().Done()
	}))
	defer server.Close()
	c, e := NewClient(&rest.Config{Host: server.URL})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	r, _ := Resolve(Builtins(), "pods")
	synced, resets := false, 0
	e = c.FollowResources(ctx, Query{Resource: r, Namespace: "apps", ResourceVersion: "42"}, func(v EventUpdate) {
		synced = synced || v.Synced
		if v.Reset {
			resets++
		}
		if v.Type == watch.Modified {
			cancel()
		}
	})
	if e != nil || lists.Load() != 0 || resets != 0 || !synced {
		t.Fatalf("resume relisted or lost sync: lists=%d resets=%d synced=%t err=%v", lists.Load(), resets, synced, e)
	}
	deadline := time.Now().Add(time.Second)
	for warms.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if warms.Load() == 0 {
		t.Fatal("established watch did not warm a spare connection")
	}
}
