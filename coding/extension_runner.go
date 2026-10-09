package coding

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// NewExtensionRunner is Pi's ExtensionRunner constructor (extensions, runtime, cwd, sessionManager, modelRegistry): the runner over the loader's extensions and its shared runtime, with the SessionManager ctx.sessionManager exposes and the registry ctx.modelRegistry reads. A nil runtime gives it a fresh one; a nil sessionManager or registry waits for the binding that supplies it.
//
// upstream: runner.ts:233-242 (constructor)
func NewExtensionRunner(extensions []extension.Extension, runtime *extension.ExtensionRuntime, cwd string, sessionManager *SessionManager, modelRegistry extension.ModelRegistry) *inproc.Runner {
	var manager extension.SessionManager
	if sessionManager != nil {
		manager = sessionManager
	}
	return inproc.NewExtensionRunner(extensions, runtime, cwd, manager, modelRegistry)
}
