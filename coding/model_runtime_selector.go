package coding

import "github.com/MichaelKinsy/PiG/tui"

// ModelRuntime is the runtime a tui.ModelSelectorRuntime reads (model-selector.ts takes a ModelRuntime; package tui cannot import this one).
var _ tui.ModelSelectorRuntime = (*ModelRuntime)(nil)
