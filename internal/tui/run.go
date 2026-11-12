package tui

import (
	"context"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/z0mbix/kqt/internal/kube"
)

func Run(ctx context.Context, s *Settings, o Options, l *kube.Loader, in, out, stderr *os.File, credentials <-chan CredentialNotice) error {
	m, err := newModel(ctx, s, o, l)
	if err != nil {
		return err
	}
	m.terminalStderr, m.credentials = stderr, credentials
	defer m.close()
	_, err = tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out)).Run()
	return err
}

func (s *Settings) SelectTheme(name string) bool {
	_, ok := s.Themes[name]
	if ok {
		s.Config.Theme = name
	}
	return ok
}
func (s *Settings) ThemeNames() []string { return themeNames(s.Themes) }
func (s *Settings) SelectLayout(name string) bool {
	if _, ok := s.Layouts[name]; ok {
		s.Config.Layout = name
		return true
	}
	return false
}

type CredentialNotice struct {
	AWSSSOExpired     bool
	DeviceLoginPrompt string
}

func (m *model) waitForCredentialNotice() tea.Cmd {
	if m.credentials == nil {
		return nil
	}
	ctx, notices := m.ctx, m.credentials
	return func() tea.Msg {
		select {
		case notice, ok := <-notices:
			if ok {
				return notice
			}
		case <-ctx.Done():
		}
		return nil
	}
}
