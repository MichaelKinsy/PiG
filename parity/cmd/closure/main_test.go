package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/parity/closure"
	"github.com/MichaelKinsy/PiG/parity/internal/gitsnapshot"
	"github.com/MichaelKinsy/PiG/parity/knowngaps"
)

func repositorySnapshotCommit(t *testing.T, root string) string {
	t.Helper()
	commit, err := gitsnapshot.Create(t.Context(), root, filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	return commit
}

func TestClosureCommandLifecycle(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "records.jsonl")
	input, err := os.Create(inputPath)
	if err != nil {
		t.Fatalf("create input: %v", err)
	}
	hash := closure.HashBytes([]byte("test"))
	records := []closure.Record{
		&closure.Snapshot{Kind: closure.KindSnapshot, ID: "snapshot:test", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&closure.Pin{Kind: closure.KindPin, ID: "pin:source", SnapshotID: "snapshot:test", Repository: "upstream", Commit: strings.Repeat("a", 40), Path: "source.ts", SemanticID: "source.run", StartLine: 1, EndLine: 1, QuoteHash: hash},
		&closure.Facet{Kind: closure.KindFacet, ID: "facet:result", Name: "result"},
		&closure.Rule{Kind: closure.KindRule, ID: "rule:result", Name: "result", DefinitionHash: hash},
		&closure.Behavior{Kind: closure.KindBehavior, ID: "behavior:run", Name: "run", OriginPinIDs: []string{"pin:source"}, Profile: "application"},
		&closure.Obligation{Kind: closure.KindObligation, ID: "obligation:result", BehaviorID: "behavior:run", FacetID: "facet:result", RuleID: "rule:result", OriginPinIDs: []string{"pin:source"}},
	}
	encoder := json.NewEncoder(input)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			t.Fatalf("encode input: %v", err)
		}
	}
	if err := input.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}

	ctx := context.Background()
	var stdout, stderr bytes.Buffer
	if err := run(ctx, []string{"rebuild", "-root", root, "-input", "records.jsonl"}, &stdout, &stderr); err != nil {
		t.Fatalf("rebuild: %v stderr=%s", err, &stderr)
	}
	if !strings.Contains(stdout.String(), "obligation:result\topen\tno accepted mapping") {
		t.Fatalf("rebuild output:\n%s", &stdout)
	}

	for _, command := range [][]string{
		{"status", "-root", root},
		{"why-open", "-root", root},
		{"explain", "-root", root, "obligation:result"},
		{"verify", "-root", root},
	} {
		stdout.Reset()
		stderr.Reset()
		if err := run(ctx, command, &stdout, &stderr); err != nil {
			t.Fatalf("%s: %v stderr=%s", command[0], err, &stderr)
		}
		if stdout.Len() == 0 {
			t.Fatalf("%s produced no output", command[0])
		}
	}
}

