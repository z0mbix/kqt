package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Options struct {
	ReadOnly, ReadOnlyExplicit       bool
	Demo                             bool
	Context, Namespace, View, Labels string
	AllNamespaces                    bool
	ScopeExplicit                    bool
}
type route struct {
	resource                             kube.Resource
	namespace, labels, fields, selection string
}
type choice struct{ name, description string }
type session struct {
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	backend kube.Backend
	closed  bool
}

func newSession(ctx context.Context) *session {
	child, cancel := context.WithCancel(ctx)
	return &session{ctx: child, cancel: cancel}
}
func (s *session) attach(b kube.Backend) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		if b != nil {
			b.Close()
		}
		return
	}
	s.backend = b
}
func (s *session) close() {
	if s == nil {
		return
	}
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.backend != nil {
		s.backend.Close()
		s.backend = nil
	}
}

type connectedMsg struct {
	generation                 uint64
	backend                    kube.Backend
	resources                  []kube.Resource
	server, namespace, warning string
	err                        error
}
type snapshotMsg struct {
	generation, request uint64
	rows                []unstructured.Unstructured
	err                 error
	usage               map[string]kube.Usage
	metricsError        string
}
type pollMsg struct{ generation, request uint64 }
type themeHiddenMsg struct{ token uint64 }
type logsMsg struct {
	generation, token uint64
	content           string
	err               error
}

