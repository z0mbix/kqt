package main

import (
	"maps"
	"slices"
	"strings"

	"github.com/z0mbix/kqt/internal/kube"
	"github.com/z0mbix/kqt/internal/tui"
)

func completeContexts(kubeconfig, config string, demo bool, prefix string) []string {
	contexts := map[string]bool{}
	if demo {
		contexts["demo"] = true
	} else {
		loader, err := kube.Load(kubeconfig)
		if err != nil {
			return nil
		}
		for _, ctx := range loader.Contexts() {
			contexts[ctx.Name] = true
		}
	}

	matches := map[string]bool{}
	add := func(name string) {
		// Cobra's completion protocol uses lines and tabs as delimiters and
		// trims whitespace. Only emit candidates the shell can insert intact.
		if strings.HasPrefix(name, prefix) && name != "" && strings.TrimSpace(name) == name && kube.Safe(name) == name {
			matches[name] = true
		}
	}
	for name := range contexts {
		add(name)
	}
	// Keep kubeconfig candidates available even if kqt's UI config is invalid.
	if settings, err := tui.LoadSettings(config); err == nil {
		for alias, target := range settings.Config.ContextAliases {
			if contexts[target] {
				add(alias)
			}
		}
	}
	return slices.Sorted(maps.Keys(matches))
}
