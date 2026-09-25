// Command lint validates parity scenarios against quality rules that
// AGENTS.md declares. Every rule here corresponds to a recurring
// problem that was previously caught only by manual session review.
//
// This linter is part of the parity compiler layer. Rules should encode
// hard-earned invariants (upstream-first evidence, isolation, honest coverage,
// scheduler rationale), not cosmetic preferences. If a rule conflicts with
// faithful upstream behavior, fix the rule or add a narrow, documented escape
// hatch; do not weaken scenarios or add comments that merely quiet the linter.
//
// Usage:
//
//	go run ./parity/cmd/lint -scenarios parity/scenarios -port-map PORT_MAP.md
//
// Exit code 0 = clean, 1 = violations found, 2 = usage error.
//
// Rules enforced:
//
//  1. no-equality-comparator: tmux scenarios must assert output equality
//     (output_equal, output_normalized_equal, output_layout_equal, or
//     escaped_output_equal).
//     Opt out with description prefix "boot-only:" for scenarios that
//     only verify startup readiness. For scenarios where genuine rendering
//     drift prevents equality comparison, a "# lint-known-gap:" comment
//     documenting the specific drift is required instead.
//
//  2. orphan-cover: every covers entry must exist in PORT_MAP.md.
//
//  3. no-upstream-evidence: tmux scenarios must document upstream probing
//     with a comment starting "# Upstream evidence" or
//     "# Manual upstream-first tmux probe".
//
//  4. deferred-no-description: deferred scenarios must have a non-empty
//     description explaining the parked drift.
//
//  5. boot-only-overclaims: boot-only scenarios may claim at most 1
//     covers entry. If you cannot assert the behavior, you cannot claim
//     to cover the file. The single allowed entry should be the entry
//     point or dispatch file, not the behavioral file.
//
//  6. unknown-divergence: any "D<N>" reference in description, comments,
//     or assertions must match an entry in DIVERGENCES.md. Prevents
//     fake-citing a divergence number to justify silent skipping.
//
//  8. literal-tmp-path: scenario fields (pig_args, pi_args, env values,
//     tmux.cwd, pre_clear_paths) must not contain literal /tmp/... paths.
//     Use the {{TEMP}} substitution token instead. The runner expands
//     {{TEMP}} to a fresh per-binary, per-run tempdir, eliminating the
//     four isolation failure modes documented in parity/runner/tokens.go.
//
//  9. uses-pre-clear-paths: pre_clear_paths is deprecated. A fresh
//     {{TEMP}} root needs nothing pre-cleared; the field exists only
//     for unmigrated scenarios and triggers a warning to drive
//     migration. New scenarios must not declare it.
//
//  11. group-rationale-missing: scenarios with an explicit `group = "..."`
//     override must carry a `# group-rationale:` comment explaining why the
//     default driver-derived group is insufficient. The scheduler is part of
//     the verification compiler; overrides must be intentional and auditable.
//     A scenario that only passes when retried is not "flaky" by default: it
//     is evidence that the timeout, group, or fixture isolation model is wrong.
//
//  12. redundant-group-override: if `group = "..."` matches the scenario's
//     default driver-derived bucket, remove it. Redundant overrides add noise
//     and make schedule audits harder.
//
//  13. registration-provider-overclaim: registration-only/list-models scenarios
//     must not claim provider streaming/conversion files. Listing a provider in
//     --list-models proves catalog registration, not payload conversion or stream
//     handling.
//
//  14. speculative-coverage: coverage evidence must name an exercised path.
//
//  15. hermetic-requires-auth: hermetic scenarios cannot depend on ambient
//     credentials. Use checked-in non-secret fixture auth instead.
//
//  16. no-behavior-assertion: non-TUI behavioral scenarios need an observable
//     oracle; execution-only tripwires must be tagged smoke-only.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

type scenario struct {
	Name          string             `toml:"name"`
	Description   string             `toml:"description"`
	Covers        []string           `toml:"covers"`
	Tags          []string           `toml:"tags"`
	Driver        string             `toml:"driver"`
	Model         string             `toml:"model"`
	Group         string             `toml:"group"`
	Assert        assertBlock        `toml:"assert"`
	Env           envBlock           `toml:"env"`
	Tmux          tmuxBlock          `toml:"tmux"`
	Cli           cliBlock           `toml:"cli"`
	Print         printBlock         `toml:"print"`
	RPC           rpcBlock           `toml:"rpc"`
	ExtensionHost extensionHostBlock `toml:"extension_host"`
	Diverge       divergeBlock       `toml:"diverge"`
}

