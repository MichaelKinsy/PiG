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
	for _, tc := range []struct{ name, yaml, want string }{
		{"no checks", "checks: []", "no checks"},
		{"no name", "checks: [{always: true, sources: [pull], version: 1}]", "has no name"},
		{"duplicate", "checks: [{name: a, always: true, sources: [pull], version: 1}, {name: a, always: true, sources: [pull], version: 1}]", "declared twice"},
		{"no version", "checks: [{name: a, always: true, sources: [pull]}]", "version of 1 or more"},
		{"neither always nor paths", "checks: [{name: a, sources: [pull], version: 1}]", "either always"},
		{"both always and paths", "checks: [{name: a, always: true, paths: [x], sources: [pull], version: 1}]", "either always"},
		{"no source", "checks: [{name: a, always: true, version: 1}]", "reads no source"},
		{"unknown source", "checks: [{name: a, always: true, sources: [nope], version: 1}]", `unknown source "nope"`},
		{"absolute path", "checks: [{name: a, paths: [/x], sources: [pull], version: 1}]", "must be relative"},
		{"checkout escapes", "checkout: x", "field checkout not found"},
		{"checkout absolute", "checks: [{name: a, always: true, sources: [pull], version: 1, checkout: [/etc]}]", "must be relative"},
		{"checkout parent", "checks: [{name: a, always: true, sources: [pull], version: 1, checkout: [../x]}]", "must be relative"},
		{"unknown field", "checks: [{name: a, always: true, sources: [pull], version: 1, extra: 1}]", "field extra not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadConfig([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
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
	cfg, err := loadConfig([]byte("checks: [{name: go, paths: ['**/*.go'], sources: [pull], version: 1}, {name: all, always: true, sources: [pull], version: 1}]"))
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
