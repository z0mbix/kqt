package tui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

type logSpan struct{ from, to int }
type logTerm struct {
	kind, field, value string
	exclude, threshold bool
	pattern            *regexp.Regexp
	highlight          *regexp.Regexp
	levels             []string
}
type logFilter struct {
	query string
	terms []logTerm
	cache map[string]bool
}

var logQuerySyntax = regexp.MustCompile(`(^|\s)(!?re:|!?include:|exclude:|![^\s]|\+[^\s]|!?[A-Za-z_][A-Za-z0-9_.-]*(>=|=))`)
var logFieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// Quotes group values with spaces; regex backslashes remain intact.
func logQueryWords(query string) ([]string, error) {
	var words []string
	var word strings.Builder
	var quote rune
	runes := []rune(query)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			if r == '\\' && i+1 < len(runes) && runes[i+1] == quote {
				word.WriteRune(quote)
				i++
			} else if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		} else if (r == '\'' || r == '"') && (word.Len() == 0 || strings.HasSuffix(word.String(), ":") || strings.HasSuffix(word.String(), "=")) {
			quote = r
		} else if unicode.IsSpace(r) {
			if word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
			}
		} else {
			word.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("close the quoted filter value")
	}
	if word.Len() > 0 {
		words = append(words, word.String())
	}
	return words, nil
}

func normalizeLogLevel(value string) string {
	switch strings.ToLower(value) {
	case "trace", "debug", "info", "notice", "warn", "error", "critical", "fatal", "panic":
		return strings.ToLower(value)
	case "warning":
		return "warn"
	case "err":
		return "error"
	case "crit":
		return "critical"
	}
	return ""
}

func compileLogFilter(query string) (*logFilter, error) {
	f := &logFilter{query: query, cache: map[string]bool{}}
	if len(query) > 4096 {
		return nil, fmt.Errorf("filter exceeds 4,096 bytes")
	}
	if strings.TrimSpace(query) == "" {
		return f, nil
	}
	words := []string{query}
	if logQuerySyntax.MatchString(query) {
		var err error
		words, err = logQueryWords(query)
		if err != nil {
			return nil, err
		}
	}
	if len(words) > 64 {
		return nil, fmt.Errorf("use at most 64 filter terms")
	}
	for _, word := range words {
		term := logTerm{kind: "text"}
		literal := strings.HasPrefix(word, "exclude:")
		if strings.HasPrefix(word, "!") || strings.HasPrefix(word, "exclude:") {
			term.exclude = true
			word = strings.TrimPrefix(strings.TrimPrefix(word, "!"), "exclude:")
		}
		literal = literal || strings.HasPrefix(word, "+") || strings.HasPrefix(word, "include:")
		word = strings.TrimPrefix(strings.TrimPrefix(word, "+"), "include:")
		if strings.HasPrefix(word, "re:") && !literal {
			term.kind, term.value = "regex", strings.TrimPrefix(word, "re:")
			if term.value == "" {
				return nil, fmt.Errorf("enter a pattern after re:")
			}
			var err error
			term.pattern, err = regexp.Compile(term.value)
			if err != nil {
				return nil, fmt.Errorf("invalid regex: %w", err)
			}
		} else if key, value, ok := strings.Cut(word, "="); ok && !literal && logFieldName.MatchString(strings.TrimSuffix(key, ">")) {
			term.kind, term.field, term.value = "field", key, value
			levelKey := strings.TrimSuffix(strings.ToLower(key), ">")
			if levelKey == "level" || levelKey == "severity" || levelKey == "lvl" {
				term.kind, term.threshold = "level", strings.HasSuffix(key, ">")
				for _, name := range strings.Split(value, ",") {
					level := normalizeLogLevel(name)
					if level == "" {
						return nil, fmt.Errorf("unknown severity %q", name)
					}
					term.levels = append(term.levels, level)
				}
				if term.threshold && len(term.levels) != 1 {
					return nil, fmt.Errorf("use one severity with >=")
				}
			} else {
				if strings.HasSuffix(key, ">") {
					return nil, fmt.Errorf(">= is supported for severity only")
				}
				term.pattern = regexp.MustCompile(`(?i)` + regexp.QuoteMeta(word))
				leaf := key[strings.LastIndex(key, ".")+1:]
				term.highlight = regexp.MustCompile(`(?:` + regexp.QuoteMeta(quotedLogValue(key)) + `|` + regexp.QuoteMeta(quotedLogValue(leaf)) + `)\s*:\s*(?:` + regexp.QuoteMeta(quotedLogValue(value)) + `|` + regexp.QuoteMeta(value) + `)`)
			}
		} else {
			if word == "" {
				return nil, fmt.Errorf("enter text after the include/exclude prefix")
			}
			term.value, term.pattern = word, regexp.MustCompile(`(?i)`+regexp.QuoteMeta(word))
		}
		f.terms = append(f.terms, term)
	}
	return f, nil
}

