package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestContextCompletion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	kubeconfig := func(name string, contexts ...string) string {
		t.Helper()
		var content strings.Builder
		content.WriteString("apiVersion: v1\nkind: Config\ncontexts:\n")
		for _, ctx := range contexts {
			fmt.Fprintf(&content, "- name: %q\n  context: {cluster: example, user: example}\n", ctx)
		}
		return write(name, content.String())
	}
	first := kubeconfig("first.yaml", "dev-west", "prod", "arn:aws:eks:example:cluster/dev", "dev-bad\nextra")
	second := kubeconfig("second.yaml", "dev-east", "dev-west", "predev")
	t.Setenv("KUBECONFIG", strings.Join([]string{first, second}, string(os.PathListSeparator)))
	config := write("settings.yaml", "context_aliases:\n  dev-short: dev-east\n  dev-stale: missing\n  prod: dev-east\n")
	invalid := write("invalid.yaml", "context_aliases: [\n")
	directive := fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp)
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"merged and deduplicated", []string{"--context", ""}, []string{"arn:aws:eks:example:cluster/dev", "dev-east", "dev-west", "predev", "prod"}},
		{"prefix and valid aliases", []string{"--config", config, "--context", "dev"}, []string{"dev-east", "dev-short", "dev-west"}},
		{"alias collision", []string{"--config", config, "--context", "pr"}, []string{"predev", "prod"}},
		{"explicit kubeconfig and equals syntax", []string{"--config", config, "--kubeconfig", first, "--context=dev"}, []string{"dev-west"}},
		{"no matches", []string{"--context", "unknown"}, nil},
		{"missing kubeconfig", []string{"--kubeconfig", filepath.Join(dir, "absent"), "--context", ""}, nil},
		{"invalid settings still allow contexts", []string{"--config", invalid, "--context", "dev"}, []string{"dev-east", "dev-west"}},
		{"demo", []string{"--demo", "--context", ""}, []string{"demo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			cmd := command()
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(append([]string{"__complete"}, tc.args...))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("completion failed: %v (%s)", err, &stderr)
			}
			lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
			if want := append(slices.Clone(tc.want), directive); !slices.Equal(lines, want) {
				t.Errorf("completion = %q, want %q (%s)", lines, want, &stderr)
			}
		})
	}
}
