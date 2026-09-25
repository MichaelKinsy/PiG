package main

import (
	"path/filepath"
	"sync"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
)

type extensionSourceResolution struct {
	definition extsource.Definition
	err        error
}

// startupExtensionSourceResolver owns extension source classification for one
// startup. Reload creates a fresh resolver so edits made after startup are
// visible without retaining process-global source state.
type startupExtensionSourceResolver struct {
	mu      sync.Mutex
	resolve extsource.ResolveFunc
	byPath  map[string]extensionSourceResolution
}

func newStartupExtensionSourceResolver(resolve extsource.ResolveFunc) *startupExtensionSourceResolver {
	if resolve == nil {
		resolve = extsource.Resolve
	}
	return &startupExtensionSourceResolver{
		resolve: resolve,
		byPath:  make(map[string]extensionSourceResolution),
	}
}

func (r *startupExtensionSourceResolver) Resolve(path string) (extsource.Definition, error) {
	key, err := filepath.Abs(path)
	if err != nil {
		key = filepath.Clean(path)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if resolution, ok := r.byPath[key]; ok {
		return resolution.definition, resolution.err
	}
	definition, resolveErr := r.resolve(path)
	r.byPath[key] = extensionSourceResolution{definition: definition, err: resolveErr}
	return definition, resolveErr
}