// Strip only recognized transport/severity prefixes before interpreting JSON.
func logJSONBody(line string) (any, string, bool) {
	original := line
	for line = strings.TrimLeftFunc(line, unicode.IsSpace); line != ""; line = strings.TrimLeftFunc(line, unicode.IsSpace) {
		if (line[0] == '{' || line[0] == '[') && json.Valid([]byte(line)) {
			var value any
			decoder := json.NewDecoder(strings.NewReader(line))
			decoder.UseNumber()
			err := decoder.Decode(&value)
			return value, original[:len(original)-len(line)], err == nil
		}
		if line[0] == '[' {
			if end := strings.IndexByte(line, ']'); end >= 0 && (line[1:end] == "kqt" || logSource.MatchString(line[1:end])) {
				line = line[end+1:]
				continue
			}
		}
		token := line
		if end := strings.IndexAny(line, " \t"); end >= 0 {
			token = line[:end]
		}
		if _, err := time.Parse(time.RFC3339Nano, token); err == nil {
			line = line[len(token):]
			continue
		}
		if match := logLevels.FindStringSubmatchIndex(line); match != nil && match[0] == 0 {
			line = strings.TrimPrefix(line[match[1]:], ":")
			continue
		}
		break
	}
	return nil, "", false
}

func jsonLogField(value any, path string) (any, bool) {
	if object, ok := value.(map[string]any); ok {
		if field, found := object[path]; found {
			return field, true
		}
	}
	for _, key := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		var found bool
		value, found = object[key]
		if !found {
			return nil, false
		}
	}
	return value, true
}

func logValueText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	data, _ := json.Marshal(value)
	return string(data)
}

func (f *logFilter) matches(line string) bool {
	if len(f.terms) == 0 {
		return true
	}
	if match, ok := f.cache[line]; ok {
		return match
	}
	var payload any
	prefix := ""
	parsed, isJSON := false, false
	for _, term := range f.terms {
		match := false
		switch term.kind {
		case "text", "regex":
			match = term.pattern.MatchString(line)
		case "field", "level":
			if !parsed {
				payload, prefix, isJSON = logJSONBody(line)
				parsed = true
			}
			if term.kind == "field" {
				if isJSON {
					value, found := jsonLogField(payload, term.field)
					match = found && logValueText(value) == term.value
				} else {
					match = term.pattern.MatchString(line)
				}
			} else {
				level := ""
				if isJSON {
					if object, ok := payload.(map[string]any); ok {
						for _, key := range []string{"level", "severity", "lvl"} {
							names := make([]string, 0, len(object))
							for name := range object {
								names = append(names, name)
							}
							slices.Sort(names)
							for _, name := range names {
								value := object[name]
								if strings.EqualFold(name, key) && level == "" {
									level = normalizeLogLevel(logValueText(value))
								}
							}
							if level != "" {
								break
							}
						}
					}
				}
				if level == "" {
					text := line
					if isJSON {
						text = prefix
					}
					if match := logLevels.FindStringSubmatchIndex(text); match != nil {
						from, to := levelSpan(match)
						level = normalizeLogLevel(text[from:to])
					}
				}
				match = term.matchesLevel(level)
			}
		}
		if match == term.exclude {
			f.cache[line] = false
			return false
		}
	}
	f.cache[line] = true
	return true
}

