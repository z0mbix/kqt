package tui

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
)

func scroll(v *viewport.Model, key string) bool {
	switch key {
	case "j", "down":
		v.ScrollDown(1)
	case "k", "up":
		v.ScrollUp(1)
	case "ctrl+d":
		v.HalfPageDown()
	case "ctrl+u":
		v.HalfPageUp()
	case "pgdown", "ctrl+f":
		v.PageDown()
	case "pgup", "ctrl+b":
		v.PageUp()
	case "g", "home":
		v.GotoTop()
	case "G", "end":
		v.GotoBottom()
	case "h", "left":
		v.ScrollLeft(4)
	case "l", "right":
		v.ScrollRight(4)
	default:
		return false
	}
	return true
}
func (m *model) startInput(mode, value string) tea.Cmd {
	m.historySearch = nil
	m.resize()
	m.mode = mode
	m.previousFilter = m.filter
	m.completion = completionState{}
	m.input.SetValue(value)
	m.input.CursorEnd()
	if mode == "command" {
		m.commandHistoryIndex = len(m.settings.Config.CommandHistory)
		m.commandDraft = value
		m.commandDraftCursor = m.input.Position()
	}
	m.input.Prompt = ":"
	if mode == "filter" {
		m.input.Prompt = "/"
	}
	return m.input.Focus()
}
func (m *model) key(msg tea.KeyPressMsg) tea.Cmd {
	m.cancelSelection()
	k := msg.String()
	if k == "ctrl+c" {
		m.close()
		return tea.Quit
	}
	if m.mode == "history-search" {
		return m.historySearchKey(msg)
	}
	if m.mode == "help-search" {
		return m.helpSearchKey(msg)
	}
	if m.mode == "help" {
		if k == "/" {
			return m.startHelpSearch()
		}
		if k == "tab" || k == "shift+tab" {
			m.helpNotesFocused = !m.helpNotesFocused
			return nil
		}
		if scroll(m.helpActivePane(), k) {
			return nil
		}
	}
	if m.mode == "bundle-save" || m.mode == "bundle-loading" {
		return m.bundleKey(msg)
	}
	if m.mode == "delete-confirm" {
		return m.deleteKey(msg)
	}
	if m.mode == "edit-review" {
		return m.editReviewKey(msg)
	}
	if m.showingDebug() {
		return m.debugKey(msg)
	}
	if m.mode == "manifest-save" {
		return m.manifestInputKey(msg)
	}
	if m.mode == "action-confirm" {
		return m.actionInputKey(msg)
	}
	if m.mode == "event-search" || m.mode == "event-save" {
		return m.eventsInputKey(msg)
	}
	if m.mode == "log-search" || m.mode == "log-save" {
		return m.logsInputKey(msg)
	}
	if m.mode == "log-since" || m.mode == "log-tail" {
		return m.logOptionsKey(msg)
	}
	if m.mode == "command" || m.mode == "filter" {
		if m.mode == "command" && k == "ctrl+r" {
			return m.startHistorySearch()
		}
		if m.mode == "command" && (k == "up" || k == "down") {
			m.recallCommand(k == "up")
			return nil
		}
		if m.mode == "command" && (k == "tab" || k == "shift+tab") {
			m.completeCommand(k == "shift+tab")
			return nil
		}
		m.completion = completionState{}
		if k == "esc" {
			if m.mode == "filter" {
				m.filter = m.previousFilter
				m.rebuild("")
			}
			m.mode = ""
			m.input.Blur()
			return nil
		}
		if k == "enter" {
			mode, value := m.mode, m.input.Value()
			m.mode = ""
			m.input.Blur()
			if mode == "command" {
				return m.executeSubmittedCommand(value)
			}
			return nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if m.mode == "filter" {
			m.filter = m.input.Value()
			m.rebuild("")
		}
		return cmd
	}
	if m.mode == "picker" {
		return m.pickerKey(msg)
	}
	if m.mode == "edit-loading" || m.mode == "editing" {
		if k == "esc" || k == "q" {
			m.cancelEdit()
			m.mode = ""
			m.notice = "Edit cancelled"
			return m.refreshResources()
		}
		return nil
	}
	if k == "z" && (m.mode == "" || m.mode == "logs" || m.mode == "events") {
		m.toggleHeader()
		return nil
	}
	if m.mode != "" {
		if k == "ctrl+r" && m.mode == "operation-error" && (m.panelTitle == "Rollout history" || m.panelTitle == "Rollout history unavailable") {
			return m.openRollouts(0)
		}
		if m.mode == "action-running" && (k == "esc" || k == "q") {
			m.cancelAction()
			m.mode = ""
			m.resize()
			m.notice = "Action request cancelled; refresh to check the final state"
			return m.refreshResources()
		}
		if m.mode == "rollout-details" {
			switch k {
			case "esc":
				return m.openPicker("rollouts")
			case "d":
				m.showRollout(m.rolloutSelected, true)
				return nil
			case "y":
				m.showRollout(m.rolloutSelected, false)
				return nil
			case "r":
				return m.confirmRollback(m.rolloutSelected)
			case "ctrl+r":
				return m.openRollouts(0)
			}
		}
		if m.mode == "overview" {
			switch k {
			case "ctrl+r":
				return m.openOverview()
			case "enter":
				return m.overviewProblems()
			case "0":
				m.options.Namespace = ""
				m.options.AllNamespaces = true
				r := m.route
				r.namespace = ""
				cmd := m.navigate(r, false)
				return tea.Batch(cmd, m.openOverview())
			case "n":
				return m.changeResource("namespaces", true)
			}
		}
		if (m.mode == "access" || m.mode == "access-loading") && k == "ctrl+r" {
			return m.openAccess(m.accessQuery)
		}
		if k == "esc" || k == "q" {
			m.cancelDebug()
			m.cancelRollouts()
			if k == "esc" && m.mode == "logs" && m.logFullScreen {
				m.toggleLogFullScreen()
				return nil
			}
			m.cancelOverview()
			m.cancelAccess()
			m.cancelRelations()
			if m.mode == "details" {
				m.clearSecret()
				m.updateInspector()
			}
			m.closeLog()
			m.logFullScreen = false
			if !m.eventGlobal {
				m.closeEvents()
			}
			m.mode = ""
			m.resize()
			if m.route.resource.Kind == "Event" && m.eventFeed == nil {
				return m.followEventTable()
			}
			return nil
		}
		if m.mode == "logs" {
			switch k {
			case "O":
				return m.openPicker("log-options")
			case "T":
				m.toggleLogTimestamps()
				return nil
			case "N":
				m.toggleLogPrefixes()
				return nil
			case "/":
				return m.logsInput("log-search")
			case "o":
				return m.logsInput("log-save")
			case "f":
				m.toggleLogFullScreen()
				return nil
			case "F":
				m.logFollow = !m.logFollow
				m.logPrevious = false
				return m.loadLogs()
			case "s":
				m.logTail = !m.logTail
				m.settings.Config.Logs.Follow = m.logTail
				m.saveLogPreference("follow", m.logTail)
				if m.logTail {
					m.logPaused = false
				}
				if !m.logPaused {
					m.logFrozen = nil
					m.renderLogs()
				} else {
					m.logTitle()
				}
				return nil
			case "space", " ":
				if !m.logPaused {
					m.logFrozen = slices.Clone(m.logLines)
					m.logFrozenDropped = m.logDropped
				}
				m.logPaused = !m.logPaused
				m.logTitle()
				if !m.logPaused {
					m.logFrozen = nil
					m.renderLogs()
				}
				return nil
			case "G", "end":
				m.logTail = true
				m.logTitle()
			case "k", "up", "g", "home", "ctrl+u", "pgup", "ctrl+b":
				m.logTail = false
				m.logTitle()
			}
			if k == "w" {
				m.logWrap = !m.logWrap
				m.settings.Config.Logs.Wrap = m.logWrap
				m.saveLogPreference("wrap", m.logWrap)
				m.panel.SoftWrap = m.logWrap
				m.panel.SetYOffset(m.panel.YOffset())
				m.logTitle()
				if m.logTail {
					m.panel.GotoBottom()
				}
				return nil
			}
			if k == "ctrl+r" {
				return m.loadLogs()
			}
			if k == "p" {
				m.logPrevious = !m.logPrevious
				return m.loadLogs()
			}
			if k == "c" {
				return m.openPicker("containers")
			}
		}
		if m.mode == "events" {
			cmd := m.eventsKey(k)
			if cmd != nil || k == "space" || k == " " || k == "G" || k == "end" {
				return cmd
			}
		}
		if m.mode == "forward-details" && k == "x" {
			m.stopForward(m.forwardDetail)
			m.showForward(m.forwardDetail)
			return nil
		}
		if m.mode == "details" {
			switch k {
			case "C":
				return m.copyManifest(m.manifestFormat(), false)
			case "o":
				return m.promptManifestExport()
			case "x":
				if o, ok := m.current(); ok && (o.GetKind() == "Pod" || o.GetKind() == "Service") {
					m.stopResourceForwards()
					return nil
				}
				return m.toggleSecret()
			case "J":
				m.openDetails(false, true)
				return nil
			case "y", "v":
				m.openDetails(true, false)
				return nil
			case "e":
				return m.openEditor()
			}
		}
		scroll(&m.panel, k)
		return nil
	}
	if k == "q" {
		m.close()
		return tea.Quit
	}
	if m.eventGlobal && m.route.resource.Kind == "Event" {
		cmd := m.eventsKey(k)
		if cmd != nil || k == "space" || k == " " || k == "G" || k == "end" {
			return cmd
		}
	}
	if k == "x" {
		if o, ok := m.current(); ok && (o.GetKind() == "Pod" || o.GetKind() == "Service") {
			m.stopResourceForwards()
			return nil
		}
		return m.toggleSecret()
	}
	if k == "tab" || k == "shift+tab" {
		if m.inspectorDimensions().width > 0 {
			m.inspectorFocused = !m.inspectorFocused
			if m.eventGlobal && m.inspectorFocused {
				m.eventTail = false
			}
		}
		return nil
	}
	if m.inspectorFocused {
		if k == "J" {
			m.setInspectorFormat("json")
			return nil
		}
		if k == "y" {
			m.setInspectorFormat("yaml")
			return nil
		}
		if k == "esc" {
			m.inspectorFocused = false
			return nil
		}
		if scroll(&m.inspector, k) {
			return nil
		}
	}
	switch k {
	case "B":
		return m.openDebug("")
	case "U":
		return m.openOverview()
	case "H":
		return m.openRollouts(0)
	case "A":
		return m.openAccess("")
	case "a":
		return m.openPicker("actions")
	case "D":
		return m.actionCommand("delete", "")
	case "C":
		return m.copyManifest(m.manifestFormat(), false)
	case "o":
		return m.promptManifestExport()
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "ctrl+f", "pgdown":
		m.move(max(1, m.tableBounds().height-3))
	case "ctrl+b", "pgup":
		m.move(-max(1, m.tableBounds().height-3))
	case "ctrl+d":
		m.move(max(1, (m.tableBounds().height-3)/2))
	case "ctrl+u":
		m.move(-max(1, (m.tableBounds().height-3)/2))
	case "g", "home":
		m.move(-len(m.visible))
	case "G", "end":
		m.move(len(m.visible))
	case "h", "left":
		m.horizontal = max(0, m.horizontal-4)
	case "l", "right":
		m.horizontal += 4
	case ":":
		return m.startInput("command", "")
	case "/":
		return m.startInput("filter", m.filter)
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.rebuild("")
		} else if len(m.history) > 0 {
			r := m.history[len(m.history)-1]
			m.history = m.history[:len(m.history)-1]
			return m.navigate(r, false)
		}
	case "enter":
		return m.drillDown()
	case "d":
		m.openDetails(false, false)
	case "y", "v":
		m.openDetails(true, false)
	case "J":
		m.openDetails(false, true)
	case "e":
		return m.openEditor()
	case "b":
		return m.openRelations()
	case "L":
		return m.openLogs(false)
	case "P":
		return m.openLogs(true)
	case "S", "E":
		return m.openEvents()
	case "M":
		return m.openResourcePanel(resourceLabelsTab)
	case "m":
		return m.toggleMetrics()
	case "f":
		return m.promptForward()
	case "F":
		return m.openPicker("forwards")
	case "n":
		return m.changeResource("namespaces", true)
	case "c":
		return m.openPicker("contexts")
	case "t":
		return m.openPicker("themes")
	case "r":
		return m.openPicker("resources")
	case "0":
		m.options.AllNamespaces = true
		m.options.Namespace = ""
		r := m.route
		r.namespace = ""
		return m.navigate(r, false)
	case "w":
		m.wide = !m.wide
	case "s":
		return m.openShell()
	case "R":
		m.setOrder(m.sortColumn, !m.descending)
	case "[", "shift+left":
		m.cycleOrder(-1)
	case "]", "shift+right":
		m.cycleOrder(1)
	case "O":
		m.cycleOrder(1)
	case "i":
		m.toggleInspector()
	case "I":
		return m.openPicker("inspector-position")
	case "ctrl+r":
		if m.backend == nil {
			return m.connect(m.contextName)
		}
		return m.refreshResources()
	case "?":
		return m.openHelp()
	}
	return nil
}
func (m *model) changeResource(name string, remember bool) tea.Cmd {
	r, err := kube.Resolve(m.resources, name)
	if err != nil {
		m.notice = kube.Safe(err.Error())
		return nil
	}
	target := m.route
	target.resource = r
	target.fields = ""
	target.labels = ""
	target.selection = ""
	return m.navigate(target, remember)
}
func (m *model) drillDown() tea.Cmd {
	o, ok := m.current()
	if !ok {
		return nil
	}
	if o.GetKind() == "Namespace" {
		m.options.AllNamespaces = false
		m.options.Namespace = o.GetName()
		r, _ := kube.Resolve(m.resources, "pods")
		return m.navigate(route{resource: r, namespace: o.GetName()}, true)
	}
	if selector, fields, ok := kube.PodSelector(o); ok {
		r, _ := kube.Resolve(m.resources, "pods")
		ns := o.GetNamespace()
		if o.GetKind() == "Node" {
			ns = ""
		}
		return m.navigate(route{resource: r, namespace: ns, labels: selector, fields: fields}, true)
	}
	m.openDetails(false, false)
	return nil
}
func (m *model) openDetails(manifest, asJSON bool) {
	_, ok := m.current()
	if !ok {
		return
	}
	m.mode = "details"
	m.panel.SoftWrap = false
	m.detailFormat = "describe"
	if manifest {
		m.detailFormat = "yaml"
	}
	if asJSON {
		m.detailFormat = "json"
	}
	m.renderDetails()
	m.panel.GotoTop()
}
func (m *model) renderDetails() {
	if m.mode != "details" {
		return
	}
	o, ok := m.current()
	if !ok {
		m.panelTitle = "Details"
		m.panel.SetContent("No resource selected.")
		return
	}
	offset := m.panel.YOffset()
	m.panelTitle = "Describe · " + o.GetName()
	content := m.describe(o)
	if m.detailFormat == "yaml" || m.detailFormat == "json" {
		asJSON := m.detailFormat == "json"
		m.panelTitle = "YAML · " + o.GetName()
		if asJSON {
			m.panelTitle = "JSON · " + o.GetName()
		}
		content = m.manifest(o, asJSON)
		if asJSON {
			content = m.theme.renderJSON(content)
		} else {
			content = m.theme.renderYAML(content)
		}
	}
	if o.GetKind() == "Secret" {
		format, _, _ := strings.Cut(m.panelTitle, " · ")
		m.panelTitle = format + " · " + m.secretLabel() + " · " + o.GetName()
	}
	m.panel.SetContent(content)
	m.panel.SetYOffset(offset)
}
func (m *model) openLogs(previous bool) tea.Cmd {
	o, ok := m.current()
	if !ok || !slices.Contains([]string{"Pod", "Deployment", "StatefulSet"}, o.GetKind()) || m.backend == nil {
		m.notice = "Select a pod, deployment, or statefulset to read logs (L)."
		return nil
	}
	m.logObject = o
	m.logSearch = ""
	m.logFullScreen = false
	m.logPrevious = previous
	_, m.logFollow = m.backend.(kube.LogStreamer)
	containers := kube.Containers(o)
	if len(containers) == 0 {
		m.notice = "Resource has no containers."
		return nil
	}
	m.logContainer = containers[0]
	if len(containers) > 1 {
		return m.openPicker("containers")
	}
	return m.loadLogs()
}
func (m *model) command(value string) tea.Cmd {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return nil
	}
	cmd := fields[0]
	arg := strings.TrimSpace(strings.TrimPrefix(value, cmd))
	m.notice = ""
	if arg != "" && slices.Contains([]string{"q", "quit", "resources", "api", "actions", "permissions", "access", "overview", "related", "relations", "status", "shell", "forwards", "edit", "refresh", "help"}, cmd) {
		m.notice = ":" + cmd + " does not take arguments"
		return nil
	}
	switch cmd {
	case "q", "quit":
		m.close()
		return tea.Quit
	case "ctx", "context", "contexts":
		if arg == "" {
			return m.openPicker("contexts")
		}
		resolved, err := m.resolveContext(arg)
		if err != nil {
			m.notice = kube.Safe(err.Error())
			return nil
		}
		arg = resolved
		if m.options.Demo {
			if arg != "demo" {
				m.notice = "Demo mode has one sample context."
				return nil
			}
		} else {
			found := false
			for _, c := range m.loader.Contexts() {
				if c.Name == arg {
					found = true
				}
			}
			if !found {
				m.notice = "Unknown context: " + kube.Safe(arg)
				return nil
			}
		}
		return m.connect(arg)
	case "ns", "namespace":
		if arg == "" {
			return m.changeResource("namespaces", true)
		}
		all := arg == "-A" || arg == "all" || arg == "*"
		if !all && len(validation.IsDNS1123Label(arg)) > 0 {
			m.notice = "Invalid namespace: " + kube.Safe(arg)
			return nil
		}
		m.options.AllNamespaces = all
		m.options.Namespace = arg
		r := m.route
		r.namespace = arg
		if all {
			r.namespace = ""
			m.options.Namespace = ""
		}
		r.fields = ""
		r.labels = ""
		return m.navigate(r, false)
	case "alias":
		return m.aliasCommand(arg, false)
	case "unalias":
		return m.aliasCommand(arg, true)
	case "theme", "themes":
		if arg == "" {
			return m.openPicker("themes")
		}
		t, ok := m.settings.Themes[arg]
		if !ok {
			m.notice = "Unknown theme: " + kube.Safe(arg)
			return nil
		}
		return m.selectTheme(t)
	case "resources", "api":
		return m.openPicker("resources")
	case "actions":
		return m.openPicker("actions")
	case "permissions", "access":
		return m.openAccess("")
	case "overview":
		return m.openOverview()
	case "rollout", "rollouts":
		return m.rolloutCommand(arg, false)
	case "rollback":
		return m.rolloutCommand(arg, true)
	case "can-i":
		return m.openAccess(arg)
	case "scale", "restart", "suspend", "resume", "run", "delete":
		return m.actionCommand(cmd, arg)
	case "copy":
		return m.manifestCommand(arg, true)
	case "export":
		return m.manifestCommand(arg, false)
	case "download-dir":
		m.downloadDirectoryCommand(arg)
	case "related", "relations":
		return m.openRelations()
	case "layout", "layouts":
		if arg == "" {
			return m.openPicker("layouts")
		}
		if strings.HasPrefix(arg, "save ") {
			m.saveLayout(strings.TrimSpace(strings.TrimPrefix(arg, "save ")))
		} else {
			m.selectLayout(arg)
		}
	case "write":
		m.saveLayout(arg)
	case "header":
		m.headerCommand(arg)
	case "inspector":
		switch arg {
		case "", "position":
			return m.openPicker("inspector-position")
		case "json", "yaml":
			m.setInspectorFormat(arg)
		case "right":
			m.placeInspector("right")
		case "bottom":
			m.placeInspector("bottom")
		case "none", "off":
			m.setInspectorVisibility(false)
		case "on":
			m.setInspectorVisibility(true)
		case "toggle":
			m.toggleInspector()
		default:
			m.notice = "Use :inspector [position|right|bottom|on|off|toggle|json|yaml]"
		}
	case "sort":
		return m.sortCommand(arg)
	case "columns":
		cols := strings.FieldsFunc(arg, func(r rune) bool { return r == ',' || r == ' ' })
		if len(cols) == 0 || len(cols) > 32 {
			m.notice = "Use :columns name,status,age (up to 32 columns)"
			return nil
		}
		for _, c := range cols {
			if !validColumn(c) {
				m.notice = "Invalid column: " + kube.Safe(c)
				return nil
			}
		}
		m.saveColumns(viewConfig{Columns: cols, Sort: m.sortColumn, Descending: m.descending})
		m.horizontal = 0
	case "label", "labels":
		if _, err := labels.Parse(arg); err != nil {
			m.notice = kube.Safe(err.Error())
			return nil
		}
		r := m.route
		r.labels = arg
		return m.navigate(r, false)
	case "logs":
		if arg != "" && arg != "previous" {
			m.notice = "Use :logs or :logs previous"
			return nil
		}
		return m.openLogs(arg == "previous")
	case "events":
		if arg == "selected" {
			return m.openEvents()
		}
		if arg != "" {
			m.notice = "Use :events or :events selected"
			return nil
		}
		return m.changeResource("events", true)
	case "status":
		return m.openEvents()
	case "bundle":
		return m.bundleCommand(arg)
	case "diagnose":
		if arg != "" {
			m.notice = "Use :diagnose on a Pod or Service"
			return nil
		}
		if o, ok := m.current(); !ok || (o.GetKind() != "Pod" && o.GetKind() != "Service") {
			m.notice = "Select a Pod or Service for diagnostics"
			return nil
		}
		return m.openResourcePanel(resourceDiagnosticsTab)
	case "metadata":
		tab := resourceLabelsTab
		switch arg {
		case "", "labels":
		case "annotations":
			tab = resourceAnnotationsTab
		case "finalizers":
			tab = resourceFinalizersTab
		case "status":
			tab = resourceStatusTab
		default:
			m.notice = "Use :metadata labels, annotations, finalizers, or status"
			return nil
		}
		return m.openResourcePanel(tab)
	case "metrics":
		if arg == "on" {
			m.metricsEnabled = false
		} else if arg == "off" {
			m.metricsEnabled = true
		} else if arg != "" {
			m.notice = "Use :metrics on or off"
			return nil
		}
		return m.toggleMetrics()
	case "shell":
		return m.openShell()
	case "debug":
		return m.openDebug(arg)
	case "forward":
		if arg == "" {
			return m.promptForward()
		}
		return m.forwardCommand(arg)
	case "forwards":
		return m.openPicker("forwards")
	case "edit":
		return m.openEditor()
	case "refresh":
		if m.backend == nil {
			return m.connect(m.contextName)
		}
		return m.refreshResources()
	case "help":
		return m.openHelp()
	default:
		if arg != "" {
			m.notice = "Resource commands do not take arguments; use :ns or :labels to set scope"
			return nil
		}
		return m.changeResource(cmd, true)
	}
	return nil
}
func (m *model) openPicker(kind string) tea.Cmd {
	m.cancelInspectorPreview()
	m.pickerReturn = ""
	if (kind == "containers" || kind == "log-options") && m.mode == "logs" {
		m.pickerReturn = "logs"
	}
	m.previousTheme = m.theme
	m.pickerKind = kind
	m.mode = "picker"
	m.choices = nil
	m.pickerIndex = 0
	switch kind {
	case "inspector-position":
		m.beginInspectorPreview()
		m.choices = []choice{{"right", "beside the resource list"}, {"bottom", "below the resource list"}}
		if m.inspectorPlacement() == "bottom" {
			m.pickerIndex = 1
		}
	case "debug-image-source", "debug-profiles", "debug-targets", "debug-attach":
		m.choices = m.debugChoices(kind)
	case "log-options":
		m.choices = m.logOptionChoices()
	case "rollouts":
		m.choices = m.rolloutChoices()
	case "actions":
		if o, ok := m.current(); ok {
			for _, action := range kube.Actions(o.GetKind()) {
				description := "requires confirmation"
				if m.options.ReadOnly {
					description = "disabled in read-only mode"
				}
				m.choices = append(m.choices, choice{action, description})
			}
		}
	case "relations":
		for _, link := range m.relations {
			m.choices = append(m.choices, choice{link.Key(), link.Title() + " · " + link.Reason})
		}
	case "sort":
		for i, column := range m.columns() {
			description := "sort ascending"
			if column == m.sortColumn {
				direction, next := "ascending", "descending"
				if m.descending {
					direction, next = next, direction
				}
				description = direction + " · select to sort " + next
				m.pickerIndex = i
			}
			m.choices = append(m.choices, choice{column, description})
		}
	case "forwards":
		m.refreshForwardChoices()
	case "shell-containers":
		for _, c := range kube.RunningContainers(m.shellObject) {
			m.choices = append(m.choices, choice{c, "running pod container"})
		}
	case "themes":
		for _, name := range themeNames(m.settings.Themes) {
			m.choices = append(m.choices, choice{name, "terminal palette"})
		}
	case "layouts":
		var names []string
		for name := range m.settings.Layouts {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			p := m.settings.Layouts[name]
			description := "inspector: " + p.Inspector
			if name == m.layout {
				description += " · active"
			}
			m.choices = append(m.choices, choice{name, description})
		}
	case "contexts":
		m.choices = m.contextChoices()
	case "resources":
		for _, r := range m.resources {
			scope := "cluster"
			if r.Namespaced {
				scope = "namespaced"
			}
			m.choices = append(m.choices, choice{r.ID(), r.Kind + " · " + scope})
		}
	case "containers":
		for _, c := range kube.Containers(m.logObject) {
			m.choices = append(m.choices, choice{c, "pod container"})
		}
		if len(kube.Containers(m.logObject)) > 1 {
			m.choices = append(m.choices, choice{"<all containers>", "combine all pod containers"})
		}
	}
	m.input.Prompt = "/ "
	m.input.SetValue("")
	m.resize()
	m.filterChoices()
	return m.input.Focus()
}
func (m *model) filterChoices() {
	m.matches = nil
	q := m.input.Value()
	type rankedChoice struct {
		choice choice
		score  int
	}
	var matches []rankedChoice
	for _, c := range m.choices {
		if score, ok := fuzzyScore(q, c.name+" "+c.description); ok {
			matches = append(matches, rankedChoice{c, score})
		}
	}
	if q != "" {
		slices.SortStableFunc(matches, func(a, b rankedChoice) int { return b.score - a.score })
	}
	for _, c := range matches {
		m.matches = append(m.matches, c.choice)
	}
	m.pickerIndex = min(m.pickerIndex, max(0, len(m.matches)-1))
	m.previewInspectorPosition()
}
func (m *model) pickerKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+r":
		if m.pickerKind == "rollouts" {
			return m.openRollouts(0)
		}
		return nil
	case "esc":
		if strings.HasPrefix(m.pickerKind, "debug-") && m.pickerReturn != "debug-confirm" {
			m.cancelDebug()
		}
		if m.pickerKind == "containers" || m.pickerKind == "shell-containers" {
			kind := "logs"
			if m.pickerKind == "shell-containers" {
				kind = "shell"
			}
			for key, run := range m.commandExecutions {
				if key.kind == kind && run.phase == "picker" {
					delete(m.commandExecutions, key)
				}
			}
		}
		if m.pickerKind == "themes" {
			m.setTheme(m.previousTheme)
		}
		m.cancelInspectorPreview()
		m.mode = m.pickerReturn
		m.input.Blur()
		m.resize()
		if m.mode == "debug-confirm" && m.debug != nil {
			m.renderDebugPreview()
		}
		if m.showingLogs() && m.logTail && !m.logPaused {
			m.panel.GotoBottom()
		}
		return nil
	case "up", "ctrl+k":
		m.pickerIndex = max(0, m.pickerIndex-1)
	case "down", "ctrl+j":
		m.pickerIndex = min(max(0, len(m.matches)-1), m.pickerIndex+1)
	case "pgup":
		m.pickerIndex = max(0, m.pickerIndex-8)
	case "pgdown":
		m.pickerIndex = min(max(0, len(m.matches)-1), m.pickerIndex+8)
	case "x":
		if m.pickerKind == "forwards" && len(m.matches) > 0 {
			id, _ := strconv.ParseUint(m.matches[m.pickerIndex].name, 10, 64)
			m.stopForward(id)
			m.refreshForwardChoices()
			return nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.filterChoices()
		return cmd
	case "enter":
		if len(m.matches) == 0 {
			return nil
		}
		c := m.matches[m.pickerIndex]
		m.mode = ""
		m.input.Blur()
		switch m.pickerKind {
		case "debug-image-source":
			if c.name == "Use BusyBox" {
				return m.useBusyBoxDebugImage()
			}
			return m.promptDebugImage()
		case "debug-profiles":
			return m.chooseDebugProfile(c.name)
		case "debug-targets":
			m.debug.target = c.name
			if c.name == "<pod network>" {
				m.debug.target = ""
			}
			return m.prepareDebug()
		case "debug-attach":
			m.debug.attach = c.name
			return m.prepareDebug()
		case "log-options":
			return m.chooseLogOption(c.name)
		case "actions":
			return m.actionCommand(c.name, "")
		case "relations":
			return m.selectRelation(c.name)
		case "rollouts":
			n, _ := strconv.ParseInt(strings.TrimPrefix(c.name, "revision "), 10, 64)
			m.showRollout(n, false)
			return nil
		case "sort":
			m.chooseOrder(c.name)
		case "shell-containers":
			m.shellContainer = c.name
			return m.runShell()
		case "forwards":
			id, _ := strconv.ParseUint(c.name, 10, 64)
			m.showForward(id)
			return nil
		case "themes":
			return m.selectTheme(m.settings.Themes[c.name])
		case "layouts":
			m.selectLayout(c.name)
		case "inspector-position":
			m.placeInspector(c.name)
		case "contexts":
			name, err := m.resolveContext(c.name)
			if err != nil {
				m.notice = kube.Safe(err.Error())
				return nil
			}
			return m.connect(name)
		case "resources":
			return m.changeResource(c.name, true)
		case "containers":
			m.logContainer = c.name
			if c.name == "<all containers>" {
				m.logContainer = "*"
			}
			return m.loadLogs()
		}
		return nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.pickerIndex = 0
		m.filterChoices()
		return cmd
	}
	if m.pickerKind == "themes" && len(m.matches) > 0 {
		m.setTheme(m.settings.Themes[m.matches[m.pickerIndex].name])
	}
	m.previewInspectorPosition()
	return nil
}