type assertBlock struct {
	ExitCode              *int                    `toml:"exit_code"`
	PigExitCode           *int                    `toml:"pig_exit_code"`
	PiExitCode            *int                    `toml:"pi_exit_code"`
	BothContain           []string                `toml:"both_contain"`
	BothNotContain        []string                `toml:"both_not_contain"`
	PigContains           []string                `toml:"pig_contains"`
	PigNotContain         []string                `toml:"pig_not_contain"`
	PiContains            []string                `toml:"pi_contains"`
	PiNotContain          []string                `toml:"pi_not_contain"`
	BothMatchRegex        []string                `toml:"both_match_regex"`
	OutputEqual           bool                    `toml:"output_equal"`
	OutputNormalizedEqual bool                    `toml:"output_normalized_equal"`
	OutputLayoutEqual     bool                    `toml:"output_layout_equal"`
	EscapedOutputEqual    bool                    `toml:"escaped_output_equal"`
	RuntimeRatioMax       float64                 `toml:"runtime_ratio_max"`
	BothReachReady        bool                    `toml:"both_reach_ready"`
	Runs                  int                     `toml:"runs"`
	NormalizeReplace      []normalizeReplaceBlock `toml:"normalize_replace"`
	Extensions            extensionAssertBlock    `toml:"extensions"`

	ArtifactNormalizedEqual bool     `toml:"artifact_normalized_equal"`
	ArtifactBothContain     []string `toml:"artifact_both_contain"`
	ArtifactBothNotContain  []string `toml:"artifact_both_not_contain"`
}

type normalizeReplaceBlock struct {
	Pattern string `toml:"pattern"`
	With    string `toml:"with"`
}

type extensionAssertBlock struct {
	RegisteredTools     []string          `toml:"registered_tools"`
	RegisteredCommands  []string          `toml:"registered_commands"`
	RegisteredProviders []string          `toml:"registered_providers"`
	RegisteredShortcuts []string          `toml:"registered_shortcuts"`
	NoDiagnostics       bool              `toml:"no_diagnostics"`
	PlacementStrategy   map[string]string `toml:"placement_strategy"`
}

type envBlock struct {
	Pig              []string `toml:"pig"`
	Pi               []string `toml:"pi"`
	PigArgs          []string `toml:"pig_args"`
	PiArgs           []string `toml:"pi_args"`
	PigExtensions    []string `toml:"pig_extensions"`
	PiExtensions     []string `toml:"pi_extensions"`
	PigBin           string   `toml:"pig_bin"`
	PiBin            string   `toml:"pi_bin"`
	OverrideBaseArgs bool     `toml:"override_base_args"`
}

type tmuxBlock struct {
	Width               int             `toml:"width"`
	Height              int             `toml:"height"`
	ReadyTimeoutSeconds int             `toml:"ready_timeout_seconds"`
	Keys                []string        `toml:"keys"`
	SettleSeconds       int             `toml:"settle_seconds"`
	ReadyPatternPig     string          `toml:"ready_pattern_pig"`
	ReadyPatternPi      string          `toml:"ready_pattern_pi"`
	CaptureStart        string          `toml:"capture_start"`
	CaptureEnd          string          `toml:"capture_end"`
	CaptureEndRegex     string          `toml:"capture_end_regex"`
	CaptureStartLast    bool            `toml:"capture_start_last"`
	CaptureJoinWrapped  bool            `toml:"capture_join_wrapped"`
	CWD                 string          `toml:"cwd"`
	GitBranch           string          `toml:"git_branch"`
	PreClearPaths       []string        `toml:"pre_clear_paths"`
	Steps               []tmuxStepBlock `toml:"steps"`
}

