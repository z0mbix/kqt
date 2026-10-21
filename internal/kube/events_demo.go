package kube

import (
	"context"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)

func (d *Demo) FollowEvents(ctx context.Context, q Query, emit func(EventUpdate)) error {
	return followNamespaceScope(ctx, q, emit, d.followEvents)
}

func (d *Demo) followEvents(ctx context.Context, q Query, emit func(EventUpdate)) error {
	selector, err := fields.ParseSelector(q.Fields)
	if err != nil {
		return err
	}
	labelSelector, err := labels.Parse(q.Labels)
	if err != nil {
		return err
	}
	uid, restricted := selector.RequiresExactMatch("involvedObject.uid")
	if !restricted {
		uid, restricted = selector.RequiresExactMatch("regarding.uid")
	}
	send := func(update EventUpdate) {
		if ctx.Err() == nil {
			emit(update)
		}
	}
	send(EventUpdate{State: "connecting"})
	rows, err := d.List(ctx, q)
	if err != nil {
		return err
	}
	for _, o := range rows {
		send(EventUpdate{Type: watch.Added, Object: o})
	}
	send(EventUpdate{State: "live", Synced: true})
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case stamp := <-timer.C:
			var updates []unstructured.Unstructured
			d.mu.Lock()
			seen := map[string]bool{}
			for _, target := range d.objects {
				if restricted {
					if string(target.GetUID()) != uid {
						continue
					}
				} else if target.GetKind() != "Pod" || seen[target.GetNamespace()] {
					continue
				}
				if q.Namespace != "" && target.GetNamespace() != q.Namespace {
					continue
				}
				seen[target.GetNamespace()] = true
				namespace := target.GetNamespace()
				if namespace == "" {
					namespace = "default"
				}
				name := "demo-live-" + target.GetName()
				var event *unstructured.Unstructured
				for i := range d.objects {
					if d.objects[i].GetKind() == "Event" && d.objects[i].GetNamespace() == namespace && d.objects[i].GetName() == name {
						event = &d.objects[i]
						break
					}
				}
				if event == nil {
					o := unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Event", "metadata": map[string]interface{}{"name": name, "namespace": target.GetNamespace()}, "type": "Normal", "reason": "DemoHeartbeat", "message": "demo event heartbeat", "involvedObject": map[string]interface{}{"kind": target.GetKind(), "name": target.GetName(), "namespace": target.GetNamespace(), "uid": string(target.GetUID())}}}
					o.SetNamespace(namespace)
					o.SetUID(types.UID(namespace + "/" + name))
					o.SetCreationTimestamp(metav1.NewTime(stamp))
					d.objects = append(d.objects, o)
					event = &d.objects[len(d.objects)-1]
				}
				count := Number(*event, "count") + 1
				event.Object["count"] = count
				event.Object["lastTimestamp"] = stamp.UTC().Format(time.RFC3339Nano)
				event.SetResourceVersion(strconv.FormatInt(count, 10))
				if labelSelector.Matches(labels.Set(event.GetLabels())) {
					updates = append(updates, *event.DeepCopy())
				}
			}
			d.mu.Unlock()
			for _, o := range updates {
				send(EventUpdate{Type: watch.Modified, Object: o})
			}
		}
	}
}

var _ EventWatcher = (*Demo)(nil)
