package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/z0mbix/kqt/internal/kube"
	"github.com/z0mbix/kqt/internal/tui"
	"k8s.io/apimachinery/pkg/labels"
)

var version = "0.7.1-dev"

func command() *cobra.Command {
	var o tui.Options
	var kubeconfig, config, theme, layout string
	cmd := &cobra.Command{Use: "kqt", Short: "A Kubernetes Query Tool", Long: "Explore Kubernetes with Maestro's terminal layout and themes. Press : for commands, / to filter, n for namespaces, ? for help, and q to quit.", Version: version, SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs}
	f := cmd.PersistentFlags()
	f.StringVar(&kubeconfig, "kubeconfig", "", "Kubeconfig file (default: KUBECONFIG or ~/.kube/config)")
	f.StringVar(&config, "config", "", "UI configuration (default: ~/.config/kqt/config.yaml)")
	f.StringVar(&o.Context, "context", "", "Kubeconfig context for this session")
	f.StringVarP(&o.Namespace, "namespace", "n", "", "Namespace or comma-separated namespaces (default: saved/context namespace)")
	f.BoolVarP(&o.AllNamespaces, "all-namespaces", "A", false, "Browse all namespaces")
	f.StringVar(&o.View, "view", "", "Initial resource name, kind, or alias (default: saved view)")
	f.StringVarP(&o.Labels, "selector", "l", "", "Kubernetes label selector")
	f.StringVar(&theme, "theme", "", "Terminal color theme")
	f.StringVar(&layout, "layout", "", "Saved layout name (kqt config shows an example)")
	f.BoolVar(&o.Demo, "demo", false, "Explore an offline sample cluster")
	f.BoolVar(&o.ReadOnly, "read-only", false, "Disable resource changes, shells and debug attachment (overrides read_only config)")
	cmd.MarkFlagsMutuallyExclusive("namespace", "all-namespaces")
	settings := func() (*tui.Settings, error) {
		s, err := tui.LoadSettings(config)
		if err != nil {
			return nil, err
		}
		if theme == "" {
			theme = os.Getenv("KQT_THEME")
		}
		if theme != "" && !s.SelectTheme(theme) {
			return nil, fmt.Errorf("unknown theme %q; run kqt themes", theme)
		}
		if layout != "" && !s.SelectLayout(layout) {
			return nil, fmt.Errorf("unknown layout %q", layout)
		}
		return s, nil
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		o.ReadOnlyExplicit = cmd.Flags().Changed("read-only")
		o.ScopeExplicit = cmd.Flags().Changed("namespace") || cmd.Flags().Changed("all-namespaces")
		s, err := settings()
		if err != nil {
			return err
		}
		if o.Namespace, err = kube.NormalizeNamespaces(o.Namespace); err != nil {
			return err
		}
		if _, err = labels.Parse(o.Labels); err != nil {
			return fmt.Errorf("invalid label selector: %w", err)
		}
		if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
			return fmt.Errorf("kqt requires an interactive terminal; try kqt --demo in a terminal, or kqt --help")
		}
		var l *kube.Loader
		if !o.Demo {
			l, err = kube.Load(kubeconfig)
			if err != nil {
				return err
			}
		}
		return tui.Run(cmd.Context(), s, o, l, os.Stdin, os.Stdout)
	}
	cmd.AddCommand(&cobra.Command{Use: "themes", Short: "List bundled and custom themes without connecting to a cluster", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := settings()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), strings.Join(s.ThemeNames(), "\n"))
		return err
	}})
	var validate bool
	cfg := &cobra.Command{Use: "config", Short: "Print example UI configuration or validate it", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if !validate {
			_, err := fmt.Fprint(cmd.OutOrStdout(), tui.SampleConfig)
			return err
		}
		s, err := settings()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Valid configuration: %s\n", s.Path)
		return err
	}}
	cfg.Flags().BoolVar(&validate, "validate", false, "Validate UI configuration and custom themes")
	cmd.AddCommand(cfg)
	return cmd
}
func main() {
	if err := command().ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "kqt:", err)
		os.Exit(1)
	}
}
