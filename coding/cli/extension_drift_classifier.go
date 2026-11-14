package cli

import (
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	upgradeplan "github.com/MichaelKinsy/PiG/extensions/sdk/upgrade/plan"
)

// The extension host classifies a failed Go build as SDK drift through the rules of extensions/sdk/upgrade/plan, which it does not import itself: a program that embeds the host without this command does not link the rules.
func init() { runtimecell.SetDriftClassifier(classifyBuildDrift) }

func classifyBuildDrift(output []byte, source func(path string) ([]byte, error)) ([]runtimecell.Drift, int) {
	diagnostics := upgradeplan.ParseDiagnostics(output)
	found := upgradeplan.Classify(diagnostics, source)
	drifts := make([]runtimecell.Drift, len(found))
	for i, drift := range found {
		drifts[i] = runtimecell.Drift(drift)
	}
	return drifts, len(diagnostics)
}
