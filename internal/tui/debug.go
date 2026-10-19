package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

type debugState struct {
	object                      unstructured.Unstructured
	profileName, target, attach string
	details                     bool
	profile                     kube.DebugProfile
	plan                        *kube.DebugPlan
}
type debugPreparedMsg struct {
	generation, token uint64
	plan              *kube.DebugPlan
	err               error
}
type debugStartedMsg struct {
	generation, token uint64
	pod               unstructured.Unstructured
	plan              *kube.DebugPlan
	err               error
}
type debugClosedMsg struct {
	generation, token uint64
	name              string
	disconnected      bool
	connected         bool
	err               error
}

func (m *model) cancelDebug() {
	m.forgetHistoryExecution("debug")
	m.debugToken++
	if m.debugCancel != nil {
		m.debugCancel()
		m.debugCancel = nil
	}
	m.debug = nil
	m.input.CharLimit = 256
}
func (m *model) showingDebug() bool { return strings.HasPrefix(m.mode, "debug-") }

func (m *model) openDebug(arg string) tea.Cmd {
	if !m.allowMutation("debug containers") {
		return nil
	}
	o, ok := m.current()
	if !ok || o.GetKind() != "Pod" {
		m.notice = "Select a pod to debug"
		return nil
	}
	if _, ok = m.backend.(kube.EphemeralDebugger); !ok {
		m.notice = "This connection does not support ephemeral debugging"
		return nil
	}
	m.cancelDebug()
	m.debug = &debugState{object: *o.DeepCopy()}
	words := strings.Fields(arg)
	if len(words) > 0 && words[0] == "attach" {
		if len(words) > 2 {
			m.notice = "Usage: :debug attach [CONTAINER]"
			return nil
		}
		if len(words) == 2 {
			m.debug.attach = words[1]
			return m.prepareDebug()
		}
		containers := kube.DebugContainers(o)
		if len(containers) == 0 {
			m.notice = "No ephemeral containers in this pod"
			return nil
		}
		if len(containers) == 1 {
			m.debug.attach = containers[0].Name
			return m.prepareDebug()
		}
		return m.openPicker("debug-attach")
	}
	if len(words) > 1 {
		m.notice = "Usage: :debug [PROFILE] or :debug attach [CONTAINER]"
		return nil
	}
	profile := m.settings.Config.Debug.profile(m.contextName, o.GetNamespace())
	if len(words) == 1 {
		profile = words[0]
	}
	if profile != "" {
		return m.chooseDebugProfile(profile)
	}
	if len(m.settings.Config.Debug.Profiles) == 0 {
		return m.openPicker("debug-image-source")
	}
	if len(m.settings.Config.Debug.Profiles) == 1 {
		for name := range m.settings.Config.Debug.Profiles {
			return m.chooseDebugProfile(name)
		}
	}
	return m.openPicker("debug-profiles")
}

func (m *model) debugChoices(kind string) []choice {
	if m.debug == nil {
		return nil
	}
	var choices []choice
	switch kind {
	case "debug-image-source":
		choices = []choice{{"Use my own image", "paste once and save for future use"}, {"Use BusyBox", "save and use public Docker Hub image; non-root UID 1000"}}
	case "debug-profiles":
		var names []string
		for name := range m.settings.Config.Debug.Profiles {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			choices = append(choices, choice{name, m.settings.Config.Debug.Profiles[name].Image})
		}
	case "debug-targets":
		choices = append(choices, choice{"<pod network>", "no process namespace target; useful when the app cannot start"})
		for _, name := range kube.DebugTargets(m.debug.object) {
			choices = append(choices, choice{name, "request this container's process namespace (runtime support varies)"})
		}
	case "debug-attach":
		for _, container := range kube.DebugContainers(m.debug.object) {
			choices = append(choices, choice{container.Name, container.Image})
		}
	}
	return choices
}

