package tui

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
)

func clickHeader(t *testing.T, m *model, column string) {
	t.Helper()
	b := m.tableBounds()
	g := m.tableGeometry(b.width)
	left := 0
	for i, c := range g.columns {
		if c == column {
			x := max(left, g.offset)
			if x >= min(left+g.widths[i], g.offset+g.dataWidth) {
				t.Fatal("header is not visible: " + column)
			}
			m.Update(tea.MouseClickMsg{X: b.x + 3 + x - g.offset, Y: b.y + 1, Button: tea.MouseLeft})
			return
		}
		left += g.widths[i] + 1
	}
	t.Fatal("missing column: " + column)
}

func TestHeaderSortingMatchesKeyboardAcrossLayoutsAndClipping(t *testing.T) {
	for _, tc := range []struct {
		layout        string
		width, height int
	}{{"classic", 80, 24}, {"inspector", 180, 48}, {"bottom", 80, 40}} {
		t.Run(tc.layout, func(t *testing.T) {
			m := demoModel(t)
			m.selectLayout(tc.layout)
			m.Update(tea.WindowSizeMsg{Width: tc.width, Height: tc.height})
			m.selected = 4
			selected, _ := m.current()
			clickHeader(t, m, "restarts")
			if m.sortColumn != "restarts" || m.descending {
				t.Fatal("new clicked column did not sort ascending")
			}
			if o, _ := m.current(); o.GetUID() != selected.GetUID() {
				t.Fatal("sorting changed selected UID")
			}
			clickHeader(t, m, "restarts")
			if !m.descending {
				t.Fatal("same column did not toggle direction")
			}
			key(m, "R")
			if m.descending {
				t.Fatal("R did not reverse")
			}
			key(m, "O")
			if m.sortColumn != "age" || m.descending {
				t.Fatal("O did not sort the next column ascending")
			}
			key(m, "]")
			if m.sortColumn != "node" || m.descending {
				t.Fatal("] did not sort the next column ascending")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
			if m.sortColumn != "age" || m.orderColumn() != "age" {
				t.Fatal("Shift+Left did not sort the previous column")
			}
			// Wide custom columns exercise Unicode terminal widths and clipping.
			m.command("columns name,/metadata/labels/界界,status,restarts,age,node")
			for i := range m.rows {
				labels := m.rows[i].GetLabels()
				labels["界界"] = strings.Repeat("界", 25)
				m.rows[i].SetLabels(labels)
			}
			m.wide = true
			m.orderSelected = "name"
			for m.orderColumn() != "labels" {
				m.selectOrderColumn(1)
			}
			if m.horizontal == 0 {
				t.Fatal("column selection did not reveal an off-screen header")
			}
			clickHeader(t, m, "labels")
			if m.sortColumn != "labels" {
				t.Fatal("clipped/Unicode header hit selected wrong field")
			}
			if m.View().MouseMode != tea.MouseModeCellMotion {
				t.Fatal("mouse reporting is not enabled")
			}
		})
	}
}

func TestOCyclesVisibleColumnsWrapsAndPreservesSelection(t *testing.T) {
	m := demoModel(t)
	m.command("columns namespace,name,status,/metadata/labels/app")
	m.wide, m.metricsEnabled = true, true
	m.setOrder("name", true)
	m.selected = 4
	selected, _ := m.current()
	columns := m.columns()
	if slices.Contains(columns, "namespace") {
		t.Fatal("scoped table exposed namespace")
	}
	for i := 1; i <= len(columns); i++ {
		key(m, "O")
		want := columns[i%len(columns)]
		if m.sortColumn != want || m.orderColumn() != want || m.descending {
			t.Fatalf("cycle %d: got %s, want %s ascending", i, m.sortColumn, want)
		}
		if object, _ := m.current(); object.GetUID() != selected.GetUID() {
			t.Fatal("cycling changed selected resource")
		}
	}
	m.setOrder("namespace", true) // A valid sort hidden in this scope.
	key(m, "O")
	if m.sortColumn != columns[0] || m.descending {
		t.Fatal("hidden sort did not cycle to first visible column")
	}
	m.command("columns name")
	m.wide, m.metricsEnabled = false, false
	m.setOrder("name", true)
	key(m, "O")
	key(m, "O")
	if m.sortColumn != "name" || m.descending {
		t.Fatal("single-column cycling acted as reverse")
	}
}

func TestSortingIsSavedPerLayoutAndAscendingClearsDescending(t *testing.T) {
	for _, layout := range []string{"classic", "custom-order"} {
		t.Run(layout, func(t *testing.T) {
			m := demoModel(t)
			if layout != "classic" {
				m.saveLayout(layout)
			} else {
				m.selectLayout(layout)
			}
			m.command("sort restarts desc")
			settings, err := LoadSettings(m.settings.Path)
			if err != nil {
				t.Fatal(err)
			}
			view := settings.Config.Views["pods"]
			if layout != "classic" {
				view = settings.Layouts[layout].Views["pods"]
			}
			if view.Sort != "restarts" || !view.Descending {
				t.Fatal("descending order was not saved")
			}
			m.command("sort restarts asc")
			settings, err = LoadSettings(m.settings.Path)
			if err != nil {
				t.Fatal(err)
			}
			view = settings.Config.Views["pods"]
			if layout != "classic" {
				view = settings.Layouts[layout].Views["pods"]
			}
			if view.Sort != "restarts" || view.Descending {
				t.Fatal("ascending did not clear saved descending state")
			}
			m.applyLayout(layout)
			if m.sortColumn != "restarts" || m.descending || !m.sortExplicit {
				t.Fatal("layout lost saved order")
			}
		})
	}
}

func TestSortPickerCompletionAndPrintableShortcuts(t *testing.T) {
	m := demoModel(t)
	m.command("sort")
	if m.mode != "picker" || m.pickerKind != "sort" {
		t.Fatal(":sort did not open the column picker")
	}
	for _, k := range []string{"S", "R", "O", "[", "]"} {
		key(m, k)
	}
	if m.input.Value() != "SRO[]" || m.mode != "picker" {
		t.Fatal("shortcuts escaped text input")
	}
	m.input.SetValue("restart")
	m.filterChoices()
	key(m, "enter")
	if m.sortColumn != "restarts" || m.descending || m.mode != "" {
		t.Fatal("sort picker did not apply a column")
	}
	m.startInput("command", "sort restarts d")
	m.completeCommand(false)
	if m.input.Value() != "sort restarts desc" {
		t.Fatal("sort direction completion failed")
	}
	key(m, "esc")
	for _, command := range []string{"sort no-such-field", "sort name sideways", "sort name asc extra"} {
		before := m.sortColumn
		m.command(command)
		if m.sortColumn != before || m.notice == "" {
			t.Fatal("invalid order changed sorting")
		}
	}
	key(m, "S")
	if m.mode != "events" {
		t.Fatal("S did not open resource status")
	}
}

func TestManualSortingOverridesFuzzyRankingAndRetainsNumericTimeOrder(t *testing.T) {
	m := demoModel(t)
	one, two := *m.rows[0].DeepCopy(), *m.rows[1].DeepCopy()
	one.SetName("web-exact")
	two.SetName("w-extra-e-extra-b")
	one.SetCreationTimestamp(metav1.NewTime(time.Now().Add(-time.Hour)))
	two.SetCreationTimestamp(metav1.NewTime(time.Now().Add(-time.Hour + time.Second)))
	one.Object["status"].(map[string]interface{})["containerStatuses"].([]interface{})[0].(map[string]interface{})["restartCount"] = int64(10)
	two.Object["status"].(map[string]interface{})["containerStatuses"].([]interface{})[0].(map[string]interface{})["restartCount"] = int64(2)
	m.rows = []unstructured.Unstructured{one, two}
	m.filter = "web"
	m.rebuild("")
	if m.visible[0].GetName() != "web-exact" {
		t.Fatal("default fuzzy ranking lost")
	}
	m.setOrder("restarts", false)
	if m.visible[0].GetName() != two.GetName() {
		t.Fatal("manual numeric sort was overridden by fuzzy ranking")
	}
	m.command("sort age desc")
	if m.sortColumn != "age" || !m.descending || m.visible[0].GetName() != one.GetName() {
		t.Fatal("age order ignored exact timestamps")
	}
}

func TestMouseIgnoresBordersModalsAndReleaseButSelectsRowsAndPanes(t *testing.T) {
	m := demoModel(t)
	m.selectLayout("inspector")
	m.Update(tea.WindowSizeMsg{Width: 180, Height: 48})
	before := m.descending
	y := m.tableBounds().y
	for _, v := range []tea.MouseClickMsg{{X: 0, Y: y + 1, Button: tea.MouseLeft}, {X: 3, Y: y, Button: tea.MouseLeft}, {X: 3, Y: y + 1, Button: tea.MouseRight}, {X: 3, Y: y + 1, Button: tea.MouseLeft, Mod: tea.ModShift}, {X: -1, Y: y + 1, Button: tea.MouseLeft}} {
		m.Update(v)
	}
	m.Update(tea.MouseReleaseMsg{X: 3, Y: y + 1, Button: tea.MouseLeft})
	if m.descending != before {
		t.Fatal("non-header click reversed sorting")
	}
	m.startInput("filter", "")
	clickHeader(t, m, "name")
	if m.descending != before {
		t.Fatal("click escaped an input")
	}
	key(m, "esc")
	key(m, "J")
	clickHeader(t, m, "name")
	if m.descending != before {
		t.Fatal("click reached table behind modal")
	}
	key(m, "esc")
	m.Update(tea.MouseClickMsg{X: 3, Y: y + 4, Button: tea.MouseLeft})
	if m.selected != 2 {
		t.Fatal("row click selected wrong row")
	}
	b := m.inspectorBounds()
	m.Update(tea.MouseClickMsg{X: b.x + 2, Y: b.y + 2, Button: tea.MouseLeft})
	if !m.inspectorFocused {
		t.Fatal("inspector click did not focus")
	}
	m.inspector.SetContent(strings.Repeat("line\n", 100))
	m.Update(tea.MouseWheelMsg{X: b.x + 2, Y: b.y + 2, Button: tea.MouseWheelDown})
	if m.inspector.YOffset() != 3 || m.selected != 2 {
		t.Fatal("wheel scrolled wrong pane")
	}
	m.Update(tea.MouseWheelMsg{X: 3, Y: 8, Button: tea.MouseWheelDown})
	if m.selected != 5 {
		t.Fatal("table wheel did not move rows")
	}
}

func TestEventSortStopsFollowingAndPreservesPausedDisplay(t *testing.T) {
	m, _ := eventModel(t, true)
	pulseEvent(m, liveEvent("first", 100, 10), watch.Added)
	pulseEvent(m, liveEvent("second", 200, 2), watch.Added)
	m.chooseOrder("count")
	if m.eventTail || m.visible[0].GetName() != "second" {
		t.Fatal("manual event sort still follows or compares counts as text")
	}
	key(m, " ")
	before := m.eventExport()
	frozenView := m.View().Content
	pulseEvent(m, liveEvent("first", 100, 10), watch.Deleted)
	if m.View().Content != frozenView {
		t.Fatal("API deletion changed a frozen event table")
	}
	pulseEvent(m, liveEvent("second", 300, 20), watch.Modified)
	if m.eventExport() != before {
		t.Fatal("sorting broke event pause")
	}
	key(m, " ")
	if !strings.Contains(m.column(m.eventStore.items["first"].object, "type"), "removed") {
		t.Fatal("unpause lost the deletion marker")
	}
	if m.visible[len(m.visible)-1].GetName() != "second" {
		t.Fatal("resumed event order did not update")
	}
	key(m, "G")
	if !m.eventTail {
		t.Fatal("G did not resume following")
	}
}

func TestSortKeepsHiddenSecretsRedacted(t *testing.T) {
	m := secretModel(t)
	m.chooseOrder("/data/token")
	if strings.Contains(ansi.Strip(m.View().Content), "demo-only") {
		t.Fatal("sorting revealed Secret values")
	}
	data, err := os.ReadFile(m.settings.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "demo-only") || !slices.Contains(m.columns(), "name") {
		t.Fatal("sorting saved secret data or destroyed columns")
	}
}
