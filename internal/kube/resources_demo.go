package kube

import (
	"context"
	"reflect"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
)

func (d *Demo) notifyResourcesLocked() {
	for _, channel := range d.resourceListeners {
		select {
		case channel <- struct{}{}:
		default:
		}
	}
}
func (d *Demo) FollowResources(ctx context.Context, q Query, emit func(EventUpdate)) error {
	return followNamespaceScope(ctx, q, emit, d.followResources)
}

func (d *Demo) followResources(ctx context.Context, q Query, emit func(EventUpdate)) error {
	d.mu.Lock()
	if d.resourceListeners == nil {
		d.resourceListeners = map[uint64]chan struct{}{}
	}
	d.resourceListenerID++
	id := d.resourceListenerID
	notify := make(chan struct{}, 1)
	d.resourceListeners[id] = notify
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.resourceListeners, id); d.mu.Unlock() }()
	previous := map[string]unstructured.Unstructured{}
	initial := true
	for ctx.Err() == nil {
		items, err := d.List(ctx, q)
		if err != nil {
			return err
		}
		next := map[string]unstructured.Unstructured{}
		if initial {
			emit(EventUpdate{State: "listing", Reset: true})
		}
		for _, o := range items {
			key := Identity(o)
			next[key] = o
			old, exists := previous[key]
			if !exists {
				emit(EventUpdate{Type: watch.Added, Object: o})
			} else if !reflect.DeepEqual(old.Object, o.Object) {
				emit(EventUpdate{Type: watch.Modified, Object: o})
			}
		}
		for key, o := range previous {
			if _, exists := next[key]; !exists {
				emit(EventUpdate{Type: watch.Deleted, Object: o})
			}
		}
		if initial {
			emit(EventUpdate{State: "live", Synced: true})
			initial = false
		}
		previous = next
		select {
		case <-ctx.Done():
			return nil
		case <-notify:
		}
	}
	return nil
}