type tmuxStepBlock struct {
	Keys                []string `toml:"keys"`
	SettleSeconds       int      `toml:"settle_seconds"`
	WaitContains        []string `toml:"wait_contains"`
	WaitVisibleContains []string `toml:"wait_visible_contains"`
	WaitNotContains     []string `toml:"wait_not_contains"`
	WaitTimeoutSeconds  int      `toml:"wait_timeout_seconds"`
}

type cliBlock struct {
	Args           []string `toml:"args"`
	PigArgs        []string `toml:"pig_args"`
	PiArgs         []string `toml:"pi_args"`
	InputLines     []string `toml:"input_lines"`
	SettleSeconds  int      `toml:"settle_seconds"`
	TimeoutSeconds int      `toml:"timeout_seconds"`
	CWD            string   `toml:"cwd"`
	SnapshotCWD    bool     `toml:"snapshot_cwd"`
	ArtifactPath   string   `toml:"artifact_path"`
}

type printBlock struct {
	Prompt         string `toml:"prompt"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
	CWD            string `toml:"cwd"`
}

type rpcBlock struct {
	InputLines     []string       `toml:"input_lines"`
	Steps          []rpcInputStep `toml:"steps"`
	TimeoutSeconds int            `toml:"timeout_seconds"`
	SettleSeconds  int            `toml:"settle_seconds"`
	CWD            string         `toml:"cwd"`
}

type rpcInputStep struct {
	Line               string   `toml:"line"`
	WaitContains       []string `toml:"wait_contains"`
	WaitTimeoutSeconds int      `toml:"wait_timeout_seconds"`
}

type extensionHostBlock struct {
	Sources        []string `toml:"sources"`
	TimeoutSeconds int      `toml:"timeout_seconds"`
	CWD            string   `toml:"cwd"`
}

type divergeBlock struct {
	PigContains []string `toml:"pig_contains"`
	PiContains  []string `toml:"pi_contains"`
	Reason      string   `toml:"reason"`
}

type violation struct {
	File    string
	Rule    string
	Message string
}

func main() {
	scenariosDir := flag.String("scenarios", "parity/scenarios", "scenarios directory")
	portMap := flag.String("port-map", "PORT_MAP.md", "path to PORT_MAP.md")
	divergencesMd := flag.String("divergences", "DIVERGENCES.md", "path to DIVERGENCES.md")
	flag.Parse()

	known, err := parsePortMapPaths(*portMap)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lint: %v\n", err)
		os.Exit(2)
	}

	knownDivergences, err := parseDivergenceIDs(*divergencesMd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lint: %v\n", err)
		os.Exit(2)
	}
	// Additive Pig-only features (no upstream Pi equivalent) live in a
	// separate registry. Scenarios may still reference them by ID for
	// downstream-evidence comments: treat them as known.
	additivePath := filepath.Join(filepath.Dir(*divergencesMd), "docs", "additive-features.md")
	if additive, addErr := parseDivergenceIDs(additivePath); addErr == nil {
		for id := range additive {
			knownDivergences[id] = true
		}
	}

	var violations []violation
	scenarioPaths := map[string]string{}
	driverCounts := map[string]int{}
	hermeticDriverCounts := map[string]int{}
	err = filepath.WalkDir(*scenariosDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".toml") {
			return err
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		source := string(raw)

		var sc scenario
		decErr := decodeScenarioWithUnknownKeyLint(path, source, &sc, &violations)
		if decErr != nil {
			return nil
		}
		violations = append(violations, lintScenarioName(path, sc.Name, scenarioPaths)...)
		driverCounts[sc.Driver]++
		if hasTag(sc.Tags, "hermetic") {
			hermeticDriverCounts[sc.Driver]++
		}

		violations = append(violations, lintScenario(path, source, &sc, known, knownDivergences)...)
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "lint: walk: %v\n", err)
		os.Exit(2)
	}
	violations = append(violations, lintOAuthProviderCoverage(*scenariosDir)...)
	violations = append(violations, lintDriverCoverage(*scenariosDir, driverCounts, hermeticDriverCounts)...)

	if len(violations) == 0 {
		fmt.Println("lint: all scenarios pass")
		os.Exit(0)
	}

	for _, v := range violations {
		fmt.Printf("%s: [%s] %s\n", v.File, v.Rule, v.Message)
	}
	fmt.Printf("\nlint: %d violation(s) found\n", len(violations))
	os.Exit(1)
}

func lintDriverCoverage(path string, counts, hermeticCounts map[string]int) []violation {
	expected := []string{"cli-mode", "extension-host", "headless-terminal", "interactive-tmux", "print-mode", "rpc-mode"}
	known := make(map[string]bool, len(expected))
	var out []violation
	for _, driver := range expected {
		known[driver] = true
		if counts[driver] == 0 {
			out = append(out, violation{path, "missing-driver-scenario", fmt.Sprintf("driver %q has no scenario", driver)})
			continue
		}
		if hermeticCounts[driver] == 0 {
			out = append(out, violation{path, "missing-hermetic-driver-scenario", fmt.Sprintf("driver %q has no hermetic scenario in the default gate", driver)})
		}
	}
	for driver := range counts {
		if !known[driver] {
			out = append(out, violation{path, "unknown-driver", fmt.Sprintf("scenario references unregistered driver %q", driver)})
		}
	}
	return out
}

func lintScenarioName(path, name string, seen map[string]string) []violation {
	name = strings.TrimSpace(name)
	if name == "" {
		return []violation{{path, "missing-name", "scenario name is required for subtest and artifact identity"}}
	}
	if first := seen[name]; first != "" {
		return []violation{{path, "duplicate-name",
			fmt.Sprintf("scenario name %q is already used by %s; names must be globally unique so subtests, skips, results, and artifacts cannot collide", name, first)}}
	}
	seen[name] = path
	return nil
}

func decodeScenarioWithUnknownKeyLint(path, source string, sc *scenario, violations *[]violation) error {
	md, decErr := toml.Decode(source, sc)
	if decErr != nil {
		*violations = append(*violations, violation{path, "parse", decErr.Error()})
		return decErr
	}
	for _, key := range md.Undecoded() {
		*violations = append(*violations, violation{path, "unknown-key",
			fmt.Sprintf("unknown TOML key %q; typo'd assertion/config keys silently disable QC", strings.Join(key, "."))})
	}
	return nil
}

func lintScenario(path, source string, sc *scenario, known map[string]bool, knownDivergences map[string]bool) []violation {
	var out []violation

	hasEq := sc.Assert.OutputEqual || sc.Assert.OutputNormalizedEqual || sc.Assert.OutputLayoutEqual || sc.Assert.EscapedOutputEqual
	isTmux := sc.Driver == "interactive-tmux" || sc.Driver == "headless-terminal"
	isBootOnly := strings.HasPrefix(sc.Description, "boot-only:")
	isDeferred := hasTag(sc.Tags, "deferred")
	isSmokeOnly := hasTag(sc.Tags, "smoke-only")
	hasKnownGap := strings.Contains(source, "# lint-known-gap:")

	if len(sc.RPC.InputLines) > 0 && len(sc.RPC.Steps) > 0 {
		out = append(out, violation{path, "rpc-input-conflict", "rpc input_lines and steps are mutually exclusive"})
	}
	for i, step := range sc.RPC.Steps {
		if strings.TrimSpace(step.Line) == "" {
			out = append(out, violation{path, "rpc-step-line", fmt.Sprintf("rpc step %d requires line", i+1)})
		}
		if len(step.WaitContains) > 0 && step.WaitTimeoutSeconds <= 0 {
			out = append(out, violation{path, "rpc-step-timeout", fmt.Sprintf("rpc step %d with wait_contains requires wait_timeout_seconds", i+1)})
		}
	}

	// Rule 1: tmux scenarios need an equality comparator, boot-only opt-out,
	// or a diagnosed rendering gap.
	if isTmux && !hasEq && !isBootOnly && !isDeferred && !hasKnownGap {
		out = append(out, violation{path, "no-equality-comparator",
			fmt.Sprintf("tmux scenario %q has no output_equal, output_normalized_equal, "+
				"output_layout_equal, or escaped_output_equal; add one, prefix description "+
				"with \"boot-only:\", or add a \"# lint-known-gap:\" comment documenting "+
				"the specific rendering drift", sc.Name)})
	}

	// Rule 2: covers entries must exist in PORT_MAP.
	for _, c := range sc.Covers {
		if !known[c] {
			out = append(out, violation{path, "orphan-cover",
				fmt.Sprintf("covers entry %q not found in PORT_MAP.md", c)})
		}
	}

	// Rule 3: tmux scenarios should have upstream evidence comment.
	if isTmux && !isBootOnly && !isDeferred && !hasUpstreamEvidence(source) {
		out = append(out, violation{path, "no-upstream-evidence",
			fmt.Sprintf("tmux scenario %q missing upstream evidence comment "+
				"(expected '# Upstream evidence' or '# Manual upstream-first tmux probe')", sc.Name)})
	}

	// Rule 4: deferred scenarios must have a description.
	if isDeferred && strings.TrimSpace(sc.Description) == "" {
		out = append(out, violation{path, "deferred-no-description",
			fmt.Sprintf("deferred scenario %q has no description", sc.Name)})
	}

	// Rule 5: boot-only scenarios may claim at most 1 covers entry.
	// A scenario that cannot assert behavior cannot claim to cover
	// multiple files: the coverage count would be inflated.
	if isBootOnly && len(sc.Covers) > 1 {
		out = append(out, violation{path, "boot-only-overclaims",
			fmt.Sprintf("boot-only scenario %q claims %d covers entries (max 1); "+
				"boot-only scenarios prove only startup readiness, not behavioral parity",
				sc.Name, len(sc.Covers))})
	}

	// Rule 6: any "D<N>" reference must match a DIVERGENCES.md entry.
	// Prevents fake-citing a divergence to justify silent skipping
	// (e.g., "D1 divergence" when D1 is about something unrelated).
	for _, id := range findDivergenceRefs(source) {
		if !knownDivergences[id] {
			out = append(out, violation{path, "unknown-divergence",
				fmt.Sprintf("scenario %q references %s but no such entry exists in DIVERGENCES.md; "+
					"either add the divergence with SCRUTINIZED:approved or remove the reference",
					sc.Name, id)})
		}
	}

	// Rule 7: env dir paths must resolve to existing directories.
	// This catches typo'd relative paths (the root cause of P0-1) at
	// lint time instead of runtime. If the snapshot mechanism would
	// t.Fatalf on a missing dir, the lint should catch it first.
	out = append(out, lintEnvDirPaths(path, sc)...)

	// Rule 8: scenario fields must not contain literal /tmp/... paths.
	// Use {{TEMP}} substitution instead. Eliminates cross-run races,
	// destructive RemoveAll on shared paths, and the four isolation
	// failure modes documented in parity/runner/tokens.go.
	out = append(out, lintNoLiteralTmpPaths(path, sc)...)

	// Rule 9: pre_clear_paths is deprecated. A fresh {{TEMP}} root has
	// nothing to clear; the field is only kept for unmigrated scenarios.
	if len(sc.Tmux.PreClearPaths) > 0 {
		out = append(out, violation{path, "uses-pre-clear-paths",
			fmt.Sprintf("scenario %q declares pre_clear_paths (deprecated). "+
				"Switch to {{TEMP}} substitution: a fresh tempdir is allocated "+
				"per binary per run; nothing needs clearing.", sc.Name)})
	}

	// Rule 10: deferred scenarios may claim at most 3 covers entries.
	// A deferred scenario asserts NO behavior: it's a placeholder for
	// surfaces that cannot yet be expressed hermetically (OAuth flows,
	// clipboard, image pickers). Allowing such a scenario to claim 8 or
	// 9 files inflates the coverage metric without actually verifying
	// anything. Cap is set at 3 (not 1 like boot-only) because a deferred
	// scenario legitimately spans a tightly-coupled cluster: e.g. the
	// OAuth flow really does need browser-callback, token-exchange, and
	// refresh-rotation all together. But 8+ files signals "I dumped every
	// loosely related path here to pump the percentage." Use multiple
	// deferred scenarios with focused covers if you genuinely need more.
	if isDeferred && len(sc.Covers) > 3 {
		out = append(out, violation{path, "deferred-overclaims",
			fmt.Sprintf("deferred scenario %q claims %d covers entries (max 3); "+
				"deferred scenarios assert no behavior, so each covers entry "+
				"is metric inflation. Split into multiple focused deferred "+
				"scenarios (one per tight cluster), or narrow this one to the "+
				"surface that actually shares an upstream test boundary",
				sc.Name, len(sc.Covers))})
	}

	// Rule 11: explicit group overrides must explain themselves.
	// The scheduler is part of the verification compiler. Require a
	// `# group-rationale:` comment so an override is deliberate and reviewable.
	if strings.TrimSpace(sc.Group) != "" && !strings.Contains(source, "# group-rationale:") {
		out = append(out, violation{path, "group-rationale-missing",
			fmt.Sprintf("scenario %q sets group=%q but has no '# group-rationale:' comment; "+
				"explain why the default driver-derived group is insufficient",
				sc.Name, sc.Group)})
	}

	// Rule 12: redundant group overrides are noise.
	// If the scenario's explicit group matches the default driver-derived
	// bucket, remove it so schedule audits only show meaningful exceptions.
	if g := strings.TrimSpace(sc.Group); g != "" && g == defaultGroupForScenario(sc) {
		out = append(out, violation{path, "redundant-group-override",
			fmt.Sprintf("scenario %q sets group=%q, which is already the default for driver=%q; remove the override or change the driver/group",
				sc.Name, g, sc.Driver)})
	}

	// Rule 13: registration-only / --list-models scenarios cannot claim
	// provider streaming/conversion implementation files. They prove provider
	// catalog registration only. A real provider coverage scenario must send a
	// request or assert serialized payload shape.
	if isRegistrationOnlyScenario(sc) {
		for _, c := range sc.Covers {
			if isProviderBehaviorPath(c) {
				out = append(out, violation{path, "registration-provider-overclaim",
					fmt.Sprintf("scenario %q is registration-only/list-models but claims provider behavior file %q; remove the cover or add a real stream/payload scenario",
						sc.Name, c)})
			}
		}
	}

	// Rule 14: coverage evidence must name an exercised production path, not
	// speculate that a broad user flow may happen to touch it.
	if speculativeCoverageRE.MatchString(source) {
		out = append(out, violation{path, "speculative-coverage",
			fmt.Sprintf("scenario %q uses speculative coverage language; prove the production path or remove the covers claim", sc.Name)})
	}

	// Rule 15: hermetic scenarios carry every credential they need in checked-in
	// fixtures. Combining hermetic with requires-auth makes execution depend on
	// ambient developer/CI secrets and silently skips coverage on clean hosts.
	if hasTag(sc.Tags, "hermetic") && hasTag(sc.Tags, "requires-auth") {
		out = append(out, violation{path, "hermetic-requires-auth",
			fmt.Sprintf("scenario %q is tagged both hermetic and requires-auth; stage a non-secret fixture and remove requires-auth, or remove hermetic", sc.Name)})
	}
	if hasTag(sc.Tags, "fixture-auth") && (!hasTag(sc.Tags, "hermetic") || hasTag(sc.Tags, "requires-auth")) {
		out = append(out, violation{path, "fixture-auth-contract",
			fmt.Sprintf("scenario %q uses fixture-auth without an exclusively hermetic auth contract", sc.Name)})
	}

	// Rule 16: a behavioral scenario must make a behavioral assertion. Merely
	// executing both binaries with runs=N cannot verify a covers claim.
	if !isTmux && !isDeferred && !isBootOnly && !isSmokeOnly && !hasBehaviorAssertion(sc) {
		out = append(out, violation{path, "no-behavior-assertion",
			fmt.Sprintf("scenario %q has no output, exit, readiness, or registration assertion; add an oracle or tag it smoke-only", sc.Name)})
	}
	if len(sc.Assert.NormalizeReplace) > 0 && !strings.Contains(source, "# normalize-rationale:") {
		out = append(out, violation{path, "normalize-rationale-missing",
			fmt.Sprintf("scenario %q uses normalize_replace without a '# normalize-rationale:' comment naming the unstable field and why masking it cannot hide behavioral drift", sc.Name)})
	}

	return out
}

