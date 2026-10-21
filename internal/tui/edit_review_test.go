package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func openEditReview(t *testing.T, m *model, b *editBuffer) {
	t.Helper()
	m.mode = "editing"
	m.editBuffer = b
	_, prepare := m.Update(editorClosedMsg{generation: m.generation, token: m.editToken, buffer: b})
	if prepare == nil {
		t.Fatal("editor exit did not prepare review")
	}
	m.Update(prepare())
	if m.mode != "edit-review" {
		t.Fatalf("review not shown: %s %s", m.mode, m.notice)
	}
}

func TestEditReviewFreezesPatchAndAppliesOnlyAfterEnter(t *testing.T) {
	m := demoModel(t)
	b, demo := editFixture(t)
	m.backend = demo
	edited := *b.editable.DeepCopy()
	edited.SetAnnotations(map[string]string{"reviewed": "yes"})
	writeEdit(t, b, edited)
	openEditReview(t, m, b)
	fresh, _ := demo.Get(context.Background(), b.resource, b.original.GetNamespace(), b.original.GetName())
	if fresh.GetAnnotations()["reviewed"] != "" {
		t.Fatal("edit applied before confirmation")
	}
	text := ansi.Strip(m.panel.GetContent())
	if !strings.Contains(text, "+ ") || !strings.Contains(text, "reviewed") {
		t.Fatalf("missing highlighted diff: %s", text)
	}
	edited.SetAnnotations(map[string]string{"unreviewed": "no"})
	writeEdit(t, b, edited)
	cmd := m.editReviewKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter did not apply")
	}
	m.Update(cmd())
	fresh, _ = demo.Get(context.Background(), b.resource, b.original.GetNamespace(), b.original.GetName())
	if fresh.GetAnnotations()["reviewed"] != "yes" || fresh.GetAnnotations()["unreviewed"] != "" {
		t.Fatal("applied a different edit from the preview")
	}
	if _, err := os.Stat(b.path); !os.IsNotExist(err) {
		t.Fatal("applied edit file retained")
	}
}

func TestEditReviewDiscardAndSecretRedaction(t *testing.T) {
	m := demoModel(t)
	b, _ := editFixture(t)
	edited := *b.editable.DeepCopy()
	edited.SetAnnotations(map[string]string{"change": "yes"})
	writeEdit(t, b, edited)
	openEditReview(t, m, b)
	m.addHistoryExecution("edit", "edit", "", m.editToken)
	m.editReviewKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.editReview != nil || m.editBuffer != nil || len(m.commandExecutions) != 0 || len(m.settings.CommandHistory) != 0 {
		t.Fatal("discard retained edit state/history")
	}
	before := b.editable
	before.SetKind("Secret")
	before.Object["data"] = map[string]any{"password": "b2xkLXNlY3JldA=="}
	after := *before.DeepCopy()
	after.Object["data"] = map[string]any{"password": "bmV3LXNlY3JldA=="}
	diff := reviewDiff(&editReview{before: before, after: after})
	if strings.Contains(diff, "b2xkLXNlY3JldA==") || strings.Contains(diff, "bmV3LXNlY3JldA==") || !strings.Contains(diff, "redacted: changed") {
		t.Fatal("Secret edit preview leaked values or hid the fact they changed")
	}
}

func TestEditReviewStillHonoursVersionConflicts(t *testing.T) {
	m := demoModel(t)
	b, demo := editFixture(t)
	edited := *b.editable.DeepCopy()
	edited.SetAnnotations(map[string]string{"change": "yes"})
	writeEdit(t, b, edited)
	openEditReview(t, m, b)
	if err := demo.Patch(context.Background(), b.resource, b.original.GetNamespace(), b.original.GetName(), []byte(fmt.Sprintf(`{"metadata":{"uid":%q,"resourceVersion":%q,"annotations":{"concurrent":"yes"}}}`, b.original.GetUID(), b.original.GetResourceVersion()))); err != nil {
		t.Fatal(err)
	}
	cmd := m.editReviewKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	if m.mode != "edit-error" || !strings.Contains(m.notice, "file retained") {
		t.Fatal("conflicting reviewed edit was applied or discarded")
	}
}
