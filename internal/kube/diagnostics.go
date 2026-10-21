package kube

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type Usage struct{ CPUMilli, MemoryBytes int64 }
type MetricsReader interface {
	Metrics(context.Context, string, string) (map[string]Usage, error)
}

func UsageKey(o unstructured.Unstructured) string { return o.GetNamespace() + "/" + o.GetName() }

func (c *Client) Metrics(ctx context.Context, kind, namespace string) (map[string]Usage, error) {
	if kind == "Pod" {
		names, err := NamespaceNames(namespace)
		if err != nil {
			return nil, err
		}
		if len(names) > 1 {
			return metricsNamespaces(ctx, kind, names, c)
		}
		namespace = strings.Join(names, ",")
	}
	name := "pods"
	if kind == "Node" {
		name = "nodes"
		namespace = ""
	} else if kind != "Pod" {
		return nil, fmt.Errorf("metrics are available for pods and nodes")
	}
	var objects []unstructured.Unstructured
	var err error
	for _, version := range []string{"v1", "v1beta1"} {
		objects, err = c.List(ctx, Query{Resource: Resource{GVR: schema.GroupVersionResource{Group: "metrics.k8s.io", Version: version, Resource: name}, Kind: kind + "Metrics", Namespaced: kind == "Pod"}, Namespace: namespace})
		if err == nil || !apierrors.IsNotFound(err) {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("Metrics API unavailable: %w", err)
	}
	result := map[string]Usage{}
	for _, object := range objects {
		var usage Usage
		if kind == "Node" {
			values, _, _ := unstructured.NestedStringMap(object.Object, "usage")
			usage = parseUsage(values)
		} else {
			for _, value := range Slice(object, "containers") {
				container, _ := value.(map[string]interface{})
				values, _ := container["usage"].(map[string]interface{})
				usage.CPUMilli += quantity(fmt.Sprint(values["cpu"]), true)
				usage.MemoryBytes += quantity(fmt.Sprint(values["memory"]), false)
			}
		}
		result[UsageKey(object)] = usage
	}
	return result, nil
}
func quantity(value string, cpu bool) int64 {
	q, err := resource.ParseQuantity(value)
	if err != nil {
		return 0
	}
	if cpu {
		return q.MilliValue()
	}
	return q.Value()
}
func parseUsage(values map[string]string) Usage {
	return Usage{quantity(values["cpu"], true), quantity(values["memory"], false)}
}

// Events are selected by immutable UID, including for cluster-scoped resources.
func ResourceEvents(ctx context.Context, b Backend, target unstructured.Unstructured) (string, error) {
	events, err := EventsForResource(ctx, b, target)
	if err != nil {
		return "", err
	}
	return resourceEventText(target, events), nil
}

func EventsForResource(ctx context.Context, b Backend, target unstructured.Unstructured) ([]unstructured.Unstructured, error) {
	if target.GetUID() == "" {
		return nil, fmt.Errorf("resource has no UID; refresh before reading its events")
	}
	r, _ := Resolve(Builtins(), "events")
	events, err := b.List(ctx, Query{Resource: r, Namespace: target.GetNamespace(), Fields: "involvedObject.uid=" + string(target.GetUID())})
	if err != nil {
		return nil, err
	}
	events = slices.Clone(events)
	events = slices.DeleteFunc(events, func(event unstructured.Unstructured) bool {
		return Text(event, "involvedObject", "uid") != string(target.GetUID())
	})
	slices.SortFunc(events, func(a, b unstructured.Unstructured) int { return eventTime(b).Compare(eventTime(a)) })
	return events, nil
}

func resourceEventText(target unstructured.Unstructured, events []unstructured.Unstructured) string {
	var text strings.Builder
	fmt.Fprintf(&text, "%s · %s/%s\n\n", Safe(target.GetKind()), Safe(target.GetNamespace()), Safe(target.GetName()))
	for _, value := range Slice(target, "status", "conditions") {
		c, _ := value.(map[string]interface{})
		fmt.Fprintf(&text, "%v: %v · %v · %v\n", c["type"], c["status"], empty(c["reason"]), empty(c["message"]))
	}
	text.WriteString("\nEvents (newest first)\n\n")
	count := 0
	for _, event := range events {
		// Also enforce the UID locally, including with backends that ignore selectors.
		if Text(event, "involvedObject", "uid") != string(target.GetUID()) {
			continue
		}
		count++
		timestamp := eventTime(event).Format(time.RFC3339)
		repeats := Number(event, "count")
		if series := Number(event, "series", "count"); series > 0 {
			repeats = series
		}
		if repeats == 0 {
			repeats = 1
		}
		fmt.Fprintf(&text, "%s  %s  %s  ×%d\n%s\n\n", timestamp, Text(event, "type"), Text(event, "reason"), repeats, Text(event, "message"))
	}
	if count == 0 {
		text.WriteString("No events for this resource. Kubernetes retains events for a limited time.\n")
	}
	return SafeLines(text.String())
}

func EventTime(event unstructured.Unstructured) time.Time { return eventTime(event) }
func empty(v interface{}) interface{} {
	if v == nil {
		return ""
	}
	return v
}
func eventTime(event unstructured.Unstructured) time.Time {
	for _, path := range [][]string{{"series", "lastObservedTime"}, {"lastTimestamp"}, {"deprecatedLastTimestamp"}, {"eventTime"}, {"metadata", "creationTimestamp"}} {
		if value, err := time.Parse(time.RFC3339Nano, Text(event, path...)); err == nil {
			return value
		}
	}
	return time.Time{}
}
