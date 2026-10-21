package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
)

func watchedEvent(name, namespace, subject, version string, count int64) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1", "kind": "Event", "metadata": map[string]interface{}{"name": name, "namespace": namespace, "uid": name, "resourceVersion": version, "labels": map[string]interface{}{"app": "web"}},
		"type": "Warning", "reason": "BackOff", "message": "retrying", "count": count, "lastTimestamp": "2026-10-13T15:00:00Z",
		"involvedObject": map[string]interface{}{"uid": subject, "kind": "Pod", "name": "web"},
	}}
}

func writeEventList(w http.ResponseWriter, version, next string, objects ...unstructured.Unstructured) {
	json.NewEncoder(w).Encode(map[string]interface{}{"apiVersion": "v1", "kind": "EventList", "metadata": map[string]interface{}{"resourceVersion": version, "continue": next}, "items": objects})
}
func writeWatch(w http.ResponseWriter, kind watch.EventType, object any) {
	if o, ok := object.(unstructured.Unstructured); ok {
		object = o.Object
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"type": kind, "object": object})
	w.(http.Flusher).Flush()
}

func TestFollowEventsCheckpointsPaginationScopeExpiryAndCancellation(t *testing.T) {
	for _, tc := range []struct{ httpExpiry, holdOpen bool }{{false, false}, {false, true}, {true, false}} {
		t.Run(fmt.Sprintf("http-expiry=%t/hold-open=%t", tc.httpExpiry, tc.holdOpen), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			lists, streams := 0, 0
			closed := make(chan struct{})
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/version" {
					return
				}
				q := r.URL.Query()
				if r.Method != "GET" || r.URL.Path != "/api/v1/namespaces/apps/events" || q.Get("fieldSelector") != "involvedObject.uid=pod-uid" || q.Get("labelSelector") != "app=web" {
					t.Errorf("wrong scoped request: %s %s", r.Method, r.URL)
				}
				if q.Get("watch") != "true" {
					mu.Lock()
					lists++
					n := lists
					mu.Unlock()
					if q.Get("limit") != "1000" {
						t.Error("list was not paginated")
					}
					switch n {
					case 1:
						// Missing per-item GVK is legal in a list; the backend fills it.
						o := watchedEvent("retry", "apps", "pod-uid", "rv/99", 1)
						o.SetKind("")
						o.SetAPIVersion("")
						writeEventList(w, "rv/100", "next", o, watchedEvent("wrong-uid", "apps", "other", "rv/99", 1))
					case 2:
						if q.Get("continue") != "next" {
							t.Error("lost pagination token")
						}
						writeEventList(w, "rv/100", "", watchedEvent("wrong-namespace", "other", "pod-uid", "rv/99", 1))
					case 3:
						writeEventList(w, "rv/200", "", watchedEvent("retry", "apps", "pod-uid", "rv/200", 4))
					default:
						t.Error("unexpected relist")
						writeEventList(w, "rv/200", "")
					}
					return
				}
				mu.Lock()
				streams++
				n := streams
				mu.Unlock()
				if q.Get("allowWatchBookmarks") != "true" || q.Get("timeoutSeconds") != "60" {
					t.Error("missing watch renewal/bookmarks")
				}
				switch n {
				case 1:
					if q.Get("resourceVersion") != "rv/100" {
						t.Error("watch missed list checkpoint")
					}
					writeWatch(w, watch.Modified, watchedEvent("retry", "apps", "pod-uid", "rv/101", 2))
					writeWatch(w, watch.Deleted, watchedEvent("retry", "apps", "pod-uid", "rv/102", 2))
					writeWatch(w, watch.Bookmark, &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Event", "metadata": map[string]interface{}{"resourceVersion": "rv/103"}}})
					writeWatch(w, watch.Added, watchedEvent("unrelated", "apps", "other", "rv/104", 1))
				case 2:
					if q.Get("resourceVersion") != "rv/104" {
						t.Errorf("reconnect lost bookmark/unrelated checkpoint: %s", q.Get("resourceVersion"))
					}
					status := map[string]interface{}{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "Expired", "message": "too old resource version", "code": 410}
					if tc.httpExpiry {
						w.WriteHeader(410)
						json.NewEncoder(w).Encode(status)
					} else {
						writeWatch(w, watch.Error, status)
						// A watch error need not coincide with the server closing
						// its body. Reconnection must actively release this stream.
						if tc.holdOpen {
							<-r.Context().Done()
						}
					}
				case 3:
					if q.Get("resourceVersion") != "rv/200" {
						t.Error("resync watch missed new checkpoint")
					}
					// Survive the ordinary client's short timeout while idle.
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					select {
					case <-r.Context().Done():
						return
					case <-time.After(100 * time.Millisecond):
					}
					writeWatch(w, watch.Modified, watchedEvent("retry", "apps", "pod-uid", "rv/201", 5))
					<-r.Context().Done()
					close(closed)
				default:
					t.Error("unexpected watch")
				}
			}))
			defer func() { cancel(); s.Close() }()
			c, err := NewClient(&rest.Config{Host: s.URL, Timeout: 30 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			events, _ := Resolve(Builtins(), "events")
			updates := make(chan EventUpdate, 32)
			done := make(chan error, 1)
			go func() {
				done <- c.FollowEvents(ctx, Query{Resource: events, Namespace: "apps", Fields: "involvedObject.uid=pod-uid", Labels: "app=web"}, func(v EventUpdate) { updates <- v })
			}()
			var objects []EventUpdate
			var trace []string
			gap := false
			deadline := time.After(5 * time.Second)
		read:
			for {
				select {
				case v := <-updates:
					trace = append(trace, fmt.Sprintf("%s rv=%s state=%s gap=%t %s", v.Type, v.Object.GetResourceVersion(), v.State, v.Gap, v.Message))
					gap = gap || v.Gap
					if v.Type != "" {
						objects = append(objects, v)
					}
					if v.Object.GetResourceVersion() == "rv/201" {
						break read
					}
				case <-deadline:
					cancel()
					mu.Lock()
					requestCounts := fmt.Sprintf("lists=%d watches=%d", lists, streams)
					mu.Unlock()
					t.Fatalf("watch did not reconnect and relist (%s): %v", requestCounts, trace)
				}
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("idle watch did not cancel")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("response body was not closed")
			}
			if !gap || len(objects) != 5 || objects[2].Type != watch.Deleted || EventCount(objects[4].Object) != 5 {
				t.Fatalf("wrong updates: gap=%t objects=%v", gap, objects)
			}
			for _, v := range objects {
				if v.Object.GetName() != "retry" || v.Object.GetNamespace() != "apps" || v.Object.GetKind() != "Event" || v.Object.GetAPIVersion() != "v1" {
					t.Fatalf("scope/GVK lost: %v", v)
				}
			}
		})
	}
}

