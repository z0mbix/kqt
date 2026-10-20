package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func TestStatusConditionsRespectPredicatePolarityAndCustomUnknowns(t *testing.T) {
	for _, tc := range []struct{ kind, name, state, reason, colour string }{
		{"Pod", "Ready", "True", "", "success"}, {"Pod", "Ready", "False", "", "warning"},
		{"Node", "DiskPressure", "True", "", "error"}, {"Node", "DiskPressure", "False", "", "success"},
		{"Node", "MemoryPressure", "Unknown", "", "warning"}, {"Job", "Failed", "True", "", "error"},
		{"Deployment", "Progressing", "True", "ReplicaSetUpdated", "warning"},
		{"Deployment", "Progressing", "True", "NewReplicaSetAvailable", "success"},
		{"Deployment", "Progressing", "False", "ProgressDeadlineExceeded", "error"},
		{"Widget", "VendorPredicate", "False", "", "focus"}, {"Widget", "VendorPredicate", "True", "", "focus"},
	} {
		if got := conditionColour(tc.kind, tc.name, tc.state, tc.reason); got != tc.colour {
			t.Errorf("%s/%s=%s: got %s, want %s", tc.kind, tc.name, tc.state, got, tc.colour)
		}
	}
}

func TestReadablePodStatusIncludesContainersConditionsAndOrderedEvents(t *testing.T) {
	m := demoModel(t)
	m.selected = 4
	o, _ := m.current()
	o = *o.DeepCopy()
	o.SetGeneration(3)
	o.Object["status"].(map[string]interface{})["conditions"] = []interface{}{map[string]interface{}{
		"type": "Ready", "status": "False", "reason": "ContainersNotReady", "message": "web is restarting", "lastTransitionTime": "2026-10-13T14:00:00Z", "observedGeneration": int64(2),
	}}
	app := o.Object["status"].(map[string]interface{})["containerStatuses"].([]interface{})[0].(map[string]interface{})
	app["lastState"] = map[string]interface{}{"terminated": map[string]interface{}{"reason": "OOMKilled", "exitCode": int64(137), "finishedAt": "2026-10-13T13:59:00Z"}}
	o.Object["spec"].(map[string]interface{})["initContainers"] = []interface{}{map[string]interface{}{"name": "setup"}}
	o.Object["status"].(map[string]interface{})["initContainerStatuses"] = []interface{}{map[string]interface{}{"name": "setup", "state": map[string]interface{}{"terminated": map[string]interface{}{"exitCode": int64(0), "reason": "Completed"}}, "ready": false, "restartCount": int64(0)}}
	o.Object["spec"].(map[string]interface{})["ephemeralContainers"] = []interface{}{map[string]interface{}{"name": "debugger"}}
	before := o.DeepCopy()
	event := unstructured.Unstructured{Object: map[string]interface{}{"type": "Warning", "reason": "BackOff", "message": "Back-off restarting web", "lastTimestamp": "2026-10-13T14:00:01Z", "count": int64(9), "series": map[string]interface{}{"count": int64(3)}}}
	styled := m.renderResourceStatus(o, []unstructured.Unstructured{event}, nil)
	plain := ansi.Strip(styled)
	for _, text := range []string{"CrashLoopBackOff", "Containers", "web (app)", "4 restarts", "OOMKilled", "code 137", "setup (init)", "Completed", "debugger (ephemeral)", "Waiting for container status", "Conditions", "ContainersNotReady", "web is restarting", "stale (generation 2/3)", "Changed 2026-10-13T14:00:00Z", "Events (newest first)", "Back-off restarting web", "×3"} {
		if !strings.Contains(plain, text) {
			t.Errorf("status view is missing %q:\n%s", text, plain)
		}
	}
	if strings.Index(plain, "Conditions") > strings.Index(plain, "Events (newest first)") || !strings.Contains(styled, m.theme.style("error").Render("Waiting · CrashLoopBackOff")) {
		t.Fatal("status order or container failure colours were lost")
	}
	if !reflect.DeepEqual(o.Object, before.Object) {
		t.Fatal("rendering modified the resource")
	}
}

