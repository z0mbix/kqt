package tui

import tea "charm.land/bubbletea/v2"

type paneBounds struct{ x, y, width, height int }

func (b paneBounds) interior(x, y int) bool {
	return x > b.x && x < b.x+b.width-1 && y > b.y && y < b.y+b.height-1
}

func (m *model) tableBounds() paneBounds {
	b := m.bodyBounds()
	d := m.inspectorDimensions()
	if d.bottom {
		b.height -= d.height
	} else {
		b.width -= d.width
	}
	return b
}
func (m *model) inspectorBounds() paneBounds {
	d := m.inspectorDimensions()
	if d.bottom {
		return paneBounds{0, m.height - 2 - d.height, d.width, d.height}
	}
	return paneBounds{m.width - d.width, m.bodyBounds().y, d.width, d.height}
}

func (m *model) mouseClick(v tea.MouseClickMsg) {
	if m.width < 40 || m.height < 12 || v.Button != tea.MouseLeft || v.Mod != 0 {
		return
	}
	if m.showingHelp() {
		m.helpClick(v)
		return
	}
	if m.mode != "" {
		return
	}
	table := m.tableBounds()
	if table.interior(v.X, v.Y) {
		geometry := m.tableGeometry(table.width)
		if v.Y == table.y+1 {
			// Account for the frame, row marker, and horizontal clipping.
			x := v.X - table.x - 3
			if x < 0 || x >= geometry.dataWidth {
				return
			}
			x += geometry.offset
			left := 0
			for i, column := range geometry.columns {
				if x >= left && x < left+geometry.widths[i] {
					m.inspectorFocused = false
					m.chooseOrder(column)
					return
				}
				left += geometry.widths[i] + 1
			}
			return
		}
		capacity := max(0, table.height-3)
		row := tableStart(m.selected, len(m.visible), capacity) + v.Y - table.y - 2
		if row >= 0 && row < len(m.visible) {
			m.inspectorFocused = false
			m.eventTail = false
			m.move(row - m.selected)
		}
	} else if m.inspectorBounds().interior(v.X, v.Y) {
		m.inspectorFocused = true
		m.eventTail = false
	}
}

func (m *model) mouseWheel(v tea.MouseWheelMsg) {
	if m.width < 40 || m.height < 12 || v.Mod != 0 {
		return
	}
	key, amount := "", 0
	switch v.Button {
	case tea.MouseWheelUp:
		key, amount = "up", -3
	case tea.MouseWheelDown:
		key, amount = "down", 3
	case tea.MouseWheelLeft:
		key, amount = "left", -4
	case tea.MouseWheelRight:
		key, amount = "right", 4
	default:
		return
	}
	steps := 3
	if key == "left" || key == "right" {
		steps = 1
	}
	if m.mode == "picker" && m.pickerKind == "inspector-position" {
		if m.modalBounds().interior(v.X, v.Y) {
			if key == "up" {
				m.pickerKey(tea.KeyPressMsg{Code: tea.KeyUp})
			} else if key == "down" {
				m.pickerKey(tea.KeyPressMsg{Code: tea.KeyDown})
			}
		}
		return
	}
	if m.showingHelp() {
		if _, pane, ok := m.helpPaneAt(v.X, v.Y); ok {
			for range steps {
				scroll(pane, key)
			}
		}
		return
	}
	if m.showingLogs() || m.showingEventPanel() {
		bounds := m.bodyBounds()
		if m.showingLogs() && m.logFullScreen {
			bounds = paneBounds{-1, -1, m.width + 2, m.panel.Height() + 2}
		}
		if !bounds.interior(v.X, v.Y) {
			return
		}
		m.logTail, m.eventTail = false, false
		if m.showingLogs() {
			m.logTitle()
		}
		for range steps {
			scroll(&m.panel, key)
		}
		return
	}
	if m.mode != "" {
		switch m.mode {
		case "details", "help", "help-search", "forward-details", "edit-error", "edit-review", "delete-confirm", "operation-error", "overview", "access", "rollout-details", "debug-image", "debug-confirm", "debug-loading", "debug-starting":
			if !m.modalBounds().interior(v.X, v.Y) {
				return
			}
			for range steps {
				scroll(&m.panel, key)
			}
		}
		return
	}
	if m.inspectorBounds().interior(v.X, v.Y) {
		for range steps {
			scroll(&m.inspector, key)
		}
		return
	}
	if m.tableBounds().interior(v.X, v.Y) {
		m.eventTail = false
		if key == "left" || key == "right" {
			m.horizontal = max(0, m.horizontal+amount)
		} else {
			m.move(amount)
		}
	}
}
