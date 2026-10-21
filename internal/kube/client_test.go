package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

func TestClientKubernetesHTTPContract(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		if r.Method != "GET" {
			t.Errorf("unexpected mutation %s", r.Method)
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			fmt.Fprint(w, `{"kind":"APIVersions","versions":["v1"]}`)
		case "/apis":
			fmt.Fprint(w, `{"kind":"APIGroupList","groups":[{"name":"demo.kqt.dev","versions":[{"groupVersion":"demo.kqt.dev/v1","version":"v1"}],"preferredVersion":{"groupVersion":"demo.kqt.dev/v1","version":"v1"}}]}`)
		case "/api/v1":
			fmt.Fprint(w, `{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"pods","kind":"Pod","namespaced":true,"verbs":["get","list"],"shortNames":["po"]},{"name":"pods/log","kind":"Pod","verbs":["get"]}]}`)
		case "/apis/demo.kqt.dev/v1":
			fmt.Fprint(w, `{"kind":"APIResourceList","groupVersion":"demo.kqt.dev/v1","resources":[{"name":"widgets","kind":"Widget","namespaced":true,"verbs":["list"],"shortNames":["wdg"]}]}`)
		case "/api/v1/namespaces/apps/pods":
			q := r.URL.Query()
			if q.Get("labelSelector") != "app=web" || q.Get("fieldSelector") != "spec.nodeName=worker" || q.Get("limit") != "1000" {
				t.Errorf("wrong list options: %s", r.URL)
			}
			name, next := "one", "page2"
			if q.Get("continue") == "page2" {
				name, next = "two", ""
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]interface{}{"continue": next}, "items": []interface{}{map[string]interface{}{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]interface{}{"name": name, "namespace": "apps", "uid": name}}}})
		case "/api/v1/pods":
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"PodList","items":[]}`)
		case "/api/v1/namespaces/apps/pods/one/log":
			q := r.URL.Query()
			if q.Get("container") != "web" || q.Get("previous") != "true" || q.Get("tailLines") != "500" || q.Get("limitBytes") != "1048576" || q.Get("timestamps") != "true" {
				t.Errorf("wrong log options: %s", r.URL)
			}
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "log line\n")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	c, err := NewClient(&rest.Config{Host: s.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	resources, err := c.Resources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	widget, err := Resolve(resources, "wdg")
	if err != nil || widget.ID() != "widgets.demo.kqt.dev" {
		t.Fatalf("CRD discovery: %v %v", widget, err)
	}
	pod, _ := Resolve(resources, "po")
	rows, err := c.List(context.Background(), Query{Resource: pod, Namespace: "apps", Labels: "app=web", Fields: "spec.nodeName=worker"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1].GetName() != "two" {
		t.Fatalf("pagination: %v", rows)
	}
	if _, err = c.List(context.Background(), Query{Resource: pod}); err != nil {
		t.Fatal(err)
	}
	logs, err := c.Logs(context.Background(), "apps", "one", "web", true)
	if err != nil || logs != "log line\n" {
		t.Fatalf("logs: %q %v", logs, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.List(ctx, Query{Resource: pod}); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 7 {
		t.Fatalf("missing HTTP requests: %v", calls)
	}
}
func TestForbiddenList(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","message":"pods is forbidden","code":403}`)
	}))
	defer s.Close()
	c, err := NewClient(&rest.Config{Host: s.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	pod, _ := Resolve(Builtins(), "pods")
	_, err = c.List(context.Background(), Query{Resource: pod})
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("lost RBAC error: %v", err)
	}
}

