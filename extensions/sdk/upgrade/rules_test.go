// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

const header = `package ext

import (
	"errors"
	"fmt"
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

var _ = errors.New
var _ = fmt.Sprint
var _ = os.Getenv

`

// planSource stages one extension file in a module and plans its upgrade.
func planSource(t *testing.T, body string) (*upgrade.Result, string) {
	t.Helper()
	dir := t.TempDir()
	goMod := "module example.com/ext\n\ngo 1.26\n\nrequire " + upgrade.SDKModulePath + " v0.0.0\n\nreplace " + upgrade.SDKModulePath + " => " + filepath.ToSlash(sdkRoot(t)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(header+body), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := openFixture(t, dir).Plan()
	if err != nil {
		t.Fatal(err)
	}
	return result, dir
}

func rewritten(t *testing.T, result *upgrade.Result) string {
	t.Helper()
	if len(result.Files) != 1 {
		t.Fatalf("files = %d, want 1 (skipped %+v, remaining %+v, errors %v)", len(result.Files), result.Skipped, result.Remaining, result.Errors)
	}
	return strings.TrimPrefix(string(result.Files[0].After), header)
}

func TestGetterHoistKeepsOrderAndReturnsTheError(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context, args string) error {
	fmt.Println("id", ctx.GetSessionID(), ctx.GetThinkingLevel())
	return nil
}
`)
	want := `func handler(ctx sdk.Context, args string) error {
	sessionID, err := ctx.GetSessionID()
	if err != nil {
		return err
	}
	thinkingLevel, err := ctx.GetThinkingLevel()
	if err != nil {
		return err
	}
	fmt.Println("id", sessionID, thinkingLevel)
	return nil
}
`
	if got := rewritten(t, result); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Context.ConfigHome returns an error since 0.5.0 (it fails when the home directory is needed and unavailable): the same rule rewrites it.
func TestConfigHomeGetterHoistsTheError(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context, args string) error {
	fmt.Println("home", ctx.ConfigHome())
	return nil
}
`)
	want := `func handler(ctx sdk.Context, args string) error {
	configHome, err := ctx.ConfigHome()
	if err != nil {
		return err
	}
	fmt.Println("home", configHome)
	return nil
}
`
	if got := rewritten(t, result); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A handler that returns values before its error returns their zero values.
func TestGetterReturnsZeroValuesOfTheHandlersResults(t *testing.T) {
	result, _ := planSource(t, `type result struct{ ID string }

func build(ctx sdk.Context) (result, []string, map[string]int, int, *os.File, error) {
	id := ctx.GetSessionID()
	return result{ID: id}, nil, nil, 0, nil, nil
}
`)
	if got := rewritten(t, result); !strings.Contains(got, "return result{}, nil, nil, 0, nil, err") {
		t.Fatalf("got:\n%s", got)
	}
}

// An existing err of another type, or one declared later in the scope, is not
// reused.
func TestGetterDoesNotCaptureAnotherErr(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context) error {
	id := ctx.GetSessionID()
	err := os.Getenv("X")
	_, _ = id, err
	return nil
}
`)
	got := rewritten(t, result)
	if !strings.Contains(got, "id, getErr := ctx.GetSessionID()") || !strings.Contains(got, "return getErr") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestGetterAssignmentToExistingVariable(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context) error {
	var id string
	id = ctx.GetSessionID()
	_ = id
	return nil
}
`)
	got := rewritten(t, result)
	if !strings.Contains(got, "sessionID, err := ctx.GetSessionID()") || !strings.Contains(got, "id = sessionID") {
		t.Fatalf("got:\n%s", got)
	}
}

// A call that another call's order depends on, or that runs conditionally, is
// left for the author with the reason.
func TestGetterSkipsWhereMovingTheCallChangesBehavior(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context, ready bool) error {
	if ready && ctx.IsIdle() {
		return nil
	}
	if !ready {
		return nil
	} else if ctx.IsProjectTrusted() {
		return nil
	}
	for ctx.HasPendingMessages() {
	}
	fmt.Println(os.Getenv("A"), ctx.GetSessionID())
	return nil
}
`)
	if result.Changed() {
		t.Fatalf("the plan rewrites source it cannot keep in order: %+v", result.Files)
	}
	var reasons []string
	for _, skipped := range result.Skipped {
		reasons = append(reasons, skipped.Symbol+": "+skipped.Reason)
	}
	for _, want := range []string{
		"Context.IsIdle: the call runs only when an earlier operand allows it",
		"Context.IsProjectTrusted: the call is not in the if condition",
		"Context.HasPendingMessages: the statement is not one pig moves a call out of",
		"Context.GetSessionID: an earlier call in the statement would run after the getter",
	} {
		if !slices.Contains(reasons, want) {
			t.Errorf("missing skip %q in %q", want, reasons)
		}
	}
}

// A method with a getter's name on another type is not the SDK's.
func TestOtherTypesWithGetterNamesAreNotRewritten(t *testing.T) {
	result, _ := planSource(t, `type store struct{}

func (store) GetSessionID() string { return "" }

func handler(ctx sdk.Context) error {
	s := store{}
	id := s.GetSessionID()
	_ = id
	var level string = ctx.GetThinkingLevel()
	_ = level
	return nil
}
`)
	got := rewritten(t, result)
	if !strings.Contains(got, "id := s.GetSessionID()") || !strings.Contains(got, "thinkingLevel, err := ctx.GetThinkingLevel()") {
		t.Fatalf("got:\n%s", got)
	}
}

// A method of another type with a getter's name and the new two-result shape,
// used where one value is wanted, is the author's own error: the plan does not
// move it.
func TestOtherTypesWithTwoResultGetterNamesAreNotRewritten(t *testing.T) {
	result, _ := planSource(t, `type store struct{}

