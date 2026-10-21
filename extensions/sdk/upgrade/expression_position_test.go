// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// extensionSource is the rewritten extension file of a plan, without the
// header; helperSource is the file the plan adds beside it, or "".
func extensionSource(t *testing.T, result *upgrade.Result) (source, helper string) {
	t.Helper()
	for _, change := range result.Files {
		if change.Created {
			helper = string(change.After)
			continue
		}
		if source != "" {
			t.Fatalf("two rewritten files in %+v", result.Files)
		}
		source = strings.TrimPrefix(string(change.After), header)
	}
	if source == "" {
		t.Fatalf("no rewritten file (skipped %+v, remaining %+v, errors %v)", result.Skipped, result.Remaining, result.Errors)
	}
	return source, helper
}

func requireClean(t *testing.T, result *upgrade.Result) {
	t.Helper()
	if len(result.Skipped) != 0 || len(result.Remaining) != 0 || len(result.Errors) != 0 {
		t.Fatalf("skipped = %+v, remaining = %+v, errors = %v", result.Skipped, result.Remaining, result.Errors)
	}
}

// A Context.ConfigHome call in a function that cannot return the error is the
// shape the upgrade used to list and then give up on: an argument of a call in a
// function that returns a string, and one in a function with no results. The
// error is reported through the call's own Context and the function returns, as
// an error would end a handler. It is never discarded: a lost home directory
// would turn the path into one relative to the working directory.
func TestConfigHomeInAFunctionWithoutAnErrorIsReportedAndReturned(t *testing.T) {
	result, _ := planSource(t, `func join(dir, name string) string { return dir + "/" + name }

func save(home string) error { return nil }

func agentPath(ctx sdk.Context, name string) string {
	return join(ctx.ConfigHome(), name)
}

func persist(ctx sdk.Context) {
	_ = save(ctx.ConfigHome())
}
`)
	requireClean(t, result)
	want := `func join(dir, name string) string { return dir + "/" + name }

func save(home string) error { return nil }

func agentPath(ctx sdk.Context, name string) string {
	configHome, err := ctx.ConfigHome()
	if err != nil {
		ctx.Notify("Context.ConfigHome failed: "+err.Error(), "error")
		return ""
	}
	return join(configHome, name)
}

func persist(ctx sdk.Context) {
	configHome2, err := ctx.ConfigHome()
	if err != nil {
		ctx.Notify("Context.ConfigHome failed: "+err.Error(), "error")
		return
	}
	_ = save(configHome2)
}
`
	if got, helper := extensionSource(t, result); got != want || helper != "" {
		t.Fatalf("got:\n%s\nwant:\n%s\nhelper:\n%s", got, want, helper)
	}
}

// The same getter in a goroutine, in an if init next to another getter, and as a
// filepath-style argument: each moves before its statement with the error
// reported or returned the way its function allows.
func TestExpressionPositionGettersMoveWithTheirErrorsHandled(t *testing.T) {
	result, _ := planSource(t, `func load(home, id string) (string, error) { return home + id, nil }

func run(ctx sdk.Context) {
	go func() {
		if got, err := load(ctx.ConfigHome(), ctx.GetSessionID()); err != nil {
			fmt.Println(got)
		}
	}()
}

func resume(ctx sdk.Context) error {
	if got, err := load(ctx.ConfigHome(), ctx.GetSessionID()); err != nil {
		return err
	} else {
		fmt.Println(got)
	}
	return nil
}
`)
	requireClean(t, result)
	got, helper := extensionSource(t, result)
	for _, want := range []string{
		"\t\tconfigHome, err := ctx.ConfigHome()\n\t\tif err != nil {\n\t\t\tctx.Notify(\"Context.ConfigHome failed: \"+err.Error(), \"error\")\n\t\t\treturn\n\t\t}\n",
		"\t\tsessionID, _ := ctx.GetSessionID() // the SDK now returns an error here: handle it\n",
		"\t\tif got, err := load(configHome, sessionID); err != nil {",
		"\tconfigHome2, err := ctx.ConfigHome()\n\tif err != nil {\n\t\treturn err\n\t}\n",
		"\tif got, err := load(configHome2, sessionID2); err != nil {",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("source lacks %q:\n%s", want, got)
		}
	}
	if helper != "" {
		t.Errorf("helper = %s", helper)
	}
}

