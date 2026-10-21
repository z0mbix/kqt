package tui

import (
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
)

var nonProductionContext = regexp.MustCompile(`(?i)(non|pre)[-_. ]?(production|prod|prd)`)

func productionContext(name string) bool {
	// Remove only the excluded occurrences: non-prod-to-prod still contains a
	// production marker. Check the longest spelling first (production before prod).
	name = strings.ToLower(nonProductionContext.ReplaceAllString(name, ""))
	return strings.Contains(name, "prod") || strings.Contains(name, "prd")
}

func (m *model) headerView() string {
	ctx := m.contextLabel(m.contextName)
	if ctx == "" {
		ctx = "select context"
	}
	ns, namespaceLabel := m.namespaceLabel()
	if ns == "" {
		ns = "all"
	}
	contextStyle := m.theme.style("accent").Bold(true)
	if productionContext(m.contextName) || productionContext(ctx) {
		contextStyle = m.theme.style("error").Bold(true)
		if m.theme.Name == "no-color" {
			contextStyle = contextStyle.Underline(true).Reverse(true)
		}
	}
	state := "connected"
	if m.backend == nil {
		state = "disconnected"
	}
	if m.busy && (m.backend == nil || m.lastRefresh.IsZero()) {
		state = "loading"
	}
	stateToken := "muted"
	if m.dataErr != "" {
		state, stateToken = "stale / disconnected", "error"
	}
	watch, announcement := "", ""
	if m.resourceFeed != nil || m.resourceWatchError != "" {
		watch = m.theme.style("muted").Render(" · " + kube.Safe(m.resourceWatchLabel()))
	}
	if time.Now().Before(m.themeUntil) {
		announcement = m.theme.style("muted").Render(" · " + kube.Safe(m.theme.Name))
	}
	left := m.theme.style("foreground").Render(" Context: ") + contextStyle.Render(kube.Safe(ctx))
	namespace := m.theme.style("foreground").Render(" · " + namespaceLabel + ": " + kube.Safe(ns))
	connection := m.theme.style(stateToken).Render(" · " + state)
	mode := ""
	if m.options.ReadOnly {
		mode += " · READ ONLY"
	}
	if m.options.Demo {
		mode += " · DEMO"
	}
	brand := "kqt · A Kubernetes Query Tool" + mode
	// Keep the context and mode visible on smaller terminals. The full title
	// returns as soon as there is room, always anchored to the right edge.
	if ansi.StringWidth(left+namespace+connection+watch+announcement)+ansi.StringWidth(brand)+3 > m.width {
		brand = "kqt" + mode
	}
	brand = m.theme.style("accent").Bold(true).Render(brand)
	budget := m.width - ansi.StringWidth(brand) - 3
	if ansi.StringWidth(left+namespace+connection+announcement) > budget {
		namespace = m.theme.style("foreground").Render(" · NS: " + kube.Safe(ns))
	}
	left += namespace + connection
	// Watch details are also in the status bar; prefer the connection state and
	// ten-second theme announcement when the terminal is too narrow for both.
	if ansi.StringWidth(left+watch+announcement) <= budget {
		left += watch
	}
	return fit(left+announcement, budget) + "  " + brand + " "
}

func (m *model) headerVisible() bool {
	visible := m.settings.Config.HeaderVisible
	return visible == nil || *visible
}

func (m *model) headerHeight() int {
	if m.headerVisible() {
		return 1
	}
	return 0
}

// Rendering, paging, mouse handling and text selection share the same body.
func (m *model) bodyBounds() paneBounds {
	top := m.headerHeight()
	return paneBounds{0, top, m.width, max(0, m.height-top-2)}
}

func (m *model) toggleHeader() {
	m.setHeaderVisibility(!m.headerVisible())
}

func (m *model) setHeaderVisibility(visible bool) {
	m.cancelSelection()
	m.settings.Config.HeaderVisible = &visible
	m.resize()
	if m.showingLogs() && m.logTail && !m.logPaused || m.showingEventPanel() && m.eventTail && !m.eventPaused {
		m.panel.GotoBottom()
	}
	m.notice = "Header shown"
	if !visible {
		m.notice = "Header hidden · z shows it again"
	}
	m.savePreferences(map[string]any{"header_visible": visible})
}

func (m *model) headerCommand(arg string) {
	switch arg {
	case "", "toggle":
		m.toggleHeader()
	case "on":
		m.setHeaderVisibility(true)
	case "off":
		m.setHeaderVisibility(false)
	default:
		m.notice = "Use :header [on|off|toggle]"
	}
}
