package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/cancelreader"
	"github.com/z0mbix/kqt/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/remotecommand"
)

type terminalSizes struct {
	ctx   context.Context
	sizes chan remotecommand.TerminalSize
}

func (q *terminalSizes) Next() *remotecommand.TerminalSize {
	select {
	case <-q.ctx.Done():
		return nil
	case size := <-q.sizes:
		return &size
	}
}

type shellInput struct {
	reader     io.Reader
	disconnect context.CancelFunc
}

// Read the native terminal before the network handshake is ready, so Ctrl+]
// can cancel a stalled connection too. Remote reads consume a bounded queue;
// canceling both readers ensures no input goroutine survives terminal handoff.
type shellQueue struct {
	sync.Mutex
	ctx    context.Context
	data   bytes.Buffer
	notify chan struct{}
}

func (q *shellQueue) Read(p []byte) (int, error) {
	for {
		if q.ctx.Err() != nil {
			return 0, io.EOF
		}
		q.Lock()
		if q.data.Len() > 0 {
			n, err := q.data.Read(p)
			q.Unlock()
			return n, err
		}
		q.Unlock()
		select {
		case <-q.ctx.Done():
			return 0, io.EOF
		case <-q.notify:
		}
	}
}
func (q *shellQueue) pump(input io.Reader) error {
	data := make([]byte, 4096)
	for {
		n, err := input.Read(data)
		if q.ctx.Err() != nil {
			return nil
		}
		if n > 0 {
			q.Lock()
			if q.data.Len()+n > 4*1024*1024 {
				q.Unlock()
				return fmt.Errorf("shell input exceeded the 4 MiB queue")
			}
			q.data.Write(data[:n])
			q.Unlock()
			select {
			case q.notify <- struct{}{}:
			default:
			}
		}
		if err != nil {
			return err
		}
	}
}

func (r shellInput) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	for i, c := range p[:n] {
		if c == 0x1d {
			r.disconnect()
			if i == 0 {
				return 0, io.EOF
			}
			return i, nil
		}
	}
	return n, err
}

type shellCommand struct {
	ctx                context.Context
	executor           kube.PodExecutor
	attacher           kube.EphemeralDebugger
	spdyOnly           bool
	disconnected       bool
	outputSeen         atomic.Bool
	target             unstructured.Unstructured
	container, heading string
	disconnectHint     string
	command            []string
	in                 io.Reader
	out, errOut        io.Writer
}

