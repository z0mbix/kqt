package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
)

func TestInspectorToggleRemembersPositionWithoutChangingLayoutOrView(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 48})
	m.selectLayout("bottom")
	m.command("columns name,status,age")
	m.setOrder("age", true)
	m.saveLayout("custom")
	m.settings.Layouts["custom"] = layoutProfile{Inspector: "bottom", InspectorPercent: 55, Views: cloneViews(m.views)}
	m.selected = 4
	selected, _ := m.current()
	columns := slices.Clone(m.columns())
	m.inspectorFocused = true
	key(m, "i")
	if m.inspectorVisible() || m.inspectorDimensions().width != 0 || m.inspectorFocused || m.layout != "custom" {
		t.Fatal("i did not just hide the inspector")
	}
	key(m, "i")
	if !m.inspectorVisible() || !m.inspectorDimensions().bottom || m.inspectorPlacement() != "bottom" || m.inspectorDimensions().height != m.bodyBounds().height*55/100 {
		t.Fatal("i did not restore the previous placement and size")
	}
	if o, _ := m.current(); o.GetUID() != selected.GetUID() || !slices.Equal(m.columns(), columns) || m.sortColumn != "age" || !m.descending {
		t.Fatal("inspector toggle changed resource selection, columns or sorting")
	}
	key(m, "i")
	key(m, "i")
	if !m.inspectorDimensions().bottom {
		t.Fatal("repeated toggles cycled positions")
	}
}

func TestInspectorPlacementChooserAndCancellation(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 48})
	m.selectLayout("classic")
	key(m, "I")
	if m.mode != "picker" || m.pickerKind != "inspector-position" || len(m.choices) != 2 {
		t.Fatal("I did not open the placement chooser")
	}
	key(m, "esc")
	if m.inspectorVisible() || m.mode != "" {
		t.Fatal("cancelling placement changed visibility")
	}
	key(m, "I")
	typeHelpQuery(m, "bottom")
	key(m, "enter")
	if !m.inspectorVisible() || !m.inspectorDimensions().bottom || m.layout != "classic" {
		t.Fatal("placement did not show the inspector without changing layout")
	}
	if cmd := m.command("inspector"); cmd == nil || m.pickerIndex != 1 {
		t.Fatal(":inspector did not select the saved placement")
	}
	key(m, "esc")
	m.command("inspector right")
	if m.inspectorDimensions().bottom || m.inspectorPlacement() != "right" {
		t.Fatal("direct placement did not move the inspector")
	}
	key(m, "i")
	key(m, "i")
	if m.inspectorPlacement() != "right" || m.inspectorDimensions().bottom {
		t.Fatal("toggle did not remember the new placement")
	}
	if cmd := m.key(tea.KeyPressMsg{Code: 'b', Text: "b"}); cmd == nil || m.mode != "relations-loading" {
		t.Fatal("b no longer opens relationships")
	}
}

