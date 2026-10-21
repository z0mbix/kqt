package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func runSubmitted(m *model, value string) tea.Cmd {
	m.startInput("command", value)
	return m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
}

type historyFailureBackend struct {
	*kube.Demo
	failList, failPatch bool
}

func (b *historyFailureBackend) List(ctx context.Context, q kube.Query) ([]unstructured.Unstructured, error) {
	if b.failList {
		return nil, errors.New("forbidden")
	}
	return b.Demo.List(ctx, q)
}
func (b *historyFailureBackend) Patch(ctx context.Context, r kube.Resource, ns, name string, data []byte) error {
	if b.failPatch {
		return errors.New("forbidden")
	}
	return b.Demo.Patch(ctx, r, ns, name, data)
}

func TestFailedAndMistypedCommandsNeverEnterHistory(t *testing.T) {
	m := demoModel(t)
	for _, value := range []string{"themse", "theme missing", "ctx missing", "ns WRONG!", "sort name sideways", "columns", "inspector jsno", "logs typo", "pods typo", "help typo", "metrics maybe", "alias bad name extra", "unalias missing", "rollback invalid", "export invalid", "forward 1:99999"} {
		runSubmitted(m, value)
		if len(m.settings.CommandHistory) != 0 {
			t.Fatalf("failed command %q was saved", value)
		}
	}
	m.backend = &historyFailureBackend{Demo: kube.NewDemo(), failList: true}
	cmd := runSubmitted(m, "deploy")
	if cmd == nil {
		t.Fatal("valid resource command did not run")
	}
	if len(m.settings.CommandHistory) != 0 {
		t.Fatal("request entered history before success")
	}
	m.Update(cmd())
	if len(m.settings.CommandHistory) != 0 || !strings.Contains(m.dataErr, "forbidden") {
		t.Fatal("API failure was saved or hidden")
	}
}

func TestHistoryWaitsForActionAndDropsCancelledOrFailedOperations(t *testing.T) {
	m := demoModel(t)
	m.Update(m.changeResource("deployments", false)())
	runSubmitted(m, "scale 3")
	if len(m.settings.CommandHistory) != 0 {
		t.Fatal("confirmation counted as successful action")
	}
	key(m, "esc")
	if len(m.settings.CommandHistory) != 0 {
		t.Fatal("cancelled action was saved")
	}
	backend := &historyFailureBackend{Demo: kube.NewDemo(), failPatch: true}
	m.backend = backend
	runSubmitted(m, "scale 3")
	m.input.SetValue("default/web")
	cmd := m.actionInputKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	if len(m.settings.CommandHistory) != 0 {
		t.Fatal("failed action was saved")
	}
	backend.failPatch = false
	runSubmitted(m, "scale 3")
	m.input.SetValue("default/web")
	m.Update(m.actionInputKey(tea.KeyPressMsg{Code: tea.KeyEnter})())
	if !slices.Equal(m.settings.CommandHistory, []string{"scale 3"}) {
		t.Fatal("successful action was not saved", m.settings.CommandHistory)
	}
}

func TestHistoryExportsOnlyAfterSuccessAndKeepsSubmissionOrder(t *testing.T) {
	m := demoModel(t)
	dir := t.TempDir()
	first := fmt.Sprintf("export json %q", filepath.Join(dir, "first.json"))
	second := fmt.Sprintf("export json %q", filepath.Join(dir, "second.json"))
	a := runSubmitted(m, first)
	b := runSubmitted(m, second)
	if len(m.settings.CommandHistory) != 0 {
		t.Fatal("exports recorded before completion")
	}
	m.Update(b())
	m.Update(a())
	if !slices.Equal(m.settings.CommandHistory, []string{first, second}) {
		t.Fatal("history used completion order", m.settings.CommandHistory)
	}
	m.Update(runSubmitted(m, first)())
	if len(m.settings.CommandHistory) != 2 {
		t.Fatal("failed overwrite entered history")
	}
	saved, err := LoadSettings(m.settings.Path)
	if err != nil {
		t.Fatal(err)
	}
	saved.loadCommandHistory()
	if !slices.Equal(saved.CommandHistory, []string{first, second}) {
		t.Fatal("failed export changed saved history", err)
	}
}

func TestHistoryIgnoresStaleResponsesAndRecordsLogConnectionWithoutOutput(t *testing.T) {
	m := demoModel(t)
	old := runSubmitted(m, "deploy")
	latest := runSubmitted(m, "pods")
	m.Update(old())
	if len(m.settings.CommandHistory) != 0 {
		t.Fatal("stale result saved a superseded command")
	}
	m.Update(latest())
	if !slices.Equal(m.settings.CommandHistory, []string{"pods"}) {
		t.Fatal("successful resource browse was not saved")
	}
	runSubmitted(m, "logs")
	key(m, "esc") // Cancel the first pod's container picker.
	if len(m.settings.CommandHistory) != 1 {
		t.Fatal("cancelled logs picker was saved")
	}
	m.mode = "logs"
	m.logFeed = newLogFeed()
	m.nextCommandSequence()
	m.addHistoryExecution("logs", "logs", "", m.logToken)
	m.logFeed.opened()
	m.Update(logPulseMsg{m.generation, m.logToken, m.logFeed})
	if !slices.Equal(m.settings.CommandHistory, []string{"pods", "logs"}) {
		t.Fatal("connected idle log stream was not saved")
	}
}

func TestHistoryDoesNotSaveActionCancelledAfterConfirmation(t *testing.T) {
	m := demoModel(t)
	m.Update(m.changeResource("deployments", false)())
	runSubmitted(m, "scale 3")
	m.input.SetValue("default/web")
	request := m.actionInputKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	key(m, "esc")
	m.Update(request())
	if len(m.settings.CommandHistory) != 0 || m.mode != "" || m.actionPlan != nil || !strings.Contains(m.notice, "cancelled") {
		t.Fatal("cancelled in-flight action was retained or reopened", m.notice)
	}
}

func TestHistoryDropsReadResultsWhenSelectedResourceChanged(t *testing.T) {
	for _, command := range []string{"rollouts", "related"} {
		m := demoModel(t)
		m.Update(m.changeResource("deployments", false)())
		request := runSubmitted(m, command)
		result := request()
		m.visible[m.selected].SetUID("replacement-selection")
		m.Update(result)
		if len(m.settings.CommandHistory) != 0 || m.mode != "" {
			t.Fatalf("%s saved or displayed a cancelled resource read", command)
		}
	}
}
