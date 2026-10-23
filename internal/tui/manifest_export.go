package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
)

type manifestSavedMsg struct {
	generation uint64
	token      uint64
	path       string
	err        error
}

func (m *model) manifestFormat() string {
	if m.showingDetails() && m.detailFormat == "json" {
		return "json"
	}
	if m.showingDetails() {
		return "yaml"
	}
	return m.inspectorFormat
}
func (m *model) exportContent(format string, clean bool) (string, error) {
	o, ok := m.current()
	if !ok {
		return "", fmt.Errorf("select a resource first")
	}
	if o.GetKind() == "Secret" && m.secretObject != nil && kube.Identity(o) == m.secretTarget {
		o = *m.secretObject.DeepCopy()
	} else {
		o = kube.Redacted(o)
	}
	return kube.ExportManifest(o, format, clean)
}
func (m *model) copyManifest(format string, clean bool) tea.Cmd {
	content, err := m.exportContent(format, clean)
	if err != nil {
		m.notice = kube.Safe(err.Error())
		return nil
	}
	if len(content) > 1024*1024 {
		m.notice = "Manifest exceeds 1 MiB; export it to a file instead"
		return nil
	}
	m.notice = strings.ToUpper(format) + " sent to terminal clipboard (OSC52)"
	return tea.SetClipboard(content)
}
func (m *model) saveManifest(path, content string) tea.Cmd {
	m.manifestSaveToken++
	token := m.manifestSaveToken
	g, directory := m.generation, m.settings.Config.DownloadDir
	return func() tea.Msg {
		path, err := saveDownload(directory, path, content)
		return manifestSavedMsg{generation: g, token: token, path: path, err: err}
	}
}
func (m *model) promptManifestExport() tea.Cmd {
	o, ok := m.current()
	if !ok {
		return nil
	}
	format := m.manifestFormat()
	content, err := m.exportContent(format, false)
	if err != nil {
		m.notice = kube.Safe(err.Error())
		return nil
	}
	path, err := downloadPath(m.settings.Config.DownloadDir, o.GetName()+"-"+strings.ToLower(o.GetKind())+"."+format)
	if err != nil {
		m.notice = "Unable to prepare download: " + kube.Safe(err.Error())
		return nil
	}
	m.manifestContent = content
	m.manifestReturn = m.mode
	m.mode = "manifest-save"
	m.input.Prompt = "Save manifest to: "
	m.input.CharLimit = 4096
	m.input.SetValue(path)
	m.input.CursorEnd()
	m.resize()
	return m.input.Focus()
}
func (m *model) manifestInputKey(v tea.KeyPressMsg) tea.Cmd {
	if v.String() == "esc" {
		m.mode = m.manifestReturn
		m.manifestContent = ""
		m.input.Blur()
		m.input.CharLimit = 256
		return nil
	}
	if v.String() == "enter" {
		path, content := m.input.Value(), m.manifestContent
		m.mode = m.manifestReturn
		m.manifestContent = ""
		m.input.Blur()
		m.input.CharLimit = 256
		return m.saveManifest(path, content)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(v)
	return cmd
}
func (m *model) manifestCommand(arg string, copy bool) tea.Cmd {
	args, err := editorArgs(strings.TrimSpace(arg))
	if arg == "" {
		args = nil
		err = nil
	}
	if err != nil {
		m.notice = "Invalid manifest options: " + kube.Safe(err.Error())
		return nil
	}
	format, clean, path := m.manifestFormat(), false, ""
	for _, value := range args {
		switch value {
		case "yaml", "json":
			format = value
		case "clean":
			clean = true
		default:
			if path != "" || copy {
				m.notice = "Use :copy [yaml|json] [clean], or :export [yaml|json] [clean] PATH"
				return nil
			}
			path = value
		}
	}
	if copy {
		return m.copyManifest(format, clean)
	}
	if path == "" {
		m.notice = "Use :export [yaml|json] [clean] PATH (quote paths containing spaces)"
		return nil
	}
	content, err := m.exportContent(format, clean)
	if err != nil {
		m.notice = kube.Safe(err.Error())
		return nil
	}
	return m.saveManifest(path, content)
}