func TestInspectorPlacementPreviewMovesLiveAndSavesOnlyOnEnter(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 48})
	m.selectLayout("classic")
	m.command("columns name,status,age")
	m.move(4)
	columns := slices.Clone(m.columns())
	before, err := os.ReadFile(m.settings.Path)
	if err != nil {
		t.Fatal(err)
	}
	key(m, "I")
	if !m.inspectorVisible() || m.inspectorDimensions().bottom || m.settings.Config.InspectorVisible != nil {
		t.Fatal("opening the chooser did not preview the hidden inspector on the right")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.mode != "picker" || !m.inspectorDimensions().bottom {
		t.Fatal("moving to bottom did not preview without closing the chooser")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.inspectorDimensions().bottom {
		t.Fatal("moving to right did not restore the right preview")
	}
	m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	data, _ := os.ReadFile(m.settings.Path)
	if string(data) != string(before) || m.settings.Config.InspectorPosition != "right" || m.settings.Config.InspectorVisible != nil {
		t.Fatal("preview changed persisted placement or visibility")
	}
	if m.layout != "classic" || m.selected != 4 || !slices.Equal(m.columns(), columns) {
		t.Fatal("preview changed layout, columns or resource selection")
	}
	key(m, "enter")
	saved, err := LoadSettings(m.settings.Path)
	if err != nil || saved.Config.InspectorPosition != "bottom" || saved.Config.InspectorVisible == nil || !*saved.Config.InspectorVisible {
		t.Fatal("Enter did not save the previewed placement", err)
	}
	if m.mode != "" || m.inspectorPreview != nil || !m.inspectorDimensions().bottom {
		t.Fatal("Enter did not finish the preview")
	}
}

func TestInspectorPlacementPreviewCancelRestoresExactPreferencesAndScroll(t *testing.T) {
	for _, layout := range []string{"classic", "inspector", "bottom", "hidden"} {
		t.Run(layout, func(t *testing.T) {
			m := demoModel(t)
			m.Update(tea.WindowSizeMsg{Width: 180, Height: 48})
			if layout == "hidden" {
				m.placeInspector("bottom")
				m.setInspectorVisibility(false)
			} else {
				m.selectLayout(layout)
			}
			m.inspectorFocused = m.inspectorVisible()
			m.inspector.SetYOffset(8)
			position, visible := m.settings.Config.InspectorPosition, m.settings.Config.InspectorVisible
			focused, offset, placement := m.inspectorFocused, m.inspector.YOffset(), m.inspectorPlacement()
			before, _ := os.ReadFile(m.settings.Path)
			key(m, "I")
			if placement == "bottom" {
				m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
			} else {
				m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			key(m, "esc")
			data, _ := os.ReadFile(m.settings.Path)
			if m.settings.Config.InspectorPosition != position || m.settings.Config.InspectorVisible != visible || string(data) != string(before) {
				t.Fatal("cancel changed preferences, including inherited visibility")
			}
			if m.inspectorFocused != focused || m.inspector.YOffset() != offset || m.inspectorPlacement() != placement || m.inspectorPreview != nil {
				t.Fatal("cancel did not restore the prior pane state")
			}
		})
	}
}

func TestInspectorPlacementPreviewFilteringWheelAndResponsiveResize(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 48})
	m.selectLayout("classic")
	key(m, "I")
	b := m.modalBounds()
	if b.width != 50 || b.height != 10 || b.x != 65 || b.y != 19 {
		t.Fatal("placement chooser is not compact and centred")
	}
	m.Update(tea.MouseWheelMsg{X: b.x + 2, Y: b.y + 2, Button: tea.MouseWheelDown})
	if !m.inspectorDimensions().bottom {
		t.Fatal("wheel did not preview the bottom position")
	}
	m.Update(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelUp})
	if m.pickerIndex != 1 {
		t.Fatal("wheel outside the chooser changed its selection")
	}
	m.Update(tea.MouseWheelMsg{X: b.x + 2, Y: b.y + 2, Button: tea.MouseWheelUp})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.inspectorPlacement() != "right" || !m.inspectorDimensions().bottom {
		t.Fatal("preview lost the preferred position during responsive fallback")
	}
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 48})
	if m.inspectorDimensions().bottom {
		t.Fatal("resizing back did not restore the right preview")
	}
	typeHelpQuery(m, "bottom")
	if !m.inspectorDimensions().bottom || len(m.matches) != 1 {
		t.Fatal("filtering did not preview the highlighted position")
	}
	typeHelpQuery(m, "-no-match")
	key(m, "enter")
	if m.mode != "picker" || len(m.matches) != 0 || !m.inspectorDimensions().bottom {
		t.Fatal("empty results committed or discarded the last valid preview")
	}
	key(m, "esc")
	if m.inspectorVisible() {
		t.Fatal("cancel did not restore the originally hidden inspector")
	}
}

func TestInspectorPlacementPreviewDoesNotRestoreScrollForAnotherResource(t *testing.T) {
	m := demoModel(t)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 24})
	m.inspector.SetYOffset(8)
	key(m, "I")
	m.move(1) // The selected object can change during a resource watch update.
	key(m, "esc")
	if m.inspector.YOffset() != 0 {
		t.Fatal("cancel applied the previous resource's scroll position to a new one")
	}
}

