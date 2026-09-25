//go:build parity

// Package runner walks parity/scenarios/**/*.toml and runs each scenario
// against both pig and upstream pi, asserting they behave equivalently.
//
// This is the canonical parity gate. See parity/README.md for the schema.
package runner

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// Scenario is the TOML schema for a parity scenario.
type Scenario struct {
	// Identity
	Name        string   `toml:"name"`
	Description string   `toml:"description"`
	Covers      []string `toml:"covers"`
	Tags        []string `toml:"tags"`
	Group       string   `toml:"group"`

	// Driver selection
	Driver string `toml:"driver"`
	Model  string `toml:"model"`

	// Driver-specific config (top-level tables to avoid key collision
	// with the `driver` string key).
	Print         PrintDriverConfig         `toml:"print"`
	Tmux          TmuxDriverConfig          `toml:"tmux"`
	CLI           CLIDriverConfig           `toml:"cli"`
	RPC           RPCDriverConfig           `toml:"rpc"`
	ExtensionHost ExtensionHostDriverConfig `toml:"extension_host"`

	// Per-binary overrides
	Env EnvOverrides `toml:"env"`

	// Assertions
	Assert AssertSpec `toml:"assert"`

	// Documented intentional divergences
	Diverge DivergeSpec `toml:"diverge"`

	// SourcePath is the .toml file the scenario was loaded from.
	// Not part of the TOML schema; populated by LoadScenario.
	SourcePath string `toml:"-"`
}