func hasBehaviorAssertion(sc *scenario) bool {
	a := sc.Assert
	return a.ExitCode != nil || a.PigExitCode != nil || a.PiExitCode != nil ||
		len(a.BothContain) > 0 || len(a.PigContains) > 0 || len(a.PiContains) > 0 ||
		len(a.BothMatchRegex) > 0 || a.OutputEqual || a.OutputNormalizedEqual ||
		a.OutputLayoutEqual || a.EscapedOutputEqual || a.BothReachReady ||
		len(a.Extensions.RegisteredTools) > 0 || len(a.Extensions.RegisteredCommands) > 0 ||
		len(a.Extensions.RegisteredProviders) > 0 || len(a.Extensions.RegisteredShortcuts) > 0 ||
		a.Extensions.NoDiagnostics || len(a.Extensions.PlacementStrategy) > 0 ||
		len(sc.Diverge.PigContains) > 0 || len(sc.Diverge.PiContains) > 0
}

func lintOAuthProviderCoverage(scenariosDir string) []violation {
	providers := []string{
		"Anthropic (Claude Pro/Max)",
		"OpenAI (ChatGPT Plus/Pro)",
		"GitHub Copilot",
	}
	seen := map[string]string{}
	_ = filepath.WalkDir(scenariosDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".toml") {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		source := string(data)
		for _, provider := range providers {
			if strings.Contains(source, provider) {
				seen[provider] = path
			}
		}
		return nil
	})
	var out []violation
	for _, provider := range providers {
		if seen[provider] == "" {
			out = append(out, violation{filepath.Join(scenariosDir, "**", "*.toml"), "oauth-provider-coverage",
				fmt.Sprintf("OAuth provider display name %q is not asserted by any scenario; add a selector scenario so provider-list regressions are caught before manual testing", provider)})
		}
	}
	return out
}