type model struct {
	namespaceGroup                           *namespaceGroupForm
	deleteYes                                bool
	debug                                    *debugState
	debugToken                               uint64
	debugCancel                              context.CancelFunc
	historySearch                            *historySearch
	historySuccesses                         []historySuccess
	commandSequence                          uint64
	commandExecutions                        map[historyOperation]*historyExecution
	commandConfigError                       bool
	manifestSaveToken                        uint64
	rolloutToken                             uint64
	rolloutCancel                            context.CancelFunc
	rollouts                                 kube.RolloutHistory
	rolloutSelected                          int64
	overviewToken                            uint64
	overviewContext                          context.Context
	overviewCancel                           context.CancelFunc
	overview                                 kube.Overview
	accessToken                              uint64
	accessCancel                             context.CancelFunc
	accessScope, accessQuery                 string
	resourceToken                            uint64
	resourceCancel                           context.CancelFunc
	resourceContext                          context.Context
	resourceFeed                             *resourceFeed
	resourceCache, resourceStaging           map[string]unstructured.Unstructured
	resourceNamespaceStaging                 map[string]map[string]unstructured.Unstructured
	resourceWatchState, resourceWatchError   string
	actionToken                              uint64
	actionCancel                             context.CancelFunc
	actionPlan                               *kube.ActionPlan
	manifestContent, manifestReturn          string
	relationToken                            uint64
	relationCancel                           context.CancelFunc
	relations                                []kube.Relation
	forwardDetail                            uint64
	ctx                                      context.Context
	settings                                 *Settings
	options                                  Options
	loader                                   *kube.Loader
	session                                  *session
	backend                                  kube.Backend
	resources                                []kube.Resource
	contextName, server                      string
	generation, request, logToken            uint64
	requestCancel, logCancel                 context.CancelFunc
	busy                                     bool
	failures                                 int
	lastRefresh                              time.Time
	dataErr, notice, discoveryWarning        string
	namespaceWarning                         string
	route                                    route
	history                                  []route
	rows, visible                            []unstructured.Unstructured
	selected, horizontal                     int
	filter, previousFilter, sortColumn       string
	descending, wide                         bool
	orderSelected                            string
	sortExplicit                             bool
	theme, previousTheme                     theme
	layout                                   string
	views                                    map[string]viewConfig
	inspectorFormat                          string
	themeUntil                               time.Time
	themeTimer                               uint64
	completion                               completionState
	commandHistoryIndex                      int
	commandDraft                             string
	commandDraftCursor                       int
	editToken                                uint64
	editCancel                               context.CancelFunc
	editBuffer                               *editBuffer
	editReview                               *editReview
	width, height                            int
	mode, panelTitle                         string
	helpQuery, helpPreviousQuery             string
	helpPreviousOffset                       int
	helpNotes                                viewport.Model
	helpNotesFocused, helpPreviousNotesFocus bool
	helpPreviousNotesOffset                  int
	input                                    textinput.Model
	choices, matches                         []choice
	pickerIndex                              int
	pickerKind                               string
	panel, inspector                         viewport.Model
	detailFormat                             string
	inspectedKey, inspectorContent           string
	inspectorFocused                         bool
	inspectorSearch                          inspectorSearchState
	inspectorPreview                         *inspectorPreview
	secretToken                              uint64
	secretCancel                             context.CancelFunc
	secretTarget                             string
	secretObject                             *unstructured.Unstructured
	logObject                                unstructured.Unstructured
	logContainer                             string
	logPrevious                              bool
	logFollow, logPaused, logTail, logDone   bool
	logWrap, logFullScreen                   bool
	selection                                *textSelection
	copyFlash                                string
	copyFlashToken                           uint64
	logFeed                                  *logFeed
	logLines                                 []string
	logColours                               map[string]string
	logSize, logDropped                      int
	logSearch                                string
	logFilter                                *logFilter
	logFilterError                           string
	logFrozen                                []string
	logFrozenDropped                         int
	pickerReturn                             string
	metricsEnabled                           bool
	usage                                    map[string]kube.Usage
	metricsWarning                           string
	eventObject                              unstructured.Unstructured
	resourcePanelTab                         resourcePanelTab
	resourceReport                           kube.DiagnosticReport
	reportError                              string
	reportLoading                            bool
	reportCancel                             context.CancelFunc
	reportToken                              uint64
	reportUpdated                            time.Time
	bundleTarget                             unstructured.Unstructured
	bundleOptions                            kube.BundleOptions
	bundleToken                              uint64
	bundleCancel                             context.CancelFunc
	eventToken                               uint64
	eventCancel                              context.CancelFunc
	eventContext                             context.Context
	eventFeed                                *eventFeed
	eventStore                               *eventBuffer
	eventRemoved                             map[string]bool
	eventGlobal, eventPaused, eventTail      bool
	eventState, eventError, eventStatusError string
	eventSearch                              string
	eventDropped, eventGaps                  int
	shellObject                              unstructured.Unstructured
	shellContainer                           string
	shellToken                               uint64
	shellCancel                              context.CancelFunc
	forwardID                                uint64
	forwards                                 map[uint64]*forwardSession
}

