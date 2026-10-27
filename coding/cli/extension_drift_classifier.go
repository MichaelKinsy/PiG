package cli

import (
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// The extension host classifies a failed Go build as SDK drift through the rules of extensions/sdk/upgrade, which it does not import itself: that package also holds the upgrade rewrites and go/types, and a program that embeds the host without this command links neither.
func init() { runtimecell.SetDriftClassifier(classifyBuildDrift) }

func classifyBuildDrift(output []byte, source func(path string) ([]byte, error)) ([]runtimecell.Drift, int) {
	diagnostics := upgrade.ParseDiagnostics(output)
	found := upgrade.Classify(diagnostics, source)
	drifts := make([]runtimecell.Drift, len(found))
	for i, drift := range found {
		drifts[i] = runtimecell.Drift(drift)
	}
	return drifts, len(diagnostics)
}
