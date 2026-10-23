package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
)

type logPreferences struct {
	Since      string `toml:"since,omitempty" yaml:"since,omitempty"`
	TailLines  *int64 `toml:"tail_lines,omitempty" yaml:"tail_lines,omitempty"`
	Timestamps bool   `toml:"timestamps" yaml:"timestamps"`
	Highlight  bool   `toml:"highlight" yaml:"highlight"`
	Prefixes   string `toml:"prefixes" yaml:"prefixes"`
	Wrap       bool   `toml:"wrap" yaml:"wrap"`
	Follow     bool   `toml:"follow" yaml:"follow"`
}

func defaultLogPreferences() logPreferences {
	return logPreferences{Timestamps: true, Highlight: true, Prefixes: "auto", Wrap: true, Follow: true}
}

func parseLogSince(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "all" {
		return "", nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return "", fmt.Errorf("use a positive duration such as 10m or 1h, or all")
	}
	return value, nil
}

func parseLogTail(value string) (*int64, error) {
	value = strings.TrimSpace(value)
	if value == "default" {
		return nil, nil
	}
	if value == "all" {
		value = "-1"
	}
	lines, err := strconv.ParseInt(value, 10, 64)
	if err != nil || lines < -1 {
		return nil, fmt.Errorf("use a line count, all (-1), or default")
	}
	return &lines, nil
}

func validateLogPreferences(p logPreferences) error {
	if _, err := parseLogSince(p.Since); err != nil {
		return fmt.Errorf("logs.since: %w", err)
	}
	if p.TailLines != nil && *p.TailLines < -1 {
		return fmt.Errorf("logs.tail_lines must be -1 (all), 0, or a positive line count")
	}
	if p.Prefixes != "auto" && p.Prefixes != "on" && p.Prefixes != "off" {
		return fmt.Errorf("logs.prefixes must be auto, on, or off")
	}
	return nil
}

func (m *model) logRequestOptions() kube.LogOptions {
	p := m.settings.Config.Logs
	options := kube.LogOptions{TailLines: p.TailLines}
	if duration, err := time.ParseDuration(p.Since); err == nil && duration > 0 {
		stamp := time.Now().UTC().Add(-duration)
		options.SinceTime = &stamp
	}
	return options
}

func (m *model) saveLogPreference(key string, value any) {
	m.savePreferences(map[string]any{"logs": map[string]any{key: value}})
}

func (m *model) logPrefixes() bool {
	switch m.settings.Config.Logs.Prefixes {
	case "on":
		return true
	case "off":
		return false
	default:
		return m.logObject.GetKind() != "Pod" || m.logContainer == "*"
	}
}

// Only recognized Kubernetes transport fields are hidden. Application JSON
// timestamps and similarly named message fields remain untouched.
func (m *model) presentLogLine(line string) string {
	if strings.HasPrefix(line, "[kqt]") {
		return line
	}
	p := m.settings.Config.Logs
	if p.Timestamps && p.Prefixes == "auto" {
		return line
	}
	prefix, rest := "", line
	if strings.HasPrefix(rest, "[") {
		if end := strings.IndexByte(rest, ']'); end >= 0 && logSource.MatchString(rest[1:end]) {
			prefix, rest = rest[:end+1], strings.TrimPrefix(rest[end+1:], " ")
		}
	}
	if !m.settings.Config.Logs.Timestamps {
		stamp, body, found := strings.Cut(rest, " ")
		if _, err := time.Parse(time.RFC3339Nano, stamp); found && err == nil {
			rest = body
		}
	}
	if m.logPrefixes() || p.Prefixes == "auto" && prefix != "" {
		if prefix == "" && m.logObject.GetKind() == "Pod" && m.logContainer != "*" {
			prefix = "[" + m.logObject.GetNamespace() + "/" + m.logObject.GetName() + ":" + m.logContainer + "]"
		}
		if prefix != "" {
			return prefix + " " + rest
		}
	}
	return rest
}

func (m *model) toggleLogTimestamps() {
	p := &m.settings.Config.Logs
	p.Timestamps = !p.Timestamps
	m.saveLogPreference("timestamps", p.Timestamps)
	m.logColours = nil
	m.renderLogs()
}

func (m *model) toggleLogPrefixes() {
	m.applyLogOption("prefixes", "")
}

