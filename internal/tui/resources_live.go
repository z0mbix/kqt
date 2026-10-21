package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
)

type resourceFeed struct {
	changes chan kube.EventUpdate
	err     error
}
type resourceBatchMsg struct {
	generation, token uint64
	feed              *resourceFeed
	changes           []kube.EventUpdate
	done              bool
	err               error
}

func waitResources(ctx context.Context, g, token uint64, f *resourceFeed) tea.Cmd {
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case change, open := <-f.changes:
			message := resourceBatchMsg{generation: g, token: token, feed: f}
			if !open {
				message.done = true
				message.err = f.err
				return message
			}
			message.changes = append(message.changes, change)
			for len(message.changes) < 64 {
				select {
				case next, open := <-f.changes:
					if !open {
						message.done = true
						message.err = f.err
						return message
					}
					message.changes = append(message.changes, next)
				default:
					return message
				}
			}
			return message
		}
	}
}
func (m *model) stopResourceWatch() {
	m.resourceToken++
	if m.resourceCancel != nil {
		m.resourceCancel()
		m.resourceCancel = nil
	}
	m.resourceFeed = nil
	m.resourceContext = nil
	m.resourceCache = nil
	m.resourceStaging = nil
	m.resourceNamespaceStaging = nil
	m.resourceWatchState = ""
}
func (m *model) startResourceWatch() tea.Cmd {
	watcher, ok := m.backend.(kube.ResourceWatcher)
	if !ok || m.resourceWatchError != "" || m.route.resource.Kind == "Event" {
		return nil
	}
	m.stopResourceWatch()
	ctx, cancel := context.WithCancel(m.session.ctx)
	m.resourceCancel = cancel
	m.resourceContext = ctx
	f := &resourceFeed{changes: make(chan kube.EventUpdate, 64)}
	m.resourceFeed = f
	m.resourceWatchState = "connecting"
	m.resourceCache = map[string]unstructured.Unstructured{}
	m.resourceNamespaceStaging = map[string]map[string]unstructured.Unstructured{}
	for _, o := range m.rows {
		m.resourceCache[kube.Identity(o)] = o
	}
	q := kube.Query{Resource: m.route.resource, Namespace: m.route.namespace, Labels: m.route.labels, Fields: m.route.fields}
	go func() {
		f.err = watcher.FollowResources(ctx, q, func(change kube.EventUpdate) {
			if change.Type != "" {
				change.Object = kube.Redacted(change.Object)
			}
			select {
			case f.changes <- change:
			case <-ctx.Done():
			}
		})
		close(f.changes)
	}()
	return waitResources(ctx, m.generation, m.resourceToken, f)
}
func (m *model) refreshResources() tea.Cmd {
	m.stopResourceWatch()
	m.resourceWatchError = ""
	if m.requestCancel != nil {
		m.requestCancel()
	}
	m.request++
	m.busy = false
	return m.fetch()
}
func (m *model) resourceBatch(v resourceBatchMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.resourceToken || v.feed != m.resourceFeed {
		return nil
	}
	changed := false
	for _, update := range v.changes {
		if update.ScopeStatus && (update.Message != "" || update.Synced) {
			m.namespaceWarning = kube.Safe(update.Message)
		}
		if update.Namespace != "" {
			changed = m.resourceNamespaceChange(update) || changed
			continue
		}
		if update.Reset {
			m.resourceStaging = map[string]unstructured.Unstructured{}
		}
		if update.State != "" {
			m.resourceWatchState = update.State
		}
		if update.Message != "" && !update.ScopeStatus {
			m.dataErr = update.Message
		}
		cache := m.resourceCache
		if m.resourceStaging != nil {
			cache = m.resourceStaging
		}
		if update.Type != "" && update.Type != watch.Bookmark {
			key := kube.Identity(update.Object)
			m.hideChangedSecret(update.Object)
			if update.Type == watch.Deleted {
				delete(cache, key)
			} else {
				cache[key] = update.Object
			}
			if m.resourceStaging == nil {
				changed = true
			}
		}
		if update.Synced {
			if m.resourceStaging != nil {
				m.resourceCache = m.resourceStaging
				m.resourceStaging = nil
				changed = true
			}
			m.dataErr = ""
			m.lastRefresh = time.Now()
		}
	}
	if changed {
		key := m.route.selection
		if o, ok := m.current(); ok {
			key = kube.Identity(o)
		}
		m.rows = nil
		for _, o := range m.resourceCache {
			m.rows = append(m.rows, o)
		}
		m.lastRefresh = time.Now()
		m.dataErr = ""
		m.rebuild(key)
	}
	if v.done {
		m.stopResourceWatch()
		if v.err != nil {
			m.resourceWatchError = kube.Safe(v.err.Error())
		} else {
			m.resourceWatchError = "watch ended"
		}
		m.notice = "Polling resources: " + m.resourceWatchError
		return m.poll()
	}
	return waitResources(m.session.ctx, v.generation, v.token, v.feed)
}

