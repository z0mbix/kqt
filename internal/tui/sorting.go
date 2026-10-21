package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
)

func (m *model) orderColumn() string {
	columns := m.columns()
	for _, candidate := range []string{m.orderSelected, m.sortColumn} {
		if slices.Contains(columns, candidate) {
			return candidate
		}
	}
	if len(columns) > 0 {
		return columns[0]
	}
	return ""
}

func (m *model) selectOrderColumn(delta int) {
	columns := m.columns()
	if len(columns) == 0 {
		return
	}
	index := slices.Index(columns, m.orderColumn())
	m.orderSelected = columns[(index+delta+len(columns))%len(columns)]
	m.inspectorFocused = false
	geometry := m.tableGeometry(m.tableBounds().width)
	left := 0
	for i, column := range geometry.columns {
		if column == m.orderSelected {
			right := left + geometry.widths[i]
			if left < geometry.offset {
				m.horizontal = left
			} else if right > geometry.offset+geometry.dataWidth {
				m.horizontal = max(left, right-geometry.dataWidth)
			}
			break
		}
		left += geometry.widths[i] + 1
	}
}

func (m *model) chooseOrder(column string) {
	descending := false
	if column == m.sortColumn {
		descending = !m.descending
	}
	m.setOrder(column, descending)
}

func (m *model) cycleOrder(delta int) {
	columns := m.columns()
	if len(columns) == 0 {
		return
	}
	index := slices.Index(columns, m.sortColumn)
	if index < 0 {
		index = -1
		if delta < 0 {
			index = 0
		}
	}
	column := columns[(index+delta+len(columns))%len(columns)]
	m.setOrder(column, false)
	m.selectOrderColumn(0)
}

func (m *model) setOrder(column string, descending bool) {
	if !validColumn(column) {
		m.notice = "Unknown sort column: " + kube.Safe(column)
		return
	}
	key := ""
	if selected := m.selectedKey(); selected != "" {
		key = selected
	}
	m.notice = ""
	m.sortColumn, m.descending = column, descending
	m.orderSelected, m.sortExplicit = column, true
	m.eventTail = false
	m.rebuild(key)
	view, ok := m.views[m.route.resource.ID()]
	if !ok {
		view = m.views[m.route.resource.GVR.Resource]
	}
	if len(view.Columns) == 0 {
		view.Columns = kube.DefaultColumns(m.route.resource.Kind)
	}
	view.Sort, view.Descending = column, descending
	m.saveColumns(view)
}

func (m *model) sortCommand(arg string) tea.Cmd {
	if arg == "" {
		return m.openPicker("sort")
	}
	parts := strings.Fields(arg)
	if len(parts) == 0 || len(parts) > 2 || !validColumn(parts[0]) || len(parts) == 2 && parts[1] != "asc" && parts[1] != "desc" {
		m.notice = "Use :sort COLUMN [asc|desc], or :sort to choose a column"
		return nil
	}
	if len(parts) == 1 {
		m.chooseOrder(parts[0])
	} else {
		m.setOrder(parts[0], parts[1] == "desc")
	}
	return nil
}