func newModel(ctx context.Context, s *Settings, o Options, l *kube.Loader) (*model, error) {
	if !o.ReadOnlyExplicit {
		o.ReadOnly = s.Config.ReadOnly
	}
	m := &model{ctx: ctx, settings: s, options: o, loader: l, resources: kube.Builtins(), theme: s.Themes[s.Config.Theme], layout: s.Config.Layout, width: 80, height: 24, sortColumn: "name"}
	m.inspectorFormat = s.Config.InspectorFormat
	m.logWrap = s.Config.Logs.Wrap
	m.themeUntil, m.themeTimer = time.Now().Add(10*time.Second), 1
	m.panel = viewport.New(viewport.WithWidth(74), viewport.WithHeight(16))
	m.panel.SoftWrap = false
	m.helpNotes = viewport.New()
	m.inspector = viewport.New()
	m.inspector.SoftWrap = true
	m.input = textinput.New()
	m.input.SetStyles(m.theme.inputStyles())
	m.input.CharLimit = 256
	m.input.SetWidth(74)
	if o.Demo {
		m.contextName = "demo"
	} else {
		m.contextName = o.Context
		if m.contextName == "" {
			m.contextName = l.Current()
		}
		if m.contextName != "" {
			resolved, err := m.resolveContext(m.contextName)
			if err != nil {
				return nil, err
			}
			m.contextName = resolved
			exists := false
			for _, c := range l.Contexts() {
				if c.Name == m.contextName {
					exists = true
				}
			}
			if !exists {
				return nil, fmt.Errorf("unknown kubeconfig context %q", m.contextName)
			}
		}
	}
	saved, hasSaved := s.Config.Contexts[m.contextName]
	if o.View == "" {
		o.View = s.Config.View
		if hasSaved && saved.View != "" {
			o.View = saved.View
		}
		if o.View == "" {
			o.View = "pods"
		}
	}
	if !o.ScopeExplicit && o.Namespace == "" && !o.AllNamespaces {
		if hasSaved {
			o.Namespace, o.AllNamespaces = saved.Namespace, saved.AllNamespaces
		} else if s.Config.LastContext == "" || s.Config.LastContext == m.contextName {
			o.Namespace, o.AllNamespaces = s.Config.Namespace, s.Config.AllNamespaces
		}
	}
	var scopeError error
	if o.Namespace, scopeError = kube.NormalizeNamespaces(o.Namespace); scopeError != nil {
		return nil, scopeError
	}
	m.options = o
	m.route.namespace, m.route.labels = o.Namespace, o.Labels
	if o.AllNamespaces {
		m.route.namespace = ""
	} else if o.Demo && m.route.namespace == "" {
		m.route.namespace = "default"
	}
	if r, e := kube.Resolve(m.resources, o.View); e == nil {
		m.route.resource = r
	}
	m.applyLayout(s.Config.Layout)
	s.loadCommandHistory()
	m.resize()
	return m, nil
}
func (m *model) Init() tea.Cmd {
	if m.contextName == "" {
		return m.openPicker("contexts")
	}
	return tea.Batch(m.connect(m.contextName), m.themeTimeout())
}
func (m *model) close() {
	m.cancelInspectorPreview()
	m.cancelBundle()
	m.commandExecutions = nil
	m.cancelRollouts()
	m.cancelOverview()
	m.cancelAccess()
	m.stopResourceWatch()
	m.cancelAction()
	m.manifestContent = ""
	m.cancelRelations()
	m.clearSecret()
	m.updateInspector()
	if m.requestCancel != nil {
		m.requestCancel()
	}
	m.closeLog()
	m.closeEvents()
	m.cancelShell()
	m.cancelDebug()
	m.stopForwards()
	m.cancelEdit()
	m.session.close()
}
func (m *model) connect(name string) tea.Cmd {
	m.close()
	m.resourceWatchError = ""
	m.generation++
	m.request++
	m.busy = true
	m.backend = nil
	m.resources = kube.Builtins()
	m.rows = nil
	m.visible = nil
	m.history = nil
	m.selected = 0
	m.horizontal = 0
	m.filter = ""
	m.dataErr = ""
	m.namespaceWarning = ""
	m.notice = ""
	m.discoveryWarning = ""
	m.server = ""
	m.mode = ""
	m.inspectorFocused = false
	m.lastRefresh = time.Time{}
	m.failures = 0
	m.usage = nil
	m.metricsWarning = ""
	m.forwards = nil
	if m.contextName != name {
		m.route.namespace = ""
		m.options.Namespace = ""
		m.options.AllNamespaces = false
		m.route.labels = ""
		m.route.fields = ""
		if state, ok := m.settings.Config.Contexts[name]; ok {
			m.route.namespace, m.options.AllNamespaces = state.Namespace, state.AllNamespaces
			m.options.Namespace = state.Namespace
			if state.View != "" {
				m.options.View = state.View
				m.route.resource = kube.Resource{}
				if r, e := kube.Resolve(m.resources, state.View); e == nil {
					m.route.resource = r
				}
			}
		}
	}
	m.contextName = name
	m.updateInspector()
	m.session = newSession(m.ctx)
	s, g, demo, loader := m.session, m.generation, m.options.Demo, m.loader
	return func() tea.Msg {
		var b kube.Backend
		var server, ns string
		var err error
		if demo {
			b = kube.NewDemo()
			server = "offline sample cluster"
			ns = "default"
		} else {
			b, server, ns, err = loader.Open(name)
		}
		s.attach(b)
		if err != nil {
			return connectedMsg{generation: g, err: err}
		}
		resources, discoveryErr := b.Resources(s.ctx)
		warning := ""
		if discoveryErr != nil {
			warning = "API discovery incomplete: " + discoveryErr.Error()
		}
		if resources == nil {
			resources = kube.Builtins()
		}
		return connectedMsg{g, b, resources, server, ns, warning, nil}
	}
}
func (m *model) fetch() tea.Cmd {
	if m.backend == nil || m.busy || m.mode == "shell" || m.mode == "editing" || m.mode == "edit-loading" {
		return nil
	}
	if m.route.resource.Kind == "Event" {
		if m.eventFeed != nil || m.showingEventPanel() {
			return nil
		}
		return m.followEventTable()
	}
	m.busy = true
	m.request++
	g, id, b, r := m.generation, m.request, m.backend, m.route
	ctx, cancel := context.WithTimeout(m.session.ctx, 15*time.Second)
	m.requestCancel = cancel
	metrics := m.hasMetrics() && (r.resource.Kind == "Pod" || r.resource.Kind == "Node")
	return func() tea.Msg {
		defer cancel()
		var usage map[string]kube.Usage
		var metricsError string
		var tasks sync.WaitGroup
		if metrics {
			tasks.Add(1)
			go func() {
				defer tasks.Done()
				if reader, ok := b.(kube.MetricsReader); ok {
					var err error
					metricsContext, done := context.WithTimeout(ctx, 3*time.Second)
					defer done()
					usage, err = reader.Metrics(metricsContext, r.resource.Kind, r.namespace)
					if err != nil {
						metricsError = kube.Safe(err.Error())
					}
				} else {
					metricsError = "Metrics API unavailable"
				}
			}()
		}
		rows, err := b.List(ctx, kube.Query{Resource: r.resource, Namespace: r.namespace, Labels: r.labels, Fields: r.fields})
		tasks.Wait()
		if metrics && len(rows) > 0 && len(usage) == 0 && metricsError == "" {
			metricsError = "Metrics API is available; waiting for samples"
		}
		return snapshotMsg{generation: g, request: id, rows: rows, err: err, usage: usage, metricsError: metricsError}
	}
}
func (m *model) poll() tea.Cmd {
	g, id := m.generation, m.request
	delay := m.settings.Interval
	for i := 0; i < min(m.failures, 4); i++ {
		delay *= 2
	}
	delay = min(delay, 30*time.Second)
	return tea.Tick(delay, func(time.Time) tea.Msg { return pollMsg{g, id} })
}
func (m *model) navigate(r route, remember bool) tea.Cmd {
	m.cancelInspectorPreview()
	m.cancelBundle()
	m.cancelDebug()
	m.cancelRollouts()
	m.cancelOverview()
	m.cancelAccess()
	m.stopResourceWatch()
	m.resourceWatchError = ""
	m.cancelAction()
	m.manifestContent = ""
	m.cancelRelations()
	if remember {
		old := m.route
		if o, ok := m.current(); ok {
			old.selection = kube.Identity(o)
		}
		m.history = append(m.history, old)
	}
	if m.requestCancel != nil {
		m.requestCancel()
	}
	m.clearSecret()
	m.closeLog()
	m.closeEvents()
	m.request++
	m.busy = false
	m.rows = nil
	m.visible = nil
	m.selected = 0
	m.horizontal = 0
	m.filter = ""
	m.mode = ""
	m.dataErr = ""
	m.namespaceWarning = ""
	m.notice = ""
	m.lastRefresh = time.Time{}
	m.failures = 0
	m.usage = nil
	m.metricsWarning = ""
	m.inspectorFocused = false
	m.route = r
	m.applyView()
	m.updateInspector()
	m.saveRoute()
	return m.fetch()
}
func (m *model) applyView() {
	m.sortExplicit = false
	m.sortColumn = "name"
	if m.route.resource.Kind == "Event" {
		m.sortColumn = "lastseen"
	}
	m.descending = false
	v, ok := m.views[m.route.resource.ID()]
	if !ok {
		v = m.views[m.route.resource.GVR.Resource]
	}
	if v.Sort != "" {
		m.sortColumn = v.Sort
		m.sortExplicit = true
	}
	m.descending = v.Descending
	m.orderSelected = m.sortColumn
}
func (m *model) columns() []string {
	v, ok := m.views[m.route.resource.ID()]
	if !ok {
		v = m.views[m.route.resource.GVR.Resource]
	}
	cols := slices.Clone(v.Columns)
	if len(cols) == 0 {
		cols = kube.DefaultColumns(m.route.resource.Kind)
	}
	if m.wide {
		for _, c := range []string{"kind", "labels"} {
			if !slices.Contains(cols, c) {
				cols = append(cols, c)
			}
		}
	}
	if m.metricsEnabled && (m.route.resource.Kind == "Pod" || m.route.resource.Kind == "Node") {
		for _, c := range []string{"cpu", "memory"} {
			if !slices.Contains(cols, c) {
				cols = append(cols, c)
			}
		}
	}
	namespaces, _ := kube.NamespaceNames(m.route.namespace)
	if len(namespaces) == 1 || !m.route.resource.Namespaced {
		cols = slices.DeleteFunc(cols, func(c string) bool { return c == "namespace" })
	} else if len(namespaces) > 1 && !slices.Contains(cols, "namespace") {
		cols = append([]string{"namespace"}, cols...)
	}
	return cols
}
func (m *model) current() (unstructured.Unstructured, bool) {
	if m.selected < 0 || m.selected >= len(m.visible) {
		return unstructured.Unstructured{}, false
	}
	return m.visible[m.selected], true
}
func (m *model) rebuild(key string) {
	slices.SortStableFunc(m.rows, func(a, b unstructured.Unstructured) int {
		av, bv := m.column(a, m.sortColumn), m.column(b, m.sortColumn)
		n := strings.Compare(av, bv)
		if metricColumn(m.sortColumn) {
			an, ao := m.metricValue(a, m.sortColumn)
			bn, bo := m.metricValue(b, m.sortColumn)
			if ao != bo {
				if ao {
					return -1
				}
				return 1
			}
			n = 0
			if an < bn {
				n = -1
			} else if an > bn {
				n = 1
			}
		} else if m.sortColumn == "lastseen" && m.route.resource.Kind == "Event" {
			n = kube.EventTime(a).Compare(kube.EventTime(b))
		} else if m.sortColumn == "age" {
			at, bt := a.GetCreationTimestamp(), b.GetCreationTimestamp()
			n = bt.Time.Compare(at.Time)
		} else {
			an, ae := strconv.ParseFloat(av, 64)
			bn, be := strconv.ParseFloat(bv, 64)
			if ae == nil && be == nil {
				n = 0
				if an < bn {
					n = -1
				} else if an > bn {
					n = 1
				}
			}
		}
		if n == 0 {
			n = strings.Compare(a.GetNamespace()+"/"+a.GetName(), b.GetNamespace()+"/"+b.GetName())
		}
		if m.descending {
			return -n
		}
		return n
	})
	m.visible = nil
	type rankedRow struct {
		object unstructured.Unstructured
		score  int
	}
	var matched []rankedRow
	for _, o := range m.rows {
		best, matches := 0, m.filter == ""
		for _, c := range m.columns() {
			if score, ok := fuzzyScore(m.filter, kube.Column(o, c)); ok {
				matches = true
				best = max(best, score)
			}
		}
		if matches {
			matched = append(matched, rankedRow{o, best})
		}
	}
	if m.filter != "" && !m.eventGlobal && !m.sortExplicit {
		slices.SortStableFunc(matched, func(a, b rankedRow) int { return b.score - a.score })
	}
	for _, row := range matched {
		m.visible = append(m.visible, row.object)
	}
	m.selected = min(m.selected, max(0, len(m.visible)-1))
	for i, o := range m.visible {
		if kube.Identity(o) == key {
			m.selected = i
			break
		}
	}
	m.updateInspector()
}
func (m *model) move(n int) {
	m.selected = max(0, min(m.selected+n, len(m.visible)-1))
	m.updateInspector()
}
func (m *model) updateInspector() {
	o, ok := m.current()
	if m.secretTarget != "" && (!ok || o.GetKind() != "Secret" || kube.Identity(o) != m.secretTarget) {
		m.clearSecret()
	}
	content, key := "No resource selected.", ""
	if ok {
		key = kube.Identity(o)
		content = m.describe(o)
		if m.inspectorFormat == "json" {
			content = m.theme.renderJSON(m.manifest(o, true))
		}
	}
	changed := content != m.inspectorContent
	if key != m.inspectedKey {
		m.resetInspectorSearch()
		m.inspectedKey = key
		m.inspector.GotoTop()
	}
	if changed {
		m.inspectorContent = content
		m.refreshInspectorSearch(false)
	}
	m.renderDetails()
}
func (m *model) describe(o unstructured.Unstructured) string {
	return fmt.Sprintf("%s  %s\nNamespace  %s\nStatus     %s\nAge        %s\n\n", kube.Safe(o.GetKind()), kube.Safe(o.GetName()), kube.Safe(o.GetNamespace()), kube.Safe(kube.Status(o)), kube.Age(o)) + m.theme.renderYAML(m.manifest(o, false))
}
func (m *model) closeLog() {
	m.selection = nil
	m.logToken++
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	m.logFeed = nil
	m.logColours = nil
	m.logFrozen = nil
	m.logFilter = nil
	m.logFilterError = ""
}
func (m *model) loadLogs() tea.Cmd {
	if m.logFollow && !m.logPrevious {
		return m.followLogs()
	}
	m.closeLog()
	g, token, b, o, c, previous := m.generation, m.logToken, m.backend, m.logObject, m.logContainer, m.logPrevious
	options := m.logRequestOptions()
	ctx, cancel := context.WithTimeout(m.session.ctx, 30*time.Second)
	m.logCancel = cancel
	m.mode = "logs"
	m.resize()
	m.panel.SoftWrap = false
	m.panel.SetXOffset(0)
	m.panel.SoftWrap = m.logWrap
	m.logPaused = false
	m.logLines = nil
	m.logSize, m.logDropped = 0, 0
	m.logDone = true
	m.logTitle()
	m.panel.SetContent("Loading logs…")
	m.panel.GotoTop()
	m.promoteHistoryExecution("logs", token)
	return func() tea.Msg {
		defer cancel()
		content, err := kube.LogSnapshot(ctx, b, o, c, previous, options)
		return logsMsg{g, token, content, err}
	}
}
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	run := m.historyExecutionFor(msg)
	defer m.observeCommandResult(run, msg)
	switch v := msg.(type) {
	case overviewMsg:
		return m, m.overviewReady(v)
	case overviewTickMsg:
		if v.generation == m.generation && v.token == m.overviewToken && m.mode == "overview" {
			return m, m.fetchOverview()
		}
	case accessMsg:
		m.accessReady(v)
	case resourceBatchMsg:
		return m, m.resourceBatch(v)
	case resourceMetricsMsg:
		return m, m.resourceMetricsReady(v)
	case actionResultMsg:
		return m, m.actionResult(v)
	case manifestSavedMsg:
		if v.generation == m.generation {
			if v.err != nil {
				m.notice = "Unable to save manifest: " + kube.Safe(v.err.Error())
			} else {
				m.notice = "Manifest saved to " + kube.Safe(v.path)
			}
		}
	case relationsMsg:
		return m, m.relationsReady(v)
	case rolloutsMsg:
		return m, m.rolloutsReady(v)
	case relationOpenedMsg:
		return m, m.relationOpened(v)
	case tea.WindowSizeMsg:
		m.cancelSelection()
		m.width = max(1, v.Width)
		m.height = max(1, v.Height)
		m.resize()
		if m.showingLogs() && m.logTail && !m.logPaused {
			m.panel.GotoBottom()
		}
	case connectedMsg:
		if v.generation != m.generation {
			return m, nil
		}
		m.busy = false
		if v.err != nil {
			m.dataErr = kube.Safe(v.err.Error())
			return m, nil
		}
		m.backend = v.backend
		m.resources = v.resources
		m.server = v.server
		m.discoveryWarning = kube.Safe(v.warning)
		if m.route.namespace == "" && !m.options.AllNamespaces {
			m.route.namespace = v.namespace
		}
		name := m.options.View
		if m.route.resource.ID() != "" {
			name = m.route.resource.ID()
		}
		r, err := kube.Resolve(m.resources, name)
		if err != nil {
			m.dataErr = kube.Safe(err.Error())
			return m, nil
		}
		m.route.resource = r
		m.applyView()
		m.saveRoute()
		return m, m.fetch()
	case snapshotMsg:
		if v.generation != m.generation || v.request != m.request {
			return m, nil
		}
		m.busy = false
		if v.err != nil && !kube.PartialNamespaces(v.err) {
			m.dataErr = kube.Safe(v.err.Error())
			m.failures++
		} else {
			key := m.route.selection
			if o, ok := m.current(); ok {
				key = kube.Identity(o)
			}
			m.route.selection = ""
			var partial *kube.NamespaceErrors
			if errors.As(v.err, &partial) {
				for _, previous := range m.rows {
					if _, failed := partial.Failures[previous.GetNamespace()]; failed {
						v.rows = append(v.rows, previous)
					}
				}
			}
			m.rows = make([]unstructured.Unstructured, len(v.rows))
			for i, o := range v.rows {
				m.hideChangedSecret(o)
				m.rows[i] = kube.Redacted(o)
			}
			m.dataErr = ""
			m.namespaceWarning = ""
			if v.err != nil {
				m.namespaceWarning = kube.Safe(v.err.Error())
			}
			m.failures = 0
			m.lastRefresh = time.Now()
			m.usage, m.metricsWarning = v.usage, v.metricsError
			m.rebuild(key)
		}
		if (v.err == nil || kube.PartialNamespaces(v.err)) && m.resourceFeed == nil && m.resourceWatchError == "" {
			return m, tea.Batch(m.startResourceWatch(), m.poll())
		}
		return m, m.poll()
	case pollMsg:
		if v.generation == m.generation && v.request == m.request {
			if m.resourceFeed != nil {
				return m, m.resourceMetrics()
			}
			return m, m.fetch()
		}
	case secretReadyMsg:
		m.secretReady(v)
	case logsMsg:
		if v.generation != m.generation || v.token != m.logToken || !(m.showingLogs() || m.mode == "picker" && m.pickerReturn == "logs") {
			return m, nil
		}
		if v.err != nil {
			m.panel.SetContent("Unable to read logs: " + kube.Safe(v.err.Error()) + "\n\nCtrl+R retries; Esc closes.")
		} else {
			content := kube.SafeLogLines(v.content)
			if content == "" {
				content = "No log lines returned."
			}
			m.logLines = strings.Split(content, "\n")
			m.logSize = len(content)
			m.logDropped = 0
			m.logTail = false
			m.renderLogs()
		}
	case logPulseMsg:
		return m, m.logPulse(v)
	case logSavedMsg:
		if v.err != nil {
			m.notice = "Unable to save logs: " + kube.Safe(v.err.Error())
		} else {
			m.notice = "Logs saved to " + kube.Safe(v.path)
		}
	case eventsMsg:
		if v.generation == m.generation && v.token == m.eventToken && m.showingEventPanel() {
			if v.err != nil {
				m.reportError = kube.Safe(v.err.Error())
				m.eventStatusError = kube.Safe(v.err.Error())
				m.panel.SetContent("Unable to read resource: " + kube.Safe(v.err.Error()) + "\n\nCtrl+R retries; Esc closes.")
			} else {
				m.eventObject = v.object
				m.eventStore = newEventBuffer()
				for _, event := range v.events {
					m.eventStore.put(event, false)
				}
				if v.eventsError != nil {
					m.eventError = kube.Safe(v.eventsError.Error())
				}
				m.renderEventPanel()
				return m, tea.Batch(m.startEventWatch(), m.loadResourceReport())
			}
		}
	case eventPulseMsg:
		return m, m.eventPulse(v)
	case eventStatusTickMsg:
		return m, m.eventStatusTick(v)
	case eventStatusMsg:
		return m, m.eventStatusResult(v)
	case bundleSavedMsg:
		return m, m.bundleSaved(v)
	case resourceReportMsg:
		return m, m.resourceReportReady(v)
	case eventSavedMsg:
		if v.generation != m.generation || v.token != m.eventToken {
			return m, nil
		}
		label := "events"
		if !m.eventGlobal && m.resourcePanelTab != resourceStatusTab {
			label = "metadata"
		}
		if v.err != nil {
			m.notice = "Unable to save " + label + ": " + kube.Safe(v.err.Error())
		} else {
			m.notice = strings.Title(label) + " saved to " + kube.Safe(v.path)
		}
	case shellClosedMsg:
		return m, m.shellClosed(v)
	case debugPreparedMsg:
		return m, m.debugPrepared(v)
	case debugStartedMsg:
		return m, m.debugStarted(v)
	case debugClosedMsg:
		return m, m.debugClosed(v)
	case forwardMsg:
		return m, m.forwardUpdate(v)
	case themeHiddenMsg:
		if v.token == m.themeTimer {
			m.themeUntil = time.Time{}
		}
	case editReadyMsg:
		return m, m.editorReady(v)
	case editorClosedMsg:
		return m, m.editorClosed(v)
	case editReviewedMsg:
		return m, m.editReviewReady(v)
	case editedMsg:
		return m, m.editFinished(v)
	case tea.KeyPressMsg:
		return m, m.key(v)
	case tea.MouseClickMsg:
		if m.mode == "namespace-group" {
			return m, m.namespaceGroupMouse(v)
		}
		if m.mode == "delete-confirm" {
			return m, m.deleteMouse(v)
		}
		m.cancelSelection()
		m.mouseClick(v)
		m.beginSelection(v)
	case tea.MouseMotionMsg:
		m.moveSelection(v)
	case tea.MouseReleaseMsg:
		return m, m.finishSelection(v)
	case tea.MouseWheelMsg:
		m.cancelSelection()
		m.mouseWheel(v)
	case copyFlashMsg:
		if v.token == m.copyFlashToken {
			m.copyFlash = ""
		}
	default:
		if m.mode == "inspector-search" {
			return m, m.updateInspectorSearch(msg)
		}
		if m.mode == "help-search" {
			return m, m.updateHelpSearch(msg)
		}
		if m.mode == "command" || m.mode == "history-search" || m.mode == "filter" || m.mode == "picker" || m.mode == "namespace-group" && m.namespaceGroup.action != "delete" || m.showingLogs() && m.mode != "logs" || m.mode == "event-search" || m.mode == "event-save" || m.mode == "bundle-save" || m.mode == "manifest-save" || m.mode == "action-confirm" || m.mode == "debug-image" {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			if m.mode == "picker" && m.pickerKind == "namespace-groups" {
				m.filterChoices()
			}
			return m, cmd
		}
	}
	return m, nil
}
