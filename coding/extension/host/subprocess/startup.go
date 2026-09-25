package subprocess

import (
	"context"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// LoadFinalExtensionSet keeps extensions initialized for project_trust and loads
// only paths not attempted in that phase. Failed preloads are not retried.
// Mirrors resource-loader.ts loadFinalExtensionSet; the caller supplies both
// resolved config sets because a failed load has no managed extension.
func (h *Host) LoadFinalExtensionSet(ctx context.Context, configs, preloaded []ExtConfig) ([]extension.Extension, []error) {
	if err := ctx.Err(); err != nil {
		return nil, []error{err}
	}
	// Give duplicate identities their host keys before matching loads to
	// paths, as LoadAll and Reload do.
	configs = disambiguateIdentities(configs)
	preloaded = disambiguateIdentities(preloaded)
	origin := func(config ExtConfig) string {
		path := extConfigOrigin(config)
		if path == "<unknown>" {
			return "fused:" + config.Name
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(h.cwd, path)
		}
		return filepath.Clean(path)
	}
	attempted := make(map[string]struct{}, len(preloaded))
	loadedByPath := make(map[string]extension.Extension)
	h.mu.Lock()
	priorErrors := append([]string(nil), h.loadErrors...)
	for _, config := range preloaded {
		if !config.Enabled {
			continue
		}
		attempted[origin(config)] = struct{}{}
		if managed := h.exts[config.Name]; managed != nil && managed.ext != nil {
			loadedByPath[origin(config)] = *managed.ext
		}
	}
	h.mu.Unlock()
	var remaining []ExtConfig
	remainingPaths := make(map[string]string)
	for _, config := range configs {
		if !config.Enabled {
			continue
		}
		if _, exists := attempted[origin(config)]; !exists {
			remaining = append(remaining, config)
			remainingPaths[config.Name] = origin(config)
		}
	}
	additional, errs := h.LoadAll(ctx, remaining)
	for _, ext := range additional {
		loadedByPath[remainingPaths[ext.Name]] = ext
	}
	h.mu.Lock()
	if len(remaining) > 0 {
		h.loadErrors = append(priorErrors, h.loadErrors...)
	}
	h.mu.Unlock()
	// As upstream does with its shared runtime, keep preloads owned until
	// shutdown even when they are absent from the final ordered selection.
	var loaded []extension.Extension
	for _, config := range configs {
		if ext, exists := loadedByPath[origin(config)]; config.Enabled && exists {
			loaded = append(loaded, ext)
		}
	}
	return loaded, errs
}
