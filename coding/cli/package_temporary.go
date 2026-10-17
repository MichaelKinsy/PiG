package cli

import (
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// resolveCLIExtensionSource resolves one -e source to the root its extensions load from; packagemanager.ResolveTemporarySource owns the temporary install.
func resolveCLIExtensionSource(cwd, agentDir string, sm *codingagent.SettingsManager, source string, progress packagemanager.ProgressCallback) (string, error) {
	return packagemanager.ResolveTemporarySource(cwd, agentDir, sm, source, progress)
}
