package tui

import (
	tea "charm.land/bubbletea/v2"
	"regexp"
	"strings"
	"time"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/z0mbix/kqt/internal/kube"
)

// Highlight the original serialized YAML rather than reformatting parsed
// values: quotes, block scalars, indentation, and newlines stay intact.
func (t theme) renderYAML(text string) string {
	text = kube.SafeLines(text)
	tokens := lexer.Tokenize(text)
	var original strings.Builder
	for _, tk := range tokens {
		original.WriteString(tk.Origin)
	}
	source := original.String()
	// A lexer may omit trailing whitespace. Any other mismatch falls back to
	// the original text, so highlighting can never change a manifest's data.
	if !strings.HasPrefix(text, source) || strings.TrimSpace(text[len(source):]) != "" {
		return text
	}
	var content strings.Builder
	for _, tk := range tokens {
		colour := yamlTokenColour(tk)
		// Style each line separately, keeping whitespace outside Lip Gloss so
		// its multiline rendering cannot trim or pad block scalar contents.
		for i, line := range strings.Split(tk.Origin, "\n") {
			if i > 0 {
				content.WriteByte('\n')
			}
			value := strings.Trim(line, " \t\r")
			if value == "" {
				content.WriteString(line)
				continue
			}
			start := strings.Index(line, value)
			content.WriteString(line[:start])
			content.WriteString(t.style(colour).Render(value))
			content.WriteString(line[start+len(value):])
		}
	}
	content.WriteString(text[len(source):])
	return content.String()
}

func yamlTokenColour(tk *token.Token) string {
	if tk.PreviousType() == token.AnchorType || tk.PreviousType() == token.AliasType {
		return "focus"
	}
	if tk.NextType() == token.MappingValueType {
		return "accent"
	}
	switch tk.Type {
	case token.StringType, token.SingleQuoteType, token.DoubleQuoteType:
		return "success"
	case token.BoolType, token.AnchorType, token.AliasType, token.TagType, token.DirectiveType:
		return "focus"
	case token.IntegerType, token.BinaryIntegerType, token.OctetIntegerType, token.HexIntegerType, token.FloatType, token.InfinityType, token.NanType:
		return "warning"
	case token.InvalidType:
		return "error"
	default:
		return "muted"
	}
}

func (m *model) setTheme(t theme) {
	m.theme = t
	m.input.SetStyles(t.inputStyles())
	m.logColours = nil
	m.updateInspector()
	if m.showingLogs() && !m.logPaused {
		m.renderLogs()
	}
	if m.showingEventPanel() && !m.eventPaused {
		m.renderEventPanel()
	}
	if m.showingHelp() {
		m.refreshHelp(false)
	}
}

var jsonTokens = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?|true|false|null|[{}\[\]:,]`)

func (t theme) renderJSON(text string) string {
	return t.renderJSONTokens(text, false)
}

func (t theme) renderJSONTokens(text string, logLevels bool) string {
	text = kube.SafeLines(text)
	var out strings.Builder
	end := 0
	levelValue := false
	for _, match := range jsonTokens.FindAllStringIndex(text, -1) {
		out.WriteString(text[end:match[0]])
		value, colour := text[match[0]:match[1]], "muted"
		switch value[0] {
		case '"':
			colour = "success"
			if strings.HasPrefix(strings.TrimLeft(text[match[1]:], " \t\r\n"), ":") {
				colour = "accent"
				levelValue = logLevels && isLogLevelKey(value)
			} else {
				if levelValue {
					if c := jsonLogLevelColour(value); c != "" {
						colour = c
					}
				}
				levelValue = false
			}
		case 't', 'f':
			colour = "focus"
		case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			colour = "warning"
		}
		if value[0] != '"' && value != ":" {
			levelValue = false
		}
		out.WriteString(t.style(colour).Render(value))
		end = match[1]
	}
	out.WriteString(text[end:])
	return out.String()
}

func (m *model) themeTimeout() tea.Cmd {
	token := m.themeTimer
	return tea.Tick(max(0, time.Until(m.themeUntil)), func(time.Time) tea.Msg { return themeHiddenMsg{token} })
}
func (m *model) selectTheme(t theme) tea.Cmd {
	m.setTheme(t)
	m.themeTimer++
	m.themeUntil = time.Now().Add(10 * time.Second)
	m.settings.Config.Theme = t.Name
	m.savePreferences(map[string]any{"theme": t.Name})
	return m.themeTimeout()
}
func (m *model) setInspectorFormat(format string) {
	m.inspectorFormat = format
	m.settings.Config.InspectorFormat = format
	m.updateInspector()
	m.inspector.GotoTop()
	if m.inspectorSearch.query != "" {
		m.refreshInspectorSearch(true)
		m.showInspectorMatch()
	}
	m.savePreferences(map[string]any{"inspector_format": format})
}