func (m *model) chooseDebugProfile(name string) tea.Cmd {
	p, ok := m.settings.Config.Debug.Profiles[name]
	if !ok {
		m.notice = "Unknown debug profile: " + kube.Safe(name)
		return nil
	}
	profile, err := compileDebugProfile(p)
	if err != nil {
		m.operationError("Debug profile", err)
		return nil
	}
	m.debug.profile, m.debug.profileName = profile, name
	m.debug.target = defaultDebugTarget(m.debug.object)
	return m.prepareDebug()
}

func defaultDebugTarget(pod unstructured.Unstructured) string {
	running, targets := kube.RunningContainers(pod), kube.DebugTargets(pod)
	if name := pod.GetAnnotations()["kubectl.kubernetes.io/default-container"]; slices.Contains(targets, name) && slices.Contains(running, name) {
		return name
	}
	for _, name := range targets {
		if slices.Contains(running, name) {
			return name
		}
	}
	return ""
}

func (m *model) prepareDebug() tea.Cmd {
	m.promoteHistoryExecution("debug", m.debugToken)
	m.mode, m.panelTitle = "debug-loading", "Debug container · checking permissions and admission"
	m.resize()
	m.panel.SoftWrap = true
	m.panel.GotoTop()
	m.panel.SetContent("Checking the selected pod and namespace-scoped permissions…\n\nRunning server-side dry-run before asking for confirmation. Escape cancels.")
	ctx, cancel := context.WithTimeout(m.session.ctx, 30*time.Second)
	m.debugCancel = cancel
	g, token, state, backend := m.generation, m.debugToken, *m.debug, m.backend.(kube.EphemeralDebugger)
	return func() tea.Msg {
		defer cancel()
		plan, err := kube.PrepareDebug(ctx, backend, state.object, state.profile, state.target, state.attach)
		return debugPreparedMsg{g, token, plan, err}
	}
}

func (m *model) debugPrepared(v debugPreparedMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.debugToken || m.mode != "debug-loading" || m.debug == nil {
		return nil
	}
	if v.err != nil {
		m.operationError("Debug preflight failed", v.err)
		return nil
	}
	m.debug.plan = v.plan
	if v.plan.AttachOnly {
		return m.startDebug()
	}
	m.mode, m.panelTitle = "debug-confirm", "Start debug container"
	m.input.Blur()
	m.resize()
	m.panel.GotoTop()
	m.renderDebugPreview()
	return nil
}

func debugIdentity(plan *kube.DebugPlan) string {
	return plan.Pod.GetNamespace() + "/" + plan.Pod.GetName()
}

func (m *model) renderDebugPreview() {
	plan := m.debug.plan
	target := plan.Container.TargetContainerName
	if target == "" {
		target = "pod network (no process target)"
	}
	content := fmt.Sprintf("Pod: %s\nContext: %s\nImage: %s\nContainer: %s\n\nPress Enter to start a debug shell in this pod.\nThe debug container entry stays until the pod is removed.\n\nc changes the target container · d shows details · Escape cancels.", kube.Safe(debugIdentity(plan)), kube.Safe(m.contextName), kube.Safe(plan.Container.Image), kube.Safe(target))
	for _, warning := range plan.Warnings {
		content += "\n\n" + m.theme.style("warning").Render(kube.Safe(warning))
	}
	if !m.debug.details {
		m.panel.SetContent(content)
		return
	}
	container := plan.Container.DeepCopy()
	// Admission webhooks may inject literal environment secrets. They are not
	// needed to approve the requested image/security settings and must stay hidden.
	for i := range container.Env {
		if container.Env[i].Value != "" {
			container.Env[i].Value = "<redacted>"
		}
	}
	data, _ := yaml.Marshal(container)
	content += "\n\nDetails (after server dry-run):\n" + m.theme.renderYAML(string(data))
	content += "\nThe container cannot be removed or restarted, and has no individual resource requests/limits.\nExisting pod placement and pull credentials apply. Process targeting depends on runtime support.\nCtrl+] disconnects; use exit to stop the shell.\nPgUp/PgDn scroll · d hides details."
	m.panel.SetContent(content)
}