func (f *logFilter) spans(line string) []logSpan {
	var spans []logSpan
	for _, term := range f.terms {
		if term.exclude {
			continue
		}
		pattern := term.pattern
		if term.kind == "field" {
			if term.highlight.MatchString(line) {
				pattern = term.highlight
			}
		} else if term.kind == "level" {
			if _, prefix, ok := logJSONBody(line); ok {
				if match := logLevels.FindStringSubmatchIndex(prefix); match != nil {
					from, to := levelSpan(match)
					if term.matchesLevel(normalizeLogLevel(prefix[from:to])) {
						spans = append(spans, logSpan{from, to})
					}
				}
				for _, match := range jsonLogSeverity.FindAllStringSubmatchIndex(line, -1) {
					if term.matchesLevel(normalizeLogLevel(line[match[2]:match[3]])) {
						spans = append(spans, logSpan{match[2], match[3]})
					}
				}
			} else if match := logLevels.FindStringSubmatchIndex(line); match != nil {
				from, to := levelSpan(match)
				spans = append(spans, logSpan{from, to})
			}
			continue
		}
		for _, match := range pattern.FindAllStringIndex(line, -1) {
			if match[0] != match[1] {
				spans = append(spans, logSpan{match[0], match[1]})
			}
		}
	}
	slices.SortFunc(spans, func(a, b logSpan) int { return a.from - b.from })
	var merged []logSpan
	for _, span := range spans {
		if len(merged) > 0 && span.from <= merged[len(merged)-1].to {
			merged[len(merged)-1].to = max(merged[len(merged)-1].to, span.to)
		} else {
			merged = append(merged, span)
		}
	}
	return merged
}

func (t logTerm) matchesLevel(level string) bool {
	if t.threshold && level != "" {
		order := []string{"trace", "debug", "info", "notice", "warn", "error", "critical", "fatal", "panic"}
		return slices.Index(order, level) >= slices.Index(order, t.levels[0])
	}
	return slices.Contains(t.levels, level)
}

var jsonLogSeverity = regexp.MustCompile(`(?i)"(?:level|severity|lvl)"\s*:\s*"([^"\\]*)"`)

func quotedLogValue(value string) string { data, _ := json.Marshal(value); return string(data) }

func (t theme) highlightLogMatches(styled string, spans []logSpan) string {
	return highlightTextMatches(styled, spans, t.selectionStyle().Bold(true))
}

// Preserve syntax colours outside matched graphemes in logs and inspectors.
func highlightTextMatches(styled string, spans []logSpan, style lipgloss.Style) string {
	if len(spans) == 0 {
		return styled
	}
	plain := ansi.Strip(styled)
	clusters := uniseg.NewGraphemes(plain)
	var expanded []logSpan
	index := 0
	for clusters.Next() {
		from, to := clusters.Positions()
		for index < len(spans) && spans[index].to <= from {
			index++
		}
		if index < len(spans) && spans[index].from < to {
			if len(expanded) > 0 && expanded[len(expanded)-1].to == from {
				expanded[len(expanded)-1].to = to
			} else {
				expanded = append(expanded, logSpan{from, to})
			}
		}
	}
	// Walk both the spans and styled input once. Repeated ansi.Cut calls scan
	// the entire line per match and freeze the UI on many disjoint matches.
	var out, match strings.Builder
	var pen uv.Style
	flush := func() {
		if match.Len() == 0 {
			return
		}
		// Render complete graphemes, including matches across syntax tokens.
		out.WriteString(ansi.NewStyle().Underline(true).Styled(style.Render(match.String())))
		out.WriteString(pen.String())
		match.Reset()
	}
	parser := ansi.GetParser()
	defer ansi.PutParser(parser)
	var state byte
	index, offset := 0, 0
	for len(styled) > 0 {
		seq, width, n, next := ansi.DecodeSequence(styled, state, parser)
		if n == 0 {
			flush()
			out.WriteString(styled)
			break
		}
		styled, state = styled[n:], next
		if width == 0 && ansi.Strip(seq) == "" {
			if ansi.HasCsiPrefix(seq) && parser.Command() == 'm' {
				uv.ReadStyle(parser.Params(), &pen)
			}
			if match.Len() == 0 {
				out.WriteString(seq)
			}
			continue
		}
		for index < len(expanded) && expanded[index].to <= offset {
			index++
		}
		if index < len(expanded) && expanded[index].from < offset+len(seq) {
			match.WriteString(seq)
		} else {
			flush()
			out.WriteString(seq)
		}
		offset += len(seq)
	}
	flush()
	return out.String()
}