func TestDiscoveryCancellation(t *testing.T) {
	started := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer s.Close()
	c, err := NewClient(&rest.Config{Host: s.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.Resources(ctx); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("discovery did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled discovery succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("discovery ignored cancellation")
	}
}

func TestResourceAmbiguityAndQualifiedNames(t *testing.T) {
	a := Resource{Kind: "Widget", Namespaced: true}
	a.GVR.Group, a.GVR.Version, a.GVR.Resource = "one.dev", "v1", "widgets"
	b := a
	b.GVR.Group = "two.dev"
	if _, err := Resolve([]Resource{a, b}, "widgets"); err == nil {
		t.Fatal("ambiguous resource silently resolved")
	}
	if r, err := Resolve([]Resource{a, b}, "widgets.two.dev"); err != nil || r.GVR.Group != "two.dev" {
		t.Fatalf("qualified resource not resolved: %v", err)
	}
}
func TestKubeconfigContextIsSessionOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	data := []byte("apiVersion: v1\nkind: Config\ncurrent-context: first\nclusters:\n- name: local\n  cluster:\n    server: http://127.0.0.1:12345\ncontexts:\n- name: first\n  context:\n    cluster: local\n    namespace: apps\n- name: second\n  context:\n    cluster: local\n    namespace: staging\nusers: []\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	b, server, ns, err := l.Open("second")
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	if ns != "staging" || server != "http://127.0.0.1:12345" || l.Current() != "first" {
		t.Fatal("incorrect context resolution")
	}
	after, _ := os.ReadFile(path)
	if !reflect.DeepEqual(after, data) {
		t.Fatal("kubeconfig was modified")
	}
	if _, _, _, err = l.Open("missing"); err == nil {
		t.Fatal("silently fell back from missing context")
	}
}
func TestRedactionSelectorsAndPodReadiness(t *testing.T) {
	secret := unstructured.Unstructured{Object: map[string]interface{}{"kind": "Secret", "metadata": map[string]interface{}{"name": "test", "annotations": map[string]interface{}{"kubectl.kubernetes.io/last-applied-configuration": "sensitive"}}, "data": map[string]interface{}{"password": "c2VjcmV0"}, "stringData": map[string]interface{}{"password": "plain"}}}
	for _, asJSON := range []bool{false, true} {
		text := Manifest(secret, asJSON)
		for _, value := range []string{"c2VjcmV0", "plain", "sensitive"} {
			if strings.Contains(text, value) {
				t.Fatalf("secret leaked: %s", text)
			}
		}
	}
	if secret.Object["data"].(map[string]interface{})["password"] != "c2VjcmV0" {
		t.Fatal("redaction mutated source")
	}
	d := NewDemo()
	pod, _ := Resolve(Builtins(), "po")
	rows, _ := d.List(context.Background(), Query{Resource: pod, Namespace: "default"})
	if Status(rows[4]) != "CrashLoopBackOff" {
		t.Fatalf("waiting reason hidden: %s", Status(rows[4]))
	}
	if Status(rows[0]) != "Running" || Column(rows[0], "ready") != "2/2" {
		t.Fatal("incorrect pod readiness")
	}
	deploy, _ := Resolve(Builtins(), "deploy")
	objects, _ := d.List(context.Background(), Query{Resource: deploy, Namespace: "default"})
	selector, fields, ok := PodSelector(objects[0])
	if !ok || selector != "app=web" || fields != "" {
		t.Fatalf("selector %s %s %t", selector, fields, ok)
	}
	if strings.Contains(SafeLines("\x1b[31mtest\x07\nnext"), "\x1b") || !strings.Contains(SafeLines("test\nnext"), "\n") {
		t.Fatal("unsafe terminal text")
	}
}

func TestClientEditGetAndVersionedMergePatch(t *testing.T) {
	var gotPatch map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/apps/pods/edit-me" {
			t.Errorf("wrong edit path: %s", r.URL)
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "PATCH" {
			if r.Header.Get("Content-Type") != "application/merge-patch+json" || r.URL.Query().Get("fieldManager") != "kqt-editor" {
				t.Errorf("wrong patch options: %s", r.URL)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotPatch); err != nil {
				t.Error(err)
			}
		} else if r.Method != "GET" {
			t.Errorf("unexpected edit method %s", r.Method)
		}
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"Pod","metadata":{"namespace":"apps","name":"edit-me","uid":"identity","resourceVersion":"7"}}`)
	}))
	defer server.Close()
	c, err := NewClient(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	pod, _ := Resolve(Builtins(), "pods")
	object, err := c.Get(context.Background(), pod, "apps", "edit-me")
	if err != nil || object.GetUID() != "identity" || object.GetResourceVersion() != "7" {
		t.Fatal("fresh resource read failed")
	}
	patch := []byte(`{"metadata":{"uid":"identity","resourceVersion":"7","annotations":{"kqt.dev/test":"yes"}}}`)
	if err = c.Patch(context.Background(), pod, "apps", "edit-me", patch); err != nil {
		t.Fatal(err)
	}
	if gotPatch == nil || gotPatch["metadata"].(map[string]any)["resourceVersion"] != "7" {
		t.Fatal("patch lost version precondition")
	}
}

func TestListVersionReportsConsistentSnapshot(t *testing.T) {
	versions := map[string]string{"": "10", "page2": "10"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next := "page2"
		if r.URL.Query().Get("continue") == "page2" {
			next = ""
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]interface{}{"resourceVersion": versions[r.URL.Query().Get("continue")], "continue": next}, "items": []interface{}{}})
	}))
	defer server.Close()
	c, err := NewClient(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, _ := Resolve(Builtins(), "pods")
	if _, version, err := c.ListVersion(context.Background(), Query{Resource: r, Namespace: "apps"}); err != nil || version != "10" {
		t.Fatalf("version=%q err=%v", version, err)
	}
	versions["page2"] = "11"
	if _, version, err := c.ListVersion(context.Background(), Query{Resource: r, Namespace: "apps"}); err != nil || version != "" {
		t.Fatalf("inconsistent pages reported version %q err=%v", version, err)
	}
}

func TestMetricsDenialIsRemembered(t *testing.T) {
	var calls sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count, _ := calls.LoadOrStore(r.URL.Path, new(int))
		*count.(*int)++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Forbidden","code":403}`)
	}))
	defer server.Close()
	c, err := NewClient(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for range 3 {
		if _, err := c.Metrics(context.Background(), "Pod", "apps"); err == nil {
			t.Fatal("metrics denial was hidden")
		}
	}
	count, _ := calls.Load("/apis/metrics.k8s.io/v1/namespaces/apps/pods")
	if count == nil || *count.(*int) != 1 {
		t.Fatalf("denied metrics were requested again: %v", count)
	}
}
