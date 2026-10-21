package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func testDebugConfig() debugConfig {
	return debugConfig{DefaultProfile: "approved", Profiles: map[string]debugProfile{"approved": {Image: "123456789012.dkr.ecr.eu-west-2.amazonaws.com/platform/debug-tools:approved", SecurityContext: map[string]any{"runAsUser": int64(1000)}}}}
}

func TestDebugConfigurationDefaultsOverridesAndStrictValidation(t *testing.T) {
	c := testDebugConfig()
	if err := validateDebugConfig(c); err != nil {
		t.Fatal(err)
	}
	p, err := compileDebugProfile(c.Profiles["approved"])
	if err != nil || p.Command[0] != "/bin/sh" || !*p.SecurityContext.RunAsNonRoot || *p.SecurityContext.AllowPrivilegeEscalation || p.SecurityContext.Capabilities.Drop[0] != "ALL" || p.SecurityContext.SeccompProfile.Type != "RuntimeDefault" || *p.SecurityContext.RunAsUser != 1000 {
		t.Fatalf("unsafe defaults: %#v %v", p, err)
	}
	c.Profiles["other"] = debugProfile{Image: "registry.example.com/debug@sha256:" + strings.Repeat("a", 64)}
	c.ContextProfiles = map[string]string{"production": "other"}
	c.NamespaceProfiles = map[string]map[string]string{"production": {"payments": "approved"}}
	if c.profile("production", "payments") != "approved" || c.profile("production", "elsewhere") != "other" || c.profile("dev", "payments") != "approved" {
		t.Fatal("profile precedence")
	}
	for _, p := range []debugProfile{
		{Image: "busybox:latest"}, {Image: "registry.example.com/debug:bad tag"}, {Image: "https://registry.example.com/debug"}, {Image: "registry.example.com/debug@sha256:abc"},
		{Image: "registry.example.com/debug", PullPolicy: "Sometimes"}, {Image: "registry.example.com/debug", SecurityContext: map[string]any{"run_as_user": 1000}},
		{Image: "registry.example.com/debug", SecurityContext: map[string]any{"runAsUser": 0}}, {Image: "registry.example.com/debug", Command: []string{""}},
	} {
		if _, err := compileDebugProfile(p); err == nil {
			t.Fatalf("accepted invalid profile: %#v", p)
		}
	}
	c.StartTimeout = "0s"
	if validateDebugConfig(c) == nil {
		t.Fatal("invalid timeout accepted")
	}
	c = testDebugConfig()
	c.DefaultProfile = "typo"
	if validateDebugConfig(c) == nil {
		t.Fatal("unknown default accepted")
	}
}

func TestDebugConfigurationYAMLTOMLAndPreferencePreservation(t *testing.T) {
	for _, format := range []string{"yaml", "toml"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config."+format)
			text := "debug:\n  default_profile: tools\n  profiles:\n    tools:\n      image: registry.example.com/debug:approved\n      security_context:\n        runAsUser: 1000\n"
			if format == "toml" {
				text = "[debug]\ndefault_profile = 'tools'\n[debug.profiles.tools]\nimage = 'registry.example.com/debug:approved'\n[debug.profiles.tools.security_context]\nrunAsUser = 1000\n"
			}
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := LoadSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.savePatch(map[string]any{"theme": "nord"}); err != nil {
				t.Fatal(err)
			}
			reloaded, err := LoadSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			p, err := compileDebugProfile(reloaded.Config.Debug.Profiles["tools"])
			if err != nil || p.SecurityContext.RunAsUser == nil || *p.SecurityContext.RunAsUser != 1000 || reloaded.Config.Debug.DefaultProfile != "tools" {
				t.Fatalf("profile lost when saving preferences: %#v %v", reloaded, err)
			}
		})
	}
}

func debugPreview(t *testing.T, m *model) {
	t.Helper()
	cmd := runSubmitted(m, "debug approved")
	if cmd == nil || m.mode != "debug-loading" {
		t.Fatal("no preflight")
	}
	m.Update(cmd())
	if m.mode != "debug-confirm" {
		t.Fatalf("no confirmation: %s %s", m.mode, m.panel.GetContent())
	}
}

