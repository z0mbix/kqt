package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
	yamlv3 "go.yaml.in/yaml/v3"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

type editBuffer struct {
	directory, path    string
	original, editable unstructured.Unstructured
	resource           kube.Resource
	backend            kube.Editable
}
type editReadyMsg struct {
	generation, token uint64
	original          unstructured.Unstructured
	resource          kube.Resource
	backend           kube.Editable
	err               error
}
type editorClosedMsg struct {
	generation, token uint64
	buffer            *editBuffer
	err               error
}
type editedMsg struct {
	generation, token uint64
	buffer            *editBuffer
	changed           bool
	err               error
}

// Split quotes and escapes without a shell: editor arguments cannot turn a
// resource name or temp path into shell expansion or a second command.
func editorArgs(command string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	for _, r := range command {
		if escaped {
			word.WriteRune(r)
			escaped = false
			started = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			started = true
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
			continue
		}
		if unicode.IsSpace(r) {
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		word.WriteRune(r)
		started = true
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("editor command has an unfinished quote or escape")
	}
	if started {
		args = append(args, word.String())
	}
	if len(args) == 0 || args[0] == "" {
		return nil, fmt.Errorf("editor command is empty")
	}
	return args, nil
}
func editorCommand(configured string) string {
	if configured != "" {
		return configured
	}
	if v := os.Getenv("EDITOR"); v != "" {
		return v
	}
	if v := os.Getenv("VISUAL"); v != "" {
		return v
	}
	return "vi"
}
func newEditBuffer(original unstructured.Unstructured, r kube.Resource, b kube.Editable) (*editBuffer, error) {
	editable := *original.DeepCopy()
	editable.SetManagedFields(nil)
	delete(editable.Object, "status")
	data, err := yaml.Marshal(editable.Object)
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "kqt-edit-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "resource.yaml")
	data = append([]byte("# Edit and save to review changes in kqt before applying. Exit without changes to cancel.\n# Keep apiVersion, kind, name, namespace, UID and resourceVersion unchanged.\n# Status and managedFields are server-managed and are omitted.\n"), data...)
	if err = os.WriteFile(path, data, 0600); err != nil {
		os.RemoveAll(directory)
		return nil, err
	}
	return &editBuffer{directory, path, original, editable, r, b}, nil
}
func (b *editBuffer) remove() { os.RemoveAll(b.directory) }
func (b *editBuffer) prepare() (*editReview, error) {
	data, err := os.ReadFile(b.path)
	if err != nil {
		return nil, err
	}
	if len(data) > 1024*1024 {
		return nil, fmt.Errorf("edited resource exceeds 1 MiB")
	}
	documents := yamlv3.NewDecoder(bytes.NewReader(data))
	var document yamlv3.Node
	if err = documents.Decode(&document); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	var extra yamlv3.Node
	if err = documents.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("edit must contain one YAML document")
	}
	jsonData, err := yaml.YAMLToJSONStrict(data)
	if err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.UseNumber()
	if err = decoder.Decode(&object); err != nil {
		return nil, err
	}
	edited := unstructured.Unstructured{Object: object}
	original := b.original
	if edited.GetAPIVersion() != original.GetAPIVersion() || edited.GetKind() != original.GetKind() || edited.GetName() != original.GetName() || edited.GetNamespace() != original.GetNamespace() || edited.GetUID() != original.GetUID() || edited.GetResourceVersion() != original.GetResourceVersion() {
		return nil, fmt.Errorf("resource identity and resourceVersion must not change")
	}
	if _, ok := object["status"]; ok {
		return nil, fmt.Errorf("status is server-managed")
	}
	if _, present, _ := unstructured.NestedFieldNoCopy(object, "metadata", "managedFields"); present {
		return nil, fmt.Errorf("managedFields is server-managed")
	}
	before, err := json.Marshal(b.editable.Object)
	if err != nil {
		return nil, err
	}
	after, err := json.Marshal(edited.Object)
	if err != nil {
		return nil, err
	}
	patch, err := jsonpatch.CreateMergePatch(before, after)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(patch, []byte("{}")) {
		return nil, nil
	}
	var values map[string]any
	if err = json.Unmarshal(patch, &values); err != nil {
		return nil, err
	}
	metadata, ok := values["metadata"].(map[string]any)
	if !ok {
		metadata = map[string]any{}
	}
	metadata["resourceVersion"] = original.GetResourceVersion()
	metadata["uid"] = string(original.GetUID())
	values["metadata"] = metadata
	patch, err = json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return &editReview{patch: patch, before: b.editable, after: edited}, nil
}

