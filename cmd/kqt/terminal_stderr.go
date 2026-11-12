package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/z0mbix/kqt/internal/tui"
	"golang.org/x/sys/unix"
)

type terminalStderr struct {
	terminal    *os.File
	reader      *os.File
	fd          int
	credentials chan tui.CredentialNotice
}

func captureTerminalStderr(stderr *os.File) (*terminalStderr, error) {
	fd := int(stderr.Fd())
	saved, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	terminal := os.NewFile(uintptr(saved), stderr.Name())
	reader, writer, err := os.Pipe()
	if err != nil {
		terminal.Close()
		return nil, err
	}
	defer writer.Close()

	// client-go's exec authenticator and logger write directly to stderr.
	// Redirect the descriptor before starting the TUI, and keep the original
	// for commands such as the editor that run with the terminal released.
	if err := unix.Dup2(int(writer.Fd()), fd); err != nil {
		reader.Close()
		terminal.Close()
		return nil, err
	}
	capture := &terminalStderr{
		terminal: terminal, reader: reader, fd: fd, credentials: make(chan tui.CredentialNotice, 4),
	}
	go capture.read()
	return capture, nil
}

func (c *terminalStderr) restore() error {
	err := unix.Dup2(int(c.terminal.Fd()), c.fd)
	if err != nil {
		err = fmt.Errorf("restore terminal stderr: %w", err)
	}
	return errors.Join(err, c.reader.Close(), c.terminal.Close())
}

func (c *terminalStderr) read() {
	defer close(c.credentials)
	reader := bufio.NewReader(c.reader)
	for {
		line, more, err := reader.ReadLine()
		if err != nil {
			return
		}
		if notice := credentialNotice(string(line)); notice != (tui.CredentialNotice{}) {
			select {
			case c.credentials <- notice:
			default:
			}
		}
		// Continue draining long diagnostic lines without allocating for them.
		for more && err == nil {
			_, more, err = reader.ReadLine()
		}
	}
}

func awsSSOExpired(line string) bool {
	line = strings.ToLower(line)
	return strings.Contains(line, "error loading sso token: token has expired and refresh failed") ||
		strings.Contains(line, "sso session associated with this profile has expired or is otherwise invalid")
}

// kubelogin writes the Microsoft Entra device sign-in instructions to stderr.
// Keep the address and code together in the status bar at narrow widths.
var deviceLogin = regexp.MustCompile(`(?i)https://((?:www\.)?microsoft\.com|aka\.ms)/devicelogin[[:space:]]+and enter (?:the )?code[: ]+([A-Z0-9][A-Z0-9-]{5,11})\b`)

func credentialNotice(line string) tui.CredentialNotice {
	if awsSSOExpired(line) {
		return tui.CredentialNotice{AWSSSOExpired: true}
	}
	if match := deviceLogin.FindStringSubmatch(line); match != nil {
		host := strings.TrimPrefix(strings.ToLower(match[1]), "www.")
		return tui.CredentialNotice{DeviceLoginPrompt: match[2] + " at " + host + "/devicelogin"}
	}
	return tui.CredentialNotice{}
}
