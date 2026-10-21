package kube

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

type ResourceWatcher interface {
	FollowResources(context.Context, Query, func(EventUpdate)) error
}

type EventWatcher interface {
	FollowEvents(context.Context, Query, func(EventUpdate)) error
}

// State updates accompany object changes. Synced means the initial list has
// completed; Gap labels an expired history rather than promising replay.
type EventUpdate struct {
	Type        watch.EventType
	Object      unstructured.Unstructured
	State       string
	Message     string
	Synced      bool
	Reset       bool
	Gap         bool
	Namespace   string // Namespace-specific list/reset checkpoint in a combined watch.
	ScopeStatus bool   // Message describes combined namespace availability.
}

func (c *Client) FollowEvents(ctx context.Context, q Query, emit func(EventUpdate)) error {
	if q.Resource.Kind != "Event" {
		return fmt.Errorf("event watches require an Event resource")
	}
	return followNamespaceScope(ctx, q, emit, c.followObjects)
}
func (c *Client) FollowResources(ctx context.Context, q Query, emit func(EventUpdate)) error {
	return followNamespaceScope(ctx, q, emit, c.followObjects)
}
func (c *Client) followObjects(ctx context.Context, q Query, emit func(EventUpdate)) error {
	selector, err := fields.ParseSelector(q.Fields)
	if err != nil {
		return err
	}
	labelSelector, err := labels.Parse(q.Labels)
	if err != nil {
		return err
	}
	var resource dynamic.ResourceInterface = c.streamDynamic.Resource(q.Resource.GVR)
	if q.Resource.Namespaced {
		resource = c.streamDynamic.Resource(q.Resource.GVR).Namespace(q.Namespace)
	} else {
		q.Namespace = ""
	}
	var version string
	synced := false
	backoff := 250 * time.Millisecond
	send := func(update EventUpdate) {
		if ctx.Err() == nil {
			if update.Type != "" && update.Type != watch.Bookmark {
				if update.Object.GetKind() == "" {
					update.Object.SetKind(q.Resource.Kind)
				}
				if update.Object.GetAPIVersion() == "" {
					update.Object.SetAPIVersion(q.Resource.GVR.GroupVersion().String())
				}
			}
			emit(update)
		}
	}
	matches := func(o unstructured.Unstructured) bool {
		if !labelSelector.Matches(labels.Set(o.GetLabels())) {
			return false
		}
		for _, field := range []string{"metadata.name", "metadata.namespace", "spec.nodeName"} {
			if wanted, restricted := selector.RequiresExactMatch(field); restricted {
				value := ""
				switch field {
				case "metadata.name":
					value = o.GetName()
				case "metadata.namespace":
					value = o.GetNamespace()
				case "spec.nodeName":
					value = Text(o, "spec", "nodeName")
				}
				if value != wanted {
					return false
				}
			}
		}
		if q.Namespace != "" && o.GetNamespace() != q.Namespace {
			return false
		}
		for _, field := range []string{"involvedObject.uid", "regarding.uid"} {
			if uid, restricted := selector.RequiresExactMatch(field); restricted && EventSubject(o, "uid") != uid {
				return false
			}
		}
		return true
	}
	send(EventUpdate{State: "connecting"})
	for ctx.Err() == nil {
		if version == "" {
			send(EventUpdate{State: "listing", Reset: true})
			request, cancel := context.WithTimeout(ctx, 15*time.Second)
			options := metav1.ListOptions{Limit: 500, FieldSelector: q.Fields, LabelSelector: q.Labels}
			var listVersion string
			for {
				var list *unstructured.UnstructuredList
				list, err = resource.List(request, options)
				if err != nil {
					break
				}
				if listVersion == "" {
					listVersion = list.GetResourceVersion()
				} else if listVersion != list.GetResourceVersion() {
					err = fmt.Errorf("resource list changed resource version between pages")
					break
				}
				for _, o := range list.Items {
					if matches(o) {
						send(EventUpdate{Type: watch.Added, Object: o})
					}
				}
				options.Continue = list.GetContinue()
				if options.Continue == "" {
					break
				}
			}
			cancel()
			if err == nil && listVersion == "" {
				err = fmt.Errorf("resource list did not report a resource version")
			}
			if err == nil {
				version, synced = listVersion, true
				send(EventUpdate{State: "connecting", Synced: true})
			}
		}
		if err == nil {
			// A server/client timeout renews healthy watches, while cancelling the
			// view interrupts both establishment and an idle response body.
			timeout := int64(60)
			request, cancel := context.WithTimeout(ctx, 75*time.Second)
			// Bound establishment separately; an idle, established watch can
			// outlive the ordinary request timeout.
			handshake := time.AfterFunc(15*time.Second, cancel)
			var stream watch.Interface
			started := time.Now()
			healthy := false
			stream, err = resource.Watch(request, metav1.ListOptions{ResourceVersion: version, AllowWatchBookmarks: true, TimeoutSeconds: &timeout, FieldSelector: q.Fields, LabelSelector: q.Labels})
			handshake.Stop()
			if err == nil {
				send(EventUpdate{State: "live", Synced: synced})
				err = consumeEventWatch(request, stream, matches, func(update EventUpdate) {
					healthy = true
					if rv := update.Object.GetResourceVersion(); rv != "" {
						version = rv
					}
					if update.Type != watch.Bookmark {
						send(update)
					}
				})
				// Abort the HTTP read before closing its decoder. The decoder
				// may already be waiting for the next frame after a watch error.
				cancel()
				stream.Stop()
				if healthy || time.Since(started) > 10*time.Second {
					backoff = 250 * time.Millisecond
				}
			}
			cancel()
		}
		if ctx.Err() != nil {
			return nil
		}
		if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || apierrors.IsBadRequest(err) || apierrors.IsNotFound(err) || apierrors.IsMethodNotSupported(err) {
			return err
		}
		gap := apierrors.IsResourceExpired(err) || apierrors.IsGone(err)
		message := ""
		if err != nil {
			message = Safe(err.Error())
		}
		if gap {
			version = ""
			message = "Watch history expired; reloading. Some changes may have been missed."
		}
		send(EventUpdate{State: "reconnecting", Message: message, Gap: gap && synced})
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff = min(10*time.Second, backoff*2)
		err = nil
	}
	return nil
}

