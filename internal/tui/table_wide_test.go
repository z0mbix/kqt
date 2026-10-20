package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func wideTableModel(t *testing.T) *model {
	t.Helper()
	m := demoModel(t)
	m.command("columns name,status,age")
	for i := range m.rows {
		m.rows[i].SetLabels(map[string]string{
			"app.kubernetes.io/component":   "frontend",
			"app.kubernetes.io/instance":    "orders-web",
			"app.kubernetes.io/name":        "web",
			"environment":                   "development",
			"topology.kubernetes.io/region": "eu-west-2",
			"topology.kubernetes.io/zone":   "eu-west-2a",
		})
	}
	m.wide = true
	m.rebuild("")
	return m
}

func TestWideTableUsesSpareSpaceForLongContent(t *testing.T) {
	m := wideTableModel(t)
	for _, width := range []int{180, 240, 320} {
		g := m.tableGeometry(width)
		labels := slices.Index(g.columns, "labels")
		if g.widths[labels] <= 64 || g.offset != 0 || g.total > width-2 || width <= 240 && g.total != width-2 {
			t.Fatalf("width %d left unused space or clipped expandable labels: %+v", width, g)
		}
		if width == 320 && g.widths[labels] != ansi.StringWidth(m.tableCellText(m.visible[0], "labels")) {
			t.Fatal("labels were truncated even though their full text fits")
		}
		if g.widths[0] > 64 {
			t.Fatal("short columns were padded instead of expanding the truncated content")
		}
	}
	// Content that fits stays compact; long custom fields share spare space.
	m.command("columns name,/metadata/annotations/message")
	for i := range m.rows {
		m.rows[i].SetAnnotations(map[string]string{"message": strings.Repeat("long-message-", 40)})
	}
	m.rebuild("")
	g := m.tableGeometry(320)
	if g.widths[slices.Index(g.columns, "/metadata/annotations/message")] <= 64 || g.widths[slices.Index(g.columns, "labels")] <= 64 || g.total != 318 {
		t.Fatal("long custom columns and labels did not share spare table space")
	}
	// Keep a bounded, scrollable table rather than inflating every long cell to
	// its full length on a small terminal.
	g = m.tableGeometry(40)
	if g.total <= 38 || g.widths[slices.Index(g.columns, "labels")] != 64 {
		t.Fatal("narrow wide view lost useful scrolling widths")
	}
	m.horizontal = 10000
	if got := m.tableGeometry(40); got.offset != got.total-38 {
		t.Fatal("horizontal offset was not clamped to the resized table")
	}
}

func TestWideTableResizeHighlightingAndMouseShareGeometry(t *testing.T) {
	m := wideTableModel(t)
	for _, layout := range []string{"classic", "inspector", "bottom"} {
		m.selectLayout(layout)
		m.command("columns name,status,age")
		m.wide = true
		for _, size := range [][2]int{{40, 12}, {80, 24}, {180, 48}, {320, 48}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			b := m.tableBounds()
			m.horizontal = 0
			m.selectOrderColumn(0)
			for m.orderColumn() != "labels" {
				m.selectOrderColumn(1)
			}
			clickHeader(t, m, "labels")
			if m.sortColumn != "labels" {
				t.Fatal("expanded label header sorted a different column")
			}
			g := m.tableGeometry(b.width)
			if g.total <= b.width-2 && g.offset != 0 {
				t.Fatal("table kept a horizontal offset after content fitted")
			}
			rows := strings.Split(m.View().Content, "\n")
			if len(rows) != size[1] {
				t.Fatal("wide table changed terminal height")
			}
			for _, row := range rows {
				if ansi.StringWidth(row) != size[0] {
					t.Fatalf("%s %dx%d: styled labels overflowed the table", layout, size[0], size[1])
				}
			}
		}
	}
}

func TestTableLabelsPreserveTextAndThemeSemantics(t *testing.T) {
	m := wideTableModel(t)
	text := m.tableCellText(m.visible[0], "labels")
	if !strings.Contains(text, "component=frontend, app.kubernetes.io/instance=orders-web") || strings.Contains(m.column(m.visible[0], "labels"), ", ") {
		t.Fatal("table label formatting changed the source column value or omitted comma spacing")
	}
	for name, palette := range builtThemes() {
		for _, selected := range []bool{false, true} {
			styles := palette.tableLabelStyles(selected)
			value := palette.renderTableLabels("app=web, empty=, zone=eu-west-2a  ", selected)
			if ansi.Strip(value) != "app=web, empty=, zone=eu-west-2a  " || !strings.Contains(value, styles.key.Render("app")) || !strings.Contains(value, styles.value.Render("web")) || !strings.Contains(value, styles.equals.Render("=")) || !strings.Contains(value, styles.separator.Render(",")) {
				t.Errorf("%s selected=%v: label values or token highlights changed", name, selected)
			}
			if name == "no-color" && hasExplicitColour(value) {
				t.Fatal("no-color labels contain explicit colours")
			}
			if clipped := palette.renderTableLabels("app=web,…", selected); !strings.Contains(clipped, styles.separator.Render(",")) || ansi.Strip(clipped) != "app=web,…" {
				t.Fatal("comma next to the truncation marker lost its highlighting")
			}
			for _, width := range []int{4, 17, 40, 180} {
				clipped := fit("界界=välué, label="+strings.Repeat("a", 80), width)
				if got := palette.renderTableLabels(clipped, selected); ansi.Strip(got) != clipped || ansi.StringWidth(got) != width {
					t.Fatalf("%s: clipping or highlighting changed Unicode column geometry", name)
				}
			}
		}
	}
	o := m.visible[0].DeepCopy()
	o.SetLabels(map[string]string{"\x1b]52;c;payload\akey": "\x1b[2J\u202evalue"})
	if safe := m.tableCellText(*o, "labels"); strings.ContainsAny(safe, "\x1b\a\u202e") {
		t.Fatal("table labels retained terminal controls")
	}
	if empty := m.theme.renderTableLabels("    ", true); empty != "    " {
		t.Fatal("empty-label cell acquired visible styling")
	}
}

func TestSelectedTableLabelContrast(t *testing.T) {
	for _, name := range []string{"accessible-dark", "accessible-light"} {
		palette := builtThemes()[name]
		styles := palette.tableLabelStyles(true)
		for i, s := range []lipgloss.Style{styles.key, styles.value, styles.equals, styles.separator} {
			for _, indexed := range []bool{false, true} {
				fg, bg := s.GetForeground(), palette.color("selection_background")
				if indexed {
					fg, bg = ansi.Convert256(fg), ansi.Convert256(bg)
				}
				if ratio := themeContrast(fg, bg); ratio < 4.5 {
					t.Errorf("%s token %d indexed=%v: %.2f contrast", name, i, indexed, ratio)
				}
			}
		}
	}
}
