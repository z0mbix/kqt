package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	yaml "go.yaml.in/yaml/v3"
)

const (
	localConfigName       = "local.yaml"
	legacyLocalConfigName = "kqt-local.yaml"
)

func fragmentDir(path string) string {
	return filepath.Join(filepath.Dir(path), "conf.d")
}

// A nil slice means split configuration is disabled. ReadDir sorts by name;
// the managed local layer is always last, even after user files named zz-*.
func configFragments(path string) ([]string, error) {
	dir := fragmentDir(path)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		if _, statErr := os.Lstat(dir); !os.IsNotExist(statErr) {
			return nil, err // A dangling conf.d symlink isn't an absent directory.
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := []string{}
	var legacyLocal, local string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || entry.IsDir() && name != localConfigName {
			continue
		}
		switch filepath.Ext(name) {
		case ".yaml", ".yml", ".toml":
		default:
			continue
		}
		file := filepath.Join(dir, name)
		if name == localConfigName && entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s: the managed config must not be a symlink", file)
		}
		info, err := os.Stat(file)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: config fragment must be a regular file", file)
		}
		switch name {
		case localConfigName:
			local = file
		case legacyLocalConfigName:
			legacyLocal = file
		default:
			files = append(files, file)
		}
	}
	if legacyLocal != "" {
		files = append(files, legacyLocal)
	}
	if local != "" {
		files = append(files, local)
	}
	return files, nil
}

func readMainConfig(path string, explicit bool) (string, []byte, error) {
	data, err := readConfig(path)
	if os.IsNotExist(err) && !explicit {
		legacy := strings.TrimSuffix(path, ".yaml") + ".toml"
		if data, err := readConfig(legacy); !os.IsNotExist(err) {
			return legacy, data, err
		}
		return path, nil, nil
	}
	return path, data, err
}

// override, if present, is an unwritten candidate for the last local layer.
func loadConfig(path string, explicit bool, fragments []string, override []byte) (config, error) {
	c := defaultConfig()
	source, data, err := readMainConfig(path, explicit)
	if err != nil {
		return c, err
	}
	if fragments == nil {
		if data != nil {
			if err = decodeConfig(source, data, &c); err != nil {
				return c, fmt.Errorf("%s: %w", source, err)
			}
		}
		return c, nil
	}
	values := map[string]any{}
	if data != nil {
		if err = mergeConfigLayer(values, source, data); err != nil {
			return c, err
		}
	}
	local := filepath.Join(fragmentDir(path), localConfigName)
	for _, file := range fragments {
		if file == local && override != nil {
			continue
		}
		data, err := readConfig(file)
		if err != nil {
			return c, err
		}
		if err = mergeConfigLayer(values, file, data); err != nil {
			return c, err
		}
	}
	if override != nil {
		if err = mergeConfigLayer(values, local, override); err != nil {
			return c, err
		}
	}
	data, err = yaml.Marshal(values)
	if err == nil {
		err = decodeYAMLConfig(data, &c)
	}
	if err != nil {
		return c, fmt.Errorf("merge config %s with conf.d: %w", path, err)
	}
	return c, nil
}

func mergeConfigLayer(dst map[string]any, path string, data []byte) error {
	// Check the schema for each file before merging, so a later override
	// cannot conceal a typo or an incompatible value type.
	if err := decodeConfig(path, data, &config{}); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // Empty or comment-only YAML.
		}
		return fmt.Errorf("%s: %w", path, err)
	}
	var src map[string]any
	var err error
	if filepath.Ext(path) == ".toml" {
		err = toml.Unmarshal(data, &src)
	} else {
		var layer map[string]layerValue
		if err = yaml.Unmarshal(data, &layer); err == nil {
			src = layerMap(layer)
		}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	mergeMaps(dst, src)
	return nil
}

type layerValue struct{ value any }

// Keep scalar spelling until the merged config is decoded into its final type.
// For example, YAML decodes an unquoted 001 as "001" in a string field, but
// as the integer 1 in an untyped map. Decode resolves aliases within each file.
func (v *layerValue) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.MappingNode:
		var entries map[string]layerValue
		if err := node.Decode(&entries); err != nil {
			return err
		}
		v.value = layerMap(entries)
	case yaml.SequenceNode:
		var items []layerValue
		if err := node.Decode(&items); err != nil {
			return err
		}
		values := make([]any, len(items))
		for i, item := range items {
			values[i] = item.value
		}
		v.value = values
	case yaml.ScalarNode:
		v.value = &yaml.Node{Kind: node.Kind, Tag: node.Tag, Value: node.Value, Style: node.Style}
	}
	return nil
}

func layerMap(entries map[string]layerValue) map[string]any {
	values := make(map[string]any, len(entries))
	for key, entry := range entries {
		values[key] = entry.value
	}
	return values
}
