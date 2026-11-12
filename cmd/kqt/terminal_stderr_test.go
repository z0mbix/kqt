package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCredentialPluginStderrIsIsolatedAndRestored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "terminal")
	stderr, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()

	func() {
		capture, err := captureTerminalStderr(stderr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := capture.restore(); err != nil {
				t.Error(err)
			}
		}()
		plugin := exec.Command("/bin/sh", "-c", "printf '\\nError loading SSO Token: Token has expired and refresh failed\\n' >&2; exit 254")
		plugin.Stderr = stderr
		if err := plugin.Run(); err == nil {
			t.Fatal("credential fixture unexpectedly succeeded")
		}
		select {
		case notice := <-capture.credentials:
			if !notice.AWSSSOExpired {
				t.Fatal("expected an AWS SSO expiry notice")
			}
		case <-time.After(time.Second):
			t.Fatal("SSO expiry was not detected")
		}
		if _, err := fmt.Fprintln(capture.terminal, "editor stderr"); err != nil {
			t.Fatal(err)
		}
	}()

	if _, err := fmt.Fprintln(stderr, "after kqt"); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "editor stderr\nafter kqt\n" {
		t.Fatalf("terminal received %q", text)
	}
}

func TestRecognizeAWSCLIExpiryWithoutUsingExitCode(t *testing.T) {
	for line, want := range map[string]bool{
		"Error loading SSO Token: Token has expired and refresh failed":                    true,
		"The SSO session associated with this profile has expired or is otherwise invalid": true,
		"An error occurred (AccessDeniedException) when calling the AssumeRole operation":  false,
		"exec: executable aws failed with exit code 254":                                   false,
	} {
		if got := awsSSOExpired(line); got != want {
			t.Errorf("awsSSOExpired(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestRecognizeMicrosoftDeviceLogin(t *testing.T) {
	for line, want := range map[string]string{
		"To sign in, open https://microsoft.com/devicelogin and enter the code KQT1-TEST to authenticate.": "KQT1-TEST at microsoft.com/devicelogin",
		"To sign in, open https://aka.ms/devicelogin and enter the code KQTTEST01":                         "KQTTEST01 at aka.ms/devicelogin",
		"To sign in, open https://unrelated.example/devicelogin and enter the code KQTTEST01":              "",
	} {
		if got := credentialNotice(line).DeviceLoginPrompt; got != want {
			t.Errorf("credentialNotice(%q).DeviceLoginPrompt = %q, want %q", line, got, want)
		}
	}
}
