// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"fmt"
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
	return planSourceRequiring(t, "v0.0.0", body)
}

// planSourceRequiring stages one extension file in a module that requires the
// SDK at version and plans its upgrade.
func planSourceRequiring(t *testing.T, version, body string) (*upgrade.Result, string) {
	t.Helper()
	dir := t.TempDir()
	goMod := "module example.com/ext\n\ngo 1.26\n\nrequire " + upgrade.SDKModulePath + " " + version + "\n\nreplace " + upgrade.SDKModulePath + " => " + filepath.ToSlash(sdkRoot(t)) + "\n"
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

// A getter in a switch statement's init runs before the tag, so it moves out
// before the switch; a getter in a && operand's right side stays for the author.
func TestGetterMovesOutOfASwitchInit(t *testing.T) {
	result, _ := planSource(t, `func clean(s string) string { return s }

func handler(ctx sdk.Context) error {
	switch home := clean(ctx.ConfigHome()); home {
	case "":
		return errors.New("no home")
	}
	if ctx.HasUI() && ctx.IsIdle() {
		return nil
	}
	return nil
}
`)
	want := `func clean(s string) string { return s }

func handler(ctx sdk.Context) error {
	configHome, err := ctx.ConfigHome()
	if err != nil {
		return err
	}
	switch home := clean(configHome); home {
	case "":
		return errors.New("no home")
	}
	if ctx.HasUI() && ctx.IsIdle() {
		return nil
	}
	return nil
}
`
	if got := rewritten(t, result); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Symbol != "Context.IsIdle" {
		t.Fatalf("skipped = %+v", result.Skipped)
	}
}

// A use listed after a rewrite above it in the file names its line in the source
// as it was: an upgrade that does not build restores that source.
func TestListedUsesNameTheirLineBeforeTheRewrite(t *testing.T) {
	source := `func handler(ctx sdk.Context) error {
	fmt.Println("home", ctx.ConfigHome())
	return nil
}

func dir(ctx sdk.Context) string { return fmt.Sprint("dir ", ctx.ConfigHome()) }
`
	result, _ := planSource(t, source)
	if !result.Changed() {
		t.Fatal("the handler's getter is not rewritten")
	}
	want := strings.Count(header, "\n") + 6
	if len(result.Skipped) != 1 || result.Skipped[0].Line != want {
		t.Fatalf("skipped = %+v, want line %d", result.Skipped, want)
	}
	if len(result.Remaining) != 1 || result.Remaining[0].Line != want {
		t.Fatalf("remaining = %+v, want line %d", result.Remaining, want)
	}
}

// A ConfigHome call that is the only argument of a variadic call compiles
// against the current SDK: its value and its error spread into the parameters,
// and the error is printed or passed on instead of handled. The plan finds it
// without a compile error, in a file that has none, and rewrites it as the other
// uses; one whose function cannot return the error is listed, never discarded.
// A call whose parameters take the value and the error, variadic or not, is the
// new shape and stays. A getter whose error dates from SDK 0.3.0 spread the same
// way is current code (it prints the error on purpose) and stays too.
func TestGetterSpreadIntoAVariadicCallIsRewritten(t *testing.T) {
	source := `func must(home string, err error) string { return home }

func check(home string, errs ...error) {}

func handler(ctx sdk.Context) error {
	fmt.Println(ctx.ConfigHome())
	fmt.Print(ctx.GetSessionName())
	check(ctx.ConfigHome())
	return nil
}

func banner(ctx sdk.Context) {
	println(ctx.ConfigHome())
	_ = must(ctx.ConfigHome())
}
`
	result, _ := planSource(t, source)
	if len(result.Errors) != 0 || len(result.Remaining) != 0 {
		t.Fatalf("errors = %v, remaining = %+v", result.Errors, result.Remaining)
	}
	want := `func must(home string, err error) string { return home }

func check(home string, errs ...error) {}

func handler(ctx sdk.Context) error {
	configHome, err := ctx.ConfigHome()
	if err != nil {
		return err
	}
	fmt.Println(configHome)
	fmt.Print(ctx.GetSessionName())
	check(ctx.ConfigHome())
	return nil
}

func banner(ctx sdk.Context) {
	println(ctx.ConfigHome())
	_ = must(ctx.ConfigHome())
}
`
	if got := rewritten(t, result); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	line := strings.Count(header, "\n") + 13
	if len(result.Skipped) != 1 || result.Skipped[0].Line != line || result.Skipped[0].Reason != "the function does not return an error, so the error of Context.ConfigHome has nowhere to go" {
		t.Fatalf("skipped = %+v, want line %d", result.Skipped, line)
	}
}

// A spread getter in a select case's communication cannot move before the
// select without changing which case runs: it is listed, and the plan of the
// rest of the file goes on.
func TestSpreadGetterInASelectCaseIsListed(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context, ch chan string) error {
	select {
	case ch <- fmt.Sprint(ctx.ConfigHome()):
	default:
	}
	return nil
}
`)
	if result.Changed() {
		t.Fatalf("the plan rewrites a select case: %s", result.Files[0].After)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Symbol != "Context.ConfigHome" || result.Skipped[0].Line != strings.Count(header, "\n")+3 {
		t.Fatalf("skipped = %+v", result.Skipped)
	}
}

// Rewriting a spread ConfigHome does not mark the file as written for an SDK
// older than 0.3.0: a nullable usage field it passes on as a pointer stays.
func TestSpreadRewriteLeavesNullableUsageAlone(t *testing.T) {
	result, _ := planSource(t, `func show(tokens *int) {}

func handler(ctx sdk.Context) error {
	fmt.Println(ctx.ConfigHome())
	u, err := ctx.GetContextUsage()
	if err != nil {
		return err
	}
	if u != nil {
		show(u.Tokens)
	}
	return nil
}
`)
	if len(result.Errors) != 0 || len(result.Remaining) != 0 || len(result.Skipped) != 0 {
		t.Fatalf("errors = %v, remaining = %+v, skipped = %+v", result.Errors, result.Remaining, result.Skipped)
	}
	if got := rewritten(t, result); !strings.Contains(got, "fmt.Println(configHome)") || !strings.Contains(got, "show(u.Tokens)") {
		t.Fatalf("got:\n%s", got)
	}
}

// An extension that compiles against the current SDK needs no upgrade: none of
// its getter calls, spread or not, is rewritten or listed, whatever else the file
// holds.
func TestCompileCleanCurrentExtensionNeedsNoUpgrade(t *testing.T) {
	result, _ := planSourceRequiring(t, "v0.5.0", `func show(tokens *int) {}

func check(home string, errs ...error) {}

func handler(ctx sdk.Context, ch chan string) error {
	select {
	case ch <- fmt.Sprint(ctx.GetActiveTools()):
	case ch <- fmt.Sprint(ctx.ConfigHome()):
	default:
	}
	fmt.Println(ctx.GetActiveTools())
	fmt.Println(ctx.ConfigHome())
	check(ctx.ConfigHome())
	u, err := ctx.GetContextUsage()
	if err != nil {
		return err
	}
	if u != nil {
		show(u.Tokens)
	}
	return nil
}

func banner(ctx sdk.Context) {
	fmt.Println(ctx.GetActiveTools())
	println(ctx.ConfigHome())
}
`)
	if result.Changed() || len(result.Skipped) != 0 || len(result.Remaining) != 0 || len(result.Errors) != 0 {
		t.Fatalf("result = %+v", result)
	}
}

// The old Context.ConfigHome never failed, so dropping its new error would turn a
// missing home directory into paths relative to the working directory. A use in a
// function that cannot return the error, or a method value passed where the old
// signature is wanted, is listed for the author and the file stays as it was.
func TestConfigHomeWithNowhereForTheErrorIsListedNotDiscarded(t *testing.T) {
	source := `func join(dir, name string) string { return dir + "/" + name }

func save(home string) error { return nil }

func resolve(home func() string) string { return home() }

func agentPath(ctx sdk.Context, name string) string {
	return join(ctx.ConfigHome(), name)
}

func persist(ctx sdk.Context) {
	_ = save(ctx.ConfigHome())
}

func handler(ctx sdk.Context) error {
	fmt.Println(resolve(ctx.ConfigHome))
	return nil
}
`
	result, _ := planSource(t, source)
	if result.Changed() {
		t.Fatalf("the plan rewrites a use whose error has nowhere to go:\n%s", result.Files[0].After)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("SDK drift reported as other compile errors: %v", result.Errors)
	}
	lines := strings.Split(header+source, "\n")
	at := func(fragment string) int {
		for i, line := range lines {
			if strings.Contains(line, fragment) {
				return i + 1
			}
		}
		t.Fatalf("no line holds %q", fragment)
		return 0
	}
	var got []string
	for _, skipped := range result.Skipped {
		got = append(got, fmt.Sprintf("%d %s: %s", skipped.Line, skipped.Symbol, skipped.Reason))
	}
	for _, want := range []string{
		fmt.Sprintf("%d Context.ConfigHome: the function does not return an error, so the error of Context.ConfigHome has nowhere to go", at("return join(ctx.ConfigHome(), name)")),
		fmt.Sprintf("%d Context.ConfigHome: the function does not return an error, so the error of Context.ConfigHome has nowhere to go", at("_ = save(ctx.ConfigHome())")),
		fmt.Sprintf("%d Context.ConfigHome: the method value is func() (string, error) now", at("resolve(ctx.ConfigHome)")),
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing skip %q in %q", want, got)
		}
	}
	if len(result.Remaining) != 1 || result.Remaining[0].Symbol != "Context.ConfigHome" || result.Remaining[0].Count != 3 {
		t.Fatalf("remaining = %+v", result.Remaining)
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

// A provider object written for map[string]any credentials names each changed field with its old and new shape; the change has no mechanical rewrite.
func TestProviderCredentialChangesAreNamed(t *testing.T) {
	result, _ := planSource(t, `var provider = sdk.Provider{
	Auth: sdk.ProviderAuth{
		APIKey: &sdk.APIKeyAuth{Login: func(sdk.AuthInteraction) (map[string]any, error) { return map[string]any{"type": "api_key"}, nil }},
		OAuth: &sdk.OAuthAuth{
			Login:  func(sdk.AuthInteraction) (map[string]any, error) { return nil, nil },
			ToAuth: func(c map[string]any) (map[string]any, error) { return c, nil },
		},
	},
	FilterModels: func(m []map[string]any, c map[string]any) ([]map[string]any, error) { return m, nil },
}

func resolve(input sdk.APIKeyAuthInput) string { key, _ := input.Credential["key"].(string); return key }
`)
	if result.Changed() || len(result.Errors) != 0 {
		t.Fatalf("result = %+v", result)
	}
	var symbols []string
	for _, drift := range result.Remaining {
		if drift.Rule != upgrade.RuleProviderCredential || drift.Rewrites || drift.Old == "" || drift.New == "" {
			t.Errorf("drift = %+v", drift)
		}
		symbols = append(symbols, drift.Symbol)
	}
	if want := []string{"APIKeyAuth.Login", "OAuthAuth.Login", "OAuthAuth.ToAuth", "Provider.FilterModels", "APIKeyAuthInput.Credential"}; !slices.Equal(symbols, want) {
		t.Fatalf("symbols = %q, want %q", symbols, want)
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

// A removed ProviderStreamOptions.TransformHeaders is named with its remedy, not rewritten: Pi never passes transformHeaders to Provider.stream (model-runtime.ts:671).
func TestClassifyNamesTheRemovedTransformHeaders(t *testing.T) {
	output := "# example.com/x\n./x.go:7:10: options.TransformHeaders undefined (type sdk.ProviderStreamOptions has no field or method TransformHeaders)\n"
	drifts := upgrade.Classify(upgrade.ParseDiagnostics([]byte(output)), nil)
	if len(drifts) != 1 || drifts[0].Rule != upgrade.RuleTransformHeaders || drifts[0].Symbol != "ProviderStreamOptions.TransformHeaders" || drifts[0].Rewrites || drifts[0].New != "removed" {
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