func (m *model) toggleLogHighlighting() {
	p := &m.settings.Config.Logs
	p.Highlight = !p.Highlight
	m.saveLogPreference("highlight", p.Highlight)
	m.logColours = nil
	m.renderLogs()
}

func (m *model) logOptionChoices() []choice {
	p := m.settings.Config.Logs
	window := p.Since
	if window == "" {
		window = "all"
	}
	tail := "default"
	if p.TailLines != nil {
		tail = strconv.FormatInt(*p.TailLines, 10)
	}
	choices := []choice{}
	for _, since := range []string{"all", "1m", "5m", "10m", "1h", "24h", "custom"} {
		description := "time window per container"
		if since == window {
			description += " · active"
		}
		choices = append(choices, choice{"since " + since, description})
	}
	for _, lines := range []string{"default", "100", "500", "1000", "all", "custom"} {
		description := "initial lines per container"
		if lines == "default" {
			description += " · live 200 / snapshot 500"
		}
		if lines == tail || lines == "all" && tail == "-1" {
			description += " · active"
		}
		choices = append(choices, choice{"tail " + lines, description})
	}
	return append(choices, choice{"highlight", fmt.Sprintf("toggle syntax highlighting · currently %t", p.Highlight)}, choice{"timestamps", fmt.Sprintf("toggle · currently %t", p.Timestamps)}, choice{"prefixes", "toggle pod/container prefixes · currently " + p.Prefixes}, choice{"prefixes auto", "show sources when combining containers"})
}

func (m *model) chooseLogOption(value string) tea.Cmd {
	m.mode = m.pickerReturn
	m.resize()
	key, arg, _ := strings.Cut(value, " ")
	if arg == "custom" {
		m.mode = "log-" + key
		m.input.Prompt = "Log window (10m, 1h, all): "
		initial := m.settings.Config.Logs.Since
		if key == "tail" {
			m.input.Prompt = "Lines per container (1000, all, default): "
			initial = "default"
			if m.settings.Config.Logs.TailLines != nil {
				initial = strconv.FormatInt(*m.settings.Config.Logs.TailLines, 10)
			}
		}
		m.input.CharLimit = 64
		m.input.SetValue(initial)
		m.input.CursorEnd()
		m.resize()
		return m.input.Focus()
	}
	return m.applyLogOption(key, arg)
}

func (m *model) applyLogOption(key, value string) tea.Cmd {
	p := &m.settings.Config.Logs
	switch key {
	case "since":
		parsed, err := parseLogSince(value)
		if err != nil {
			m.logFilterError = err.Error()
			return nil
		}
		p.Since = parsed
		m.saveLogPreference("since", parsed)
	case "tail":
		parsed, err := parseLogTail(value)
		if err != nil {
			m.logFilterError = err.Error()
			return nil
		}
		p.TailLines = parsed
		if parsed == nil {
			m.saveLogPreference("tail_lines", nil)
		} else {
			m.saveLogPreference("tail_lines", *parsed)
		}
	case "timestamps":
		m.toggleLogTimestamps()
		return nil
	case "highlight":
		m.toggleLogHighlighting()
		return nil
	case "prefixes":
		if value == "auto" {
			p.Prefixes = "auto"
		} else {
			visible := m.logPrefixes()
			p.Prefixes = "on"
			if visible {
				p.Prefixes = "off"
			}
		}
		m.saveLogPreference("prefixes", p.Prefixes)
	}
	if m.mode == "log-since" || m.mode == "log-tail" {
		m.mode = "logs"
		m.input.Blur()
		m.input.CharLimit = 256
		m.logFilterError = ""
		m.resize()
	}
	if key == "since" || key == "tail" {
		return m.loadLogs()
	}
	m.logColours = nil
	m.renderLogs()
	return nil
}

func (m *model) logOptionsKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.mode = "logs"
		m.logFilterError = ""
		m.input.CharLimit = 256
		m.input.Blur()
		m.resize()
		return nil
	case "enter":
		cmd := m.applyLogOption(strings.TrimPrefix(m.mode, "log-"), m.input.Value())
		if m.logFilterError != "" {
			m.input.Prompt = "Invalid value (Esc cancels): "
		}
		return cmd
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return cmd
	}
}