// A getter that cannot move before its statement without running at another
// time (a && operand, an else-if condition, a loop condition, a call after
// another call in the statement) is replaced by a call of a generated helper
// that returns the old getter's zero value on failure, in one file beside the
// extension. The file builds.
func TestGetterThatCannotMoveTakesAGeneratedHelper(t *testing.T) {
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
	requireClean(t, result)
	got, helper := extensionSource(t, result)
	for _, want := range []string{
		"if ready && pigUpgradeIsIdle(ctx) {",
		"} else if pigUpgradeIsProjectTrusted(ctx) {",
		"for pigUpgradeHasPendingMessages(ctx) {",
		`fmt.Println(os.Getenv("A"), pigUpgradeGetSessionID(ctx))`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("source lacks %q:\n%s", want, got)
		}
	}
	for _, want := range []string{
		"package ext",
		`sdk "github.com/MichaelKinsy/PiG/extensions/sdk"`,
		"func pigUpgradeIsIdle(ctx sdk.Context) bool {\n\tvalue, _ := ctx.IsIdle()\n\treturn value\n}",
		"func pigUpgradeGetSessionID(ctx sdk.Context) string {",
	} {
		if !strings.Contains(helper, want) {
			t.Errorf("helper lacks %q:\n%s", want, helper)
		}
	}
	var created []string
	for _, change := range result.Files {
		if change.Created {
			created = append(created, change.Path)
		}
	}
	if len(created) != 1 || !strings.HasSuffix(created[0], "pig_upgrade_getters.go") {
		t.Fatalf("created files = %v", created)
	}
}

// A getter with parameters and a pointer result keep both in its helper.
func TestGetterHelperKeepsParametersAndPointerResults(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context, ready bool) error {
	for ctx.GetFlag("x") != nil && ctx.GetSessionName() != "" {
		return nil
	}
	return nil
}
`)
	_, helper := extensionSource(t, result)
	for _, want := range []string{
		"func pigUpgradeGetFlag(ctx sdk.Context, a0 string) any {",
		"value, _ := ctx.GetFlag(a0)",
		"func pigUpgradeGetSessionName(ctx sdk.Context) string {",
		"if value == nil {\n\t\treturn \"\"\n\t}\n\treturn *value",
	} {
		if !strings.Contains(helper, want) {
			t.Errorf("helper lacks %q:\n%s", want, helper)
		}
	}
}

// A ConfigHome that cannot move, such as a loop condition, has no old value to
// fall back on that is safe to use as a path: it is listed with its file, line
// and the form to write, and the rest of the file is still rewritten.
func TestConfigHomeThatCannotMoveIsListedWithItsFormAndTheRestIsRewritten(t *testing.T) {
	source := `func size(s string) int { return len(s) }

func handler(ctx sdk.Context) error {
	id := ctx.GetSessionID()
	fmt.Println(id)
	for i := 0; i < size(ctx.ConfigHome()); i++ {
	}
	return nil
}
`
	result, _ := planSource(t, source)
	got, _ := extensionSource(t, result)
	if !strings.Contains(got, "id, err := ctx.GetSessionID()") {
		t.Fatalf("the safe rewrite is missing:\n%s", got)
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("skipped = %+v", result.Skipped)
	}
	skipped := result.Skipped[0]
	if want := strings.Count(header, "\n") + 6; skipped.Symbol != "Context.ConfigHome" || skipped.Line != want {
		t.Fatalf("skipped = %+v, want line %d", skipped, want)
	}
	if !strings.Contains(skipped.Suggest, "configHome, err := ctx.ConfigHome()") || !strings.Contains(skipped.Suggest, "handle err") {
		t.Fatalf("suggestion = %q", skipped.Suggest)
	}
	if len(result.Remaining) != 1 || result.Remaining[0].Symbol != "Context.ConfigHome" {
		t.Fatalf("remaining = %+v", result.Remaining)
	}
}

// One plan covers every changed API an extension uses, so a second attempt does
// not find another class behind the first: all getter forms, the option and the
// usage field are rewritten, and the one change with no mechanical form is the
// only thing left.
func TestOnePlanRewritesEveryChangedAPIAndListsOnlyTheRest(t *testing.T) {
	result, _ := planSource(t, `func join(dir, name string) string { return dir + "/" + name }

func overlayPath(ctx sdk.Context) string {
	return join(ctx.ConfigHome(), "overlays.json")
}

func footer(ctx sdk.Context) string {
	usage := ctx.GetContextUsage()
	active := ctx.GetActiveTools()
	all := ctx.GetAllTools()
	level := ctx.GetThinkingLevel()
	return fmt.Sprint(usage != nil, len(active), len(all), level)
}

func hello(ctx sdk.Context, args string) error {
	id := ctx.GetSessionID()
	return ctx.SendMessage("id", id, true, sdk.SendMessageOptions{TriggerTurn: true})
}