func TestDebugPreviewCancellationHistoryAndReadOnly(t *testing.T) {
	m := demoModel(t)
	m.settings.Config.Debug = testDebugConfig()
	debugPreview(t, m)
	for _, text := range []string{"Pod: default/web-7c9f6d8b5-0000", "Enter to start", "stays until", "Container: web", "123456789012.dkr.ecr"} {
		if !strings.Contains(ansi.Strip(m.panel.GetContent()), text) {
			t.Fatalf("preview omits %q", text)
		}
	}
	if m.input.Focused() || strings.Contains(m.panel.GetContent(), "Type default/") {
		t.Fatal("pod name entry is still required")
	}
	oldToken := m.debugToken
	key(m, "esc")
	m.Update(debugPreparedMsg{generation: m.generation, token: oldToken, plan: &kube.DebugPlan{}})
	if m.debug != nil || m.mode != "" || len(m.settings.CommandHistory) != 0 {
		t.Fatal("cancelled preview survived or entered history")
	}
	runSubmitted(m, "debug missing")
	if len(m.settings.CommandHistory) != 0 || !strings.Contains(m.notice, "Unknown debug profile") {
		t.Fatal("unknown profile entered history")
	}
	m.options.ReadOnly = true
	if cmd := m.openDebug("approved"); cmd != nil || !strings.Contains(strings.ToLower(m.notice), "read-only") {
		t.Fatal("read-only debugging allowed")
	}
}

func TestDebugConfirmedStartKeepsHistoryPendingUntilAttachment(t *testing.T) {
	m := demoModel(t)
	m.settings.Config.Debug = testDebugConfig()
	debugPreview(t, m)
	plan := m.debug.plan
	m.input.SetValue("")
	cmd := m.debugKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("confirmation did not start")
	}
	started := cmd().(debugStartedMsg)
	if started.err != nil {
		t.Fatal(started.err)
	}
	_, attach := m.Update(started)
	if attach == nil || m.mode != "shell" || len(m.settings.CommandHistory) != 0 {
		t.Fatal("start did not hand off terminal or history saved early")
	}
	name := plan.Container.Name
	token := m.debugToken
	m.Update(debugClosedMsg{generation: m.generation, token: token, name: name, disconnected: true, connected: true})
	if !slices.Contains(m.settings.CommandHistory, "debug approved") || !strings.Contains(m.notice, ":debug attach "+name) {
		t.Fatalf("successful disconnect not saved / reattach missing: %#v %s", m.settings.CommandHistory, m.notice)
	}
	// A cancelled connection before receiving any remote output is not successful.
	m.addHistoryExecution("debug attach "+name, "debug", "", m.debugToken)
	m.mode = "shell"
	m.Update(debugClosedMsg{generation: m.generation, token: m.debugToken, name: name, disconnected: true})
	if slices.Contains(m.settings.CommandHistory, "debug attach "+name) {
		t.Fatal("unconnected attachment entered history")
	}
}

type debugDeniedBackend struct{ *kube.Demo }

func (b *debugDeniedBackend) ReviewAccess(context.Context, kube.AccessRequest) (kube.AccessDecision, error) {
	return kube.AccessDecision{Allowed: false, Reason: "policy"}, nil
}

func TestDebugFailureHistoryAndStaleResults(t *testing.T) {
	m := demoModel(t)
	m.settings.Config.Debug = testDebugConfig()
	m.backend = &debugDeniedBackend{kube.NewDemo()}
	cmd := runSubmitted(m, "debug approved")
	m.Update(cmd())
	if m.mode != "operation-error" || len(m.settings.CommandHistory) != 0 || !strings.Contains(m.panel.GetContent(), "permission denied") {
		t.Fatal("denied preflight succeeded")
	}
	m.mode = "debug-starting"
	m.debug = &debugState{}
	m.Update(debugStartedMsg{generation: m.generation - 1, token: m.debugToken, err: errors.New("old")})
	if m.mode != "debug-starting" {
		t.Fatal("stale result changed current view")
	}
}

func TestDebugPreviewGeometryAndRedactsAdmissionEnvironment(t *testing.T) {
	m := demoModel(t)
	m.settings.Config.Debug = testDebugConfig()
	debugPreview(t, m)
	m.debug.plan.Container.Env = []corev1.EnvVar{{Name: "INJECTED", Value: "must-not-appear"}}
	m.debug.details = true
	m.renderDebugPreview()
	if strings.Contains(m.panel.GetContent(), "must-not-appear") {
		t.Fatal("admission-injected secret shown in preview")
	}
	for _, size := range [][2]int{{40, 12}, {80, 24}, {180, 48}} {
		m.width, m.height = size[0], size[1]
		m.resize()
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) != m.height {
			t.Fatalf("height %d: %d", m.height, len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != m.width {
				t.Fatalf("width %d: %d", m.width, ansi.StringWidth(line))
			}
		}
	}
}

