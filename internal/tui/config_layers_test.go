package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeConfigFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertConfigContents(t *testing.T, path, want string) {
	t.Helper()
	if got, err := os.ReadFile(path); err != nil || string(got) != want {
		t.Fatalf("%s changed: %q (%v)", path, got, err)
	}
}

func TestConfigFragmentsMergeMappingsInFilenameOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	dir := fragmentDir(path)
	writeConfigFixture(t, path, `
editor: nvim
read_only: true
logs: {timestamps: true, tail_lines: 200}
context_aliases: {old: work, common: work, numbered: 001}
contexts:
  work: &work
    view: deployments
    namespace: apps
    namespace_groups: {team: [apps, ops], shared: [shared]}
  other:
    <<: *work
    namespace: 007
    namespace_groups: {numbered: [008, 009]}
  001: {view: pods}
layouts:
  parent: {inspector: bottom, inspector_percent: 42}
`)
	writeConfigFixture(t, filepath.Join(dir, "20-device.toml"), `
editor = "vim"
[contexts.work]
namespace = "work,shared"
[contexts.work.namespace_groups]
team = ["work"]
[contexts."001"]
namespace = "numbered"
[context_aliases]
dev = "work"
[layouts.child]
extends = "parent"
`)
	writeConfigFixture(t, filepath.Join(dir, "10-defaults.yaml"), "editor: emacs\n")
	writeConfigFixture(t, filepath.Join(dir, "30-extra.yml"), `
read_only: false
logs: {timestamps: false, tail_lines: null}
context_aliases: {old: null}
contexts:
  work:
    namespace_groups: {qa: [qa]}
`)
	writeConfigFixture(t, filepath.Join(dir, "zz-last.yaml"), "editor: nano\nlogs: {follow: false}\n")
	writeConfigFixture(t, filepath.Join(dir, "kqt-local.yaml"), "editor: micro\nlogs: {follow: true}\n")
	writeConfigFixture(t, filepath.Join(dir, "local.yaml"), "editor: code\nlogs: {wrap: false}\n")
	writeConfigFixture(t, filepath.Join(dir, "empty.yaml"), "# intentional placeholder\n")
	writeConfigFixture(t, filepath.Join(dir, ".hidden.yaml"), "not valid yaml : [\n")
	writeConfigFixture(t, filepath.Join(dir, "README.txt"), "not a config file\n")
	writeConfigFixture(t, filepath.Join(dir, "archive.yaml", "ignore.yaml"), "not valid yaml : [\n")
	s, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Config
	if c.Editor != "code" || c.ReadOnly || c.Logs.Timestamps || c.Logs.Wrap || !c.Logs.Follow || c.Logs.TailLines != nil {
		t.Fatalf("layer precedence, false/null or defaults lost: %+v", c)
	}
	work := c.Contexts["work"]
	if work.View != "deployments" || work.Namespace != "work,shared" || !slices.Equal(work.NamespaceGroups["team"], []string{"work"}) ||
		!slices.Equal(work.NamespaceGroups["shared"], []string{"shared"}) || !slices.Equal(work.NamespaceGroups["qa"], []string{"qa"}) {
		t.Fatalf("recursive merge or list replacement failed: %+v", work)
	}
	if numbered := c.Contexts["001"]; numbered.View != "pods" || numbered.Namespace != "numbered" {
		t.Fatalf("numeric YAML mapping key changed identity: %+v", c.Contexts)
	}
	if other := c.Contexts["other"]; other.View != "deployments" || other.Namespace != "007" || !slices.Equal(other.NamespaceGroups["numbered"], []string{"008", "009"}) {
		t.Fatalf("YAML merge key or numeric-looking string changed: %+v", other)
	}
	if _, exists := c.ContextAliases["old"]; exists || c.ContextAliases["common"] != "work" || c.ContextAliases["dev"] != "work" || c.ContextAliases["numbered"] != "001" {
		t.Fatalf("alias merge/removal failed: %v", c.ContextAliases)
	}
	if child := s.Layouts["child"]; child.Inspector != "bottom" || child.InspectorPercent != 42 {
		t.Fatalf("layout inheritance did not see earlier files: %+v", child)
	}
}

