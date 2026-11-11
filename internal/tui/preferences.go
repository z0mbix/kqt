package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
	"github.com/z0mbix/kqt/internal/kube"
	yaml "go.yaml.in/yaml/v3"
)

// Merge only changed preferences into the latest file. YAML nodes preserve
// comments and unrelated settings edited while kqt is open.
func (s *Settings) savePatch(patch map[string]any) error {
	fragments, err := configFragments(s.Path)
	if err != nil {
		return err
	}
	if s.splitConfig != (fragments != nil) {
		return fmt.Errorf("%s changed; restart kqt before saving preferences", fragmentDir(s.Path))
	}
	if s.splitConfig {
		return s.saveLocalPatch(fragments, patch)
	}
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
		output, err = patchYAMLConfig(data, s.Config, patch, false)
	}
	if err != nil {
		return err
	}
	// Validate before replacing the file, including layout inheritance.
	check := defaultConfig()
	if err = decodeConfig(path, output, &check); err != nil {
		return err
	}
	if err = validateHistoryLimit(check.HistoryLimit); err != nil {
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
	if err = normalizeNamespacePreferences(&check); err != nil {
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
	return replaceConfig(path, output)
}

func (s *Settings) saveLocalPatch(fragments []string, patch map[string]any) error {
	path := filepath.Join(fragmentDir(s.Path), localConfigName)
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%s: the managed config must be a regular file", path)
	}
	data, err := readConfig(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	output, err := patchYAMLConfig(data, map[string]any{}, patch, true)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	merged, err := loadConfig(s.Path, s.explicitConfig, fragments, output)
	if err != nil {
		return err
	}
	if _, err = validateSettings(s.Path, merged); err != nil {
		return fmt.Errorf("config %s with conf.d: %w", s.Path, err)
	}
	return replaceConfig(path, output)
}

func patchYAMLConfig(data []byte, initial any, patch map[string]any, keepNull bool) ([]byte, error) {
	var node yaml.Node
	var prefix []byte
	if data != nil {
		if err := decodeYAMLConfig(data, &config{}); err != nil {
			if !keepNull || !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("config changed: %w", err)
			}
			prefix = data // Preserve comments in an otherwise empty local file.
		} else if err := yaml.Unmarshal(data, &node); err != nil {
			return nil, err
		}
	}
	if node.Kind == 0 {
		if err := node.Encode(initial); err != nil {
			return nil, err
		}
	}
	root := &node
	if node.Kind == yaml.DocumentNode {
		root = node.Content[0]
	}
	if keepNull && root.Kind == yaml.ScalarNode && root.ShortTag() == "!!null" {
		root.Kind, root.Tag, root.Value, root.Style = yaml.MappingNode, "!!map", "", 0
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config must be a YAML mapping")
	}
	var changes yaml.Node
	if err := changes.Encode(patch); err != nil {
		return nil, err
	}
	mergeNodes(root, &changes, keepNull)
	var buf bytes.Buffer
	if len(prefix) > 0 {
		buf.Write(prefix)
		if prefix[len(prefix)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	err := encoder.Encode(&node)
	if closeErr := encoder.Close(); err == nil {
		err = closeErr
	}
	return buf.Bytes(), err
}

func replaceConfig(path string, output []byte) error {
	if len(output) > maxConfigSize {
		return fmt.Errorf("configuration too large: %s", path)
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
func mergeNodes(dst, src *yaml.Node, keepNull bool) {
	for i := 0; i < len(src.Content); i += 2 {
		key, value := src.Content[i], src.Content[i+1]
		index := -1
		for j := 0; j < len(dst.Content); j += 2 {
			if dst.Content[j].Value == key.Value {
				index = j
				break
			}
		}
		if value.Tag == "!!null" && !keepNull {
			if index >= 0 {
				dst.Content = append(dst.Content[:index], dst.Content[index+2:]...)
			}
			continue
		}
		if value.Kind == yaml.MappingNode {
			if index >= 0 && dst.Content[index+1].Kind == yaml.MappingNode && (keepNull || key.Value != "context_aliases") {
				mergeNodes(dst.Content[index+1], value, keepNull)
				continue
			}
			if !keepNull {
				fresh := *value
				fresh.Content = nil
				mergeNodes(&fresh, value, false)
				value = &fresh
			}
		}
		if index < 0 {
			dst.Content = append(dst.Content, key, value)
		} else {
			old := dst.Content[index+1]
			value.HeadComment = old.HeadComment
			value.LineComment = old.LineComment
			value.FootComment = old.FootComment
			dst.Content[index+1] = value
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
	state := m.settings.Config.Contexts[m.contextName]
	state.View, state.Namespace, state.AllNamespaces = m.route.resource.ID(), m.route.namespace, m.route.namespace == ""
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
	if o, ok := m.current(); ok {
		key = kube.Identity(o)
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
	views := map[string]any{}
	for key := range m.settings.Config.Layouts[name].Views {
		views[key] = nil // Remove views that aren't in the replacement snapshot.
	}
	for key, view := range p.Views {
		views[key] = viewPreferences(view)
	}
	m.settings.Config.Layouts[name] = p
	layouts, err := resolveLayouts(m.settings.Config.Layouts)
	if err != nil {
		m.notice = err.Error()
		return
	}
	m.settings.Layouts = layouts
	m.layout = name
	m.settings.Config.Layout = name
	profile := map[string]any{"extends": p.Extends, "inspector": p.Inspector, "inspector_percent": p.InspectorPercent, "wide": wide, "views": views}
	m.savePreferences(map[string]any{"layout": name, "layouts": map[string]any{name: profile}})
	if m.notice == "" {
		m.notice = "Saved layout " + name
	}
}

func viewPreferences(v viewConfig) map[string]any {
	return map[string]any{"columns": v.Columns, "sort": v.Sort, "descending": v.Descending}
}

func (m *model) saveColumns(v viewConfig) {
	name := m.route.resource.ID()
	m.views[name] = v
	// Preserve an explicit false when merging into a previously descending view.
	values := viewPreferences(v)
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
