package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
)

type accessLine struct {
	label    string
	decision kube.AccessDecision
	err      error
}
type accessMsg struct {
	generation, token uint64
	identity          kube.UserIdentity
	identityError     error
	lines             []accessLine
}

func (m *model) cancelAccess() {
	m.accessToken++
	if m.accessCancel != nil {
		m.accessCancel()
		m.accessCancel = nil
	}
}
func (m *model) allowMutation(action string) bool {
	if m.options.ReadOnly {
		m.notice = "Read-only mode: " + action + " is disabled"
		return false
	}
	return true
}
func (m *model) openAccess(arg string) tea.Cmd {
	reader, ok := m.backend.(kube.AccessReviewer)
	if !ok {
		m.notice = "Permission review is unavailable for this backend"
		return nil
	}
	o, selected := m.current()
	r := m.route.resource
	ns, name := m.route.namespace, ""
	if !r.Namespaced {
		ns = ""
	}
	if selected {
		name = o.GetName()
		if r.Namespaced {
			ns = o.GetNamespace()
		}
	}
	var requests []kube.AccessRequest
	var labels []string
	namespaces, err := kube.NamespaceNames(ns)
	if err != nil {
		m.notice = "Invalid namespace selection: " + kube.Safe(err.Error())
		return nil
	}
	if len(namespaces) == 0 {
		namespaces = []string{""}
	}
	add := func(label, verb, subresource string, resource kube.Resource, targetName string) {
		for _, namespace := range namespaces {
			requests = append(requests, kube.AccessRequest{Resource: resource, Namespace: namespace, Name: targetName, Verb: verb, Subresource: subresource})
			text := label
			if len(namespaces) > 1 {
				text = namespace + ": " + label
			}
			labels = append(labels, text)
		}
	}
	if arg != "" {
		fields := strings.Fields(arg)
		if len(fields) > 2 {
			m.notice = "Use :can-i VERB [SUBRESOURCE]"
			return nil
		}
		sub := ""
		if len(fields) == 2 {
			sub = fields[1]
		}
		verb := fields[0]
		targetName := name
		if verb == "list" || verb == "watch" || (verb == "create" && sub == "") {
			targetName = ""
		}
		add(strings.TrimSpace(verb+" "+sub), verb, sub, r, targetName)
	} else {
		for _, verb := range []string{"list", "watch", "get", "create", "patch", "delete"} {
			target := name
			if verb == "list" || verb == "watch" || verb == "create" {
				target = ""
			}
			add(verb, verb, "", r, target)
		}
		if r.Kind == "Pod" {
			add("logs", "get", "log", r, name)
			add("shell (WebSocket)", "get", "exec", r, name)
			add("shell (SPDY)", "create", "exec", r, name)
			add("debug (add ephemeral container)", "patch", "ephemeralcontainers", r, name)
			add("debug attach (WebSocket)", "get", "attach", r, name)
			add("debug attach (SPDY / WebSocket create check)", "create", "attach", r, name)
			add("forward (WebSocket)", "get", "portforward", r, name)
			add("forward (SPDY)", "create", "portforward", r, name)
		}
		if r.Kind == "CronJob" {
			jobs, _ := kube.Resolve(kube.Builtins(), "jobs")
			add("run now (create Job)", "create", "", jobs, "")
		}
	}
	m.cancelAccess()
	m.accessQuery = arg
	ctx, cancel := context.WithTimeout(m.session.ctx, 15*time.Second)
	m.accessCancel = cancel
	m.mode = "access-loading"
	m.panelTitle = "Permissions · " + r.ID()
	m.panel.SoftWrap = true
	m.panel.GotoTop()
	m.panel.SetContent("Checking current identity and permissions…")
	scope := ns
	if !r.Namespaced {
		scope = "cluster scope"
	} else if scope == "" {
		scope = "all namespaces"
	}
	m.accessScope = fmt.Sprintf("Context: %s\nResource: %s\nNamespace: %s", kube.Safe(m.contextName), kube.Safe(r.ID()), kube.Safe(scope))
	g, token := m.generation, m.accessToken
	return func() tea.Msg {
		defer cancel()
		identity, identityErr := reader.Identity(ctx)
		result := accessMsg{generation: g, token: token, identity: identity, identityError: identityErr}
		for i, q := range requests {
			decision, err := reader.ReviewAccess(ctx, q)
			result.lines = append(result.lines, accessLine{labels[i], decision, err})
		}
		return result
	}
}
func (m *model) accessReady(v accessMsg) {
	if v.generation != m.generation || v.token != m.accessToken || m.mode != "access-loading" {
		return
	}
	m.mode = "access"
	var text strings.Builder
	text.WriteString(m.accessScope + "\n")
	if m.options.ReadOnly {
		text.WriteString(m.theme.style("warning").Render("Read-only mode: mutations and shells are disabled locally.") + "\n")
	}
	if v.identityError != nil {
		text.WriteString("Identity unavailable: " + kube.Safe(v.identityError.Error()) + "\n")
	} else {
		text.WriteString("Identity: " + kube.Safe(v.identity.Username) + "\nGroups: " + kube.Safe(strings.Join(v.identity.Groups, ", ")) + "\n")
	}
	text.WriteString("\nKubernetes permissions\n")
	for _, line := range v.lines {
		state, colour, reason := "Denied", "error", line.decision.Reason
		if line.err != nil {
			state, colour, reason = "Unknown", "warning", line.err.Error()
		} else if line.decision.Allowed {
			state, colour = "Allowed", "success"
		}
		fmt.Fprintf(&text, "  %-22s %s\n", line.label, m.theme.style(colour).Render(state))
		if reason != "" {
			text.WriteString("    " + kube.SafeLines(reason) + "\n")
		}
	}
	text.WriteString("\nThese are API permission checks; admission policies may impose additional rules.\nCtrl+R rechecks; Escape closes.")
	m.panel.SetContent(text.String())
}