func TestStatusSummaryWorkloadsAndGenericFields(t *testing.T) {
	m := demoModel(t)
	m.Update(m.changeResource("deployments", false)())
	o, _ := m.current()
	if state, _ := m.resourceState(o); state != "Ready" {
		t.Fatalf("ready Deployment is %s", state)
	}
	o = *o.DeepCopy()
	o.Object["status"].(map[string]interface{})["updatedReplicas"] = int64(2)
	if state, _ := m.resourceState(o); state != "Progressing" {
		t.Fatal("an incomplete rollout was presented as ready")
	}
	o.Object["status"].(map[string]interface{})["conditions"] = []interface{}{map[string]interface{}{"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded"}}
	if state, colour := m.resourceState(o); state != "Rollout blocked" || colour != "error" {
		t.Fatal("failed rollout was not distinguished")
	}
	widget := unstructured.Unstructured{Object: map[string]interface{}{"kind": "Widget", "metadata": map[string]interface{}{"name": "custom"}, "status": map[string]interface{}{"phase": "Reconciling", "customField": "vendor detail", "attempts": int64(3), "conditions": []interface{}{map[string]interface{}{"type": "VendorPredicate", "status": "True", "vendorField": "useful detail"}}}}}
	before := widget.DeepCopy()
	text := ansi.Strip(m.renderResourceStatus(widget, nil, nil))
	if !strings.Contains(text, "Reported status fields") || !strings.Contains(text, "customField: vendor detail") || !strings.Contains(text, "vendorField: useful detail") || !reflect.DeepEqual(widget.Object, before.Object) {
		t.Fatal("custom status fields were lost or changed")
	}
	secret := unstructured.Unstructured{Object: map[string]interface{}{"kind": "Secret", "metadata": map[string]interface{}{"name": "hidden"}, "data": map[string]interface{}{"token": "DO_NOT_RENDER"}}}
	text = ansi.Strip(m.renderResourceStatus(secret, nil, nil))
	if !strings.Contains(text, "No reported status") || strings.Contains(text, "DO_NOT_RENDER") {
		t.Fatal("resource without status was misrepresented or exposed data")
	}
}

type eventsDeniedBackend struct{ *kube.Demo }

func (b *eventsDeniedBackend) List(ctx context.Context, query kube.Query) ([]unstructured.Unstructured, error) {
	if query.Resource.Kind == "Event" {
		return nil, errors.New("events is forbidden")
	}
	return b.Demo.List(ctx, query)
}

func TestStatusViewRetainsStatusWhenEventsDeniedAndRejectsLateResults(t *testing.T) {
	m := demoModel(t)
	m.backend = &eventsDeniedBackend{m.backend.(*kube.Demo)}
	m.Update(m.openEvents()())
	plain := ansi.Strip(m.panel.GetContent())
	for _, text := range []string{"Status", "Running", "Conditions", "events is forbidden", "Resource status is shown above"} {
		if !strings.Contains(plain, text) {
			t.Fatalf("missing %q when events are unavailable", text)
		}
	}
	cmd := m.loadEvents()
	response := cmd()
	m.closeEvents()
	m.mode = ""
	before := m.panel.GetContent()
	m.Update(response)
	if m.panel.GetContent() != before {
		t.Fatal("late status response replaced a different view")
	}
	cmd = m.openEvents()
	response = cmd()
	m.generation++
	m.Update(response)
	if !strings.Contains(m.panel.GetContent(), "Loading") {
		t.Fatal("old-context status response was accepted")
	}
}

func TestStatusRejectsReplacedResourceAndPreservesSortBinding(t *testing.T) {
	m := demoModel(t)
	backend := m.backend.(*kube.Demo)
	o, _ := m.current()
	replacement := *o.DeepCopy()
	replacement.SetUID(types.UID("new-incarnation"))
	m.backend = &secretReader{Demo: backend, read: func(context.Context) (unstructured.Unstructured, error) { return replacement, nil }}
	m.Update(m.openEvents()())
	if !strings.Contains(m.panel.GetContent(), "resource was replaced") {
		t.Fatal("status silently followed a different UID")
	}
	key(m, "esc")
	before := m.descending
	key(m, "R")
	if m.descending == before || m.mode != "" {
		t.Fatal("R no longer reverses the table sort")
	}
	m.backend = backend
	m.Update(m.command("status")())
	if m.mode != "events" || !strings.Contains(m.panel.GetContent(), "Conditions") {
		t.Fatal(":status did not open readable status")
	}
}

func TestStatusViewHidesSecretsSanitizesTextAndFitsTerminal(t *testing.T) {
	m := secretModel(t)
	revealSecret(t, m)
	m.Update(m.openEvents()())
	assertSecretHidden(t, m)
	m.inspector.SetContent(strings.Repeat("¤", 1000))
	for _, size := range [][2]int{{40, 12}, {80, 24}, {180, 48}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(m.View().Content, "\n")
		if strings.Contains(m.View().Content, "¤") || m.panel.Width() != size[0]-2 || m.panel.Height() != size[1]-6 {
			t.Fatal("the status view did not replace the underlying main pane")
		}
		if len(lines) != size[1] {
			t.Fatal("status view overflowed the terminal height")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != size[0] {
				t.Fatal("status view overflowed the terminal width")
			}
		}
	}
	o := unstructured.Unstructured{Object: map[string]interface{}{"kind": "Widget", "metadata": map[string]interface{}{"name": "unsafe"}, "status": map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "\x1b]52;c;payload\aReady", "status": "True", "message": "\x1b[2J\u202ehidden"}}}}}
	plain := ansi.Strip(m.renderResourceStatus(o, nil, nil))
	if strings.ContainsAny(plain, "\x1b\a\u202e") {
		t.Fatal("status labels/messages issued terminal controls")
	}
}
