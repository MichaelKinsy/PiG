package inproc

import "github.com/MichaelKinsy/PiG/coding/extension"

// GetModelRegistry is upstream runner.getModelRegistry (runner.ts:843-845): the registry the runner was given, or the one a mode's BindCore or BindTools replaced it with. It is nil before any binding supplies one.
func (r *Runner) GetModelRegistry() extension.ModelRegistry {
	return r.contextActions.ModelRegistry
}

// GetActiveTools is upstream runner.getActiveTools (runner.ts:868-871): the names of the tools declared to the model, read through the runtime's bound action. A stale runner returns its stale error, and a runtime no host has bound returns [extension.ErrRuntimeNotInitialized], as upstream's `notInitialized` stub throws.
func (r *Runner) GetActiveTools() ([]string, error) {
	if err := r.assertActive(); err != nil {
		return nil, err
	}
	if r.runtime.GetActiveTools == nil {
		return nil, extension.ErrRuntimeNotInitialized
	}
	return r.runtime.GetActiveTools(), nil
}
