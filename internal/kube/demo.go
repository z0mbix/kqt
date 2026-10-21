package kube

import (
	"context"
	"encoding/json"
	"fmt"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	"io"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"strconv"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type Demo struct {
	resourceListenerID uint64
	resourceListeners  map[uint64]chan struct{}
	objects            []unstructured.Unstructured
	mu                 sync.RWMutex
	logStart           time.Time
}

func NewDemo() *Demo {
	d := &Demo{logStart: time.Now().UTC().Truncate(time.Second).Add(-time.Minute)}
	add := func(version, kind, ns, name string, spec, status map[string]interface{}) {
		o := unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": version, "kind": kind, "metadata": map[string]interface{}{"name": name, "namespace": ns, "uid": ns + "/" + name}}}
		o.SetCreationTimestamp(metav1.NewTime(time.Now().Add(-27 * time.Hour)))
		o.SetUID(types.UID(ns + "/" + name))
		o.SetGeneration(1)
		o.SetResourceVersion("1")
		if spec != nil {
			o.Object["spec"] = spec
		}
		if status != nil {
			o.Object["status"] = status
		}
		d.objects = append(d.objects, o)
	}
	for _, ns := range []string{"default", "platform", "kube-system"} {
		add("v1", "Namespace", "", ns, nil, map[string]interface{}{"phase": "Active"})
	}
	for i := 1; i <= 3; i++ {
		add("v1", "Node", "", fmt.Sprintf("worker-%d", i), nil, map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}, "nodeInfo": map[string]interface{}{"kubeletVersion": "v1.36.0", "architecture": "arm64", "operatingSystem": "linux"}, "allocatable": map[string]interface{}{"cpu": "4", "memory": "8Gi"}})
		d.objects[len(d.objects)-1].SetLabels(map[string]string{
			"kubernetes.io/arch":               "arm64",
			"kubernetes.io/hostname":           fmt.Sprintf("worker-%d", i),
			"kubernetes.io/os":                 "linux",
			"node.kubernetes.io/instance-type": "standard-4",
			"node-role.kubernetes.io/worker":   "",
			"topology.kubernetes.io/region":    "eu-west-2",
			"topology.kubernetes.io/zone":      fmt.Sprintf("eu-west-2%c", 'a'+i-1),
		})
	}
	for i := 0; i < 24; i++ {
		ns, app := "default", "web"
		if i >= 12 {
			ns, app = "platform", "gateway"
		}
		phase, ready := "Running", true
		state := map[string]interface{}{"running": map[string]interface{}{"startedAt": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}}
		if i == 4 {
			ready = false
			state = map[string]interface{}{"waiting": map[string]interface{}{"reason": "CrashLoopBackOff", "message": "Back-off restarting failed container"}}
		}
		if i == 8 {
			phase, ready = "Pending", false
			state = map[string]interface{}{"waiting": map[string]interface{}{"reason": "ContainerCreating"}}
		}
		spec := map[string]interface{}{"nodeName": fmt.Sprintf("worker-%d", i%3+1), "containers": []interface{}{map[string]interface{}{"name": app, "image": "nginx:1.28", "ports": []interface{}{map[string]interface{}{"containerPort": int64(80)}}}}}
		if i == 0 {
			spec["containers"] = append(spec["containers"].([]interface{}), map[string]interface{}{"name": "sidecar", "image": "busybox:1.37"})
			spec["initContainers"] = []interface{}{map[string]interface{}{"name": "setup", "image": "busybox:1.37"}}
		}
		status := map[string]interface{}{"phase": phase, "podIP": fmt.Sprintf("10.42.%d.%d", i%3, i+10), "conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "False"}}, "containerStatuses": []interface{}{map[string]interface{}{"name": app, "ready": ready, "restartCount": int64(i % 5), "state": state}}}
		if ready {
			status["conditions"] = []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}
		}
		if i == 0 {
			status["containerStatuses"] = append(status["containerStatuses"].([]interface{}), map[string]interface{}{"name": "sidecar", "ready": true, "restartCount": int64(0), "state": state})
			status["initContainerStatuses"] = []interface{}{map[string]interface{}{"name": "setup", "ready": true, "restartCount": int64(0), "state": map[string]interface{}{"terminated": map[string]interface{}{"reason": "Completed", "exitCode": int64(0)}}}}
		}
		add("v1", "Pod", ns, fmt.Sprintf("%s-7c9f6d8b5-%04d", app, i), spec, status)
		d.objects[len(d.objects)-1].SetLabels(map[string]string{"app": app})
	}
	for _, ns := range []string{"default", "platform"} {
		app := "web"
		if ns == "platform" {
			app = "gateway"
		}
		add("apps/v1", "Deployment", ns, app, map[string]interface{}{"replicas": int64(12), "selector": map[string]interface{}{"matchLabels": map[string]interface{}{"app": app}}, "template": map[string]interface{}{"spec": map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": app, "image": "nginx:1.28"}}}}}, map[string]interface{}{"replicas": int64(12), "readyReplicas": int64(12), "updatedReplicas": int64(12), "availableReplicas": int64(12), "observedGeneration": int64(1)})
		add("v1", "Service", ns, app, map[string]interface{}{"type": "ClusterIP", "clusterIP": "10.96.0.12", "selector": map[string]interface{}{"app": app}, "ports": []interface{}{map[string]interface{}{"port": int64(80), "protocol": "TCP"}}}, nil)
	}
	add("apps/v1", "StatefulSet", "platform", "redis", map[string]interface{}{"replicas": int64(2), "serviceName": "redis", "selector": map[string]interface{}{"matchLabels": map[string]interface{}{"app": "redis"}}, "template": map[string]interface{}{"spec": map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "redis", "image": "redis:8"}}}}}, map[string]interface{}{"readyReplicas": int64(2), "observedGeneration": int64(1)})
	for i := 0; i < 2; i++ {
		add("v1", "Pod", "platform", fmt.Sprintf("redis-%d", i), map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "redis", "image": "redis:8"}}, "nodeName": "worker-1"}, map[string]interface{}{"phase": "Running", "conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}, "containerStatuses": []interface{}{map[string]interface{}{"name": "redis", "ready": true, "restartCount": int64(0)}}})
		d.objects[len(d.objects)-1].SetLabels(map[string]string{"app": "redis"})
	}
	add("v1", "ConfigMap", "default", "web-settings", nil, nil)
	d.objects[len(d.objects)-1].Object["data"] = map[string]interface{}{"log-level": "info", "environment": "demo"}
	add("v1", "Secret", "default", "demo-credentials", nil, nil)
	d.objects[len(d.objects)-1].Object["data"] = map[string]interface{}{"token": "ZGVtby1vbmx5"}
	add("v1", "Event", "default", "web-backoff", nil, nil)
	e := &d.objects[len(d.objects)-1]
	e.Object["type"] = "Warning"
	e.Object["reason"] = "BackOff"
	e.Object["message"] = "Back-off restarting failed container web"
	for _, object := range d.objects {
		if object.GetKind() == "Pod" && object.GetNamespace() == "default" {
			e.Object["involvedObject"] = map[string]interface{}{"uid": string(object.GetUID()), "name": object.GetName(), "namespace": object.GetNamespace(), "kind": "Pod"}
			break
		}
	}
	add("exploration.kqt.dev/v1", "Widget", "platform", "example", map[string]interface{}{"description": "Discovered custom resource"}, map[string]interface{}{"phase": "Ready"})
	demoRelationships(d)
	demoRollouts(d)
	demoActions(d)
	demoDiagnostics(d)
	return d
}
func (d *Demo) Resources(context.Context) ([]Resource, error) {
	return Merge([]Resource{{schema.GroupVersionResource{Group: "exploration.kqt.dev", Version: "v1", Resource: "widgets"}, "Widget", true, []string{"wdg"}}}), nil
}
func (d *Demo) List(ctx context.Context, q Query) ([]unstructured.Unstructured, error) {
	if q.Resource.Namespaced {
		if _, err := NamespaceNames(q.Namespace); err != nil {
			return nil, err
		}
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selector, err := labels.Parse(q.Labels)
	if err != nil {
		return nil, err
	}
	fieldSelector, err := fields.ParseSelector(q.Fields)
	if err != nil {
		return nil, err
	}
	var result []unstructured.Unstructured
	for _, o := range d.objects {
		if o.GetKind() != q.Resource.Kind || q.Resource.Namespaced && !NamespaceIncluded(q.Namespace, o.GetNamespace()) || !selector.Matches(labels.Set(o.GetLabels())) {
			continue
		}
		if !fieldSelector.Matches(fields.Set{"metadata.name": o.GetName(), "metadata.namespace": o.GetNamespace(), "spec.nodeName": Text(o, "spec", "nodeName"), "involvedObject.uid": Text(o, "involvedObject", "uid")}) {
			continue
		}
		result = append(result, *o.DeepCopy())
	}
	return result, nil
}
func (d *Demo) Logs(ctx context.Context, ns, pod, container string, previous bool, limits ...LogOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	suffix := ""
	if previous {
		suffix = " (previous instance)"
	}
	options := logRequest(500, limits)
	messages := []string{fmt.Sprintf("INFO starting%s", suffix), "configuration loaded", "listening on :80", "GET /health 200", "GET / 200", `{"level":"info","message":"request completed","request_id":"demo-123","http":{"status":200},"status":200,"duration_ms":3.4}`, "WARN slow request path=/api duration_ms=1200"}
	seconds := []int{0, 1, 2, 5, 8, 9, 10}
	var lines []string
	for i, message := range messages {
		stamp := d.logStart.Add(time.Duration(seconds[i]) * time.Second)
		if options.SinceTime == nil || !stamp.Before(options.SinceTime.Time) {
			lines = append(lines, stamp.Format(time.RFC3339Nano)+" "+message)
		}
	}
	if options.TailLines != nil && *options.TailLines >= 0 && *options.TailLines < int64(len(lines)) {
		lines = lines[len(lines)-int(*options.TailLines):]
	}
	return strings.Join(lines, "\n"), nil
}
func (*Demo) Close() {}

func (d *Demo) Metrics(ctx context.Context, kind, namespace string) (map[string]Usage, error) {
	resource, _ := Resolve(Builtins(), strings.ToLower(kind))
	objects, err := d.List(ctx, Query{Resource: resource, Namespace: namespace})
	if err != nil {
		return nil, err
	}
	usage := map[string]Usage{}
	for index, object := range objects {
		usage[UsageKey(object)] = Usage{int64((index + 1) * 35), int64(index+1) * 24 * 1024 * 1024}
	}
	return usage, nil
}
func (d *Demo) StreamLogs(ctx context.Context, namespace, pod, container string, since *time.Time, limits ...LogOptions) (io.ReadCloser, error) {
	reader, writer := io.Pipe()
	go func() {
		defer writer.Close()
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-ctx.Done():
				writer.CloseWithError(ctx.Err())
			case <-done:
			}
		}()
		if since == nil {
			options := logRequest(200, limits)
			initial := LogOptions{TailLines: options.TailLines}
			if options.SinceTime != nil {
				stamp := options.SinceTime.Time
				initial.SinceTime = &stamp
			}
			text, _ := d.Logs(ctx, namespace, pod, container, false, initial)
			if text != "" {
				if _, err := fmt.Fprintln(writer, text); err != nil {
					return
				}
			}
		}
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case stamp := <-timer.C:
				if _, err := fmt.Fprintf(writer, "%s live heartbeat %s/%s\n", stamp.UTC().Format(time.RFC3339Nano), namespace, pod); err != nil {
					return
				}
			}
		}
	}()
	return reader, nil
}