func TestInspectorPreferencesPersistYAMLAndTOML(t *testing.T) {
	for _, ext := range []string{"yaml", "toml"} {
		t.Run(ext, func(t *testing.T) {
			m := demoModel(t)
			m.settings.Path = filepath.Join(t.TempDir(), "config."+ext)
			m.placeInspector("bottom")
			m.setInspectorVisibility(false)
			saved, err := LoadSettings(m.settings.Path)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Config.InspectorVisible == nil || *saved.Config.InspectorVisible || saved.Config.InspectorPosition != "bottom" {
				t.Fatal("hidden state and remembered position were not saved")
			}
			restarted, err := newModel(context.Background(), saved, Options{Demo: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.close()
			if restarted.inspectorVisible() {
				t.Fatal("restart showed a hidden inspector")
			}
			key(restarted, "i")
			if !restarted.inspectorVisible() || restarted.inspectorPlacement() != "bottom" {
				t.Fatal("restart lost remembered placement")
			}
		})
	}
}

func TestInspectorSavedLayoutsAndLegacyDefaults(t *testing.T) {
	m := demoModel(t)
	for _, name := range []string{"classic", "inspector", "bottom"} {
		m.selectLayout(name)
		if m.inspectorVisible() != (name != "classic") {
			t.Fatal("legacy layout visibility changed")
		}
		if name != "classic" && m.inspectorPlacement() != m.settings.Layouts[name].Inspector {
			t.Fatal("selected layout did not apply its position")
		}
	}
	m.selectLayout("inspector")
	m.placeInspector("bottom")
	m.saveLayout("placed")
	m.setInspectorVisibility(false)
	m.saveLayout("hidden")
	m.selectLayout("placed")
	if !m.inspectorVisible() || m.inspectorPlacement() != "bottom" {
		t.Fatal("named layout did not capture placement")
	}
	m.selectLayout("hidden")
	if m.inspectorVisible() {
		t.Fatal("named layout did not capture visibility")
	}
	before := m.settings.Config
	m.selectLayout("does-not-exist")
	if m.settings.Config.InspectorPosition != before.InspectorPosition || m.settings.Config.InspectorVisible != before.InspectorVisible {
		t.Fatal("invalid layout changed inspector preferences")
	}
}

func TestInspectorResponsiveFallbackDoesNotChangePreference(t *testing.T) {
	m := demoModel(t)
	m.placeInspector("right")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !m.inspectorDimensions().bottom || m.inspectorPlacement() != "right" {
		t.Fatal("right inspector did not temporarily fit below the list")
	}
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 48})
	if m.inspectorDimensions().bottom || m.inspectorDimensions().width == 0 || m.inspectorPlacement() != "right" {
		t.Fatal("widening did not restore right placement")
	}
	m.placeInspector("bottom")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	if m.inspectorDimensions().bottom || m.inspectorDimensions().width == 0 || m.inspectorPlacement() != "bottom" {
		t.Fatal("short terminal did not temporarily fit the inspector on the right")
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if m.inspectorDimensions().width != 0 {
		t.Fatal("inspector squeezed an unusable terminal")
	}
	m.setInspectorVisibility(false)
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 48})
	if m.inspectorDimensions().width != 0 {
		t.Fatal("resize showed an explicitly hidden inspector")
	}
}

func TestInspectorConfigValidationAndSecretHide(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, body := range []string{"inspector_position: left\n", "inspector_visible: maybe\n"} {
		if err := os.WriteFile(path, []byte("version: 1\n"+body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSettings(path); err == nil {
			t.Fatal("invalid inspector preference accepted")
		}
	}
	m := demoModel(t)
	r, _ := kube.Resolve(m.resources, "secrets")
	m.Update(m.navigate(route{resource: r, namespace: "default"}, false)())
	m.placeInspector("bottom")
	m.Update(m.toggleSecret()())
	if m.secretTarget == "" {
		t.Fatal("secret fixture was not revealed")
	}
	key(m, "i")
	key(m, "i")
	if m.secretTarget != "" || strings.Contains(m.inspectorContent, "demo-only") {
		t.Fatal("hidden inspector retained revealed values")
	}
}

func TestInspectorCommandsRecordOnlySuccess(t *testing.T) {
	m := demoModel(t)
	for _, command := range []string{"inspector off", "inspector bottom", "inspector toggle", "inspector on"} {
		runSubmitted(m, command)
	}
	if len(m.settings.Config.CommandHistory) != 4 {
		t.Fatal("successful inspector commands were not remembered")
	}
	runSubmitted(m, "inspector typo")
	if len(m.settings.Config.CommandHistory) != 4 {
		t.Fatal("invalid inspector command was remembered")
	}
	m.startInput("command", "inspector pos")
	m.completeCommand(false)
	if m.input.Value() != "inspector position" {
		t.Fatal("placement completion is unavailable")
	}
}