func (m *model) debugKey(v tea.KeyPressMsg) tea.Cmd {
	if m.mode == "debug-image" {
		return m.debugImageKey(v)
	}
	if v.String() == "esc" || v.String() == "q" {
		name := ""
		if m.debug != nil && m.debug.plan != nil {
			name = m.debug.plan.Container.Name
		}
		starting := m.mode == "debug-starting"
		m.cancelDebug()
		m.mode = ""
		m.input.Blur()
		m.resize()
		if starting {
			m.notice = "Cancelled waiting; " + name + " may still exist. Refresh and use :debug attach " + name
		}
		return nil
	}
	if v.String() == "pgup" || v.String() == "ctrl+b" {
		m.panel.PageUp()
		return nil
	}
	if v.String() == "pgdown" || v.String() == "ctrl+f" {
		m.panel.PageDown()
		return nil
	}
	if m.mode != "debug-confirm" {
		return nil
	}
	if v.String() == "c" {
		cmd := m.openPicker("debug-targets")
		m.pickerReturn = "debug-confirm"
		return cmd
	}
	if v.String() == "d" {
		m.debug.details = !m.debug.details
		m.renderDebugPreview()
		m.panel.GotoTop()
		return nil
	}
	if v.String() != "enter" {
		scroll(&m.panel, v.String())
		return nil
	}
	return m.startDebug()
}

func (m *model) startDebug() tea.Cmd {
	if !m.allowMutation("debug containers") || m.debug == nil || m.debug.plan == nil {
		return nil
	}
	m.input.Blur()
	m.mode = "debug-starting"
	m.panelTitle = "Starting debug container"
	m.panel.SetContent("Waiting for " + m.debug.plan.Container.Name + " to start…\n\nEscape cancels waiting. The ephemeral container may remain; use :debug attach to reconnect.")
	ctx, cancel := context.WithTimeout(m.session.ctx, m.settings.Config.Debug.timeout())
	m.debugCancel = cancel
	g, token, backend, plan := m.generation, m.debugToken, m.backend.(kube.EphemeralDebugger), *m.debug.plan
	return func() tea.Msg {
		defer cancel()
		pod, err := kube.ApplyDebug(ctx, backend, &plan)
		if err == nil {
			pod, err = kube.WaitDebug(ctx, backend, pod, plan.Container.Name)
		}
		return debugStartedMsg{g, token, pod, &plan, err}
	}
}

func (m *model) debugStarted(v debugStartedMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.debugToken || m.mode != "debug-starting" || m.debug == nil {
		return nil
	}
	if v.err != nil {
		m.operationError("Debug container failed", v.err)
		return nil
	}
	m.stopResourceWatch()
	if m.requestCancel != nil {
		m.requestCancel()
	}
	m.request++
	m.busy = false
	m.closeLog()
	m.closeEvents()
	ctx, cancel := context.WithCancel(m.session.ctx)
	m.debugCancel = cancel
	m.mode = "shell"
	command := &shellCommand{ctx: ctx, attacher: m.backend.(kube.EphemeralDebugger), spdyOnly: v.plan.SPDYOnly, target: v.pod, container: v.plan.Container.Name,
		heading: "Debug · " + kube.Safe(debugIdentity(v.plan)) + " · " + v.plan.Container.Name, disconnectHint: "Ctrl+] disconnects (container may keep running); exit stops the shell"}
	return tea.Exec(command, func(err error) tea.Msg {
		return debugClosedMsg{v.generation, v.token, v.plan.Container.Name, command.disconnected, command.outputSeen.Load(), err}
	})
}

func (m *model) debugClosed(v debugClosedMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.debugToken || m.mode != "shell" {
		return nil
	}
	m.cancelDebug()
	m.mode = ""
	m.resize()
	if v.err != nil {
		m.operationError("Debug attachment ended", fmt.Errorf("%w\n\nThe container may still be running. Reattach: :debug attach %s", v.err, v.name))
	} else if v.disconnected {
		m.notice = "Disconnected; container may keep running. Reattach: :debug attach " + v.name
	} else {
		m.notice = "Debug session ended; the ephemeral container entry remains in the pod spec"
	}
	return m.refreshResources()
}
