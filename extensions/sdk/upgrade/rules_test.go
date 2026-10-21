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

// rewritten is the one extension file a plan rewrites, without the header. A
// helper file the plan adds beside it is not one of the rewritten files.
func rewritten(t *testing.T, result *upgrade.Result) string {
	t.Helper()
	var source []string
	for _, change := range result.Files {
		if !change.Created {
			source = append(source, strings.TrimPrefix(string(change.After), header))
		}
	}
	if len(source) != 1 {
		t.Fatalf("files = %d, want 1 (skipped %+v, remaining %+v, errors %v)", len(source), result.Skipped, result.Remaining, result.Errors)
	}
	return source[0]
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
// before the switch; a getter in a && operand's right side cannot move without
// running when its left operand does not allow it, so it takes a helper.
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
	if ctx.HasUI() && pigUpgradeIsIdle(ctx) {
		return nil
	}
	return nil
}
`
	if got := rewritten(t, result); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if len(result.Skipped) != 0 || len(result.Files) != 2 || !result.Files[1].Created {
		t.Fatalf("skipped = %+v, files = %d", result.Skipped, len(result.Files))
	}
}

// A use listed after a rewrite above it in the file names its line in the source
// as it was: an upgrade that does not build restores that source.
func TestListedUsesNameTheirLineBeforeTheRewrite(t *testing.T) {
	source := `func handler(ctx sdk.Context) error {
	fmt.Println("home", ctx.ConfigHome())
	return nil
}

func size(s string) int { return len(s) }

func dir(ctx sdk.Context) int {
	for i := 0; i < size(ctx.ConfigHome()); i++ {
	}
	return 0
}
`
	result, _ := planSource(t, source)
	if !result.Changed() {
		t.Fatal("the handler's getter is not rewritten")
	}
	want := strings.Count(header, "\n") + 9
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
	configHome2, err := ctx.ConfigHome()
	if err != nil {
		ctx.Notify("Context.ConfigHome failed: "+err.Error(), "error")
		return
	}
	println(configHome2)
	_ = must(ctx.ConfigHome())
}
`
	if got := rewritten(t, result); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("skipped = %+v", result.Skipped)
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
// function that cannot return the error reports it to the user through the
// call's own Context and returns. A use that cannot do that (a Context that is
// not a plain variable) and a method value passed where the old signature is
// wanted are listed for the author, with their line and the form to write.
func TestConfigHomeWithNowhereForTheErrorIsReportedNotDiscarded(t *testing.T) {
	source := `func join(dir, name string) string { return dir + "/" + name }

func save(home string) error { return nil }

func resolve(home func() string) string { return home() }

func current() sdk.Context { return sdk.Context{} }

func agentPath(ctx sdk.Context, name string) string {
	return join(ctx.ConfigHome(), name)
}

func persist(ctx sdk.Context) {
	_ = save(ctx.ConfigHome())
}

func lookup() string {
	return join(current().ConfigHome(), "x")
}

func handler(ctx sdk.Context) error {
	fmt.Println(resolve(ctx.ConfigHome))
	return nil
}
`
	result, _ := planSource(t, source)
	if len(result.Errors) != 0 {
		t.Fatalf("SDK drift reported as other compile errors: %v", result.Errors)
	}
	got := rewritten(t, result)
	for _, want := range []string{
		"func agentPath(ctx sdk.Context, name string) string {\n\tconfigHome, err := ctx.ConfigHome()\n\tif err != nil {\n\t\tctx.Notify(\"Context.ConfigHome failed: \"+err.Error(), \"error\")\n\t\treturn \"\"\n\t}\n\treturn join(configHome, name)\n}",
		"func persist(ctx sdk.Context) {\n\tconfigHome2, err := ctx.ConfigHome()\n\tif err != nil {\n\t\tctx.Notify(\"Context.ConfigHome failed: \"+err.Error(), \"error\")\n\t\treturn\n\t}\n\t_ = save(configHome2)\n}",
		"return join(current().ConfigHome(), \"x\")",
		"resolve(ctx.ConfigHome)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("source lacks %q:\n%s", want, got)
		}
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
	var listed []string
	for _, skipped := range result.Skipped {
		listed = append(listed, fmt.Sprintf("%d %s: %s", skipped.Line, skipped.Symbol, skipped.Reason))
		if skipped.Suggest == "" {
			t.Errorf("no suggested form for %+v", skipped)
		}
	}
	for _, want := range []string{
		fmt.Sprintf("%d Context.ConfigHome: the function does not return an error, so the error of Context.ConfigHome has nowhere to go, and the call's receiver is not a plain variable to report it through", at("return join(current().ConfigHome()")),
		fmt.Sprintf("%d Context.ConfigHome: the method value is func() (string, error) now", at("resolve(ctx.ConfigHome)")),
	} {
		if !slices.Contains(listed, want) {
			t.Errorf("missing skip %q in %q", want, listed)
		}
	}
	if len(result.Skipped) != 2 || len(result.Remaining) != 1 || result.Remaining[0].Symbol != "Context.ConfigHome" || result.Remaining[0].Count != 2 {
		t.Fatalf("skipped = %+v, remaining = %+v", result.Skipped, result.Remaining)
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

// A ConfigHome call that another call's order depends on, or that runs
// conditionally, is left for the author with the reason: no old value of it is
// safe to use in its place. The other getters take a helper there.
func TestConfigHomeSkipsWhereMovingTheCallChangesBehavior(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context, ready bool) error {
	if ready && ctx.ConfigHome() != "" {
		return nil
	}
	if !ready {
		return nil
	} else if ctx.ConfigHome() == "x" {
		return nil
	}
	for ctx.ConfigHome() == "y" {
	}
	fmt.Println(os.Getenv("A"), ctx.ConfigHome())
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
		"Context.ConfigHome: the call runs only when an earlier operand allows it",
		"Context.ConfigHome: the call is not in the if condition",
		"Context.ConfigHome: the statement is not one pig moves a call out of",
		"Context.ConfigHome: an earlier call in the statement would run after the getter",
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

// A ModelRegistry.Find use written for the single-result signature is named with its old and new shape; the change has no mechanical rewrite.
func TestModelRegistryFindChangeIsNamed(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context) error {
	model := ctx.ModelRegistry().Find("p", "m")
	fmt.Println(model)
	return nil
}
`)
	if result.Changed() || len(result.Remaining) != 1 {
		t.Fatalf("result = %+v", result)
	}
	drift := result.Remaining[0]
	if drift.Rule != upgrade.RuleModelRegistryFind || drift.Symbol != "ModelRegistry.Find" || drift.Rewrites || drift.New != "func(string, string) (map[string]any, error)" {
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

// A dialog's result is an any: a use that still treats it as a string or a bool fails to compile, and the plan names the dialog method with its old and new shape; the
// change has no mechanical rewrite because the extension chooses what to do with a non-string answer.
func TestDialogResultChangesAreNamed(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context) error {
	choice, ok, err := ctx.Select("Pick", []string{"a"})
	if err != nil || !ok {
		return err
	}
	var label string = choice
	return ctx.AppendEntry("picked", label)
}
`)
	if result.Changed() || len(result.Remaining) != 1 {
		t.Fatalf("result = %+v", result)
	}
	drift := result.Remaining[0]
	if drift.Rule != upgrade.RuleDialogResults || drift.Symbol != "Context.Select" || drift.Rewrites || drift.New != "func(...) (any, bool, error)" {
		t.Fatalf("drift = %+v", drift)
	}
}