func isRegistrationOnlyScenario(sc *scenario) bool {
	if hasTag(sc.Tags, "registration-only") {
		return true
	}
	if sc.Driver == "cli-mode" && slices.Contains(sc.Cli.Args, "--list-models") {
		return true
	}
	return false
}

func isProviderBehaviorPath(path string) bool {
	if !strings.HasPrefix(path, "packages/ai/src/providers/") {
		return false
	}
	base := filepath.Base(path)
	return base != "register-builtins.ts"
}

// parityFixtureExtensions mirrors parity/runner/scheduling.go: parity's own
// in-process fixtures (the faux provider) start no subprocess extension host,
// so they must not push a scenario into the throttled -ext scheduling group.
// Keep in sync with parityFixtureExtensions / hasSchedulingExtensions in the
// runner so this linter's redundant-group check agrees with the real grouping.
var parityFixtureExtensions = map[string]bool{
	"test-faux-provider.ts": true,
}

// schedulingExtensionsPresent reports whether the scenario loads an extension
// that starts a subprocess extension host, i.e. the extensions the -ext groups
// throttle. In-process parity fixtures do not count.
func schedulingExtensionsPresent(sc *scenario) bool {
	for _, ext := range sc.Env.PigExtensions {
		if !parityFixtureExtensions[filepath.Base(ext)] {
			return true
		}
	}
	for _, ext := range sc.Env.PiExtensions {
		if !parityFixtureExtensions[filepath.Base(ext)] {
			return true
		}
	}
	return false
}