func (m *model) resourceNamespaceChange(update kube.EventUpdate) bool {
	ns := update.Namespace
	if !kube.NamespaceIncluded(m.route.namespace, ns) {
		return false
	}
	if update.Reset {
		if m.resourceNamespaceStaging == nil {
			m.resourceNamespaceStaging = map[string]map[string]unstructured.Unstructured{}
		}
		m.resourceNamespaceStaging[ns] = map[string]unstructured.Unstructured{}
	}
	cache := m.resourceCache
	stage, staging := m.resourceNamespaceStaging[ns]
	if staging {
		cache = stage
	}
	changed := false
	if update.Type != "" && update.Type != watch.Bookmark && update.Object.GetNamespace() == ns {
		m.hideChangedSecret(update.Object)
		key := kube.Identity(update.Object)
		if update.Type == watch.Deleted {
			delete(cache, key)
		} else {
			cache[key] = update.Object
		}
		changed = !staging
	}
	if update.Synced && staging {
		for key, object := range m.resourceCache {
			if object.GetNamespace() == ns {
				delete(m.resourceCache, key)
			}
		}
		for key, object := range stage {
			m.resourceCache[key] = object
		}
		delete(m.resourceNamespaceStaging, ns)
		changed = true
	}
	return changed
}

type resourceMetricsMsg struct {
	generation, token uint64
	usage             map[string]kube.Usage
	err               error
}

func (m *model) resourceMetrics() tea.Cmd {
	if !m.hasMetrics() || (m.route.resource.Kind != "Pod" && m.route.resource.Kind != "Node") {
		return m.poll()
	}
	reader, ok := m.backend.(kube.MetricsReader)
	if !ok {
		return m.poll()
	}
	g, token, kind, ns := m.generation, m.resourceToken, m.route.resource.Kind, m.route.namespace
	parent := m.resourceContext
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 3*time.Second)
		defer cancel()
		usage, err := reader.Metrics(ctx, kind, ns)
		return resourceMetricsMsg{g, token, usage, err}
	}
}
func (m *model) resourceMetricsReady(v resourceMetricsMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.resourceToken || m.resourceFeed == nil {
		return nil
	}
	m.metricsWarning = ""
	m.usage = v.usage
	if v.err != nil {
		m.metricsWarning = kube.Safe(v.err.Error())
	} else if len(m.usage) == 0 {
		m.metricsWarning = "Metrics API is available; waiting for samples"
	}
	key := ""
	if o, ok := m.current(); ok {
		key = kube.Identity(o)
	}
	m.rebuild(key)
	return m.poll()
}
func (m *model) resourceWatchLabel() string {
	if m.resourceFeed != nil {
		if m.resourceWatchState == "live" {
			return "live"
		}
		return "watch · " + m.resourceWatchState
	}
	if m.resourceWatchError != "" {
		return fmt.Sprintf("polling · watch unavailable: %s", m.resourceWatchError)
	}
	return "polling"
}
