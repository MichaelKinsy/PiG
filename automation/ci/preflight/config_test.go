// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

func TestShippedConfigLoads(t *testing.T) {
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Checks) == 0 {
		t.Fatal("no checks")
	}
}

func TestConfigRejects(t *testing.T) {
	const cheap = `{name: a, when: {always: true}, stage: cheap, reads: [pull], rule: up-to-date}`
	const scanOK = `{input: added, run: [x], exit: {clean: [0]}, report: {in: stdout, format: lines, pattern: '(?P<message>.+)'}}`
	for _, tc := range []struct{ name, yaml, want string }{
		{"no checks", "checks: []", "no checks"},
		{"no name", "checks: [{when: {always: true}, stage: cheap, reads: [pull], rule: up-to-date}]", "has no name"},
		{"duplicate", "checks: [" + cheap + ", " + cheap + "]", "declared twice"},
		{"neither always nor paths", "checks: [{name: a, stage: cheap, reads: [pull], rule: up-to-date}]", "either always"},
		{"both always and paths", "checks: [{name: a, when: {always: true, paths: [x]}, stage: cheap, reads: [pull], rule: up-to-date}]", "either always"},
		{"absolute path", "checks: [{name: a, when: {paths: [/x]}, stage: cheap, reads: [pull], rule: up-to-date}]", "must be relative"},
		{"unknown stage", "checks: [{name: a, when: {always: true}, stage: later}]", "neither cheap nor scan"},
		{"unknown rule", "checks: [{name: a, when: {always: true}, stage: cheap, reads: [pull], rule: nope}]", `unknown rule "nope"`},
		{"no source", "checks: [{name: a, when: {always: true}, stage: cheap, rule: up-to-date}]", "reads at least one source"},
		{"unknown source", "checks: [{name: a, when: {always: true}, stage: cheap, reads: [nope], rule: up-to-date}]", `unknown source "nope"`},
		{"bad rule params", "checks: [{name: a, when: {always: true}, stage: cheap, reads: [commits], rule: all-commits, with: {help: x}}]", "either verified"},
		{"cheap with a scan", "checks: [{name: a, when: {always: true}, stage: cheap, reads: [pull], rule: up-to-date, scan: " + scanOK + "}]", "has a rule, not a scan"},
		{"scan without scan", "checks: [{name: a, when: {always: true}, stage: scan}]", "needs scan:"},
		{"scan with a rule", "checks: [{name: a, when: {always: true}, stage: scan, rule: up-to-date, scan: " + scanOK + "}]", "has a scan, not a rule"},
		{"bad input", "checks: [{name: a, when: {always: true}, stage: scan, scan: {input: all, run: [x], exit: {clean: [0]}, report: {in: stdout, format: lines, pattern: '(?P<message>.+)'}}}]", "not added, changed or tree"},
		{"no run", "checks: [{name: a, when: {always: true}, stage: scan, scan: {input: added, exit: {clean: [0]}, report: {in: stdout, format: lines, pattern: '(?P<message>.+)'}}}]", "run is empty"},
		{"no clean code", "checks: [{name: a, when: {always: true}, stage: scan, scan: {input: added, run: [x], report: {in: stdout, format: lines, pattern: '(?P<message>.+)'}}}]", "clean codes"},
		{"code both", "checks: [{name: a, when: {always: true}, stage: scan, scan: {input: added, run: [x], exit: {clean: [0], findings: [0]}, report: {in: stdout, format: lines, pattern: '(?P<message>.+)'}}}]", "both clean and findings"},
		{"checkout escapes", "checks: [{name: a, when: {always: true}, stage: scan, scan: {input: added, run: [x], checkout: [../x], exit: {clean: [0]}, report: {in: stdout, format: lines, pattern: '(?P<message>.+)'}}}]", "must be relative"},
		{"pattern without message", "checks: [{name: a, when: {always: true}, stage: scan, scan: {input: added, run: [x], exit: {clean: [0]}, report: {in: stdout, format: lines, pattern: '(?P<file>.+)'}}}]", "(?P<message>"},
		{"json without message", "checks: [{name: a, when: {always: true}, stage: scan, scan: {input: added, run: [x], exit: {clean: [0]}, report: {in: stdout, format: json}}}]", "names the message field"},
		{"bad report place", "checks: [{name: a, when: {always: true}, stage: scan, scan: {input: added, run: [x], exit: {clean: [0]}, report: {in: file, format: json, message: m}}}]", "neither out nor stdout"},
		{"tool without pin", "tools: [{name: x, version: '1', url: 'https://x'}]\nchecks: [" + cheap + "]", "sha256"},
		{"tool both kinds", "tools: [{name: x, version: '1', pip: r.txt, url: 'https://x'}]\nchecks: [" + cheap + "]", "not both"},
		{"unknown field", "checks: [{name: a, when: {always: true}, stage: cheap, reads: [pull], rule: up-to-date, extra: 1}]", "field extra not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadConfig([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestDefinitionHashFollowsTheDeclaration(t *testing.T) {
	load := func(rule string) string {
		cfg, err := loadConfig([]byte("checks: [{name: a, when: {always: true}, stage: cheap, reads: [commits], rule: all-commits, with: {verified: true, help: " + rule + "}}]"))
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Checks[0].definition
	}
	if a, b := load("x"), load("y"); a == "" || a == b {
		t.Fatalf("definitions %q and %q: an edited check must hash differently", a, b)
	}
}

func TestGlobMatching(t *testing.T) {
	for _, tc := range []struct {
		glob string
		path string
		want bool
	}{
		{"**/*.go", "main.go", true},
		{"**/*.go", "a/b/c.go", true},
		{"**/*.go", "a/b/c.go.txt", false},
		{".github/**", ".github/workflows/ci.yml", true},
		{".github/**", "x/.github/a", false},
		{"**/testdata/**", "ai/testdata/x/golden.json", true},
		{"**/testdata/**", "testdata/x", true},
		{"go.mod", "go.mod", true},
		{"go.mod", "a/go.mod", false},
		{"**/go.mod", "a/go.mod", true},
		{"**/requirements*.txt", "extensions/sdk-py/requirements-dev.txt", true},
		{"a?c", "abc", true},
		{"a?c", "a/c", false},
		{"*.md", "docs/a.md", false},
		{"README.md", "READMExmd", false},
	} {
		re, err := globRegexp(tc.glob)
		if err != nil {
			t.Fatal(err)
		}
		if got := re.MatchString(tc.path); got != tc.want {
			t.Errorf("%q matches %q = %v, want %v", tc.glob, tc.path, got, tc.want)
		}
	}
}

func TestRelevant(t *testing.T) {
	cfg, err := loadConfig([]byte("checks: [{name: go, when: {paths: ['**/*.go']}, stage: cheap, reads: [pull], rule: up-to-date}, {name: all, when: {always: true}, stage: cheap, reads: [pull], rule: up-to-date}]"))
	if err != nil {
		t.Fatal(err)
	}
	docs := []string{"README.md", "docs/a.md"}
	if cfg.Checks[0].relevant(docs) {
		t.Error("a Go check is relevant to a docs-only change")
	}
	if !cfg.Checks[0].relevant(append(docs, "x/y.go")) {
		t.Error("a Go check is not relevant to a Go change")
	}
	if !cfg.Checks[1].relevant(nil) {
		t.Error("an always check is not relevant to an empty change")
	}
}