func defaultGroupForScenario(sc *scenario) string {
	if hasTag(sc.Tags, "serial") {
		return "exclusive"
	}
	hasExtensions := schedulingExtensionsPresent(sc)
	switch sc.Driver {
	case "interactive-tmux":
		if hasExtensions {
			return "tmux-ext"
		}
		return "tmux"
	case "headless-terminal":
		return "ht"
	case "rpc-mode":
		return "rpc"
	case "print-mode":
		if hasExtensions {
			return "process-ext"
		}
		return "process"
	default:
		return "process"
	}
}

// lintNoLiteralTmpPaths flags any scenario field that hard-codes a
// /tmp path. The runner provides {{TEMP}} for hermetic per-run dirs;
// literal paths defeat that isolation.
func lintNoLiteralTmpPaths(path string, sc *scenario) []violation {
	var out []violation
	check := func(field string, parts []string) {
		for _, p := range parts {
			if strings.HasPrefix(p, "/tmp/") || strings.Contains(p, "=/tmp/") {
				out = append(out, violation{path, "literal-tmp-path",
					fmt.Sprintf("scenario %q field %s contains literal /tmp path %q. "+
						"Use {{TEMP}} (or {{TEMP}}/<subdir>) so the runner can allocate "+
						"a fresh per-binary tempdir. See parity/runner/tokens.go.",
						sc.Name, field, p)})
			}
		}
	}
	check("env.pig_args", sc.Env.PigArgs)
	check("env.pi_args", sc.Env.PiArgs)
	check("env.pig", sc.Env.Pig)
	check("env.pi", sc.Env.Pi)
	check("tmux.pre_clear_paths", sc.Tmux.PreClearPaths)
	if sc.Tmux.CWD != "" {
		check("tmux.cwd", []string{sc.Tmux.CWD})
	}
	return out
}

