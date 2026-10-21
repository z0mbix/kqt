package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)

func applyNamespaceCommand(t *testing.T, m *model, command string) {
	t.Helper()
	cmd := runSubmitted(m, command)
	if cmd == nil {
		t.Fatalf("namespace command did not execute: %s: %s", command, m.notice)
	}
	m.Update(cmd())
}

func TestNamespaceListsGroupsPersistenceAndContextIsolation(t *testing.T) {
	for _, extension := range []string{"yaml", "toml"} {
		t.Run(extension, func(t *testing.T) {
			m := demoModel(t)
			m.settings.Path = filepath.Join(t.TempDir(), "config."+extension)
			applyNamespaceCommand(t, m, "ns default, platform,default")
			if m.route.namespace != "default,platform" || len(m.visible) != 26 || !slices.Contains(m.columns(), "namespace") {
				t.Fatalf("combined selection: scope=%q rows=%d visible=%d columns=%v error=%s notice=%s", m.route.namespace, len(m.rows), len(m.visible), m.columns(), m.dataErr, m.notice)
			}
			runSubmitted(m, "ns save production")
			if m.notice != "Saved namespace group production" || !slices.Contains(m.settings.CommandHistory, "ns save production") {
				t.Fatal("namespace group was not saved or remembered", m.notice)
			}
			applyNamespaceCommand(t, m, "ns default")
			if len(m.visible) != 12 || slices.Contains(m.columns(), "namespace") {
				t.Fatal("single namespace behaviour changed")
			}
			applyNamespaceCommand(t, m, "ns @production")
			m.Update(tea.WindowSizeMsg{Width: 180, Height: 40})
			if !strings.Contains(ansi.Strip(m.View().Content), "Namespaces: production (2)") {
				t.Fatal("header did not identify the selected group")
			}
			saved, err := LoadSettings(m.settings.Path)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Config.Namespace != "default,platform" || saved.Config.AllNamespaces || !slices.Equal(saved.Config.Contexts["demo"].NamespaceGroups["production"], []string{"default", "platform"}) {
				t.Fatal("config lost the selection or group")
			}
			restarted, err := newModel(context.Background(), saved, Options{Demo: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.close()
			if restarted.route.namespace != "default,platform" {
				t.Fatal("restart lost namespace selection")
			}
			override, err := newModel(context.Background(), saved, Options{Demo: true, Namespace: "kube-system", ScopeExplicit: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer override.close()
			if override.route.namespace != "kube-system" {
				t.Fatal("explicit namespace did not override saved selection")
			}
			m.contextName = "other"
			if cmd := m.namespaceCommand("@production"); cmd != nil || !strings.HasPrefix(m.notice, "Unknown namespace group") {
				t.Fatal("namespace group leaked across contexts")
			}
		})
	}
}

func TestInvalidNamespaceSelectionsDoNotChangeScopeConfigOrHistory(t *testing.T) {
	m := demoModel(t)
	applyNamespaceCommand(t, m, "ns default,platform")
	before, _ := os.ReadFile(m.settings.Path)
	history := slices.Clone(m.settings.CommandHistory)
	for _, command := range []string{"ns default,", "ns ,default", "ns default,,platform", "ns default,*", "ns @unknown", "ns save invalid name"} {
		if cmd := runSubmitted(m, command); cmd != nil {
			t.Fatalf("invalid namespace command executed: %s", command)
		}
		after, _ := os.ReadFile(m.settings.Path)
		if m.route.namespace != "default,platform" || string(before) != string(after) || !slices.Equal(history, m.settings.CommandHistory) {
			t.Fatal("invalid selection changed saved scope or history")
		}
	}
	applyNamespaceCommand(t, m, "ns all")
	if cmd := runSubmitted(m, "ns save everything"); cmd != nil || strings.HasPrefix(m.notice, "Saved") {
		t.Fatal("all namespaces became an implicit explicit group")
	}
	// A group can name a literal namespace called all without selecting the
	// special all-namespace mode used by the :ns all shortcut.
	state := m.settings.Config.Contexts[m.contextName]
	state.NamespaceGroups = map[string][]string{"literal": {"all"}}
	m.settings.Config.Contexts[m.contextName] = state
	m.namespaceCommand("@literal")
	if m.route.namespace != "all" || m.options.AllNamespaces {
		t.Fatal("literal namespace group broadened to all namespaces")
	}
}

func TestNamespaceCompletionAndConfigValidation(t *testing.T) {
	m := demoModel(t)
	applyNamespaceCommand(t, m, "ns default,platform")
	runSubmitted(m, "ns save production")
	for input, want := range map[string]string{"ns default,pla": "ns default,platform", "ns default, pla": "ns default, platform", "ns @pro": "ns @production", "ns save pro": "ns save production"} {
		m.startInput("command", input)
		m.completeCommand(false)
		if m.input.Value() != want {
			t.Fatalf("completion %q = %q, want %q", input, m.input.Value(), want)
		}
	}
	for _, body := range []string{"namespace: 'default,'\n", "contexts:\n  work:\n    namespace: 'default,*'\n", "contexts:\n  work:\n    namespace_groups:\n      broken: []\n", "contexts:\n  work:\n    namespace_groups:\n      prod: ['*']\n"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(path, []byte("version: 1\n"+body), 0600)
		if _, err := LoadSettings(path); err == nil {
			t.Fatal("invalid saved scope accepted", body)
		}
	}
}

func TestNamespaceWatchResetsOnlyItsOwnResourcesAndKeepsWarnings(t *testing.T) {
	m := demoModel(t)
	applyNamespaceCommand(t, m, "ns default,platform")
	feed := &resourceFeed{}
	m.resourceFeed = feed
	m.resourceCache = map[string]unstructured.Unstructured{}
	for _, object := range m.rows {
		m.resourceCache[kube.Identity(object)] = object
	}
	initial := slices.Clone(m.rows)
	changed := *m.rows[0].DeepCopy()
	changed.SetNamespace("default")
	changed.SetName("replacement")
	changed.SetUID(types.UID("default/replacement"))
	batch := func(changes ...kube.EventUpdate) {
		m.resourceBatch(resourceBatchMsg{generation: m.generation, token: m.resourceToken, feed: feed, changes: changes})
	}
	batch(kube.EventUpdate{Namespace: "default", Reset: true}, kube.EventUpdate{Namespace: "default", Type: watch.Added, Object: changed})
	if len(m.rows) != 26 {
		t.Fatal("incomplete namespace relist replaced the displayed snapshot")
	}
	batch(kube.EventUpdate{Namespace: "default", Synced: true}, kube.EventUpdate{ScopeStatus: true, State: "partial", Synced: true, Message: "Partial namespace results: platform forbidden"})
	if len(m.rows) != 15 || m.namespaceWarning == "" {
		t.Fatalf("namespace reset rows=%d cache=%d stages=%v warning=%q", len(m.rows), len(m.resourceCache), m.resourceNamespaceStaging, m.namespaceWarning)
	}
	for _, object := range m.rows {
		if object.GetNamespace() == "default" && object.GetName() != "replacement" {
			t.Fatal("old resources survived the namespace resync")
		}
	}
	batch(kube.EventUpdate{Namespace: "default", Type: watch.Modified, Object: changed})
	if m.namespaceWarning == "" {
		t.Fatal("healthy update hid a failed namespace")
	}
	before := len(m.rows)
	out := *changed.DeepCopy()
	out.SetNamespace("unselected")
	batch(kube.EventUpdate{Namespace: "unselected", Type: watch.Added, Object: out})
	if len(m.rows) != before {
		t.Fatal("unselected namespace entered the cache")
	}
	// A partial polling refresh keeps the last data from failed namespaces.
	m.stopResourceWatch()
	m.rows = initial
	m.Update(snapshotMsg{generation: m.generation, request: m.request, rows: []unstructured.Unstructured{changed}, err: &kube.NamespaceErrors{Total: 2, Available: 1, Failures: map[string]error{"platform": errors.New("forbidden")}}})
	if len(m.rows) != 15 || m.namespaceWarning == "" || m.dataErr != "" {
		t.Fatal("partial polling lost usable/stale rows or hid incomplete results")
	}
}

func TestNamespaceScopeSurvivesCustomColumnsAndNodeDrillDown(t *testing.T) {
	m := demoModel(t)
	applyNamespaceCommand(t, m, "ns default,platform")
	m.views["pods"] = viewConfig{Columns: []string{"name", "status"}}
	if !slices.Contains(m.columns(), "namespace") {
		t.Fatal("custom columns hid the namespace of combined resources")
	}
	cmd := m.command("nodes")
	m.Update(cmd())
	if len(m.rows) != 3 || slices.Contains(m.columns(), "namespace") {
		t.Fatal("cluster-scoped listing was duplicated or showed a namespace column")
	}
	cmd = m.drillDown()
	if cmd == nil || m.route.namespace != "default,platform" {
		t.Fatal("node drill-down broadened the selected namespaces")
	}
	m.Update(cmd())
	for _, object := range m.rows {
		if !kube.NamespaceIncluded("default,platform", object.GetNamespace()) {
			t.Fatal("node drill-down included an unselected namespace")
		}
	}
}