func (store) GetLeafID() (string, error) { return "", nil }

func handler(ctx sdk.Context) error {
	leaf := store{}.GetLeafID()
	_ = leaf
	return nil
}
`)
	if result.Changed() {
		t.Fatalf("the plan rewrites another type's method: %s", result.Files[0].After)
	}
}

// Code that already treats usage as nullable keeps its nil checks and derefs.
func TestContextUsageKeepsNullableUses(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context) error {
	id := ctx.GetSessionID()
	usage, err := ctx.GetContextUsage()
	if err != nil || usage == nil {
		return err
	}
	if usage.Tokens != nil && *usage.Tokens > 0 {
		fmt.Println(id, *usage.Tokens)
	}
	fmt.Println(usage.Percent)
	return nil
}
`)
	got := rewritten(t, result)
	if !strings.Contains(got, "usage.Tokens != nil && *usage.Tokens > 0") || !strings.Contains(got, "fmt.Println(usage.PercentOr(0))") {
		t.Fatalf("got:\n%s", got)
	}
}

// A change with no mechanical rewrite stays in the compiler's errors, and the
// plan names it with the SDK's old and new shape.
func TestManualChangesAreNamedNotRewritten(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context) error {
	return ctx.SetEditorComponent(42)
}
`)
	if result.Changed() || len(result.Remaining) != 1 {
		t.Fatalf("result = %+v", result)
	}
	drift := result.Remaining[0]
	if drift.Rule != upgrade.RuleEditorComponent || drift.Symbol != "Context.SetEditorComponent" || drift.Rewrites || drift.Old != "func(any) error" || drift.New != "func(EditorFactory) error" {
		t.Fatalf("drift = %+v", drift)
	}
}

// An unrelated compile error is not SDK drift.
func TestUnrelatedErrorsAreNotDrift(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context) error {
	var n int = "text"
	return nil
}
`)
	if len(result.Remaining) != 0 || len(result.Errors) == 0 {
		t.Fatalf("remaining = %+v, errors = %v", result.Remaining, result.Errors)
	}
}

// Classify reads the compiler's own output, as the build path does, and names
// each change with its old and new shape.
func TestClassifyNamesChangesFromBuildOutput(t *testing.T) {
	dir := stageFixture(t, "sdk-0.2.0")
	output, err := goBuild(t, dir)
	if err == nil {
		t.Fatal("the old extension builds")
	}
	drifts := upgrade.Classify(upgrade.ParseDiagnostics([]byte(output)), inDir(dir))
	var symbols []string
	for _, drift := range drifts {
		symbols = append(symbols, drift.Symbol)
		if drift.Old == "" || drift.New == "" || drift.Since == "" || !drift.Rewrites {
			t.Errorf("drift lacks its shapes: %+v", drift)
		}
	}
	for _, want := range []string{"Context.GetSessionID", "Context.IsIdle", "Context.GetThinkingLevel", "Context.GetSystemPrompt", "Context.GetSessionFile"} {
		if !slices.Contains(symbols, want) {
			t.Errorf("missing %s in %v", want, symbols)
		}
	}
	for _, drift := range drifts {
		if drift.Symbol == "Context.GetSessionID" && !strings.Contains(drift.String(), "func() string -> func() (string, error)") {
			t.Errorf("drift = %s", drift)
		}
	}
}

func TestClassifyNamesTheBoolFieldFromTheSource(t *testing.T) {
	dir := stageFixture(t, "sdk-dev")
	output, _ := goBuild(t, dir)
	var found bool
	for _, drift := range upgrade.Classify(upgrade.ParseDiagnostics([]byte(output)), inDir(dir)) {
		if drift.Symbol == "SendMessageOptions.TriggerTurn" && drift.Old == "bool" && drift.New == "*bool" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the bool field is not classified:\n%s", output)
	}
	// Without the source the diagnostic does not name the SDK symbol.
	for _, drift := range upgrade.Classify(upgrade.ParseDiagnostics([]byte(output)), nil) {
		if drift.Symbol == "SendMessageOptions.TriggerTurn" {
			t.Fatalf("classified without source: %+v", drift)
		}
	}
}

func TestClassifyLeavesUnrelatedErrors(t *testing.T) {
	output := "# example.com/x\n./x.go:6:43: undefined: foo\n./x.go:9:2: declared and not used: n\n"
	if drifts := upgrade.Classify(upgrade.ParseDiagnostics([]byte(output)), nil); len(drifts) != 0 {
		t.Fatalf("drifts = %+v", drifts)
	}
}

func TestEveryRuleSymbolIsUnique(t *testing.T) {
	seen := map[string]string{}
	for _, rule := range upgrade.Rules() {
		if rule.Remedy == "" || rule.Summary == "" || len(rule.Changes) == 0 {
			t.Errorf("rule %s is incomplete", rule.ID)
		}
		for _, change := range rule.Changes {
			if other, dup := seen[change.Symbol]; dup {
				t.Errorf("%s is covered by %s and %s", change.Symbol, other, rule.ID)
			}
			seen[change.Symbol] = rule.ID
			if change.Old == "" || change.New == "" || change.Since == "" {
				t.Errorf("%s lacks a shape or a release", change.Symbol)
			}
		}
	}
}

// inDir reads the files a build in dir names relative to dir.
func inDir(dir string) upgrade.FileSource {
	return func(path string) ([]byte, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		return os.ReadFile(path)
	}
}