// snapshotEnvKeys lists env keys whose values are directory paths that
// the snapshot mechanism copies. Must stay in sync with
// parity/runner/snapshot.go:snapshotEnvKeys.
var lintSnapshotKeys = map[string]bool{
	"PIG_CODING_AGENT_DIR": true,
	"PI_CODING_AGENT_DIR":  true,
	"PIG_HOME":             true,
	"PI_HOME":              true,
}

// lintEnvDirPaths validates that every snapshotable env key in the
// scenario's [env] section points to an existing directory. Paths are
// resolved relative to the scenario file, exactly as the runner does.
func lintEnvDirPaths(scenarioPath string, sc *scenario) []violation {
	var out []violation
	dir := filepath.Dir(scenarioPath)

	check := func(envSlice []string, label string) {
		for _, kv := range envSlice {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || v == "" {
				continue
			}
			if !lintSnapshotKeys[k] {
				continue
			}
			resolved := v
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(dir, resolved)
			}
			if fi, err := os.Stat(resolved); err != nil {
				out = append(out, violation{scenarioPath, "env-dir-missing",
					fmt.Sprintf("[env].%s: %s=%s resolves to %s which does not exist",
						label, k, v, resolved)})
			} else if !fi.IsDir() {
				out = append(out, violation{scenarioPath, "env-dir-not-dir",
					fmt.Sprintf("[env].%s: %s=%s resolves to %s which is not a directory",
						label, k, v, resolved)})
			}
		}
	}

	check(sc.Env.Pig, "pig")
	check(sc.Env.Pi, "pi")
	return out
}

