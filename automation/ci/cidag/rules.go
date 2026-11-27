// SPDX-License-Identifier: MIT

package cidag

import (
	"path"
	"strings"
)

// RuleKind specifies how a matched path rule affects the CI plan.
type RuleKind int

const (
	// RuleNone means no rule matched; the path must be classified by the Go graph
	// (or else fails closed as unowned).
	RuleNone RuleKind = iota
	// RuleFull forces the full CI plan (CI definitions, build pins, root module files).
	RuleFull
	// RuleDocs marks project prose and repository metadata that no product suite reads.
	RuleDocs
	// RuleLanes maps the matched path to an explicit list of lanes.
	RuleLanes
)

// PathRule assigns a set of glob patterns to a RuleKind and (for RuleLanes)
// the lanes that must run. Patterns use slash-separated paths where "*" crosses
// "/" (matching fnmatch semantics from CI-DAG-DESIGN.md and classify-changes.sh).
type PathRule struct {
	Name     string
	Patterns []string
	Kind     RuleKind
	Lanes    []string
}

var (
	gateRuleLanes = []string{"preflight", "build", "checks", "contracts", "test-fast"}
	protocolLanes = []string{
		"preflight", "build", "checks", "contracts",
		"test-subprocess", "test-conformance", "toolchains",
		"win-ext-host", "win-ext-conformance", "mac-ext-host", "node-runtime",
	}
	runtimeNodeLanes = []string{
		"preflight", "build", "checks", "contracts",
		"test-subprocess", "node-runtime", "node-loader",
		"win-ext-host", "mac-ext-host",
	}
)

