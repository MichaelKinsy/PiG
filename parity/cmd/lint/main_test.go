package main

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestLintRules(t *testing.T) {
	known := map[string]bool{
		"packages/tui/src/autocomplete.ts":                true,
		"packages/coding-agent/src/main.ts":               true,
		"packages/ai/src/providers/openai-completions.ts": true,
		"packages/ai/src/providers/register-builtins.ts":  true,
	}
	knownDivergences := map[string]bool{
		"D1": true,
		"D2": true,
	}

	tests := []struct {
		name      string
		source    string
		sc        scenario
		wantRules []string // expected violation rule names
	}{
		{
			name:   "clean tmux scenario passes",
			source: "# Upstream evidence (pi v0.69.0)\nsome toml",
			sc: scenario{
				Name:   "test",
				Driver: "interactive-tmux",
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Tags:   []string{"fast", "hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: nil,
		},
		{
			name:   "tmux without equality comparator fails",
			source: "# Upstream evidence (pi v0.69.0)\nsome toml",
			sc: scenario{
				Name:   "weak-test",
				Driver: "interactive-tmux",
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Tags:   []string{"fast", "hermetic"},
			},
			wantRules: []string{"no-equality-comparator"},
		},
		{
			name:   "boot-only description exempts equality rule",
			source: "# Upstream evidence (pi v0.69.0)\nsome toml",
			sc: scenario{
				Name:        "boot-test",
				Description: "boot-only: verifies startup",
				Driver:      "interactive-tmux",
				Covers:      []string{"packages/tui/src/autocomplete.ts"},
				Tags:        []string{"fast", "hermetic"},
			},
			wantRules: nil,
		},
		{
			name:   "boot-only with multiple covers fails",
			source: "# Upstream evidence (pi v0.69.0)\nsome toml",
			sc: scenario{
				Name:        "boot-multi",
				Description: "boot-only: checks two things",
				Driver:      "interactive-tmux",
				Covers: []string{
					"packages/tui/src/autocomplete.ts",
					"packages/coding-agent/src/main.ts",
				},
				Tags: []string{"fast", "hermetic"},
			},
			wantRules: []string{"boot-only-overclaims"},
		},
		{
			name:   "lint-known-gap exempts equality rule",
			source: "# Upstream evidence (pi v0.69.0)\n# lint-known-gap: rendering differs\nsome toml",
			sc: scenario{
				Name:   "gap-test",
				Driver: "interactive-tmux",
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Tags:   []string{"fast", "hermetic"},
			},
			wantRules: nil,
		},
		{
			name:   "orphan cover detected",
			source: "# Upstream evidence (pi v0.69.0)\nsome toml",
			sc: scenario{
				Name:   "orphan-test",
				Driver: "interactive-tmux",
				Covers: []string{"packages/nonexistent/file.ts"},
				Tags:   []string{"fast", "hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: []string{"orphan-cover"},
		},
		{
			name:   "speculative coverage language fails",
			source: "# Upstream evidence\n# This may exercise the renderer.\n",
			sc: scenario{
				Name:   "speculative",
				Driver: "interactive-tmux",
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: []string{"speculative-coverage"},
		},
		{
			name:   "hermetic scenario cannot depend on ambient auth",
			source: "# fixture-backed scenario\n",
			sc: scenario{
				Name:   "contradictory-tags",
				Driver: "cli-mode",
				Tags:   []string{"hermetic", "requires-auth"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: []string{"hermetic-requires-auth"},
		},
		{
			name:   "runs alone are not a behavioral assertion",
			source: "# no oracle\n",
			sc: scenario{
				Name:   "assertion-free",
				Driver: "print-mode",
				Assert: assertBlock{Runs: 3},
			},
			wantRules: []string{"no-behavior-assertion"},
		},
		{
			name:   "fixture auth is only valid for hermetic scenarios",
			source: "# fixture auth\n",
			sc: scenario{
				Name:   "fixture-auth-live",
				Driver: "cli-mode",
				Tags:   []string{"fixture-auth"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: []string{"fixture-auth-contract"},
		},
		{
			name:   "smoke-only permits execution tripwire",
			source: "# intentionally weak\n",
			sc: scenario{
				Name:   "smoke",
				Driver: "print-mode",
				Tags:   []string{"smoke-only"},
				Assert: assertBlock{Runs: 3},
			},
			wantRules: nil,
		},
		{
			name:   "generic crash denial alone is not a behavioral oracle",
			source: "# no positive oracle\n",
			sc: scenario{
				Name:   "no-crash-only",
				Driver: "cli-mode",
				Assert: assertBlock{BothNotContain: []string{"panic:", "goroutine "}},
			},
			wantRules: []string{"no-behavior-assertion"},
		},
		{
			name:   "normalization requires a bounded rationale",
			source: "# timing varies\n",
			sc: scenario{
				Name:   "unexplained-normalization",
				Driver: "cli-mode",
				Assert: assertBlock{
					OutputEqual:      true,
					NormalizeReplace: []normalizeReplaceBlock{{Pattern: `Took .*`, With: "Took N"}},
				},
			},
			wantRules: []string{"normalize-rationale-missing"},
		},
		{
			name:   "missing upstream evidence detected",
			source: "# just a comment\nsome toml",
			sc: scenario{
				Name:   "no-evidence",
				Driver: "interactive-tmux",
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Tags:   []string{"fast", "hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: []string{"no-upstream-evidence"},
		},
		{
			name:   "legacy upstream evidence phrasing accepted",
			source: "# Manual upstream-first tmux probe (2026-05-14):\nsome toml",
			sc: scenario{
				Name:   "legacy-evidence",
				Driver: "interactive-tmux",
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Tags:   []string{"fast", "hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: nil,
		},
		{
			name:   "deferred without description fails",
			source: "# some comment",
			sc: scenario{
				Name:   "deferred-empty",
				Driver: "interactive-tmux",
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Tags:   []string{"deferred"},
			},
			wantRules: []string{"deferred-no-description"},
		},
		{
			name:   "deferred with description passes",
			source: "# some comment",
			sc: scenario{
				Name:        "deferred-ok",
				Description: "parked: pig renders error differently",
				Driver:      "interactive-tmux",
				Covers:      []string{"packages/tui/src/autocomplete.ts"},
				Tags:        []string{"deferred"},
			},
			wantRules: nil,
		},
		{
			// Rule 10: deferred scenarios may claim at most 3 covers entries.
			// Prevents metric inflation (a single deferred scenario claiming
			// 8+ files pumps the "covered" count without verifying anything).
			name:   "deferred with 3 covers passes (at boundary)",
			source: "# some comment",
			sc: scenario{
				Name:        "deferred-three",
				Description: "deferred: 3 tightly-coupled paths",
				Driver:      "interactive-tmux",
				Covers: []string{
					"packages/tui/src/autocomplete.ts",
					"packages/coding-agent/src/main.ts",
					"packages/tui/src/autocomplete.ts", // dup ok for test
				},
				Tags: []string{"deferred"},
			},
			wantRules: nil,
		},
		{
			name:   "deferred with 4 covers fails",
			source: "# some comment",
			sc: scenario{
				Name:        "deferred-four",
				Description: "deferred: too many entries",
				Driver:      "interactive-tmux",
				Covers: []string{
					"packages/tui/src/autocomplete.ts",
					"packages/coding-agent/src/main.ts",
					"packages/tui/src/autocomplete.ts",
					"packages/coding-agent/src/main.ts",
				},
				Tags: []string{"deferred"},
			},
			wantRules: []string{"deferred-overclaims"},
		},
		{
			// Non-deferred scenarios are not affected by Rule 10: a real
			// behavioral scenario legitimately covers many files (e.g.
			// providers-registry/01-list-models-env-builtins covers 14).
			name: "non-deferred with many covers passes Rule 10",
			source: "# Upstream evidence (pi v0.69.0): captured " +
				"command output",
			sc: scenario{
				Name:        "real-many-covers",
				Description: "non-deferred, asserts behavior",
				Driver:      "interactive-tmux",
				Covers: []string{
					"packages/tui/src/autocomplete.ts",
					"packages/coding-agent/src/main.ts",
					"packages/tui/src/autocomplete.ts",
					"packages/coding-agent/src/main.ts",
					"packages/tui/src/autocomplete.ts",
				},
				Tags:   []string{"hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: nil,
		},
		{
			name:   "registration-only scenario cannot claim provider behavior file",
			source: "# Upstream evidence (pi v0.69.0): captured --list-models output",
			sc: scenario{
				Name:   "list-models-overclaim",
				Driver: "cli-mode",
				Covers: []string{
					"packages/ai/src/providers/openai-completions.ts",
				},
				Tags:   []string{"fast", "hermetic", "registration-only"},
				Cli:    cliBlock{Args: []string{"--list-models"}},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: []string{"registration-provider-overclaim"},
		},
		{
			name:   "registration-only scenario may claim provider registry file",
			source: "# Upstream evidence (pi v0.69.0): captured --list-models output",
			sc: scenario{
				Name:   "list-models-registry",
				Driver: "cli-mode",
				Covers: []string{
					"packages/ai/src/providers/register-builtins.ts",
				},
				Tags:   []string{"fast", "hermetic", "registration-only"},
				Cli:    cliBlock{Args: []string{"--list-models"}},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: nil,
		},
		{
			name:   "behavioral provider scenario may claim provider behavior file",
			source: "# Upstream evidence (pi v0.69.0): captured provider stream payload",
			sc: scenario{
				Name:   "provider-stream-behavior",
				Driver: "print-mode",
				Covers: []string{
					"packages/ai/src/providers/openai-completions.ts",
				},
				Tags:   []string{"fast", "hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: nil,
		},
		{
			name:   "explicit custom group with rationale passes",
			source: "# Upstream evidence (pi v0.69.0)\n# group-rationale: this tmux case mutates global tmux server state and must run in a tighter bucket\n",
			sc: scenario{
				Name:   "group-override-ok",
				Driver: "interactive-tmux",
				Group:  "tmux-tight",
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Tags:   []string{"fast", "hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: nil,
		},
		{
			name:   "explicit group without rationale fails",
			source: "# Upstream evidence (pi v0.69.0)\n",
			sc: scenario{
				Name:   "group-override-missing-rationale",
				Driver: "interactive-tmux",
				Group:  "tmux-tight",
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Tags:   []string{"fast", "hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: []string{"group-rationale-missing"},
		},
		{
			name:   "redundant default group override fails",
			source: "# Upstream evidence (pi v0.69.0)\n# group-rationale: wrote it down anyway\n",
			sc: scenario{
				Name:   "group-redundant",
				Driver: "interactive-tmux",
				Group:  "tmux",
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Tags:   []string{"fast", "hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: []string{"redundant-group-override"},
		},
		{
			name:   "faux fixture tmux is redundant with plain tmux group",
			source: "# Upstream evidence (pi v0.69.0)\n# group-rationale: pinned anyway\n",
			sc: scenario{
				Name:   "group-redundant-faux",
				Driver: "interactive-tmux",
				Group:  "tmux",
				Env:    envBlock{PiExtensions: []string{"../../testdata/test-faux-provider.ts"}},
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Tags:   []string{"fast", "hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: []string{"redundant-group-override"},
		},
		{
			name:   "non-tmux driver skips tmux rules",
			source: "# no evidence needed",
			sc: scenario{
				Name:   "print-test",
				Driver: "print-mode",
				Covers: []string{"packages/coding-agent/src/main.ts"},
				Tags:   []string{"fast", "hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: nil,
		},
		{
			name: "known divergence reference passes",
			source: "# Upstream evidence (pi v0.69.0)\n" +
				"# This scenario asserts the D1 line-renderer crop only.\n",
			sc: scenario{
				Name:   "diverge-ok",
				Driver: "interactive-tmux",
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Tags:   []string{"fast", "hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: nil,
		},
		{
			name: "unknown divergence reference fails",
			source: "# Upstream evidence (pi v0.69.0)\n" +
				"# Skipped because of D42 divergence.\n",
			sc: scenario{
				Name:   "diverge-fake",
				Driver: "interactive-tmux",
				Covers: []string{"packages/tui/src/autocomplete.ts"},
				Tags:   []string{"fast", "hermetic"},
				Assert: assertBlock{OutputEqual: true},
			},
			wantRules: []string{"unknown-divergence"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := lintScenario("test.toml", tt.source, &tt.sc, known, knownDivergences)
			gotRules := make(map[string]bool)
			for _, v := range violations {
				gotRules[v.Rule] = true
			}

			if len(tt.wantRules) == 0 && len(violations) > 0 {
				t.Errorf("expected no violations, got %d:", len(violations))
				for _, v := range violations {
					t.Errorf("  [%s] %s", v.Rule, v.Message)
				}
				return
			}

			for _, want := range tt.wantRules {
				if !gotRules[want] {
					t.Errorf("expected violation rule %q, got rules: %v", want, violations)
				}
			}

			if len(tt.wantRules) > 0 && len(violations) != len(tt.wantRules) {
				t.Errorf("expected %d violations, got %d:", len(tt.wantRules), len(violations))
				for _, v := range violations {
					t.Errorf("  [%s] %s", v.Rule, v.Message)
				}
			}
		})
	}
}

func TestLintScenarioNameRejectsMissingAndDuplicateNames(t *testing.T) {
	seen := map[string]string{}
	if got := lintScenarioName("empty.toml", " ", seen); len(got) != 1 || got[0].Rule != "missing-name" {
		t.Fatalf("missing-name violations = %#v", got)
	}
	if got := lintScenarioName("first.toml", "same", seen); len(got) != 0 {
		t.Fatalf("first name violations = %#v", got)
	}
	if got := lintScenarioName("second.toml", "same", seen); len(got) != 1 || got[0].Rule != "duplicate-name" {
		t.Fatalf("duplicate-name violations = %#v", got)
	}
}

func TestAC67LintDriverCoverageRequiresEveryHermeticExecutionMode(t *testing.T) {
	counts := map[string]int{
		"cli-mode": 1, "extension-host": 1, "headless-terminal": 1,
		"interactive-tmux": 1, "print-mode": 1, "rpc-mode": 1,
	}
	hermetic := maps.Clone(counts)
	if got := lintDriverCoverage("scenarios", counts, hermetic); len(got) != 0 {
		t.Fatalf("complete drivers = %#v", got)
	}
	delete(hermetic, "rpc-mode")
	counts["typo-mode"] = 1
	got := lintDriverCoverage("scenarios", counts, hermetic)
	if len(got) != 2 || got[0].Rule != "missing-hermetic-driver-scenario" || got[1].Rule != "unknown-driver" {
		t.Fatalf("driver violations = %#v", got)
	}
}

func TestHasUpstreamEvidence(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"# Upstream evidence (pi v0.69.0):", true},
		{"# Manual upstream-first tmux probe (2026-05-14):", true},
		{"# just a comment", false},
		{"# upstream is mentioned but not as evidence", false},
	}
	for _, tt := range tests {
		got := hasUpstreamEvidence(tt.source)
		if got != tt.want {
			t.Errorf("hasUpstreamEvidence(%q) = %v, want %v", tt.source, got, tt.want)
		}
	}
}

func TestLintEnvDirPaths(t *testing.T) {
	// Create a real testdata dir so we can test resolution.
	dir := t.TempDir()
	scenarioPath := filepath.Join(dir, "family", "01-test.toml")
	_ = os.MkdirAll(filepath.Join(dir, "family", "testdata", "pig-agent"), 0o700)

	tests := []struct {
		name      string
		sc        scenario
		wantRules []string
	}{
		{
			name: "valid relative path passes",
			sc: scenario{
				Name: "valid",
				Env: envBlock{
					Pig: []string{"PIG_CODING_AGENT_DIR=testdata/pig-agent"},
				},
			},
			wantRules: nil,
		},
		{
			name: "missing dir caught",
			sc: scenario{
				Name: "missing",
				Env: envBlock{
					Pig: []string{"PIG_CODING_AGENT_DIR=../nonexistent/path"},
				},
			},
			wantRules: []string{"env-dir-missing"},
		},
		{
			name: "stray nested path caught (P0-1 repro)",
			sc: scenario{
				Name: "stray",
				Env: envBlock{
					Pig: []string{"PIG_CODING_AGENT_DIR=../scenarios/settings/testdata/pig-agent"},
				},
			},
			wantRules: []string{"env-dir-missing"},
		},
		{
			name: "non-snapshot key ignored",
			sc: scenario{
				Name: "ignored",
				Env: envBlock{
					Pig: []string{"SOME_OTHER_VAR=../nonexistent"},
				},
			},
			wantRules: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := lintEnvDirPaths(scenarioPath, &tt.sc)
			if len(tt.wantRules) == 0 && len(violations) > 0 {
				t.Errorf("expected no violations, got:")
				for _, v := range violations {
					t.Errorf("  [%s] %s", v.Rule, v.Message)
				}
			}
			for _, want := range tt.wantRules {
				found := false
				for _, v := range violations {
					if v.Rule == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected violation rule %q not found in %v", want, violations)
				}
			}
		})
	}
}

// TestLintNoLiteralTmpPaths exercises Rule 8 (no literal /tmp paths in
// scenario fields). Documented in parity/runner/tokens.go: scenarios
// must use {{TEMP}} substitution so the runner can allocate hermetic
// per-binary, per-run tempdirs.
func TestLintNoLiteralTmpPaths(t *testing.T) {
	mkSC := func(field string, val string) *scenario {
		sc := &scenario{Name: "test"}
		switch field {
		case "pig_args":
			sc.Env.PigArgs = []string{"--session-dir", val}
		case "pi_args":
			sc.Env.PiArgs = []string{"--session-dir", val}
		case "pig_env":
			sc.Env.Pig = []string{"PIG_HOME=" + val}
		case "pre_clear":
			sc.Tmux.PreClearPaths = []string{val}
		case "cwd":
			sc.Tmux.CWD = val
		}
		return sc
	}
	tests := []struct {
		name    string
		sc      *scenario
		wantTmp bool
	}{
		{"clean pig_args", mkSC("pig_args", "{{TEMP}}/sessions"), false},
		{"clean pi_args", mkSC("pi_args", "{{TEMP}}/sessions"), false},
		{"literal /tmp in pig_args", mkSC("pig_args", "/tmp/pig-parity-x-sessions"), true},
		{"literal /tmp in pi_args", mkSC("pi_args", "/tmp/pi-parity-x-sessions"), true},
		{"literal /tmp embedded in env", mkSC("pig_env", "/tmp/foo"), true},
		{"literal /tmp in pre_clear_paths", mkSC("pre_clear", "/tmp/anything"), true},
		{"literal /tmp in cwd", mkSC("cwd", "/tmp/cwd-fixture"), true},
		{"relative cwd is fine", mkSC("cwd", "testdata/foo"), false},
		{"embedded =/tmp/ form caught", mkSC("pig_args", "--log=/tmp/x.log"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := lintNoLiteralTmpPaths("test.toml", tt.sc)
			gotTmp := false
			for _, vv := range v {
				if vv.Rule == "literal-tmp-path" {
					gotTmp = true
				}
			}
			if gotTmp != tt.wantTmp {
				t.Errorf("want literal-tmp-path=%v, got=%v (violations=%+v)", tt.wantTmp, gotTmp, v)
			}
		})
	}
}

// TestLintPreClearPathsDeprecated exercises Rule 9 (pre_clear_paths is
// superseded by {{TEMP}} substitution; new scenarios must not use it).
func TestLintPreClearPathsDeprecated(t *testing.T) {
	sc := &scenario{Name: "test"}
	sc.Tmux.PreClearPaths = []string{"{{TEMP}}/x"} // even with token, the field itself is deprecated
	known := map[string]bool{}
	knownDiv := map[string]bool{}
	violations := lintScenario("t.toml", "# Upstream evidence\n", sc, known, knownDiv)
	found := false
	for _, v := range violations {
		if v.Rule == "uses-pre-clear-paths" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected uses-pre-clear-paths violation, got: %+v", violations)
	}
}

func TestDecodeScenarioWithUnknownKeyLint(t *testing.T) {
	source := `
name = "typo"
description = "typo"
covers = ["packages/tui/src/autocomplete.ts"]
tags = ["hermetic"]
driver = "interactive-tmux"

[assert]
output_normalized_equal = true

[[assert.normalize_replace]]
pattern = "Took .*"
replace = "Took Ns"
`
	var sc scenario
	var violations []violation
	if err := decodeScenarioWithUnknownKeyLint("typo.toml", source, &sc, &violations); err != nil {
		t.Fatalf("decodeScenarioWithUnknownKeyLint returned parse error: %v", err)
	}
	found := false
	for _, v := range violations {
		if v.Rule == "unknown-key" && v.Message == `unknown TOML key "assert.normalize_replace.replace"; typo'd assertion/config keys silently disable QC` {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unknown-key for normalize_replace.replace, got %+v", violations)
	}
}

func TestLintOAuthProviderCoverage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "providers.toml"), []byte(`
name = "providers"
description = "providers"
covers = ["packages/tui/src/autocomplete.ts"]
tags = ["hermetic"]
driver = "interactive-tmux"
[assert]
both_contain = ["Anthropic (Claude Pro/Max)", "OpenAI (ChatGPT Plus/Pro)", "GitHub Copilot"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lintOAuthProviderCoverage(dir); len(got) != 0 {
		t.Fatalf("lintOAuthProviderCoverage complete set got violations: %+v", got)
	}

	missingDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(missingDir, "providers.toml"), []byte(`name = "missing"`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := lintOAuthProviderCoverage(missingDir)
	if len(got) != 3 {
		t.Fatalf("lintOAuthProviderCoverage missing set got %d violations, want 3: %+v", len(got), got)
	}
}