func editor(ctx sdk.Context) error {
	return ctx.SetEditorComponent(42)
}
`)
	if len(result.Skipped) != 0 || len(result.Errors) != 0 {
		t.Fatalf("skipped = %+v, errors = %v", result.Skipped, result.Errors)
	}
	if len(result.Remaining) != 1 || result.Remaining[0].Symbol != "Context.SetEditorComponent" {
		t.Fatalf("remaining = %+v, want only SetEditorComponent", result.Remaining)
	}
	got, _ := extensionSource(t, result)
	for _, want := range []string{"sdk.Bool(true)", "overlayPath(ctx sdk.Context) string {\n\tconfigHome, err := ctx.ConfigHome()", "id, err := ctx.GetSessionID()", "ctx.GetContextUsage()"} {
		if !strings.Contains(got, want) {
			t.Errorf("source lacks %q:\n%s", want, got)
		}
	}
	for _, symbol := range []string{"Context.ConfigHome", "Context.GetSessionID", "Context.GetContextUsage", "Context.GetActiveTools", "Context.GetAllTools", "Context.GetThinkingLevel", "SendMessageOptions.TriggerTurn", "Context.SetEditorComponent"} {
		if !slices.ContainsFunc(result.Drift(), func(d upgrade.Drift) bool { return d.Symbol == symbol }) {
			t.Errorf("Drift lacks %s: %+v", symbol, result.Drift())
		}
	}
}

// Drift is the one list of what an upgrade touches: the changes the plan
// rewrites, the ones it lists, and the ones the compiler still rejects, each
// once, with whether a mechanical rewrite finishes it.
func TestDriftCountsRewrittenAndListedUsesOnce(t *testing.T) {
	result, _ := planSource(t, `func size(s string) int { return len(s) }

func handler(ctx sdk.Context) error {
	id := ctx.GetSessionID()
	fmt.Println(id)
	for i := 0; i < size(ctx.ConfigHome()); i++ {
	}
	return ctx.SetEditorComponent(42)
}
`)
	byName := map[string]upgrade.Drift{}
	for _, drift := range result.Drift() {
		byName[drift.Symbol] = drift
	}
	if got := byName["Context.GetSessionID"]; got.Count != 1 || !got.Rewrites {
		t.Errorf("GetSessionID = %+v", got)
	}
	if got := byName["Context.ConfigHome"]; got.Count != 1 || got.Rewrites {
		t.Errorf("ConfigHome = %+v", got)
	}
	if got := byName["Context.SetEditorComponent"]; got.Count != 1 || got.Rewrites {
		t.Errorf("SetEditorComponent = %+v", got)
	}
	if len(byName) != 3 {
		t.Errorf("drift = %+v", fmt.Sprint(result.Drift()))
	}
}

// A getter that is the only argument of a builtin that takes one value counts as
// two arguments: the compiler says `too many arguments for len(...)`, which names
// the builtin and not the getter. The diagnostic is still the getter's change,
// and the call is rewritten like any other argument.
func TestBuiltinTakingAGetterCallIsClassifiedAndRewritten(t *testing.T) {
	diagnostics := []upgrade.Diagnostic{
		{File: "main.go", Line: 10, Col: 9, Message: "invalid operation: too many arguments for len(ctx.GetAllTools()) (expected 1, found 2)"},
		{File: "main.go", Line: 20, Col: 2, Message: "invalid operation: too many arguments for panic(ctx.GetSessionID()) (expected 1, found 2)"},
		{File: "main.go", Line: 30, Col: 9, Message: "invalid operation: too many arguments for len(items.Values()) (expected 1, found 2)"},
	}
	drifts := upgrade.Classify(diagnostics, func(string) ([]byte, error) {
		return []byte("package main\nimport sdk \"" + upgrade.SDKModulePath + "\"\nvar _ sdk.Context\n"), nil
	})
	var symbols []string
	for _, drift := range drifts {
		symbols = append(symbols, drift.Symbol)
	}
	if want := []string{"Context.GetAllTools", "Context.GetSessionID"}; !slices.Equal(symbols, want) {
		t.Fatalf("classified = %v, want %v", symbols, want)
	}

	result, _ := planSource(t, `func count(ctx sdk.Context) int {
	return len(ctx.GetAllTools())
}

func report(ctx sdk.Context) {
	fmt.Println(len(ctx.GetActiveTools()), cap(ctx.GetActiveTools()))
	panic(ctx.GetSessionID())
}
`)
	requireClean(t, result)
	got, _ := extensionSource(t, result)
	for _, want := range []string{"allTools, _ := ctx.GetAllTools()", "return len(allTools)", "panic(sessionID)"} {
		if !strings.Contains(got, want) {
			t.Errorf("source lacks %q:\n%s", want, got)
		}
	}
}