func consumeEventWatch(ctx context.Context, stream watch.Interface, matches func(unstructured.Unstructured) bool, emit func(EventUpdate)) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case change, open := <-stream.ResultChan():
			if !open {
				return nil
			}
			if change.Type == watch.Error {
				return apierrors.FromObject(change.Object)
			}
			o, ok := change.Object.(*unstructured.Unstructured)
			if !ok {
				return fmt.Errorf("unexpected event watch object %T", change.Object)
			}
			if change.Type != watch.Added && change.Type != watch.Modified && change.Type != watch.Deleted && change.Type != watch.Bookmark {
				return fmt.Errorf("unexpected event watch change %q", change.Type)
			}
			// Advance the stream checkpoint even when a server returns an
			// unrelated object. The UI receives only objects within the scope.
			if change.Type == watch.Bookmark || !matches(*o) {
				emit(EventUpdate{Type: watch.Bookmark, Object: *o})
			} else {
				emit(EventUpdate{Type: change.Type, Object: *o})
			}
		}
	}
}

func EventSubject(o unstructured.Unstructured, field string) string {
	if value := Text(o, "involvedObject", field); value != "" {
		return value
	}
	return Text(o, "regarding", field)
}

func EventMessage(o unstructured.Unstructured) string {
	if value := Text(o, "message"); value != "" {
		return value
	}
	return Text(o, "note")
}

func EventCount(o unstructured.Unstructured) int64 {
	if count := Number(o, "series", "count"); count > 0 {
		return count
	}
	return max(1, Number(o, "count"), Number(o, "deprecatedCount"))
}
