package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestNodeViewPickerSavesSelectedLabelsAndContextDefault(t *testing.T) {
	m := demoModel(t)
	m.Update(m.changeResource("nodes", false)())
	m.wide = true
	const zone = "label:topology.kubernetes.io/zone"
	const worker = "label:node-role.kubernetes.io/worker"
	const wantSave = "nodeviews @save capacity"
	if !slices.Contains(m.columns(), "labels") {
		t.Fatal("standard wide mode should include the labels column")
	}
	runSubmitted(m, "nodeviews")
	groupControl(m, 'n')
	if m.input.Value() != "nodeviews @save " {
		t.Fatal("new action did not prompt for a name")
	}
	typeHelpQuery(m, "capacity")
	key(m, "enter")
	if m.pickerKind != "node-view-labels" || slices.Contains(m.settings.CommandHistory, wantSave) {
		t.Fatal("the label picker did not defer saving and history")
	}
	typeHelpQuery(m, "tpzn")
	if len(m.matches) == 0 || m.matches[0].name != "topology.kubernetes.io/zone" {
		t.Fatalf("fuzzy search could not find the zone label: %v", m.matches)
	}
	key(m, "enter")
	m.input.SetValue("node-role.kubernetes.io/worker")
	m.filterChoices()
	key(m, "enter")
	groupControl(m, 's')
	if m.pickerKind != "node-views" || m.activeNodeView() != "capacity" || !slices.Contains(m.settings.CommandHistory, wantSave) {
		t.Fatal("saving did not apply the view and record the command")
	}
	groupControl(m, 't')
	key(m, "esc")
	want := []string{"name", "status", "age", zone, worker}
	if got := m.columns(); !slices.Equal(got, want) {
		t.Fatalf("wide node columns = %v, want %v", got, want)
	}
	view := ansi.Strip(m.table(112, 14))
	for _, fragment := range []string{"capacity", "ZONE", "WORKER", "eu-west-2a", "✓"} {
		if !strings.Contains(view, fragment) {
			t.Fatalf("node table missing %q: %s", fragment, view)
		}
	}
	m.setOrder(zone, true)
	if got := m.visible[0].GetName(); got != "worker-3" {
		t.Fatalf("label sort should place worker-3 first, got %s", got)
	}
	m.Update(m.changeResource("pods", false)())
	if slices.Contains(m.columns(), zone) {
		t.Fatal("node view leaked into pod columns")
	}
	saved, err := LoadSettings("")
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := newModel(context.Background(), saved, Options{Demo: true, View: "nodes"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.close)
	_, cmd := restarted.Update(restarted.connect(restarted.contextName)())
	restarted.Update(cmd())
	if restarted.activeNodeView() != "capacity" || !slices.Equal(restarted.columns(), want) || restarted.sortColumn != zone || !restarted.descending {
		t.Fatal("default, columns or sorting did not survive restart")
	}
	runSubmitted(restarted, "nodeviews @standard")
	if restarted.activeNodeView() != "" {
		t.Fatal("selecting standard did not override the default")
	}
	restarted.Update(restarted.changeResource("pods", false)())
	restarted.Update(restarted.changeResource("nodes", false)())
	if restarted.activeNodeView() != "capacity" {
		t.Fatal("returning to Nodes did not restore the default")
	}
}

func TestNodeViewEditAndDeleteInheritedDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	base := DefaultConfigPath()
	fragment := filepath.Join(fragmentDir(base), "20-device.yaml")
	const source = `contexts:
  demo:
    default_node_view: capacity
    node_views:
      capacity:
        columns: [name, /metadata/labels/topology.kubernetes.io~1zone]
  other:
    default_node_view: capacity
    node_views:
      capacity:
        columns: [name, label:kubernetes.io/arch]
`
	writeConfigFixture(t, fragment, source)
	s, err := LoadSettings("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := newModel(context.Background(), s, Options{Demo: true, View: "nodes"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.close)
	_, cmd := m.Update(m.connect(m.contextName)())
	m.Update(cmd())
	const edit = "nodeviews @edit capacity"
	runSubmitted(m, edit)
	typeHelpQuery(m, "topology.kubernetes.io/zone")
	if len(m.matches) != 1 || m.matches[0].description != "[x]" {
		t.Fatalf("existing JSON Pointer label was not selected: %v", m.matches)
	}
	key(m, "enter")
	key(m, "esc")
	if len(m.columns()) != 2 || slices.Contains(m.settings.CommandHistory, edit) {
		t.Fatal("cancelled edit changed the active view or history")
	}
	runSubmitted(m, edit)
	typeHelpQuery(m, "topology.kubernetes.io/zone")
	key(m, "enter")
	groupControl(m, 's')
	key(m, "esc")
	if !slices.Equal(m.columns(), []string{"name"}) || !slices.Contains(m.settings.CommandHistory, edit) {
		t.Fatal("saved edit did not remove the existing label column")
	}
	runSubmitted(m, "nodeviews @delete capacity")
	saved, err := LoadSettings("")
	if err != nil {
		t.Fatal(err)
	}
	state := saved.Config.Contexts["demo"]
	if len(state.NodeViews) != 0 || state.DefaultNodeView != "" || m.activeNodeView() != "" {
		t.Fatal("deletion did not clear the inherited view and default")
	}
	if saved.Config.Contexts["other"].DefaultNodeView != "capacity" {
		t.Fatal("deletion changed another context's default")
	}
	assertConfigContents(t, fragment, source)
	if _, err := os.Stat(filepath.Join(fragmentDir(base), "local.yaml")); err != nil {
		t.Fatal(err)
	}
}
