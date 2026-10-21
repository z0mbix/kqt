package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	yaml "go.yaml.in/yaml/v3"
)

type viewConfig struct {
	Columns    []string `toml:"columns" yaml:"columns"`
	Sort       string   `toml:"sort,omitempty" yaml:"sort,omitempty"`
	Descending bool     `toml:"descending,omitempty" yaml:"descending,omitempty"`
}
type contextState struct {
	View            string              `toml:"view,omitempty" yaml:"view,omitempty"`
	Namespace       string              `toml:"namespace,omitempty" yaml:"namespace,omitempty"`
	AllNamespaces   bool                `toml:"all_namespaces,omitempty" yaml:"all_namespaces,omitempty"`
	NamespaceGroups map[string][]string `toml:"namespace_groups,omitempty" yaml:"namespace_groups,omitempty"`
}
type layoutProfile struct {
	Wide             *bool                 `toml:"wide,omitempty" yaml:"wide,omitempty"`
	Extends          string                `toml:"extends,omitempty" yaml:"extends,omitempty"`
	Inspector        string                `toml:"inspector,omitempty" yaml:"inspector,omitempty"`
	InspectorPercent int                   `toml:"inspector_percent,omitempty" yaml:"inspector_percent,omitempty"`
	Views            map[string]viewConfig `toml:"views,omitempty" yaml:"views,omitempty"`
}
type config struct {
	Version           int                      `toml:"version" yaml:"version"`
	Theme             string                   `toml:"theme" yaml:"theme"`
	Layout            string                   `toml:"layout" yaml:"layout"`
	View              string                   `toml:"view,omitempty" yaml:"view,omitempty"`
	Namespace         string                   `toml:"namespace,omitempty" yaml:"namespace,omitempty"`
	AllNamespaces     bool                     `toml:"all_namespaces,omitempty" yaml:"all_namespaces,omitempty"`
	LastContext       string                   `toml:"last_context,omitempty" yaml:"last_context,omitempty"`
	InspectorFormat   string                   `toml:"inspector_format,omitempty" yaml:"inspector_format,omitempty"`
	InspectorPosition string                   `toml:"inspector_position,omitempty" yaml:"inspector_position,omitempty"`
	InspectorVisible  *bool                    `toml:"inspector_visible,omitempty" yaml:"inspector_visible,omitempty"`
	HeaderVisible     *bool                    `toml:"header_visible,omitempty" yaml:"header_visible,omitempty"`
	Refresh           string                   `toml:"refresh_interval" yaml:"refresh_interval"`
	Editor            string                   `toml:"editor,omitempty" yaml:"editor,omitempty"`
	Shell             string                   `toml:"shell,omitempty" yaml:"shell,omitempty"`
	DownloadDir       string                   `toml:"download_dir,omitempty" yaml:"download_dir,omitempty"`
	Logs              logPreferences           `toml:"logs" yaml:"logs"`
	Debug             debugConfig              `toml:"debug,omitempty" yaml:"debug,omitempty"`
	HistoryLimit      int                      `toml:"history_limit,omitempty" yaml:"history_limit,omitempty"`
	LegacyHistory     []string                 `toml:"command_history,omitempty" yaml:"command_history,omitempty"` // Read only for migration.
	ReadOnly          bool                     `toml:"read_only,omitempty" yaml:"read_only,omitempty"`
	Views             map[string]viewConfig    `toml:"views,omitempty" yaml:"views,omitempty"`
	Layouts           map[string]layoutProfile `toml:"layouts,omitempty" yaml:"layouts,omitempty"`
	Contexts          map[string]contextState  `toml:"contexts,omitempty" yaml:"contexts,omitempty"`
	ContextAliases    map[string]string        `toml:"context_aliases,omitempty" yaml:"context_aliases,omitempty"`
}
type Settings struct {
	Path           string
	Config         config
	Themes         map[string]theme
	Layouts        map[string]layoutProfile
	Interval       time.Duration
	CommandHistory []string
	historyPath    string
	historyWarning string
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

func validName(s string) bool { return identifier.MatchString(s) }

// Custom palettes retain Maestro's TOML format.
func decodeFile(path string, value interface{}) ([]byte, error) {
	data, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	err = toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return data, nil
}
func readConfig(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > 1024*1024 {
		return nil, fmt.Errorf("configuration too large: %s", path)
	}
	return data, nil
}
func DefaultConfigPath() string {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(root, "kqt", "config.yaml")
}
func defaultConfig() config {
	return config{Version: 1, Theme: "catppuccin-mocha", Layout: "inspector", View: "pods", InspectorFormat: "yaml", Refresh: "2s", HistoryLimit: defaultHistoryLimit, Logs: defaultLogPreferences(), Views: map[string]viewConfig{}, Layouts: map[string]layoutProfile{}, Contexts: map[string]contextState{}}
}
func decodeConfig(path string, data []byte, c *config) error {
	if filepath.Ext(path) == ".toml" {
		return toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(c)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(c); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("configuration must contain one YAML document")
	} else if err != io.EOF {
		return err
	}
	return nil
}
func LoadSettings(path string) (*Settings, error) {
	explicit := path != ""
	if path == "" {
		path = DefaultConfigPath()
	}
	source := path
	data, err := readConfig(source)
	if os.IsNotExist(err) && !explicit {
		legacy := strings.TrimSuffix(path, ".yaml") + ".toml"
		if legacyData, legacyErr := readConfig(legacy); legacyErr == nil {
			source, data, err = legacy, legacyData, nil
		} else if !os.IsNotExist(legacyErr) {
			return nil, legacyErr
		}
	}
	c := defaultConfig()
	if err != nil && (explicit || !os.IsNotExist(err)) {
		return nil, err
	}
	if err == nil {
		if err = decodeConfig(source, data, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
	}
	if c.Views == nil {
		c.Views = map[string]viewConfig{}
	}
	if c.Layouts == nil {
		c.Layouts = map[string]layoutProfile{}
	}
	if c.Contexts == nil {
		c.Contexts = map[string]contextState{}
	}
	if err = validateHistoryLimit(c.HistoryLimit); err != nil {
		return nil, err
	}
	if err = validateDebugConfig(c.Debug); err != nil {
		return nil, err
	}
	if err = validateLogPreferences(c.Logs); err != nil {
		return nil, err
	}
	if err = validateContextAliases(c.ContextAliases); err != nil {
		return nil, err
	}
	if c.Version != 1 {
		return nil, fmt.Errorf("config version must be 1")
	}
	if c.InspectorFormat != "yaml" && c.InspectorFormat != "json" {
		return nil, fmt.Errorf("inspector_format must be yaml or json")
	}
	if err := validateInspectorPreferences(c); err != nil {
		return nil, err
	}
	if c.Shell != "" {
		if _, err := editorArgs(c.Shell); err != nil {
			return nil, fmt.Errorf("invalid shell command: %w", err)
		}
	}
	if _, err := downloadDirectory(c.DownloadDir); err != nil {
		return nil, fmt.Errorf("invalid download_dir: %w", err)
	}
	if err = normalizeNamespacePreferences(&c); err != nil {
		return nil, err
	}
	d, err := time.ParseDuration(c.Refresh)
	if err != nil || d < 500*time.Millisecond || d > time.Minute {
		return nil, fmt.Errorf("refresh_interval must be between 500ms and 1m")
	}
	themes, err := loadThemes(filepath.Join(filepath.Dir(path), "themes"))
	if err != nil {
		return nil, err
	}
	if _, ok := themes[c.Theme]; !ok {
		return nil, fmt.Errorf("unknown theme %q", c.Theme)
	}
	if err = validateViews(c.Views); err != nil {
		return nil, err
	}
	layouts, err := resolveLayouts(c.Layouts)
	if err != nil {
		return nil, err
	}
	if _, ok := layouts[c.Layout]; !ok {
		return nil, fmt.Errorf("unknown layout %q; use :layouts", c.Layout)
	}
	return &Settings{Path: path, Config: c, Themes: themes, Layouts: layouts, Interval: d}, nil
}
func validateViews(views map[string]viewConfig) error {
	for name, v := range views {
		if len(v.Columns) == 0 || len(v.Columns) > 32 {
			return fmt.Errorf("view %s requires 1 to 32 columns", name)
		}
		for _, col := range v.Columns {
			if !validColumn(col) {
				return fmt.Errorf("view %s: invalid column %q", name, col)
			}
		}
		if v.Sort != "" && !validColumn(v.Sort) {
			return fmt.Errorf("view %s: invalid sort %q", name, v.Sort)
		}
	}
	return nil
}
func validColumn(s string) bool {
	if strings.HasPrefix(s, "/") {
		return len(s) <= 256 && !strings.ContainsAny(s, "\n\r\x1b") && !regexp.MustCompile(`~([^01]|$)`).MatchString(s)
	}
	return slices.Contains([]string{"name", "namespace", "kind", "status", "age", "ready", "restarts", "node", "ip", "type", "ports", "labels", "reason", "message", "lastseen", "object", "count", "cpu", "memory", "cpu%", "memory%"}, s)
}
func cloneViews(values map[string]viewConfig) map[string]viewConfig {
	r := map[string]viewConfig{}
	for k, v := range values {
		v.Columns = slices.Clone(v.Columns)
		r[k] = v
	}
	return r
}
func resolveLayouts(custom map[string]layoutProfile) (map[string]layoutProfile, error) {
	result := map[string]layoutProfile{"classic": {Inspector: "none", InspectorPercent: 38}, "inspector": {Inspector: "right", InspectorPercent: 38}, "bottom": {Inspector: "bottom", InspectorPercent: 38}}
	// Validate reserved names before the cache also contains custom parents.
	for name := range custom {
		if _, ok := result[name]; ok {
			return nil, fmt.Errorf("built-in layout %s cannot be replaced", name)
		}
	}
	visiting := map[string]bool{}
	var resolve func(string) (layoutProfile, error)
	resolve = func(name string) (layoutProfile, error) {
		if p, ok := result[name]; ok {
			return p, nil
		}
		p, ok := custom[name]
		if !ok {
			return p, fmt.Errorf("unknown parent layout %q", name)
		}
		if !validName(name) || visiting[name] {
			return p, fmt.Errorf("invalid layout name or inheritance cycle: %s", name)
		}
		visiting[name] = true
		defer delete(visiting, name)
		parent := p.Extends
		if parent == "" {
			parent = "inspector"
		}
		base, err := resolve(parent)
		if err != nil {
			return p, err
		}
		if p.Inspector != "" {
			base.Inspector = p.Inspector
		}
		if p.InspectorPercent != 0 {
			base.InspectorPercent = p.InspectorPercent
		}
		if p.Wide != nil {
			wide := *p.Wide
			base.Wide = &wide
		}
		base.Views = cloneViews(base.Views)
		for k, v := range p.Views {
			base.Views[k] = v
		}
		base.Extends = ""
		if !slices.Contains([]string{"none", "right", "bottom"}, base.Inspector) || base.InspectorPercent < 20 || base.InspectorPercent > 70 {
			return p, fmt.Errorf("layout %s: inspector must be none/right/bottom and percent 20 to 70", name)
		}
		if err = validateViews(base.Views); err != nil {
			return p, err
		}
		result[name] = base
		return base, nil
	}
	for name := range custom {
		if _, err := resolve(name); err != nil {
			return nil, err
		}
	}
	return result, nil
}

const SampleConfig = `version: 1
theme: catppuccin-mocha
layout: inspector
view: pods
namespace: default
all_namespaces: false
# Select several with :ns prod-dep,prod-ccp,prod-global,prod-system
# :ns save production saves a group; :ns @production selects it later.
# Groups belong to actual kubeconfig contexts, not their display aliases:
# contexts:
#   my-work-context:
#     namespace: prod-dep,prod-ccp,prod-global,prod-system
#     namespace_groups:
#       production: [prod-dep, prod-ccp, prod-global, prod-system]
inspector_format: yaml
# inspector_position: right # right or bottom; I opens the placement chooser
# inspector_visible: true # i toggles; omitted uses the selected layout's default
# header_visible: true # z toggles the explorer header
refresh_interval: 2s
history_limit: 100 # number of successful commands retained; positive integer
# read_only: true # disables resource changes and shells
# editor: "code --wait" # default: $EDITOR, then $VISUAL, then vi
# shell: "/bin/sh" # command inside the selected running container
# download_dir: "~/Downloads/kqt" # default: current directory

# Debugging has no default image. Uncomment and set an approved private image.
# debug:
#   default_profile: tools
#   start_timeout: 90s
#   profiles:
#     tools:
#       image: 123456789012.dkr.ecr.eu-west-2.amazonaws.com/platform/debug-tools:approved
#       command: ["/bin/sh"]
#       image_pull_policy: IfNotPresent
#       security_context:
#         runAsUser: 1000
#         runAsGroup: 1000
#   context_profiles:
#     production: tools # actual kubeconfig context, not its display alias
#   namespace_profiles:
#     production:
#       payments: tools

logs:
  # since: 10m # omit for all available history within the line limit
  # tail_lines: 1000 # omit for live 200 / snapshot 500; -1 all; 0 no initial lines
  timestamps: true
  prefixes: auto # auto, "on", "off"
  wrap: true
  follow: true

views:
  pods:
    columns: [namespace, name, ready, status, restarts, age, node]
    sort: name
  deployments.apps:
    columns: [namespace, name, ready, status, /spec/replicas, age]
    sort: name

layouts:
  troubleshooting:
    extends: inspector
    inspector_percent: 40
    views:
      pods:
        columns: [namespace, name, ready, status, restarts, node, age]
        sort: restarts
        descending: true
`
