package runtimecell

// Drift is one SDK change an extension was written for the old shape of. It is what a failed Go build records (BuildFailure.Drift) and what `pig extension upgrade` and the startup prompt read.
type Drift struct {
	// Rule is the rule that covers the change.
	Rule string `json:"rule"`
	// Symbol is the SDK symbol, Old and New its shapes, and Since the SDK release that changed it.
	Symbol string `json:"symbol"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Since  string `json:"since"`
	// Rewrites reports whether pig upgrades this use mechanically.
	Rewrites bool `json:"rewrites"`
	// File and Line are the first use. Count is the number of uses seen.
	File  string `json:"file"`
	Line  int    `json:"line"`
	Count int    `json:"count"`
}

// DriftClassifier reads the compiler output of a failed Go build and returns the SDK changes its diagnostics name and how many diagnostics the output holds. source reads a file a diagnostic names.
type DriftClassifier func(output []byte, source func(path string) ([]byte, error)) (drifts []Drift, diagnostics int)

var driftClassifier DriftClassifier

// SetDriftClassifier installs the classification of failed Go builds as SDK drift; the pig command installs it from an init function. The classification lives in extensions/sdk/upgrade, whose package also holds the extension upgrade rewrites and with them go/types: a program that embeds the extension host without the upgrade command does not link either, and a Go build that fails reports the compiler's message as it is. Call it before any build, from an init function.
func SetDriftClassifier(classify DriftClassifier) { driftClassifier = classify }