// DefaultRules is the first-match-wins ownership table (CI-DAG-DESIGN.md §2.2, §4.1).
// Every file matching no rule falls through to Go-graph ownership; if unowned
// there too, selection fails closed to full=true.
var DefaultRules = []PathRule{
	{
		Name: "ci-and-pins-full",
		Kind: RuleFull,
		Patterns: []string{
			"Makefile",
			"automation/make/*",
			".github/workflows/*",
			".github/actions/*",
			".github/required-checks.txt",
			".go-version",
			".node-version",
			"go.mod",
			"go.sum",
			"go.work",
			"go.work.sum",
			".upstream/pin",
			"automation/pin/*",
			"automation/ci/ci-plan.sh",
			"automation/ci/ci-tiers.txt",
			"automation/ci/classify-changes.sh",
			"automation/ci/os-specific-paths.txt",
			"automation/ci/cidag/*",
			"automation/ci/test-grouped.sh",
			"automation/ci/test-fixtures.sh",
			"automation/ci/test-shard-*",
			"automation/ci/windows-*",
			"automation/ci/macos-*",
			"automation/ci/install-*",
			"automation/ci/with-isolated-pig-home.sh",
			"automation/ci/gocache-trim.sh",
			"test/parity/runner/*",
			"embed.go",
			"flake.nix",
			"flake.lock",
			"nix/*",
		},
	},
	{
		// Exact docs-only set aligned with automation/ci/classify-changes.sh.
		Name: "docs-only",
		Kind: RuleDocs,
		Patterns: []string{
			"README.md",
			"CHANGELOG.md",
			"CHANGELOG.md.gz",
			"CHANGELOG.md.zst",
			".github/CONTRIBUTING.md",
			".github/CODE_OF_CONDUCT.md",
			"docs/project/GOVERNANCE.md",
			"docs/project/MAINTAINERS.md",
			".github/SECURITY.md",
			".github/SUPPORT.md",
			"CITATION.cff",
			"docs/media/*",
			".github/ISSUE_TEMPLATE/*",
			".github/pull_request_template.md",
			".github/CODEOWNERS",
			".github/dependabot.yml",
			".github/security-insights.yml",
			".coderabbit.yaml",
			"LICENSE",
			"LICENSES/*",
			"NOTICE",
			"THIRD_PARTY_NOTICES.md",
			"REUSE.toml",
			"ISSUE-147-PR.md",
		},
	},
	{
		Name:     "changelog-fragments",
		Kind:     RuleLanes,
		Patterns: []string{"changelog.d/*"},
		Lanes:    []string{"checks"},
	},
	{
		Name: "protocol-and-sdk-conformance",
		Kind: RuleLanes,
		Patterns: []string{
			"coding/extension/host/subprocess/protocol*.go",
			"coding/extension/host/subprocess/*sdk*",
			"test/extension-conformance/sdkscenarios/*",
			"test/parity/sdk-vectors/*",
			"test/parity/sdk-bulk/*",
		},
		Lanes: protocolLanes,
	},
	{
		Name: "parity-ledgers-and-docs",
		Kind: RuleLanes,
		Patterns: []string{
			"docs/parity/*",
			"test/parity/interfaces/*",
			"test/parity/interface-closure/*",
			"test/parity/interface-extractor/*",
			"test/parity/generated/*",
			"test/parity/closuredata/*",
			"test/parity/corpus/*",
			"test/parity/dependency-sources/*",
			"test/parity/ledgers/*",
			"test/parity/pin-ledgers/*",
			"test/parity/probes/*",
			"test/parity/unit-evidence/*",
			"test/parity/upstream-sync/*",
			"test/parity/coverage.md",
			"test/parity/*.toml",
			"test/parity/*.json",
			"test/parity/*.txt",
			"test/parity/*.md",
			"docs/bug-ledger*",
			"docs/site/*",
		},
		Lanes: []string{"checks", "contracts", "portmap"},
	},
	{
		Name:     "docs-other",
		Kind:     RuleLanes,
		Patterns: []string{"docs/*", ".github/AGENTS.md", ".github/badges/*", "AGENTS.md"},
		Lanes:    []string{"checks"},
	},
	{
		Name: "node-runtime-embedded",
		Kind: RuleLanes,
		Patterns: []string{
			"coding/extension/host/subprocess/runtime-node/*",
			"coding/extension/host/subprocess/runtime-node.pack",
			"coding/extension/host/subprocess/runtime_node_digest_generated.go",
		},
		Lanes: runtimeNodeLanes,
	},
	{
		Name: "sdk-rs-py-lisp",
		Kind: RuleLanes,
		Patterns: []string{
			"extensions/sdk-rs/*",
			"extensions/sdk-py/*",
			"extensions/sdk-lisp/*",
		},
		Lanes: []string{"toolchains", "test-conformance", "win-ext-conformance", "contracts", "checks"},
	},
	{
		Name:     "sdk-ts",
		Kind:     RuleLanes,
		Patterns: []string{"extensions/sdk-ts/*"},
		Lanes:    []string{"toolchains", "node-runtime", "test-conformance", "win-ext-conformance", "contracts", "checks"},
	},
	{
		Name:     "sdk-go-and-bundles",
		Kind:     RuleLanes,
		Patterns: []string{"extensions/sdk/*", "extensions/RUBRIC.md"},
		Lanes:    []string{"toolchains", "test-fast", "test-conformance", "win-ext-conformance", "contracts", "checks"},
	},
	{
		Name:     "durable-contract-and-cf-host",
		Kind:     RuleLanes,
		Patterns: []string{"durable/contract/*", "durable/core/host/*", "durable/core/budget/*", "durable/interop/*", "durable/images/*"},
		Lanes:    []string{"durable-cf-host", "durable-contract", "test-fast", "checks"},
	},
	{
		Name:     "chord-node-surfaces",
		Kind:     RuleLanes,
		Patterns: []string{"chord/nextnode/*"},
		Lanes:    []string{"chord", "test-fast", "checks"},
	},
	{
		Name:     "examples",
		Kind:     RuleLanes,
		Patterns: []string{"examples/*"},
		Lanes:    []string{"toolchains", "checks"},
	},
	{
		// Parity scenarios and fixtures are also narrowed per-scenario in Select().
		Name:     "parity-scenarios-and-fixtures",
		Kind:     RuleLanes,
		Patterns: []string{"test/parity/scenarios/*", "test/parity/testdata/*"},
		Lanes:    []string{"checks", "contracts", "portmap"},
	},
	{
		Name:     "integration-and-journeys",
		Kind:     RuleLanes,
		Patterns: []string{"test/journeys/*", "test/integration/*", "test/term-engines/*"},
		Lanes:    []string{"toolchains", "test-fast"},
	},
	{
		Name: "ci-tools-and-generators",
		Kind: RuleLanes,
		Patterns: []string{
			"automation/ci/allocbase/*",
		},
		Lanes: []string{"alloc-ratchet", "checks", "contracts", "build", "preflight", "test-fast"},
	},
	{
		Name: "ci-tools-and-generators-other",
		Kind: RuleLanes,
		Patterns: []string{
			"automation/ci/*",
			".golangci.yml",
			".gitleaks.toml",
			"automation/gen/*",
			"variables/*",
		},
		Lanes: gateRuleLanes,
	},
	{
		Name: "automation-misc",
		Kind: RuleLanes,
		Patterns: []string{
			"automation/perf/*",
			"automation/release/*",
			"automation/porter/*",
			"automation/portlens/*",
			"automation/dev/*",
			"automation/layout/*",
			"automation/images/*",
			"automation/README.md",
		},
		Lanes: []string{"checks"},
	},
	{
		Name:     "piglets",
		Kind:     RuleLanes,
		Patterns: []string{"piglets/*"},
		Lanes:    []string{"test-fast", "contracts", "checks", "preflight"},
	},
	{
		Name:     "testdata-provenance-and-evals",
		Kind:     RuleLanes,
		Patterns: []string{"test/testdata-provenance.toml", "test/evals/*"},
		Lanes:    []string{"checks", "contracts", "test-fast"},
	},
	{
		Name:     "third-party-assets",
		Kind:     RuleLanes,
		Patterns: []string{"third_party/*"},
		Lanes:    []string{"test-fast", "checks", "contracts"},
	},
	{
		Name:     "non-product-allowlist",
		Kind:     RuleLanes,
		Patterns: []string{"research/*", "plans/*", "handoffs/*", "bench/*", ".agents/*", ".devcontainer/*", "schemas/*"},
		Lanes:    []string{"checks"},
	},
}

// MatchRule returns the first matching PathRule for relPath (POSIX slash form).
func MatchRule(relPath string, rules []PathRule) (PathRule, bool) {
	clean := normalizePath(relPath)
	for _, r := range rules {
		for _, pat := range r.Patterns {
			if matchGlob(pat, clean) {
				return r, true
			}
		}
	}
	return PathRule{}, false
}

// normalizePath cleans a repository-relative path into canonical POSIX form.
func normalizePath(p string) string {
	p = strings.TrimPrefix(p, "./")
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	return p
}

// matchGlob matches pattern against name with fnmatch/shell-case semantics
// where "*" matches any sequence of characters (including "/"), "?" matches any
// single character, and literal characters match exactly.
func matchGlob(pattern, name string) bool {
	return globMatchBytes(pattern, name)
}

func globMatchBytes(p, s string) bool {
	pi, si := 0, 0
	starIdx, matchIdx := -1, 0
	for si < len(s) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == s[si]):
			pi++
			si++
		case pi < len(p) && p[pi] == '*':
			starIdx = pi
			matchIdx = si
			pi++
		case starIdx != -1:
			pi = starIdx + 1
			matchIdx++
			si = matchIdx
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