func (b *editBuffer) apply(ctx context.Context) (bool, error) {
	review, err := b.prepare()
	if err != nil || review == nil {
		return false, err
	}
	return true, b.applyReview(ctx, review)
}
func (b *editBuffer) applyReview(ctx context.Context, review *editReview) error {
	return b.backend.Patch(ctx, b.resource, b.original.GetNamespace(), b.original.GetName(), review.patch)
}

func (m *model) cancelEdit() {
	m.forgetHistoryExecution("edit")
	m.editReview = nil
	m.editToken++
	if m.editCancel != nil {
		m.editCancel()
		m.editCancel = nil
	}
}
func (m *model) openEditor() tea.Cmd {
	if !m.allowMutation("editing") {
		return nil
	}
	o, ok := m.current()
	if !ok || m.backend == nil {
		return nil
	}
	b, ok := m.backend.(kube.Editable)
	if !ok {
		m.notice = "This backend does not support editing."
		return nil
	}
	if _, err := editorArgs(editorCommand(m.settings.Config.Editor)); err != nil {
		m.notice = err.Error()
		return nil
	}
	m.stopResourceWatch()
	m.clearSecret()
	m.updateInspector()
	if m.requestCancel != nil {
		m.requestCancel()
	}
	m.request++
	m.busy = false
	m.closeLog()
	m.cancelEdit()
	m.mode = "edit-loading"
	m.notice = "Loading a fresh resource for the editor…"
	ctx, cancel := context.WithCancel(m.session.ctx)
	m.editCancel = cancel
	g, token, r := m.generation, m.editToken, m.route.resource
	return func() tea.Msg {
		request, done := context.WithTimeout(ctx, 15*time.Second)
		defer done()
		fresh, err := b.Get(request, r, o.GetNamespace(), o.GetName())
		if err == nil && o.GetUID() != "" && fresh.GetUID() != o.GetUID() {
			err = fmt.Errorf("selected resource was replaced; refresh before editing")
		}
		return editReadyMsg{g, token, fresh, r, b, err}
	}
}
func (m *model) editorReady(v editReadyMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.editToken {
		return nil
	}
	if v.err != nil {
		m.mode = ""
		m.notice = "Unable to edit: " + kube.Safe(v.err.Error())
		return m.refreshResources()
	}
	buffer, err := newEditBuffer(v.original, v.resource, v.backend)
	if err != nil {
		m.mode = ""
		m.notice = "Unable to prepare editor: " + kube.Safe(err.Error())
		return m.refreshResources()
	}
	m.editBuffer = buffer
	args, err := editorArgs(editorCommand(m.settings.Config.Editor))
	if err != nil {
		buffer.remove()
		m.mode = ""
		m.notice = err.Error()
		return m.refreshResources()
	}
	m.mode = "editing"
	m.notice = "Editor open · changes will be reviewed before applying"
	process := exec.CommandContext(m.session.ctx, args[0], append(args[1:], buffer.path)...)
	process.Stderr = m.terminalStderr
	return tea.ExecProcess(process, func(err error) tea.Msg { return editorClosedMsg{v.generation, v.token, buffer, err} })
}
func (m *model) editorClosed(v editorClosedMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.editToken {
		return nil
	}
	if v.err != nil {
		m.editFailure("Editor exited with an error: "+kube.Safe(v.err.Error()), v.buffer.path)
		return m.refreshResources()
	}
	m.mode = "edit-loading"
	m.notice = "Preparing edit review…"
	ctx, cancel := context.WithTimeout(m.session.ctx, 15*time.Second)
	m.editCancel = cancel
	return func() tea.Msg {
		defer cancel()
		review, err := v.buffer.prepare()
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		return editReviewedMsg{v.generation, v.token, v.buffer, review, err}
	}
}
func (m *model) editFinished(v editedMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.editToken {
		return nil
	}
	m.mode = ""
	m.editBuffer = nil
	m.editReview = nil
	if v.err != nil {
		m.editFailure("Edit failed: "+kube.Safe(v.err.Error()), v.buffer.path)
	} else {
		v.buffer.remove()
		m.notice = "Edit cancelled: no changes"
		if v.changed {
			m.notice = "Updated " + v.buffer.original.GetKind() + " " + v.buffer.original.GetName()
		}
	}
	return m.refreshResources()
}

func (m *model) editFailure(message, path string) {
	m.notice = message + " · file retained: " + path
	m.mode = "edit-error"
	m.panelTitle = "Edit failed"
	m.panel.SoftWrap = true
	m.panel.SetContent(message + "\n\nYour edited file is retained at:\n" + kube.Safe(path) + "\n\nEscape returns to the resource table. Reopen with e to load the latest resource.")
	m.panel.GotoTop()
}