// PrintDriverConfig configures the print-mode driver.
type PrintDriverConfig struct {
	Prompt         string `toml:"prompt"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
	CWD            string `toml:"cwd"` // working directory (relative to scenario file)
}

// CLIDriverConfig configures the cli-mode driver.
type CLIDriverConfig struct {
	Args           []string `toml:"args"`
	PigArgs        []string `toml:"pig_args"` // replaces Args for pig when non-nil
	PiArgs         []string `toml:"pi_args"`  // replaces Args for pi when non-nil
	InputLines     []string `toml:"input_lines"`
	SettleSeconds  int      `toml:"settle_seconds"`
	TimeoutSeconds int      `toml:"timeout_seconds"`
	CWD            string   `toml:"cwd"` // working directory (relative to scenario file)
	// SnapshotCWD copies the cwd fixture to a fresh per-binary temp dir and
	// runs there, outside the checkout, as the other drivers always do. Set it
	// for a fixture project whose .pi or .pig resources make the run depend on
	// project trust, so the checkout's own trust decision cannot answer it.
	SnapshotCWD bool `toml:"snapshot_cwd"`

	// ArtifactPath names a file the command writes, read into Result.Artifact
	// after the run so AssertSpec can compare it. Supports {{TEMP}}, so each
	// binary reads back its own copy. Required for commands whose real output
	// is a file rather than stdout.
	ArtifactPath string `toml:"artifact_path"`
}

// RPCDriverConfig configures the rpc-mode driver.
type RPCDriverConfig struct {
	InputLines     []string       `toml:"input_lines"` // JSON lines to write to stdin
	Steps          []RPCInputStep `toml:"steps"`
	TimeoutSeconds int            `toml:"timeout_seconds"`
	// SettleSeconds is the time to wait after writing all input lines
	// before closing stdin. Required for prompt commands where the binary
	// needs time to process and emit events before stdin EOF triggers
	// shutdown. 0 means close stdin immediately.
	SettleSeconds int    `toml:"settle_seconds"`
	CWD           string `toml:"cwd"` // working directory (relative to scenario file)
}

// RPCInputStep sends one JSONL command and waits for observable output before
// the next command. It is used for cancellation and response-driven flows.
type RPCInputStep struct {
	Line               string   `toml:"line"`
	WaitContains       []string `toml:"wait_contains"`
	WaitTimeoutSeconds int      `toml:"wait_timeout_seconds"`
}

// ExtensionHostDriverConfig configures the pig-only extension host validation
// driver. It shells through `pig install <sources...> --validate-only --json`
// and lets AssertSpec.Extensions inspect the structured registration report.
type ExtensionHostDriverConfig struct {
	Sources        []string `toml:"sources"`
	TimeoutSeconds int      `toml:"timeout_seconds"`
	CWD            string   `toml:"cwd"`
}

// TmuxDriverConfig configures the interactive-tmux driver.
type TmuxDriverConfig struct {
	Width               int        `toml:"width"`
	Height              int        `toml:"height"`
	ReadyPatternPig     string     `toml:"ready_pattern_pig"`
	ReadyPatternPi      string     `toml:"ready_pattern_pi"`
	ReadyTimeoutSeconds int        `toml:"ready_timeout_seconds"`
	Keys                []string   `toml:"keys"`
	SettleSeconds       int        `toml:"settle_seconds"`
	Steps               []TmuxStep `toml:"steps"`
	CaptureStart        string     `toml:"capture_start"`
	CaptureEnd          string     `toml:"capture_end"`
	CaptureEndRegex     string     `toml:"capture_end_regex"`
	CaptureStartLast    bool       `toml:"capture_start_last"`
	// CaptureJoinWrapped captures soft-wrapped terminal rows as the single
	// logical line the program wrote (tmux capture-pane -J). Use it when an
	// assertion names text whose on-screen wrap point depends on the host,
	// such as a path under the runner's temporary directory.
	CaptureJoinWrapped bool     `toml:"capture_join_wrapped"`
	PreClearPaths      []string `toml:"pre_clear_paths"`
	// CWD changes the working directory of the launched binary. Resolved
	// relative to the scenario source file (matching print/cli drivers).
	// Required for interactive scenarios that mutate files (e.g. edit-tool
	// rendering) since the runner's snapshot mechanism only copies
	// env-referenced agent dirs, not arbitrary CWD fixtures.
	CWD string `toml:"cwd"`
	// GitBranch makes the per-binary cwd copy an empty git repository whose
	// HEAD is this unborn branch, for scenarios that assert the footer's
	// branch display. Without it the cwd is not inside any repository.
	GitBranch string `toml:"git_branch"`
}

// TmuxStep is one staged interaction block for an interactive-tmux
// scenario. Each step sends its keys in order, then optionally waits
// before the next step. This allows scenarios to model
// prompt → wait-for-response → /tree-style flows without falling back
// to legacy shell scripts.
type TmuxStep struct {
	Keys                []string `toml:"keys"`
	SettleSeconds       int      `toml:"settle_seconds"`
	WaitContains        []string `toml:"wait_contains"`
	WaitVisibleContains []string `toml:"wait_visible_contains"`
	WaitNotContains     []string `toml:"wait_not_contains"`
	WaitTimeoutSeconds  int      `toml:"wait_timeout_seconds"`
}

// EnvOverrides lets a scenario inject per-binary environment variables
// and extra extension paths. This enables faux-provider parity tests
// without live LLM calls.
type EnvOverrides struct {
	Pig           []string `toml:"pig"`            // extra KEY=VALUE pairs for pig
	Pi            []string `toml:"pi"`             // extra KEY=VALUE pairs for pi
	PiExtensions  []string `toml:"pi_extensions"`  // extra -e <path> args for pi
	PigExtensions []string `toml:"pig_extensions"` // extra -e <path> args for pig
	PigArgs       []string `toml:"pig_args"`       // extra CLI args for pig
	PiArgs        []string `toml:"pi_args"`        // extra CLI args for pi
	PigBin        string   `toml:"pig_bin"`        // override pig binary path for this scenario
	PiBin         string   `toml:"pi_bin"`         // override upstream binary path for this scenario
	// OverrideBaseArgs, when true, suppresses the per-binary baseline
	// args (BinaryRef.Args, e.g. pi's default `--no-extensions`). Use
	// this only when the scenario must control the exact argv: e.g.
	// subcommands like `pi config` / `pig config` that must appear as
	// argv[0] for the dispatcher to recognise them.
	OverrideBaseArgs bool `toml:"override_base_args"`
}

// AssertSpec lists the assertions to evaluate against driver results.
type AssertSpec struct {
	ExitCode              *int     `toml:"exit_code"`
	PigExitCode           *int     `toml:"pig_exit_code"`
	PiExitCode            *int     `toml:"pi_exit_code"`
	BothContain           []string `toml:"both_contain"`
	BothNotContain        []string `toml:"both_not_contain"`
	PigContains           []string `toml:"pig_contains"`
	PigNotContain         []string `toml:"pig_not_contain"`
	PiContains            []string `toml:"pi_contains"`
	PiNotContain          []string `toml:"pi_not_contain"`
	BothMatchRegex        []string `toml:"both_match_regex"`
	OutputEqual           bool     `toml:"output_equal"`
	OutputNormalizedEqual bool     `toml:"output_normalized_equal"`
	OutputLayoutEqual     bool     `toml:"output_layout_equal"`
	EscapedOutputEqual    bool     `toml:"escaped_output_equal"`
	RuntimeRatioMax       float64  `toml:"runtime_ratio_max"`
	BothReachReady        bool     `toml:"both_reach_ready"`
	Runs                  int      `toml:"runs"`

	// ArtifactNormalizedEqual compares the file named by the driver's
	// artifact_path between pig and pi, after the same normalize() the
	// output comparators use. Asserts on what the command wrote.
	ArtifactNormalizedEqual bool `toml:"artifact_normalized_equal"`
	// ArtifactBothContain requires each substring in both binaries' artifacts.
	ArtifactBothContain []string `toml:"artifact_both_contain"`
	// ArtifactBothNotContain rejects each substring in either artifact.
	ArtifactBothNotContain []string `toml:"artifact_both_not_contain"`

	// NormalizeReplace lets a scenario declare regex replacements applied
	// during normalize() (after ANSI strip, before equality compare).
	NormalizeReplace []NormalizeReplaceRule `toml:"normalize_replace"`

	// Extensions asserts structured output from the extension-host driver.
	Extensions ExtensionAssertSpec `toml:"extensions"`
}

// ExtensionAssertSpec defines structured assertions over the JSON emitted by
// `pig install --validate-only --json`. It is intentionally pig-only: extension
// runtime cells and subprocess SDKs are documented downstream divergences from
// upstream pi, but they still need first-class automated QC.
type ExtensionAssertSpec struct {
	RegisteredTools     []string          `toml:"registered_tools"`
	RegisteredCommands  []string          `toml:"registered_commands"`
	RegisteredProviders []string          `toml:"registered_providers"`
	RegisteredShortcuts []string          `toml:"registered_shortcuts"`
	NoDiagnostics       bool              `toml:"no_diagnostics"`
	PlacementStrategy   map[string]string `toml:"placement_strategy"`
}

// NormalizeReplaceRule defines one regex→replacement pair applied during
// output_normalized_equal comparisons.
type NormalizeReplaceRule struct {
	Pattern string `toml:"pattern"`
	With    string `toml:"with"`
}

// DivergeSpec captures documented intentional differences.
type DivergeSpec struct {
	PigContains []string `toml:"pig_contains"`
	PiContains  []string `toml:"pi_contains"`
	Reason      string   `toml:"reason"`
}

// LoadScenario parses a TOML scenario file and fills in defaults.
func LoadScenario(path string) (*Scenario, error) {
	var sc Scenario
	if _, err := toml.DecodeFile(path, &sc); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	sc.SourcePath = path
	applyDefaults(&sc)
	if err := validate(&sc); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	return &sc, nil
}

func applyDefaults(sc *Scenario) {
	if sc.Assert.Runs <= 0 {
		sc.Assert.Runs = 1
	}
	if sc.Tmux.Width <= 0 {
		sc.Tmux.Width = 100
	}
	if sc.Tmux.Height <= 0 {
		sc.Tmux.Height = 35
	}
	if sc.Tmux.ReadyTimeoutSeconds <= 0 {
		// 30s default: node extension cold-start under PARITY_PARALLEL=12
		// tmux contention can legitimately push first-byte past 15s on a
		// busy machine. Failing-case-only deadline; passing scenarios pay
		// only the real boot time.
		sc.Tmux.ReadyTimeoutSeconds = 30
	}
}

func validate(sc *Scenario) error {
	if sc.Name == "" {
		return fmt.Errorf("scenario must have a name")
	}
	if sc.Driver == "" {
		return fmt.Errorf("scenario %q must declare a driver", sc.Name)
	}
	if len(sc.Covers) == 0 {
		return fmt.Errorf("covers must list at least one upstream file")
	}
	if sc.Assert.ExitCode != nil && (sc.Assert.PigExitCode != nil || sc.Assert.PiExitCode != nil) {
		return fmt.Errorf("exit_code cannot be combined with pig_exit_code or pi_exit_code")
	}
	if (sc.Assert.PigExitCode == nil) != (sc.Assert.PiExitCode == nil) {
		return fmt.Errorf("pig_exit_code and pi_exit_code must be declared together")
	}
	if sc.CLI.SnapshotCWD && sc.CLI.CWD == "" {
		return fmt.Errorf("cli.snapshot_cwd requires cli.cwd")
	}
	return nil
}

// HasTag reports whether the scenario carries the given tag.
func (sc *Scenario) HasTag(tag string) bool {
	for _, t := range sc.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
