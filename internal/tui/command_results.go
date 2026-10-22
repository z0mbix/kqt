package tui

import (
	tea "charm.land/bubbletea/v2"
	"slices"
	"strings"
)

type historyOperation struct {
	kind              string
	generation, token uint64
}
type historyExecution struct {
	value, phase string
	sequence     uint64
	operation    historyOperation
}

func (m *model) addHistoryExecution(value, kind, phase string, token uint64, sequence ...uint64) {
	if m.commandExecutions == nil {
		m.commandExecutions = map[historyOperation]*historyExecution{}
	}
	for key := range m.commandExecutions {
		if key.generation != m.generation || key.kind == kind && kind != "forward" && kind != "manifest" {
			delete(m.commandExecutions, key)
		}
	}
	key := historyOperation{kind, m.generation, token}
	order := m.commandSequence
	if len(sequence) > 0 {
		order = sequence[0]
	}
	m.commandExecutions[key] = &historyExecution{value: value, phase: phase, sequence: order, operation: key}
}
func (m *model) forgetHistoryExecution(kind string) {
	for key := range m.commandExecutions {
		if key.kind == kind {
			delete(m.commandExecutions, key)
		}
	}
}
func (m *model) promoteHistoryExecution(kind string, token uint64) {
	for _, run := range m.commandExecutions {
		if run.operation.kind == kind && run.phase == "picker" && run.operation.generation == m.generation {
			value := run.value
			m.addHistoryExecution(value, kind, "", token, run.sequence)
			return
		}
	}
}

func (m *model) executeSubmittedCommand(value string) tea.Cmd {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	sequence := m.nextCommandSequence()
	m.commandConfigError = false
	cmd := m.command(value)
	if m.commandConfigError {
		return cmd
	}
	word, arg, _ := strings.Cut(value, " ")
	arg = strings.TrimSpace(arg)
	// These commands complete locally, or successfully open a local picker.
	local := slices.Contains([]string{"q", "quit", "theme", "themes", "resources", "api", "actions", "layout", "layouts", "write", "inspector", "header", "sort", "columns", "copy", "download-dir", "alias", "unalias", "forwards", "help"}, word) || slices.Contains([]string{"ctx", "context", "contexts"}, word) && arg == "" || word == "forward" && strings.HasPrefix(arg, "stop ")
	if local {
		// Only the explicit success notices below are positive local results.
		success := m.notice == "" || m.mode == "help"
		for _, prefix := range []string{"Saved layout ", "Download directory:", "Context alias saved:", "Context alias removed:", "Stopping", "Inspector shown", "Inspector hidden", "Inspector enabled", "Header shown", "Header hidden"} {
			success = success || strings.HasPrefix(m.notice, prefix)
		}
		if cmd != nil && word == "copy" {
			success = true
		}
		if success {
			m.rememberCommandAt(value, sequence)
		}
		return cmd
	}
	if word == "ns" || word == "namespace" {
		if m.mode == "namespace-group" {
			m.namespaceGroup.command, m.namespaceGroup.sequence = value, sequence
			return cmd
		}
		if strings.HasPrefix(m.notice, "Saved namespace group ") || strings.HasPrefix(m.notice, "Renamed namespace group ") {
			m.rememberCommandAt(value, sequence)
			return cmd
		}
	}
	if m.mode == "delete-confirm" && word == "delete" {
		m.addHistoryExecution(value, "action", "", m.actionToken)
		return cmd
	}
	if cmd == nil {
		return nil
	}
	switch m.mode {
	case "bundle-save", "bundle-loading":
		m.addHistoryExecution(value, "bundle", "", m.bundleToken)
	case "command": // An incomplete command has opened its argument prompt.
		return cmd
	case "picker":
		switch m.pickerKind {
		case "containers":
			m.addHistoryExecution(value, "logs", "picker", m.logToken)
		case "shell-containers":
			m.addHistoryExecution(value, "shell", "picker", m.shellToken)
		case "debug-image-source", "debug-profiles", "debug-targets", "debug-attach":
			m.addHistoryExecution(value, "debug", "picker", m.debugToken)
		default:
			m.rememberCommandAt(value, sequence)
		}
	case "action-confirm", "delete-confirm":
		m.addHistoryExecution(value, "action", "", m.actionToken)
	case "debug-loading":
		m.addHistoryExecution(value, "debug", "", m.debugToken)
	case "relations-loading":
		m.addHistoryExecution(value, "relations", "", m.relationToken)
	case "rollouts-loading":
		m.addHistoryExecution(value, "rollouts", "", m.rolloutToken)
	case "access-loading":
		m.addHistoryExecution(value, "access", "", m.accessToken)
	case "overview":
		m.addHistoryExecution(value, "overview", "", m.overviewToken)
	case "events":
		m.addHistoryExecution(value, "status", "", m.eventToken)
	case "logs":
		m.addHistoryExecution(value, "logs", "", m.logToken)
	case "edit-loading":
		m.addHistoryExecution(value, "edit", "", m.editToken)
	case "shell":
		m.addHistoryExecution(value, "shell", "", m.shellToken)
	default:
		switch {
		case slices.Contains([]string{"ctx", "context", "contexts"}, word):
			m.addHistoryExecution(value, "connect", "", 0)
		case word == "export":
			m.addHistoryExecution(value, "manifest", "", m.manifestSaveToken)
		case word == "forward":
			m.addHistoryExecution(value, "forward", "", m.forwardID)
		case m.eventGlobal:
			m.addHistoryExecution(value, "event-table", "", m.eventToken)
		default:
			m.addHistoryExecution(value, "snapshot", "", m.request)
		}
	}
	return cmd
}