func TestDebugCompletionUsesProfilesAndExistingEphemerals(t *testing.T) {
	m := demoModel(t)
	m.settings.Config.Debug = testDebugConfig()
	m.startInput("command", "deb")
	m.completeCommand(false)
	if m.input.Value() != "debug" {
		t.Fatalf("debug missing: %q", m.input.Value())
	}
	m.startInput("command", "debug appr")
	m.completeCommand(false)
	if m.input.Value() != "debug approved" {
		t.Fatal("profile completion missing")
	}
	if !slices.Contains(commandNames, "debug") {
		t.Fatal("command not discoverable")
	}
}

func TestDebugRequiresConfiguredImageAndProfilePickerCancellation(t *testing.T) {
	m := demoModel(t)
	if cmd := m.openDebug(""); cmd == nil || m.mode != "picker" || m.pickerKind != "debug-image-source" {
		t.Fatal("missing image did not open first-use setup")
	}
	key(m, "esc")
	m.settings.Config.Debug = testDebugConfig()
	m.settings.Config.Debug.DefaultProfile = ""
	m.settings.Config.Debug.Profiles["other"] = debugProfile{Image: "registry.example.com/other:approved"}
	runSubmitted(m, "debug")
	if m.mode != "picker" || m.pickerKind != "debug-profiles" {
		t.Fatal("no profile picker")
	}
	cmd := m.pickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || m.mode != "debug-loading" || len(m.settings.CommandHistory) != 0 {
		t.Fatal("profile selection prematurely saved history")
	}
	m.Update(cmd())
	key(m, "esc")
	if m.debug != nil || len(m.commandExecutions) != 0 || len(m.settings.CommandHistory) != 0 {
		t.Fatal("cancelled picker kept pending debug state")
	}
}

func TestDebugAutomaticContainerAndOptionalPicker(t *testing.T) {
	m := demoModel(t)
	m.settings.Config.Debug = testDebugConfig()
	debugPreview(t, m)
	if m.debug.target != "web" {
		t.Fatal("main container was not chosen")
	}
	m.debugKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if m.mode != "picker" || m.pickerKind != "debug-targets" {
		t.Fatal("optional picker missing")
	}
	key(m, "esc")
	if m.mode != "debug-confirm" || m.debug == nil {
		t.Fatal("leaving optional picker cancelled debugging")
	}
	m.debugKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	m.input.SetValue("sidecar")
	m.filterChoices()
	cmd := m.pickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("target change not checked")
	}
	m.Update(cmd())
	if m.debug.plan.Container.TargetContainerName != "sidecar" {
		t.Fatal("target change was ignored")
	}
	m.debugKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if !strings.Contains(ansi.Strip(m.panel.GetContent()), "runAsNonRoot: true") {
		t.Fatal("full details unavailable")
	}
	pod := m.debug.object
	pod.SetAnnotations(map[string]string{"kubectl.kubernetes.io/default-container": "sidecar"})
	if defaultDebugTarget(pod) != "sidecar" {
		t.Fatal("default-container annotation ignored")
	}
	unstructured.RemoveNestedField(pod.Object, "status", "containerStatuses")
	if defaultDebugTarget(pod) != "" {
		t.Fatal("unavailable process target used instead of pod network")
	}
}

func TestDebugReattachSkipsPickerAndConfirmationForSingleContainer(t *testing.T) {
	m := demoModel(t)
	m.settings.Config.Debug = testDebugConfig()
	debugPreview(t, m)
	created, err := kube.ApplyDebug(context.Background(), m.backend.(kube.EphemeralDebugger), m.debug.plan)
	if err != nil {
		t.Fatal(err)
	}
	key(m, "esc")
	m.rows[0] = created
	m.rebuild("")
	cmd := runSubmitted(m, "debug attach")
	if cmd == nil || m.mode != "debug-loading" {
		t.Fatal("unnecessary reattach picker")
	}
	_, start := m.Update(cmd())
	if start == nil || m.mode != "debug-starting" {
		t.Fatal("reattach still asks for confirmation")
	}
	_, attach := m.Update(start())
	if attach == nil || m.mode != "shell" {
		t.Fatal("reattach did not reach the terminal")
	}
}