func (d *Demo) Get(ctx context.Context, r Resource, namespace, name string) (unstructured.Unstructured, error) {
	if ctx.Err() != nil {
		return unstructured.Unstructured{}, ctx.Err()
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, o := range d.objects {
		if o.GetKind() == r.Kind && o.GetNamespace() == namespace && o.GetName() == name {
			return *o.DeepCopy(), nil
		}
	}
	return unstructured.Unstructured{}, apierrors.NewNotFound(r.GVR.GroupResource(), name)
}
func (d *Demo) Patch(ctx context.Context, r Resource, namespace, name string, patch []byte) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var values map[string]any
	if err := json.Unmarshal(patch, &values); err != nil {
		return err
	}
	metadata, _ := values["metadata"].(map[string]any)
	for i, o := range d.objects {
		if o.GetKind() != r.Kind || o.GetNamespace() != namespace || o.GetName() != name {
			continue
		}
		if metadata["resourceVersion"] != o.GetResourceVersion() || metadata["uid"] != string(o.GetUID()) {
			return apierrors.NewConflict(r.GVR.GroupResource(), name, fmt.Errorf("resource changed since editing began"))
		}
		data, _ := json.Marshal(o.Object)
		updated, err := jsonpatch.MergePatch(data, patch)
		if err != nil {
			return err
		}
		var next unstructured.Unstructured
		if err = next.UnmarshalJSON(updated); err != nil {
			return err
		}
		rv, _ := strconv.Atoi(o.GetResourceVersion())
		next.SetResourceVersion(strconv.Itoa(rv + 1))
		d.objects[i] = next
		d.notifyResourcesLocked()
		return nil
	}
	return apierrors.NewNotFound(r.GVR.GroupResource(), name)
}