// Match before Update handles the result: successful handlers may advance their
// cancellation token. This keeps history independent from UI rendering and from
// later background refresh failures, without wrapping Bubble Tea control messages.
func (m *model) historyExecutionFor(msg tea.Msg) *historyExecution {
	var key historyOperation
	valid := false
	switch v := msg.(type) {
	case bundleSavedMsg:
		key = historyOperation{"bundle", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.bundleToken && m.mode == "bundle-loading"
	case debugPreparedMsg:
		key = historyOperation{"debug", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.debugToken && m.mode == "debug-loading"
	case debugStartedMsg:
		key = historyOperation{"debug", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.debugToken && m.mode == "debug-starting"
	case debugClosedMsg:
		key = historyOperation{"debug", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.debugToken && m.mode == "shell"
	case connectedMsg:
		key = historyOperation{"connect", v.generation, 0}
		valid = v.generation == m.generation
	case snapshotMsg:
		key = historyOperation{"snapshot", v.generation, v.request}
		valid = v.generation == m.generation && v.request == m.request
	case manifestSavedMsg:
		key = historyOperation{"manifest", v.generation, v.token}
		valid = v.generation == m.generation
	case forwardMsg:
		key = historyOperation{"forward", v.generation, v.id}
		valid = v.generation == m.generation && m.forwards[v.id] != nil
	case actionResultMsg:
		key = historyOperation{"action", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.actionToken && m.mode == "action-running"
	case rolloutsMsg:
		key = historyOperation{"rollouts", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.rolloutToken && m.mode == "rollouts-loading"
	case accessMsg:
		key = historyOperation{"access", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.accessToken
	case overviewMsg:
		key = historyOperation{"overview", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.overviewToken
	case eventsMsg:
		key = historyOperation{"status", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.eventToken
	case eventPulseMsg:
		key = historyOperation{"event-table", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.eventToken && v.feed == m.eventFeed
	case logsMsg:
		key = historyOperation{"logs", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.logToken
	case logPulseMsg:
		key = historyOperation{"logs", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.logToken && v.feed == m.logFeed
	case relationsMsg:
		key = historyOperation{"relations", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.relationToken
	case editReadyMsg:
		key = historyOperation{"edit", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.editToken && m.mode == "edit-loading"
	case editorClosedMsg:
		key = historyOperation{"edit", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.editToken && m.mode == "editing"
	case editReviewedMsg:
		key = historyOperation{"edit", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.editToken && m.mode == "edit-loading"
	case editedMsg:
		key = historyOperation{"edit", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.editToken && m.mode == "edit-loading"
	case shellClosedMsg:
		key = historyOperation{"shell", v.generation, v.token}
		valid = v.generation == m.generation && v.token == m.shellToken && m.mode == "shell"
	}
	if !valid {
		return nil
	}
	return m.commandExecutions[key]
}

func (m *model) observeCommandResult(run *historyExecution, msg tea.Msg) {
	if run == nil {
		return
	}
	complete, success := true, false
	switch v := msg.(type) {
	case bundleSavedMsg:
		success = v.err == nil
	case debugPreparedMsg:
		complete = v.err != nil
	case debugStartedMsg:
		complete = v.err != nil
	case debugClosedMsg:
		success = v.err == nil && (!v.disconnected || v.connected)
	case connectedMsg:
		delete(m.commandExecutions, run.operation)
		if v.err == nil && m.dataErr == "" {
			kind, token := "snapshot", m.request
			if m.eventGlobal {
				kind, token = "event-table", m.eventToken
			}
			m.addHistoryExecution(run.value, kind, "", token, run.sequence)
		}
		return
	case snapshotMsg:
		success = v.err == nil
	case manifestSavedMsg:
		success = v.err == nil
	case forwardMsg:
		success = !v.done && m.forwards[v.id].state == "active"
	case actionResultMsg:
		success = v.err == nil
	case rolloutsMsg:
		if v.err == nil && v.revision > 0 && m.actionPlan != nil && m.mode == "action-confirm" {
			delete(m.commandExecutions, run.operation)
			m.addHistoryExecution(run.value, "action", "", m.actionToken, run.sequence)
			return
		}
		success = v.err == nil && v.revision == 0 && (m.mode == "picker" && m.pickerKind == "rollouts" || m.mode == "operation-error" && m.panelTitle == "Rollout history")
	case accessMsg:
		success = v.identityError == nil
		for _, line := range v.lines {
			success = success && line.err == nil
		}
	case overviewMsg:
		success = len(v.snapshot.Objects) > 0
	case eventsMsg:
		success = v.err == nil
		if strings.HasPrefix(run.value, "events") {
			success = success && v.eventsError == nil
		}
	case eventPulseMsg:
		complete = m.eventState == "live" || m.eventError != "" || m.eventState == "ended"
		success = m.eventState == "live" && m.eventError == ""
	case logsMsg:
		success = v.err == nil
	case logPulseMsg:
		v.feed.Lock()
		ready, done, err := v.feed.ready, v.feed.done, v.feed.err
		v.feed.Unlock()
		complete, success = ready || done, ready && err == nil
	case relationsMsg:
		success = v.err == nil && m.mode == "picker" && m.pickerKind == "relations"
	case editReadyMsg:
		complete = v.err != nil || m.mode != "editing"
	case editorClosedMsg:
		complete = v.err != nil || m.mode != "edit-loading"
	case editReviewedMsg:
		complete, success = v.err != nil || v.review == nil, v.err == nil && v.review == nil
	case editedMsg:
		success = v.err == nil
	case shellClosedMsg:
		success = v.err == nil
	}
	if complete {
		delete(m.commandExecutions, run.operation)
		if success {
			m.rememberCommandAt(run.value, run.sequence)
		}
	}
}