const helpText = `kqt · A Kubernetes Query Tool

Navigation
  j/k, arrows        Move through resources
  g/G, Home/End      First / last row
  Ctrl+F/B, PgDn/Up  Full page down / up in lists and focused panels
  Ctrl+D/U          Half page down / up in lists and focused panels
  h/l, left/right    Scroll wide tables and manifests
  Shift+Left/Right   Sort previous / next column (also [ / ])
  O / R              Sort next column / reverse current order
  Mouse              Click column to sort, click row to select, wheel to scroll
  Enter              Workload/service/node -> pods; namespace -> scope
  Escape             Close, clear filter, or go back
  Tab                Focus inspector (outside a command)

Explore
  :                  Command prompt; Tab / Shift+Tab cycle completions
  Up / Down          Recall last 50 commands in the command prompt
  Ctrl+R in :        Reverse search successful command history
  History search     Ctrl+R/S older/newer; Enter runs, Tab edits, Esc cancels
  /                  Fuzzy search: web0003 matches web-7c9f6d8b5-0003
  n / :ns            Browse namespaces; Enter selects one
  :ns NAME / :ns -A   Set namespace / all namespaces (also 0)
  c / :ctx           Search kubeconfig contexts (session only)
  :alias NAME [CTX]  Save a short context name; :unalias NAME removes it
  r / :resources     Search discovered APIs / CRDs
  b / :related       Browse owners, backends, pods and storage relationships
  a / :actions       Scale/restart workloads; suspend/resume/run CronJobs
  D / :delete        Confirm deletion of the selected resource
  A / :permissions   Inspect identity and resource permissions
  U / :overview      Cluster health, capacity, usage and recent warnings
  H / :rollouts      Deployment revisions, images and template comparison
  Rollout details    d compare, y template, r confirmed rollback; Ctrl+R reload
  :rollback REV      Confirm restoring a retained Deployment revision
  :can-i VERB        Check a selected resource action (optional subresource)
  :po :deploy :svc    Resource names, kinds, and aliases
  :labels app=web    Kubernetes label selector; :labels clears it
  d                  Describe resource with highlighted YAML
  C / o              Copy / export selected manifest (also in YAML/JSON)
  :copy json clean   Copy reusable JSON; :export yaml clean PATH saves YAML
  y / v, J           Open highlighted YAML / JSON
  Inspector: J / y   Switch focused inspector to JSON / YAML
  Details: J / y     Switch open manifest panel to JSON / YAML
  Secrets: x         Toggle decoded values; hidden by default
  L / P              Logs / previous logs: pod, deployment, statefulset
  S / :status        Status, conditions, containers, and live events (E also works)
  M / :metadata      Labels, annotations and finalizers in the status panel
  Resource panel     1 status, 2 labels, 3 annotations, 4 finalizers; Tab cycles
  5 / :diagnose      Pod scheduling or Service backends, ports and selectors
  :bundle [--logs] [PATH]  Export diagnostics; logs opt-in, Secrets excluded
  :events            Live namespace event table (0 = all namespaces)
  Events             Space pause, / filter, G follow latest, o save
  m / :metrics       Toggle pod/node CPU and memory columns
  s / :shell         Shell in a running pod/container; Ctrl+] disconnects
  B / :debug         Ephemeral debug container using a configured image profile
  :debug PROFILE     Use a saved image; Enter starts on the selected pod
  Debug preview      c changes the target container; d shows full details
  :debug attach NAME Reattach an existing debugger; Ctrl+] leaves it running
  f                  Forward a pod/service port (LOCAL:REMOTE; LOCAL 0 auto)
  x (pod/service)    Stop forwards for the selected resource
  F / :forwards      Manage forwards; x stops the selected forward
  Logs: w / s / f    Toggle wrapping / follow latest / logs-only terminal
  Logs: c / p / F    Container / previous instance / live or snapshot
  Logs: O / T / N    Saved time/line options / timestamps / source prefixes
  Logs: /            Filter text; include:TEXT !TEXT re:REGEX (quote spaces)
  Log fields         level>=warn, level=warn,error, request_id=abc, http.status=500
  Log filters        Terms combine; ! excludes; empty input clears the filter
  Drag text          Select and copy readable text; bottom-right confirmation
  e / :edit          Edit fresh resource in configured editor or $EDITOR
  Edit review        Highlighted diff; Enter applies, Escape discards
  D / :delete        Compact Yes/No confirmation; No selected initially

Appearance and saved settings
  z / :header        Toggle the explorer header; saved between sessions
  :header on|off     Show / hide the header (:header toggle also works)
  :download-dir PATH  Default directory for all file exports; clear resets it
  t / :themes        Fuzzy theme picker; Escape cancels previews
  :layouts           Search and apply saved layout profiles
  :layout NAME       Apply layout directly
  :write NAME        Save current arrangement and columns as a layout
  i                  Toggle inspector off/on; remember its position
  I / :inspector     Preview right/bottom placement; Enter saves, Esc restores
  :inspector json    Also yaml, right, bottom, on, off, toggle (none = off)
  w                  Wide fields
  :sort              Column picker; :sort COLUMN asc|desc sets an order
  :columns name,status,age   Configure columns for the active layout
  /spec/replicas     JSON Pointer column (also supported in config)
  Pickers            Type to search; arrows / Ctrl+J/K move; Enter selects

Refresh and exit
  Ctrl+R / :refresh  Refresh resources or retry connection
  ? / :help          This help; / fuzzy searches shortcuts, descriptions and notes
  q / :q / Ctrl+C    Quit
`

const helpNotesText = `Preferences
View and namespace are saved per context in ~/.config/kqt/config.yaml.
Themes, layouts and inspector format are saved when applied.
The theme name is shown for ten seconds after selection.

Security
Kubeconfig is never modified. Secret values are hidden by default; x reveals
decoded values for the selected Secret. x, closing details, selection/context
changes, and editing hide them again. The reveal state is never saved.

Editing
Editing reads a fresh full object and shows a diff before applying a versioned
patch. Errors/conflicts retain the private file. Unchanged edits do not write.

Logs
Live logs: Space pauses, / searches, o saves, F switches live/snapshot.
w wraps, s follows latest lines, f fills the terminal; Esc leaves focus first.
Snapshots: last 500 lines / 1 MiB per source; aggregate at most 4 MiB.
Live logs retain 4 MiB / 10,000 lines and follow at most 64 containers.

Clipboard
Drag copies visible text using the terminal's OSC52 clipboard support.

Metrics
Metrics require the cluster Metrics API. cpu%/memory% compare pod limits
or node allocatable capacity; missing limits/samples display a dash.

Refresh and forwarding
Tables refresh every 2s by default. Forwarding binds 127.0.0.1 and ends on context
switch or quit. :forward stop ID / all stops a forward without opening it.
`