func TestClosureReportCommand(t *testing.T) {
	root := t.TempDir()
	hash := closure.HashBytes([]byte("report-command"))
	value, err := json.Marshal(map[string]any{"fields": []string{"`packages/example.ts`", "`example.go`", "✅"}})
	if err != nil {
		t.Fatal(err)
	}
	orderValue, err := json.Marshal(map[string]any{"subjects": []string{"packages/example.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	recordsPath := filepath.Join(root, "records.jsonl")
	file, err := os.Create(recordsPath)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, record := range []closure.Record{
		&closure.Snapshot{Kind: closure.KindSnapshot, ID: "snapshot:report", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash},
		&closure.Pin{Kind: closure.KindPin, ID: "pin:report", SnapshotID: "snapshot:report", Repository: "pig", Commit: strings.Repeat("b", 40), Path: "PORT_MAP.md", SemanticID: "denominator:port-map", StartLine: 1, EndLine: 2, QuoteHash: hash},
		&closure.Fact{Kind: closure.KindFact, ID: "fact:denominator-order:port-map:rows", SnapshotID: "snapshot:report", FactType: "denominator-order:port-map", SubjectID: "rows", Resolution: "resolved", PinIDs: []string{"pin:report"}, Value: orderValue},
		&closure.Fact{Kind: closure.KindFact, ID: "fact:denominator:port-map:example", SnapshotID: "snapshot:report", FactType: "denominator:port-map", SubjectID: "packages/example.ts", Resolution: "observed", PinIDs: []string{"pin:report"}, Value: value},
		&closure.ProvisionalClaim{Kind: closure.KindProvisionalClaim, ID: "provisional:denominator:port-map:example", SnapshotID: "snapshot:report", SubjectID: "packages/example.ts", SourcePinID: "pin:report", Status: "✅"},
	} {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run(t.Context(), []string{"rebuild", "-root", root, "-input", "records.jsonl"}, &stdout, &stderr); err != nil {
		t.Fatalf("rebuild: %v stderr=%s", err, &stderr)
	}
	stdout.Reset()
	stderr.Reset()
	if err := run(t.Context(), []string{"report", "-root", root}, &stdout, &stderr); err != nil {
		t.Fatalf("report: %v stderr=%s", err, &stderr)
	}
	if !strings.Contains(stdout.String(), "| `packages/example.ts` | `example.go` | ✅ | 0 | 0 | 1 | 0 | 0 | imported status ✅ |") {
		t.Fatalf("report:\n%s", stdout.String())
	}
}

func TestClosureCoverageAuditCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\nfunc run() { println(1) }\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cover.out"), []byte("mode: set\nsample.go:2.14,2.26 1 0\n"), 0o600); err != nil {
		t.Fatalf("write coverage: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"coverage-audit", "-root", root, "-profile", "cover.out", "-path", "sample.go"}, &stdout, &stderr); err != nil {
		t.Fatalf("coverage-audit: %v stderr=%s", err, &stderr)
	}
	if !strings.Contains(stdout.String(), "sample.go\t2.14-2.26\t1\t0") {
		t.Fatalf("coverage output:\n%s", &stdout)
	}
}

func TestImportDenominatorUsesCompiledCorrespondenceDenominator(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	targetCommit := repositorySnapshotCommit(t, root)
	storeDir, err := os.MkdirTemp(root, ".closure-command-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(storeDir) })
	storePath := filepath.Join(storeDir, "graph.db")
	storeRelative, err := filepath.Rel(root, storePath)
	if err != nil {
		t.Fatal(err)
	}
	hash := closure.HashBytes([]byte("command-correspondence"))
	args := []string{
		"import-denominators", "-root", root, "-db", storeRelative,
		"-upstream-commit", coding.UpstreamCommit,
		"-target-commit", targetCommit,
		"-toolchain-hash", hash, "-environment-hash", hash,
	}
	var stdout, stderr bytes.Buffer
	err = run(t.Context(), args, &stdout, &stderr)
	blocked, problem := knowngaps.Blocked(root, "correspondence", err, func(findings int) string {
		return fmt.Sprintf("correspondence denominator has %d findings", findings)
	})
	if problem != nil {
		t.Fatalf("import-denominators: %v stderr=%s", problem, &stderr)
	}
	if blocked {
		t.Log("listed correspondence gaps block import-denominators")
		return
	}
	status, err := closure.ReadStatus(t.Context(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(status), "obligation:correspondence:setting:autocompact:current-state") || strings.Contains(string(status), "obligation:foundation-state:") {
		t.Fatalf("canonical status does not use compiled denominator:\n%s", status[:min(len(status), 4000)])
	}
}

func TestCommittedBundlePathsIgnoreUntrackedAndRejectDirty(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "parity", "closuredata", "proposals", "test")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(directory, "tracked.json")
	if err := os.WriteFile(tracked, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "untracked.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", "parity/closuredata/proposals/test/tracked.json"},
		{"-c", "user.name=Pig Test", "-c", "user.email=pig@example.invalid", "commit", "--quiet", "-m", "fixture"},
	} {
		command := exec.CommandContext(t.Context(), "git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	paths, err := committedBundlePaths(t.Context(), root, "snapshot:test")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"parity/closuredata/proposals/test/tracked.json"}; !slices.Equal(paths, want) {
		t.Fatalf("committed paths = %v, want %v", paths, want)
	}
	if err := os.WriteFile(tracked, []byte("{\"dirty\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := committedBundlePaths(t.Context(), root, "snapshot:test"); err == nil || !strings.Contains(err.Error(), "differs from HEAD") {
		t.Fatalf("dirty committed bundle error = %v", err)
	}
	command := exec.CommandContext(t.Context(), "git", "checkout", "--", "parity/closuredata/proposals/test/tracked.json")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restore tracked bundle: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(directory, "payload.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, "payload.json", filepath.Join(directory, "linked.json"))
	for _, args := range [][]string{
		{"add", "parity/closuredata/proposals/test/linked.json"},
		{"-c", "user.name=Pig Test", "-c", "user.email=pig@example.invalid", "commit", "--quiet", "-m", "symlink"},
	} {
		command := exec.CommandContext(t.Context(), "git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if _, err := committedBundlePaths(t.Context(), root, "snapshot:test"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlinked committed bundle error = %v", err)
	}
}

func TestClosureCommandRejectsMissingInputsAndEscapingPaths(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"rebuild"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "at least one -input") {
		t.Fatalf("rebuild error = %v, want missing input", err)
	}
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.jsonl")
	if err := run(context.Background(), []string{"rebuild", "-root", root, "-input", outside}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "escapes repository root") {
		t.Fatalf("escaping input error = %v", err)
	}
	if runtime.GOOS != "windows" {
		symlink := filepath.Join(root, "linked-outside")
		if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("create outside input: %v", err)
		}
		defer func() { _ = os.Remove(outside) }()
		testenv.Symlink(t, filepath.Dir(root), symlink)
		linkedOutside := filepath.Join(symlink, filepath.Base(outside))
		if err := run(context.Background(), []string{"rebuild", "-root", root, "-input", linkedOutside}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "symlink escapes repository root") {
			t.Fatalf("symlinked input error = %v", err)
		}
	}
	if err := run(context.Background(), []string{"import-denominators"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "requires -upstream-commit") {
		t.Fatalf("import-denominators error = %v, want required snapshot inputs", err)
	}
	if err := run(context.Background(), []string{"coverage-audit"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "requires -profile") {
		t.Fatalf("coverage-audit error = %v, want required piglet", err)
	}
	if err := run(context.Background(), []string{"unknown"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unknown command error = %v", err)
	}
}
