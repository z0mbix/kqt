package kube

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Overview struct {
	Namespace           string
	Updated             time.Time
	Objects             map[string][]unstructured.Unstructured
	Errors              map[string]string
	PodUsage, NodeUsage map[string]Usage
}

func ReadOverview(ctx context.Context, b Backend, namespace string) Overview {
	result := Overview{Namespace: namespace, Objects: map[string][]unstructured.Unstructured{}, Errors: map[string]string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	launch := func(work func(context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			request, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			work(request)
		}()
	}
	for _, name := range []string{"nodes", "pods", "deployments", "statefulsets", "daemonsets", "jobs", "events"} {
		launch(func(request context.Context) {
			r, _ := Resolve(Builtins(), name)
			ns := namespace
			if !r.Namespaced {
				ns = ""
			}
			objects, err := b.List(request, Query{Resource: r, Namespace: ns})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Errors[name] = Safe(err.Error())
			}
			if err == nil || PartialNamespaces(err) {
				result.Objects[name] = objects
			}
		})
	}
	if reader, ok := b.(MetricsReader); ok {
		for _, kind := range []string{"Pod", "Node"} {
			launch(func(request context.Context) {
				ns := namespace
				if kind == "Node" {
					ns = ""
				}
				usage, err := reader.Metrics(request, kind, ns)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					result.Errors[kind+" metrics"] = Safe(err.Error())
				}
				if kind == "Pod" {
					result.PodUsage = usage
				} else {
					result.NodeUsage = usage
				}
			})
		}
	} else {
		mu.Lock()
		result.Errors["Pod metrics"] = "Metrics API unavailable"
		result.Errors["Node metrics"] = "Metrics API unavailable"
		mu.Unlock()
	}
	wg.Wait()
	for _, name := range []string{"nodes", "pods", "deployments", "statefulsets", "daemonsets", "jobs", "events"} {
		if _, exists := result.Objects[name]; !exists && result.Errors[name] == "" {
			result.Errors[name] = "Refresh cancelled before this section completed"
		}
	}
	result.Updated = time.Now()
	if ctx.Err() != nil {
		result.Errors["refresh"] = ctx.Err().Error()
	}
	return result
}
func WorkloadStatus(o unstructured.Unstructured) string {
	state := Status(o)
	if o.GetKind() == "Node" {
		for _, value := range Slice(o, "status", "conditions") {
			c, _ := value.(map[string]interface{})
			if c["status"] == "True" && slices.Contains([]string{"MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable"}, fmt.Sprint(c["type"])) {
				return fmt.Sprint(c["type"])
			}
		}
	}
	if o.GetKind() == "Deployment" {
		for _, value := range Slice(o, "status", "conditions") {
			c, _ := value.(map[string]interface{})
			if c["type"] == "Progressing" && c["status"] == "False" {
				return "Rollout blocked"
			}
		}
		desired := Number(o, "spec", "replicas")
		if _, present, _ := unstructured.NestedFieldNoCopy(o.Object, "spec", "replicas"); !present {
			desired = 1
		}
		if Number(o, "status", "updatedReplicas") < desired || Number(o, "status", "availableReplicas") < desired || Number(o, "status", "replicas") > desired {
			return "Progressing"
		}
	}
	return state
}
func (o Overview) Problems() []Relation {
	var links []Relation
	for name, objects := range o.Objects {
		if name == "events" {
			continue
		}
		r, _ := Resolve(Builtins(), name)
		for _, object := range objects {
			state := WorkloadStatus(object)
			if state == "Ready" || state == "Running" || state == "Succeeded" {
				continue
			}
			links = append(links, Relation{Resource: r, Name: object.GetName(), Namespace: object.GetNamespace(), UID: object.GetUID(), Reason: state})
		}
	}
	slices.SortFunc(links, func(a, b Relation) int { return strings.Compare(a.Title(), b.Title()) })
	return links
}
func (o Overview) Warnings() []unstructured.Unstructured {
	var events []unstructured.Unstructured
	for _, event := range o.Objects["events"] {
		if Text(event, "type") == "Warning" {
			events = append(events, event)
		}
	}
	slices.SortFunc(events, func(a, b unstructured.Unstructured) int { return EventTime(b).Compare(EventTime(a)) })
	return events[:min(10, len(events))]
}
func (o Overview) Capacity() (cpu, memory int64, reported int) {
	for _, node := range o.Objects["nodes"] {
		c, errC := resource.ParseQuantity(Text(node, "status", "allocatable", "cpu"))
		m, errM := resource.ParseQuantity(Text(node, "status", "allocatable", "memory"))
		if errC != nil || errM != nil {
			continue
		}
		cpu += c.MilliValue()
		memory += m.Value()
		reported++
	}
	return
}
func (o Overview) Health(name string) (healthy, total int) {
	for _, object := range o.Objects[name] {
		total++
		state := WorkloadStatus(object)
		if state == "Ready" || state == "Running" || state == "Succeeded" {
			healthy++
		}
	}
	return
}
func (o Overview) UsageTotals(nodes bool) (Usage, int) {
	var total Usage
	count := 0
	kind := "pods"
	usage := o.PodUsage
	if nodes {
		kind = "nodes"
		usage = o.NodeUsage
	}
	for _, object := range o.Objects[kind] {
		if sample, ok := usage[UsageKey(object)]; ok {
			total.CPUMilli += sample.CPUMilli
			total.MemoryBytes += sample.MemoryBytes
			count++
		}
	}
	return total, count
}
func (o Overview) Scope() string {
	if o.Namespace == "" {
		return "all namespaces"
	}
	return o.Namespace
}
func (o Overview) Summary(name string) string {
	if err := o.Errors[name]; err != "" {
		if _, available := o.Objects[name]; !available {
			return "Unavailable: " + err
		}
		healthy, total := o.Health(name)
		return fmt.Sprintf("%d healthy / %d total · %s", healthy, total, err)
	}
	healthy, total := o.Health(name)
	return fmt.Sprintf("%d healthy / %d total", healthy, total)
}