func TestSplitConfigKeepsSharedFilesAndSavesContextEditsLocally(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	base := DefaultConfigPath()
	dir := fragmentDir(base)
	shared := filepath.Join(t.TempDir(), "shared.yaml")
	const main = "version: 1\neditor: vim # shared static setting\ncontext_aliases: {legacy: demo}\n"
	writeConfigFixture(t, shared, main)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, base); err != nil {
		t.Fatal(err)
	}
	device := filepath.Join(dir, "90-work.yaml")
	const work = `
context_aliases: {work: demo}
contexts:
  demo:
    namespace_groups:
      development: [default, platform]
      obsolete: [platform]
      retained: [default]
`
	writeConfigFixture(t, device, work)
	legacyLocal := filepath.Join(dir, "kqt-local.yaml")
	const legacy = "context_aliases: {legacy-local: demo}\n"
	writeConfigFixture(t, legacyLocal, legacy)
	local := filepath.Join(dir, "local.yaml")
	writeConfigFixture(t, local, "# local comment\n")
	s, err := LoadSettings("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := newModel(context.Background(), s, Options{Demo: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.close)
	_, cmd := m.Update(m.connect(m.contextName)())
	m.Update(cmd())
	applyNamespaceCommand(t, m, "ns @development")
	if !m.renameNamespaceGroup("development", "dev-team") {
		t.Fatal(m.notice)
	}
	runSubmitted(m, "ns @delete obsolete")
	key(m, "y")
	m.aliasCommand("legacy", true)
	m.aliasCommand("local demo", false)
	saved, err := LoadSettings("")
	if err != nil {
		t.Fatal(err)
	}
	state := saved.Config.Contexts["demo"]
	if state.Namespace != "default,platform" || len(state.NamespaceGroups) != 2 || !slices.Equal(state.NamespaceGroups["dev-team"], []string{"default", "platform"}) ||
		!slices.Equal(state.NamespaceGroups["retained"], []string{"default"}) {
		t.Fatalf("saved route, rename or deletion did not survive reload: %+v", state)
	}
	if _, exists := saved.Config.ContextAliases["legacy"]; exists || saved.Config.ContextAliases["work"] != "demo" || saved.Config.ContextAliases["local"] != "demo" || saved.Config.ContextAliases["legacy-local"] != "demo" {
		t.Fatalf("saved alias changes lost or copied over other aliases: %v", saved.Config.ContextAliases)
	}
	assertConfigContents(t, base, main)
	assertConfigContents(t, device, work)
	assertConfigContents(t, legacyLocal, legacy)
	if link, err := os.Readlink(base); err != nil || link != shared {
		t.Fatalf("main config symlink changed: %q (%v)", link, err)
	}
	data, err := os.ReadFile(local)
	if err != nil || !strings.Contains(string(data), "# local comment") || strings.Contains(string(data), "editor:") || strings.Contains(string(data), "retained:") || strings.Contains(string(data), "work:") {
		t.Fatalf("local layer should contain only changed preferences: %s (%v)", data, err)
	}
	if info, err := os.Stat(local); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("local layer must have private permissions: %v (%v)", info, err)
	}
	writeConfigFixture(t, device, "logs: {tail_lines: invalid}\n")
	if err := saved.savePatch(map[string]any{"theme": "nord"}); err == nil || !strings.Contains(err.Error(), "90-work.yaml") {
		t.Fatalf("save didn't reject invalid external change: %v", err)
	}
	assertConfigContents(t, local, string(data))
}

