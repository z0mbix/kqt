package tui

import (
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Measure exactly the text rendered in the table, including label separators.
// Keep the underlying column value unchanged for sorting and other consumers.
func (m *model) tableCellText(o unstructured.Unstructured, column string) string {
	value := kube.Safe(m.column(o, column))
	if column == "labels" {
		value = strings.ReplaceAll(value, ",", ", ")
	}
	return value
}

type tableLabelStyles struct {
	key, value, equals, separator lipgloss.Style
}

func (t theme) tableLabelStyles(selected bool) tableLabelStyles {
	style := func(token string) lipgloss.Style {
		s := t.style(token)
		if selected && t.Colors["selection_background"] != "default" {
			fg, bg := t.color(token), t.color("selection_background")
			// Bright selection backgrounds can make the normal syntax palette
			// unreadable. Keep the theme's selected foreground in that case,
			// with underlined keys and commas still distinguishing tokens.
			if labelContrast(fg, bg) < 4.5 || labelContrast(ansi.Convert256(fg), ansi.Convert256(bg)) < 4.5 {
				s = t.style("selection_foreground")
			}
		}
		return s
	}
	return tableLabelStyles{
		key:       style("accent").Bold(true).Underline(selected || t.Name == "no-color"),
		value:     style("success"),
		equals:    style("muted"),
		separator: style("warning").Bold(true).Underline(selected || t.Name == "no-color"),
	}
}

func (t theme) renderTableLabels(text string, selected bool) string {
	styles := t.tableLabelStyles(selected)
	content := strings.TrimRight(text, " ")
	var out strings.Builder
	for i, pair := range strings.Split(content, ",") {
		if i > 0 {
			out.WriteString(styles.separator.Render(","))
			if strings.HasPrefix(pair, " ") {
				out.WriteByte(' ')
				pair = pair[1:]
			}
		}
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			// Includes a key truncated before its '=' and the empty-label cell.
			out.WriteString(styles.key.Render(pair))
			continue
		}
		out.WriteString(styles.key.Render(key))
		out.WriteString(styles.equals.Render("="))
		out.WriteString(styles.value.Render(value))
	}
	return out.String() + text[len(content):]
}

func labelContrast(a, b color.Color) float64 {
	luminance := func(c color.Color) float64 {
		r, g, b, _ := c.RGBA()
		linear := func(v uint32) float64 {
			x := float64(v) / 65535
			if x <= .04045 {
				return x / 12.92
			}
			return math.Pow((x+.055)/1.055, 2.4)
		}
		return .2126*linear(r) + .7152*linear(g) + .0722*linear(b)
	}
	x, y := luminance(a), luminance(b)
	return (math.Max(x, y) + .05) / (math.Min(x, y) + .05)
}
