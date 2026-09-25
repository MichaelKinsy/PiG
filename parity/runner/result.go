//go:build parity

package runner

// Result captures one run of one binary on one scenario.
type Result struct {
	System    string // "pig" or "pi"
	Output    string // captured stdout/stderr or tmux pane
	Escaped   string // tmux -e capture, if available
	ExitCode  int
	RuntimeMs int64
	ReadyOK   bool // tmux: did the ready pattern appear?
	Err       error

	// Artifact holds the contents of the file named by the driver's
	// artifact_path, read after the run. It lets a scenario assert on what
	// a command wrote rather than only on what it printed: an exporter that
	// reports success on stdout proves nothing about the file it produced.
	Artifact string
	// ArtifactErr records why artifact_path could not be read, so a missing
	// or unreadable artifact fails loudly instead of comparing empty to empty.
	ArtifactErr error
}

// SystemResults groups multiple runs of the same binary on one scenario.
type SystemResults struct {
	System   string
	Runs     []Result
	MedianMs int64
	AllOK    bool
}

// ScenarioOutcome is the final verdict for one scenario across both systems.
type ScenarioOutcome struct {
	Scenario *Scenario
	Pig      SystemResults
	Pi       SystemResults
	Failures []string // human-readable assertion failures, empty == pass
	// SkipPerfGate is set by the test driver when the scenario ran under
	// CPU contention (parallel mode). EvaluateOutcome will still record
	// the runtime ratio for offline analysis but will NOT add it to
	// Failures. Use `make parity-perf` to enforce the perf gate.
	SkipPerfGate bool
}

// Passed is true when no assertion failed.
func (o *ScenarioOutcome) Passed() bool { return len(o.Failures) == 0 }
