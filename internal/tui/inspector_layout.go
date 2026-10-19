package tui

import "fmt"

type inspectorPreview struct {
	position         string
	inspectedKey     string
	focused          bool
	xOffset, yOffset int
}

func (m *model) previewingInspector() bool {
	return m.inspectorPreview != nil && m.mode == "picker" && m.pickerKind == "inspector-position"
}

func (m *model) inspectorPlacement() string {
	if m.previewingInspector() {
		return m.inspectorPreview.position
	}
	if position := m.settings.Config.InspectorPosition; position != "" {
		return position
	}
	if position := m.settings.Layouts[m.layout].Inspector; position == "right" || position == "bottom" {
		return position
	}
	return "right"
}

func (m *model) inspectorVisible() bool {
	if m.previewingInspector() {
		return true
	}
	if visible := m.settings.Config.InspectorVisible; visible != nil {
		return *visible
	}
	return m.settings.Layouts[m.layout].Inspector != "none"
}

func (m *model) inspectorProfile() layoutProfile {
	p := m.settings.Layouts[m.layout]
	p.Inspector = "none"
	if m.inspectorVisible() {
		p.Inspector = m.inspectorPlacement()
	}
	return p
}

func validateInspectorPreferences(c config) error {
	if c.InspectorPosition != "" && c.InspectorPosition != "right" && c.InspectorPosition != "bottom" {
		return fmt.Errorf("inspector_position must be right or bottom")
	}
	return nil
}

func (m *model) toggleInspector() {
	m.setInspectorVisibility(!m.inspectorVisible())
}

func (m *model) setInspectorVisibility(visible bool) {
	m.setInspector(m.inspectorPlacement(), visible)
}

func (m *model) placeInspector(position string) {
	m.setInspector(position, true)
}

func (m *model) setInspector(position string, visible bool) {
	m.inspectorPreview = nil
	m.settings.Config.InspectorPosition = position
	m.settings.Config.InspectorVisible = &visible
	if !visible {
		m.inspectorFocused = false
		m.clearSecret()
	}
	m.resize()
	m.updateInspector()
	m.notice = "Inspector hidden"
	if visible {
		m.notice = "Inspector shown at " + position
		if d := m.inspectorDimensions(); d.width == 0 {
			m.notice = fmt.Sprintf("Inspector enabled · needs 120 columns or %d rows", m.headerHeight()+20)
		} else if (position == "bottom") != d.bottom {
			m.notice = "Inspector shown where space permits · preferred position: " + position
		}
	}
	m.savePreferences(map[string]any{"inspector_position": position, "inspector_visible": visible})
}

func (m *model) beginInspectorPreview() {
	m.inspectorPreview = &inspectorPreview{
		position: m.inspectorPlacement(), focused: m.inspectorFocused,
		inspectedKey: m.inspectedKey,
		xOffset:      m.inspector.XOffset(), yOffset: m.inspector.YOffset(),
	}
}

func (m *model) previewInspectorPosition() {
	if !m.previewingInspector() || len(m.matches) == 0 {
		return
	}
	m.inspectorPreview.position = m.matches[m.pickerIndex].name
	m.resize()
	m.updateInspector()
}

func (m *model) cancelInspectorPreview() {
	previous := m.inspectorPreview
	if previous == nil {
		return
	}
	m.inspectorPreview = nil
	m.resize()
	m.inspectorFocused = previous.focused && m.inspectorDimensions().width > 0
	if m.inspectedKey == previous.inspectedKey {
		m.inspector.SetXOffset(previous.xOffset)
		m.inspector.SetYOffset(previous.yOffset)
	}
}
