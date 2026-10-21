package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
	"github.com/z0mbix/kqt/internal/kube"
	yaml "go.yaml.in/yaml/v3"
)

// Merge only changed preferences into the latest file. YAML nodes preserve
// comments and unrelated settings edited while kqt is open.
func (s *Settings) savePatch(patch map[string]any) error {
	path := s.Path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	data, err := readConfig(path)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return err
	}
	var output []byte
	if filepath.Ext(path) == ".toml" {
		values := map[string]any{}
		if missing {
			initial, _ := toml.Marshal(s.Config)
			if err = toml.Unmarshal(initial, &values); err != nil {
				return err
			}
		} else {
			c := defaultConfig()
			if err = decodeConfig(path, data, &c); err != nil {
				return err
			}
			if err = toml.Unmarshal(data, &values); err != nil {
				return err
			}
		}
		mergeMaps(values, patch)
		output, err = toml.Marshal(values)
	} else {
		var node yaml.Node
		if missing {
			if err = node.Encode(s.Config); err != nil {
				return err
			}
		} else {
			c := defaultConfig()
			if err = decodeConfig(path, data, &c); err != nil {
				return fmt.Errorf("config changed: %w", err)
			}
			if err = yaml.Unmarshal(data, &node); err != nil {
				return err
			}
		}
		root := &node
		if node.Kind == yaml.DocumentNode {
			root = node.Content[0]
		}
		if root.Kind != yaml.MappingNode {
			return fmt.Errorf("config must be a YAML mapping")
		}
		var changes yaml.Node
		if err = changes.Encode(patch); err != nil {
			return err
		}
		mergeNodes(root, &changes)
		var buf bytes.Buffer
		encoder := yaml.NewEncoder(&buf)
		encoder.SetIndent(2)
		err = encoder.Encode(&node)
		if closeErr := encoder.Close(); err == nil {
			err = closeErr
		}
		output = buf.Bytes()
	}
	if err != nil {
		return err
	}
	// Validate before replacing the file, including layout inheritance.
	check := defaultConfig()
	if err = decodeConfig(path, output, &check); err != nil {
		return err
	}
	if _, err = resolveLayouts(check.Layouts); err != nil {
		return err
	}
	if _, err = downloadDirectory(check.DownloadDir); err != nil {
		return fmt.Errorf("invalid download_dir: %w", err)
	}
	if err = validateContextAliases(check.ContextAliases); err != nil {
		return err
	}
	if err = validateLogPreferences(check.Logs); err != nil {
		return err
	}
	if err = validateInspectorPreferences(check); err != nil {
		return err
	}
	if err = validateDebugConfig(check.Debug); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".kqt-config-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(output)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