func TestFollowEventsPermissionsAndStalledHandshake(t *testing.T) {
	for _, where := range []string{"list", "watch", "stalled"} {
		t.Run(where, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{}, 1)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				watching := r.URL.Query().Get("watch") == "true"
				if !watching && where != "list" {
					writeEventList(w, "1", "")
					return
				}
				if where == "stalled" {
					started <- struct{}{}
					<-r.Context().Done()
					return
				}
				w.WriteHeader(403)
				fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","reason":"Forbidden","status":"Failure","message":"events is forbidden","code":403}`)
			}))
			defer func() { cancel(); s.Close() }()
			c, err := NewClient(&rest.Config{Host: s.URL})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			events, _ := Resolve(Builtins(), "events")
			done := make(chan error, 1)
			go func() { done <- c.FollowEvents(ctx, Query{Resource: events}, func(EventUpdate) {}) }()
			if where == "stalled" {
				select {
				case <-started:
					cancel()
				case <-time.After(time.Second):
					t.Fatal("watch did not start")
				}
			}
			select {
			case err := <-done:
				if where == "stalled" && err != nil || where != "stalled" && !apierrors.IsForbidden(err) {
					t.Fatalf("wrong failure: %v", err)
				}
			case <-time.After(time.Second):
				cancel()
				t.Fatal("watch did not stop")
			}
		})
	}
}

func TestModernEventFieldsAndDemoScopedFollow(t *testing.T) {
	o := unstructured.Unstructured{Object: map[string]interface{}{"regarding": map[string]interface{}{"uid": "target", "name": "web", "kind": "Pod"}, "note": "healthy", "deprecatedCount": int64(3), "series": map[string]interface{}{"count": int64(7), "lastObservedTime": "2026-10-13T15:00:00Z"}}}
	if EventSubject(o, "uid") != "target" || EventMessage(o) != "healthy" || EventCount(o) != 7 || EventTime(o).IsZero() {
		t.Fatal("events.k8s.io fields lost")
	}
	d := NewDemo()
	events, _ := Resolve(Builtins(), "events")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	seen := false
	err := d.FollowEvents(ctx, Query{Resource: events, Fields: "involvedObject.uid=/worker-1"}, func(v EventUpdate) {
		if v.Type == watch.Modified {
			if EventSubject(v.Object, "uid") != "/worker-1" || v.Object.GetNamespace() != "default" || !strings.HasPrefix(v.Object.GetName(), "demo-live-") {
				t.Error("demo scope invalid")
			}
			seen = true
			cancel()
		}
	})
	if err != nil || !seen {
		t.Fatalf("demo stream: %t %v", seen, err)
	}
}