func (c *shellCommand) SetStdin(in io.Reader)   { c.in = in }
func (c *shellCommand) SetStdout(out io.Writer) { c.out = out }
func (c *shellCommand) SetStderr(out io.Writer) {
	if c.errOut == nil {
		c.errOut = out
	}
}
func (c *shellCommand) Run() error {
	file, ok := c.in.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return fmt.Errorf("pod shells require an interactive terminal")
	}
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	reader, err := cancelreader.NewReader(file)
	if err != nil {
		return err
	}
	defer reader.Close()
	defer reader.Cancel()
	fmt.Fprintln(c.out, c.heading+" · "+c.disconnectHint)
	state, err := term.MakeRaw(file.Fd())
	if err != nil {
		return err
	}
	defer term.Restore(file.Fd(), state)
	input := &shellQueue{ctx: ctx, notify: make(chan struct{}, 1)}
	inputDone := make(chan error, 1)
	go func() { err := input.pump(shellInput{reader, cancel}); inputDone <- err; cancel() }()
	queue := &terminalSizes{ctx: ctx, sizes: make(chan remotecommand.TerminalSize, 1)}
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		previous := remotecommand.TerminalSize{}
		for {
			if w, h, err := term.GetSize(file.Fd()); err == nil && w > 0 && h > 0 {
				size := remotecommand.TerminalSize{Width: uint16(w), Height: uint16(h)}
				if size != previous {
					select {
					case <-queue.sizes:
					default:
					}
					select {
					case queue.sizes <- size:
					default:
					}
					previous = size
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	options := remotecommand.StreamOptions{Stdin: input, Stdout: c.out, Stderr: c.errOut, Tty: true, TerminalSizeQueue: queue}
	if c.attacher != nil {
		options.Stdout = &debugOutput{Writer: c.out, seen: &c.outputSeen}
		err = c.attacher.Attach(ctx, c.target, c.container, c.spdyOnly, options)
	} else {
		err = c.executor.Exec(ctx, c.target, c.container, c.command, options)
	}
	intentionalDisconnect := ctx.Err() != nil && c.ctx.Err() == nil
	c.disconnected = intentionalDisconnect
	cancel()
	reader.Cancel()
	inputErr := <-inputDone
	<-monitorDone
	// Ctrl+] is an intentional local disconnect, while Ctrl+C is delivered to
	// the remote terminal. Normal network/remote exit failures remain visible.
	if intentionalDisconnect {
		if inputErr != nil && inputErr != io.EOF && inputErr != cancelreader.ErrCanceled {
			return inputErr
		}
		return nil
	}
	return err
}

type debugOutput struct {
	io.Writer
	seen *atomic.Bool
}

func (w *debugOutput) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if n > 0 {
		w.seen.Store(true)
	}
	return n, err
}

type shellClosedMsg struct {
	generation, token uint64
	err               error
}

func (m *model) cancelShell() {
	m.shellToken++
	if m.shellCancel != nil {
		m.shellCancel()
		m.shellCancel = nil
	}
}
func (m *model) openShell() tea.Cmd {
	if !m.allowMutation("shells") {
		return nil
	}
	o, ok := m.current()
	if !ok {
		return nil
	}
	if _, ok = m.backend.(kube.PodExecutor); !ok {
		m.notice = "Shells require a live Kubernetes connection"
		return nil
	}
	if o.GetKind() != "Pod" || kube.Text(o, "status", "phase") != "Running" {
		m.notice = "Select a running pod to open a shell"
		return nil
	}
	containers := kube.RunningContainers(o)
	if len(containers) == 0 {
		m.notice = "No running containers in this pod"
		return nil
	}
	m.shellObject = o
	m.shellContainer = containers[0]
	if len(containers) > 1 {
		return m.openPicker("shell-containers")
	}
	return m.runShell()
}
func (m *model) runShell() tea.Cmd {
	if !m.allowMutation("shells") {
		return nil
	}
	if !slices.Contains(kube.RunningContainers(m.shellObject), m.shellContainer) {
		m.notice = "Selected container is not running"
		return nil
	}
	command := m.settings.Config.Shell
	if command == "" {
		command = "/bin/sh"
	}
	args, err := editorArgs(command)
	if err != nil {
		m.notice = "Invalid shell command: " + kube.Safe(err.Error())
		return nil
	}
	m.stopResourceWatch()
	if m.requestCancel != nil {
		m.requestCancel()
	}
	m.request++
	m.busy = false
	m.closeLog()
	m.closeEvents()
	m.cancelShell()
	ctx, cancel := context.WithCancel(m.session.ctx)
	m.shellCancel = cancel
	m.mode = "shell"
	g, token := m.generation, m.shellToken
	m.promoteHistoryExecution("shell", token)
	adapter := &shellCommand{ctx: ctx, errOut: m.terminalStderr, executor: m.backend.(kube.PodExecutor), target: m.shellObject, container: m.shellContainer, command: args, heading: "kqt shell · " + kube.Safe(m.contextName) + " · " + kube.Safe(m.shellObject.GetNamespace()+"/"+m.shellObject.GetName()+":"+m.shellContainer)}
	adapter.disconnectHint = m.shortcuts("", shortcut{"Ctrl+]", "disconnect"})
	return tea.Exec(adapter, func(err error) tea.Msg { return shellClosedMsg{g, token, err} })
}
func (m *model) shellClosed(v shellClosedMsg) tea.Cmd {
	if v.generation != m.generation || v.token != m.shellToken {
		return nil
	}
	m.cancelShell()
	m.mode = ""
	m.notice = "Shell closed"
	if v.err != nil {
		m.operationError("Shell", v.err)
	}
	return m.refreshResources()
}
func (m *model) operationError(action string, err error) {
	m.mode = "operation-error"
	m.panelTitle = action
	m.panel.SoftWrap = true
	m.panel.SetContent(action + " failed: " + kube.Safe(err.Error()) + "\n\nEscape returns to the resource list.")
	m.panel.GotoTop()
}
