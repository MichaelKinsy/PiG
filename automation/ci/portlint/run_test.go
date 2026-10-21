// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaselineRoundTripAndRatchet(t *testing.T) {
	hits := []Hit{
		{Check: "regexfold", File: "a/a.go", Line: 3, Func: "f", Snippet: `re := regexp.MustCompile("(?i)x")`},
		{Check: "regexfold", File: "a/a.go", Line: 9, Func: "f", Snippet: `re := regexp.MustCompile("(?i)x")`},
		{Check: "clock", File: "b/b.go", Line: 4, Func: "T.g", Snippet: "time.Now()"},
	}
	path := filepath.Join(t.TempDir(), "baseline.toml")
	if err := os.WriteFile(path, []byte(renderBaseline(hits)), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := loadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(base) != 2 {
		t.Fatalf("baseline entries = %d, want 2 (identical hits share one counted entry)", len(base))
	}
	if unknown, stale := compare(hits, base); len(unknown) != 0 || len(stale) != 0 {
		t.Fatalf("unknown = %v, stale = %v; the baseline must cover exactly its own hits", unknown, stale)
	}
	// A third identical hit exceeds the count: the ratchet rejects it.
	more := append(append([]Hit{}, hits...), hits[0])
	if unknown, _ := compare(more, base); len(unknown) != 1 {
		t.Fatalf("unknown = %v, want the extra copy", unknown)
	}
	// A fixed hit leaves a stale entry that fails until it is deleted.
	if _, stale := compare(hits[:2], base); len(stale) != 1 || !strings.Contains(stale[0], "clock") {
		t.Fatalf("stale = %v, want the fixed clock entry", stale)
	}
	// Moving a line does not churn the baseline: the key ignores line numbers.
	moved := []Hit{{Check: "clock", File: "b/b.go", Line: 400, Func: "T.g", Snippet: "time.Now()"}, hits[0], hits[1]}
	if unknown, stale := compare(moved, base); len(unknown) != 0 || len(stale) != 0 {
		t.Fatalf("a moved line churned the baseline: %v %v", unknown, stale)
	}
}

func TestBaselineRejectsUnknownCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.toml")
	if err := os.WriteFile(path, []byte("[[hit]]\ncheck = \"nosuch\"\nfile = \"a.go\"\nfunc = \"f\"\nsnippet = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBaseline(path); err == nil || !strings.Contains(err.Error(), "unknown check") {
		t.Fatalf("err = %v, want unknown check", err)
	}
}

// The driver loads a module, runs every analyzer and honors the allow marker end to end.
func TestScanFindsAndAllows(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.26\n")
	write("m.go", `package m

import "regexp"

var bad = regexp.MustCompile("(?i)x")

//portlint:allow regexfold ASCII-only pattern
var ok = regexp.MustCompile("(?i)y")
`)
	hits, err := scan(dir, "", []string{"./..."}, allAnalyzers())
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Check != "regexfold" || hits[0].Line != 5 || hits[0].Func != "(decl)" {
		t.Fatalf("hits = %+v, want one regexfold hit on line 5", hits)
	}
}

// The Go SDK is a separate module that ./... does not load: the default run reads it with the ordering checks only, so no other check's baseline grows.
func TestDefaultSpecsReadTheSDKWithTheOrderingChecks(t *testing.T) {
	specs := defaultSpecs()
	if len(specs) != 2 || specs[1].patterns[0] != sdkPattern {
		t.Fatalf("specs = %+v, want the root module and %s", specs, sdkPattern)
	}
	got := map[string]bool{}
	for _, a := range specs[1].analyzers {
		got[a.Name] = true
	}
	want := map[string]bool{"boundarygoroutine": true, "unorderedfanin": true, "rpcwriteroutsidesink": true}
	if len(got) != len(want) || !got["boundarygoroutine"] || !got["unorderedfanin"] || !got["rpcwriteroutsidesink"] {
		t.Fatalf("SDK analyzers = %v, want %v", got, want)
	}
	if len(specs[0].analyzers) != len(allAnalyzers()) {
		t.Fatalf("root analyzers = %d, want every check", len(specs[0].analyzers))
	}
}
