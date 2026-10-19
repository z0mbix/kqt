package tui

func (m *model) headerVisible() bool {
	visible := m.settings.Config.HeaderVisible
	return visible == nil || *visible
}

func (m *model) headerHeight() int {
	if m.headerVisible() {
		return 4
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