func TestConfigFragmentsRejectInvalidLayers(t *testing.T) {
	for _, body := range []string{
		"contexts: {work: {namesapce: apps}}\n",
		"logs: {wrap: [true]}\n",
		"theme: nord\n---\ntheme: nord\n",
		"theme: nord\ntheme: nord\n",
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		writeConfigFixture(t, path, "theme = 'nord'\n")
		writeConfigFixture(t, filepath.Join(fragmentDir(path), "10-bad.yaml"), body)
		writeConfigFixture(t, filepath.Join(fragmentDir(path), "20-override.yaml"), "theme: nord\ncontexts: null\nlogs: null\n")
		if _, err := LoadSettings(path); err == nil || !strings.Contains(err.Error(), "10-bad.yaml") {
			t.Fatalf("bad fragment %q did not name its file: %v", body, err)
		}
	}
}

func TestSplitConfigSavedLayoutReplacesInheritedSettings(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	base := filepath.Join(t.TempDir(), "config.yaml")
	writeConfigFixture(t, base, "layout: classic\n")
	device := filepath.Join(fragmentDir(base), "20-layouts.yaml")
	const source = `layouts:
  team:
    extends: bottom
    wide: true
    views:
      pods: {columns: [name, age], sort: age, descending: true}
      nodes: {columns: [name, status]}
`
	writeConfigFixture(t, device, source)
	s, err := LoadSettings(base)
	if err != nil {
		t.Fatal(err)
	}
	m, err := newModel(context.Background(), s, Options{Demo: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.close)
	_, cmd := m.Update(m.connect(m.contextName)())
	m.Update(cmd())
	m.setOrder("name", false)
	m.saveLayout("team")
	saved, err := LoadSettings(base)
	if err != nil {
		t.Fatal(err)
	}
	layout := saved.Config.Layouts["team"]
	if saved.Config.Layout != "team" || layout.Extends != "" || layout.Inspector != "none" || layout.Wide == nil || *layout.Wide || len(layout.Views) != 1 || layout.Views["pods"].Descending || layout.Views["pods"].Sort != "name" {
		t.Fatalf("saved layout inherited values that should be replaced: %+v", layout)
	}
	assertConfigContents(t, device, source)
}

func TestSplitLegacyConfigDoesNotRewriteBaseAndReadsDebugProfiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := DefaultConfigPath()
	legacy := strings.TrimSuffix(base, ".yaml") + ".toml"
	const source = "theme='nord'\n[debug.profiles.default]\nimage='registry.example/legacy:1'\n"
	writeConfigFixture(t, legacy, source)
	device := filepath.Join(fragmentDir(base), "20-debug.yaml")
	const fragment = "debug:\n  profiles:\n    default-2:\n      image: registry.example/device:1\n"
	writeConfigFixture(t, device, fragment)
	s, err := LoadSettings("")
	if err != nil {
		t.Fatal(err)
	}
	name, err := s.saveDebugProfile(debugProfile{Image: "registry.example/new:1"})
	if err != nil || name != "default-3" {
		t.Fatalf("debug save should avoid existing profiles from every layer: %s (%v)", name, err)
	}
	assertConfigContents(t, legacy, source)
	assertConfigContents(t, device, fragment)
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatalf("split save created a main YAML file: %v", err)
	}
	reloaded, err := LoadSettings("")
	if err != nil || reloaded.Config.Theme != "nord" || len(reloaded.Config.Debug.Profiles) != 3 || reloaded.Config.Debug.DefaultProfile != name {
		t.Fatalf("split legacy config or debug settings lost: %v", err)
	}
}

func TestSplitConfigDoesNotFallBackToSharedWrites(t *testing.T) {
	base := filepath.Join(t.TempDir(), "config.yaml")
	const source = "theme: nord\n"
	writeConfigFixture(t, base, source)
	dir := fragmentDir(base)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(base)
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(dir, localConfigName)
	if err := os.Symlink(base, local); err != nil {
		t.Fatal(err)
	}
	if err := s.savePatch(map[string]any{"editor": "vim"}); err == nil {
		t.Fatal("saving through a managed-file symlink was allowed")
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := s.savePatch(map[string]any{"editor": "vim"}); err == nil {
		t.Fatal("saving after conf.d was removed fell back to the main file")
	}
	assertConfigContents(t, base, source)
}