func mergeMaps(dst, src map[string]any) {
	for k, v := range src {
		if v == nil {
			delete(dst, k)
			continue
		}
		if child, ok := v.(map[string]any); ok {
			old, ok := dst[k].(map[string]any)
			if !ok {
				old = map[string]any{}
			}
			mergeMaps(old, child)
			dst[k] = old
		} else {
			dst[k] = v
		}
	}
}
func mergeNodes(dst, src *yaml.Node) {
	for i := 0; i < len(src.Content); i += 2 {
		key, value := src.Content[i], src.Content[i+1]
		found := false
		for j := 0; j < len(dst.Content); j += 2 {
			if dst.Content[j].Value != key.Value {
				continue
			}
			old := dst.Content[j+1]
			if value.Tag == "!!null" {
				dst.Content = append(dst.Content[:j], dst.Content[j+2:]...)
				found = true
				break
			}
			if old.Kind == yaml.MappingNode && value.Kind == yaml.MappingNode && key.Value != "context_aliases" {
				mergeNodes(old, value)
			} else {
				value.HeadComment = old.HeadComment
				value.LineComment = old.LineComment
				value.FootComment = old.FootComment
				dst.Content[j+1] = value
			}
			found = true
			break
		}
		if !found && value.Tag != "!!null" {
			dst.Content = append(dst.Content, key, value)
		}
	}
}
func (m *model) savePreferences(patch map[string]any) {
	if err := m.settings.savePatch(patch); err != nil {
		m.commandConfigError = true
		m.notice = "Unable to save preferences: " + kube.Safe(err.Error())
	}
}
func (m *model) saveRoute() {
	if m.route.resource.ID() == "" || m.contextName == "" || m.backend == nil {
		return
	}
	state := contextState{View: m.route.resource.ID(), Namespace: m.route.namespace, AllNamespaces: m.route.namespace == ""}
	m.settings.Config.Contexts[m.contextName] = state
	m.settings.Config.View = state.View
	m.settings.Config.Namespace = state.Namespace
	m.settings.Config.AllNamespaces = state.AllNamespaces
	m.settings.Config.LastContext = m.contextName
	m.savePreferences(map[string]any{"view": state.View, "namespace": state.Namespace, "all_namespaces": state.AllNamespaces, "last_context": m.contextName, "contexts": map[string]any{m.contextName: map[string]any{"view": state.View, "namespace": state.Namespace, "all_namespaces": state.AllNamespaces}}})
}
func (m *model) applyLayout(name string) bool {
	p, ok := m.settings.Layouts[name]
	if !ok {
		m.notice = "Unknown layout: " + kube.Safe(name)
		return false
	}
	m.layout = name
	m.wide = p.Wide != nil && *p.Wide
	m.views = cloneViews(m.settings.Config.Views)
	for k, v := range p.Views {
		m.views[k] = v
	}
	key := ""
	if selected := m.selectedKey(); selected != "" {
		key = selected
	}
	m.applyView()
	m.rebuild(key)
	m.resize()
	return true
}
func (m *model) selectLayout(name string) {
	profile, ok := m.settings.Layouts[name]
	if !ok {
		m.applyLayout(name)
		return
	}
	position := ""
	if profile.Inspector == "none" {
		position = m.inspectorPlacement()
	}
	m.settings.Config.InspectorVisible = nil
	m.settings.Config.InspectorPosition = position
	if m.applyLayout(name) {
		m.settings.Config.Layout = name
		patch := map[string]any{"layout": name, "inspector_visible": nil, "inspector_position": nil}
		if position != "" {
			patch["inspector_position"] = position
		}
		m.savePreferences(patch)
	}
}
func (m *model) saveLayout(name string) {
	if !validName(name) {
		m.notice = "Use :write NAME with a simple layout name"
		return
	}
	if name == "classic" || name == "inspector" || name == "bottom" {
		m.notice = "Choose a new name for your layout"
		return
	}
	p := m.settings.Layouts[m.layout]
	p.Inspector = m.inspectorProfile().Inspector
	p.Views = cloneViews(m.views)
	v, ok := p.Views[m.route.resource.ID()]
	if !ok {
		v = m.views[m.route.resource.GVR.Resource]
	}
	if len(v.Columns) == 0 {
		v.Columns = kube.DefaultColumns(m.route.resource.Kind)
	}
	v.Sort, v.Descending = m.sortColumn, m.descending
	p.Views[m.route.resource.ID()] = v
	wide := m.wide
	p.Wide = &wide
	p.Extends = ""
	m.settings.Config.Layouts[name] = p
	layouts, err := resolveLayouts(m.settings.Config.Layouts)
	if err != nil {
		m.notice = err.Error()
		return
	}
	m.settings.Layouts = layouts
	m.layout = name
	m.settings.Config.Layout = name
	m.savePreferences(map[string]any{"layout": name, "layouts": map[string]any{name: p}})
	if m.notice == "" {
		m.notice = "Saved layout " + name
	}
}
func (m *model) saveColumns(v viewConfig) {
	name := m.route.resource.ID()
	m.views[name] = v
	// Preserve an explicit false when merging into a previously descending view.
	values := map[string]any{"columns": v.Columns, "sort": v.Sort, "descending": v.Descending}
	if p, ok := m.settings.Config.Layouts[m.layout]; ok {
		if p.Views == nil {
			p.Views = map[string]viewConfig{}
		}
		p.Views[name] = v
		m.settings.Config.Layouts[m.layout] = p
		m.settings.Layouts[m.layout].Views[name] = v
		m.savePreferences(map[string]any{"layouts": map[string]any{m.layout: map[string]any{"views": map[string]any{name: values}}}})
	} else {
		m.settings.Config.Views[name] = v
		m.savePreferences(map[string]any{"views": map[string]any{name: values}})
	}
}
