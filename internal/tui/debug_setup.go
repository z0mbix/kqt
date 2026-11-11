package tui

import (
	"fmt"
	"maps"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
)

const busyBoxDebugImage = "docker.io/library/busybox:1.37"

func (m *model) useBusyBoxDebugImage() tea.Cmd {
	name, err := m.settings.saveDebugProfile(debugProfile{Image: busyBoxDebugImage, SecurityContext: map[string]any{"runAsUser": int64(1000)}})
	if err != nil {
		m.operationError("Unable to save debug image", err)
		return nil
	}
	return m.chooseDebugProfile(name)
}

func (m *model) promptDebugImage() tea.Cmd {
	m.mode, m.panelTitle = "debug-image", "Choose a debug image · first use"
	m.input.Prompt = "Image: "
	m.input.CharLimit = 512
	m.input.SetValue("")
	m.input.CursorEnd()
	m.resize()
	m.panel.SoftWrap = true
	m.panel.GotoTop()
	m.renderDebugImagePrompt("")
	return m.input.Focus()
}

func (m *model) renderDebugImagePrompt(message string) {
	content := "Which image should kqt use for debug containers?\n\nPaste your approved image, including its registry and repository.\nIt will be saved for next time. Your image must support non-root execution.\n\nEnter saves and continues · Escape cancels."
	if message != "" {
		content = m.theme.style("warning").Render(kube.Safe(message)) + "\n\n" + content
	}
	m.panel.SetContent(content)
}

func (m *model) debugImageKey(v tea.KeyPressMsg) tea.Cmd {
	if v.String() == "esc" {
		m.cancelDebug()
		m.mode = ""
		m.input.Blur()
		m.resize()
		return nil
	}
	if v.String() == "pgup" {
		m.panel.PageUp()
		return nil
	}
	if v.String() == "pgdown" {
		m.panel.PageDown()
		return nil
	}
	if v.String() != "enter" {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(v)
		return cmd
	}
	if !m.allowMutation("debug containers") {
		return nil
	}
	p := debugProfile{Image: strings.TrimSpace(m.input.Value())}
	if _, err := compileDebugProfile(p); err != nil {
		m.renderDebugImagePrompt(err.Error())
		m.panel.GotoTop()
		return nil
	}
	name, err := m.settings.saveDebugProfile(p)
	if err != nil {
		m.renderDebugImagePrompt("Unable to save debug image: " + err.Error())
		m.panel.GotoTop()
		return nil
	}
	m.input.Blur()
	m.input.CharLimit = 256
	m.notice = "Debug image saved for future sessions"
	return m.chooseDebugProfile(name)
}

// Merge into the latest on-disk configuration, and publish the in-memory choice
// only after an atomic save succeeds. Never overwrite an existing profile.
func (s *Settings) saveDebugProfile(p debugProfile) (string, error) {
	if _, err := compileDebugProfile(p); err != nil {
		return "", err
	}
	latest := s.Config
	fragments, err := configFragments(s.Path)
	if err != nil {
		return "", err
	}
	if fragments != nil {
		latest, err = loadConfig(s.Path, s.explicitConfig, fragments, nil)
		if err != nil {
			return "", fmt.Errorf("config changed: %w", err)
		}
	} else {
		data, err := readConfig(s.Path)
		if err == nil {
			latest = defaultConfig()
			if err = decodeConfig(s.Path, data, &latest); err != nil {
				return "", fmt.Errorf("config changed: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if err = validateDebugConfig(latest.Debug); err != nil {
		return "", err
	}
	c := latest.Debug
	c.Profiles = maps.Clone(c.Profiles)
	if c.Profiles == nil {
		c.Profiles = map[string]debugProfile{}
	}
	name := "default"
	for i := 2; ; i++ {
		if _, exists := c.Profiles[name]; !exists {
			break
		}
		name = fmt.Sprintf("default-%d", i)
	}
	c.Profiles[name] = p
	patch := map[string]any{"profiles": map[string]any{name: p}}
	if c.DefaultProfile == "" {
		c.DefaultProfile = name
		patch["default_profile"] = name
	}
	if err = s.savePatch(map[string]any{"debug": patch}); err != nil {
		return "", err
	}
	s.Config.Debug = c
	return name, nil
}