// hasUpstreamEvidence returns true if the scenario source contains
// evidence of upstream probing. Accepts both canonical and legacy
// phrasings so older scenarios don't produce false negatives.
func hasUpstreamEvidence(source string) bool {
	return strings.Contains(source, "# Upstream evidence") ||
		strings.Contains(source, "# Manual upstream-first tmux probe")
}

func hasTag(tags []string, tag string) bool {
	return slices.Contains(tags, tag)
}

var portMapRowRE = regexp.MustCompile(`^\|\s+` + "`" + `([^` + "`" + `]+)` + "`" + `\s+\|`)

var speculativeCoverageRE = regexp.MustCompile(`(?i)\b(?:may|might|possibly)\s+exercise\b`)

// divergenceRefRE finds "D<N>" tokens that are NOT part of larger
// identifiers. \b ensures word boundaries so "ID" or "DATA" don't
// match.
var divergenceRefRE = regexp.MustCompile(`\bD(\d+)\b`)

// divergenceHeaderRE matches the "## D<N> ..." headers in DIVERGENCES.md
// that declare each active or retired entry.
var divergenceHeaderRE = regexp.MustCompile(`^##\s+D(\d+)\b`)

// findDivergenceRefs returns the unique "D<N>" identifiers cited
// anywhere in the scenario source (description, comments, etc.).
func findDivergenceRefs(source string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range divergenceRefRE.FindAllStringSubmatch(source, -1) {
		id := "D" + m[1]
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func parseDivergenceIDs(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool)
	for line := range strings.SplitSeq(string(data), "\n") {
		m := divergenceHeaderRE.FindStringSubmatch(line)
		if m != nil {
			out["D"+m[1]] = true
		}
	}
	return out, nil
}

func parsePortMapPaths(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool)
	for line := range strings.SplitSeq(string(data), "\n") {
		m := portMapRowRE.FindStringSubmatch(line)
		if m != nil {
			out[m[1]] = true
		}
	}
	return out, nil
}
