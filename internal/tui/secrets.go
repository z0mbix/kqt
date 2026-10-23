package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type secretReadyMsg struct {
	generation, token uint64
	object            unstructured.Unstructured
	err               error
}

func (m *model) clearSecret() {
	m.resetInspectorSearch()
	m.secretToken++
	if m.secretCancel != nil {
		m.secretCancel()
		m.secretCancel = nil
	}
	m.secretTarget = ""
	m.secretObject = nil
}

func (m *model) hideChangedSecret(o unstructured.Unstructured) {
	if m.secretTarget == "" || o.GetKind() != "Secret" || kube.Identity(o) != m.secretTarget {
		return
	}
	previous := m.secretObject
	if previous == nil {
		// A refresh can arrive while the explicit reveal request is pending.
		if selected, ok := m.current(); ok && kube.Identity(selected) == m.secretTarget {
			previous = &selected
		}
	}
	if previous != nil && o.GetResourceVersion() != previous.GetResourceVersion() {
		m.clearSecret()
		m.notice = "Secret changed; values hidden. Press x to reveal again"
	}
}

func (m *model) manifest(o unstructured.Unstructured, asJSON bool) string {
	if o.GetKind() == "Secret" && m.secretObject != nil && kube.Identity(o) == m.secretTarget {
		return kube.DecodedSecretManifest(*m.secretObject, asJSON)
	}
	return kube.Manifest(o, asJSON)
}

func (m *model) secretLabel() string {
	if m.secretTarget == "" {
		return "values hidden · x reveal"
	}
	if m.secretObject == nil {
		return "loading values · x cancel"
	}
	return "values visible · decoded · x hide"
}

func (m *model) toggleSecret() tea.Cmd {
	o, ok := m.current()
	if !ok || o.GetKind() != "Secret" || m.backend == nil {
		return nil
	}
	if m.secretTarget != "" {
		m.clearSecret()
		m.updateInspector()
		return nil
	}
	reader, ok := m.backend.(kube.ResourceGetter)
	if !ok {
		m.notice = "This backend cannot read individual Secrets."
		return nil
	}
	m.notice = ""
	m.secretToken++
	m.secretTarget = kube.Identity(o)
	ctx, cancel := context.WithTimeout(m.session.ctx, 15*time.Second)
	m.secretCancel = cancel
	if m.mode != "details" && m.inspectorDimensions().width == 0 {
		m.openDetails(true, false)
	}
	m.updateInspector()
	g, token, resource := m.generation, m.secretToken, m.route.resource
	return func() tea.Msg {
		defer cancel()
		fresh, err := reader.Get(ctx, resource, o.GetNamespace(), o.GetName())
		if err == nil && (fresh.GetKind() != "Secret" || fresh.GetNamespace() != o.GetNamespace() || fresh.GetName() != o.GetName() || fresh.GetUID() != o.GetUID()) {
			err = fmt.Errorf("selected Secret was replaced; refresh and select it again")
		}
		return secretReadyMsg{g, token, fresh, err}
	}
}

func (m *model) secretReady(v secretReadyMsg) {
	o, ok := m.current()
	if v.generation != m.generation || v.token != m.secretToken || m.secretTarget == "" || !ok || o.GetKind() != "Secret" || kube.Identity(o) != m.secretTarget {
		return
	}
	if m.secretCancel != nil {
		m.secretCancel()
		m.secretCancel = nil
	}
	if v.err != nil {
		m.clearSecret()
		m.notice = "Unable to reveal Secret: " + kube.Safe(v.err.Error())
	} else {
		m.secretObject = v.object.DeepCopy()
	}
	m.updateInspector()
}
