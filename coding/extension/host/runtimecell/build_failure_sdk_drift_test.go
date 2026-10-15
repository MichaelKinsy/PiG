package runtimecell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goDriftFixture writes an extension written for SDK 0.2.0: its getter returns one value and its option is a bool.
func goDriftFixture(t *testing.T, name string) GoExtension {
	t.Helper()
	extension := goExtensionFixture(t, name, `sdk.New("`+name+`")`)
	source := "package " + name + "\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\n" +
		"func Extension() *sdk.Extension {\n\text := sdk.New(\"" + name + "\")\n" +
		"\text.Command(\"id\", \"Show the session\", func(ctx sdk.Context, args string) error {\n" +
		"\t\tid := ctx.GetSessionID()\n" +
		"\t\treturn ctx.SendMessage(\"id\", id, true, sdk.SendMessageOptions{TriggerTurn: true})\n" +
		"\t})\n\treturn ext\n}\n"
	if err := os.WriteFile(filepath.Join(extension.Root, "extension.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return extension
}

// A build that fails only on SDK changes says the extension was written for an
// older SDK, names each change with its old and new shape, and keeps the
// changes in the recorded failure so the next start names them without compiling.
func TestBuildGoPackedCellNamesSDKDrift(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	extension := goDriftFixture(t, "olddrift")
	cacheRoot := filepath.Join(configRoot, "cache")

	for _, attempt := range []string{"first", "recorded"} {
		key := "drift-cell"
		_, err := BuildGoPackedCell(context.Background(), cacheRoot, key, []GoExtension{extension})
		failure, ok := errors.AsType[*BuildFailure](err)
		if !ok {
			t.Fatalf("%s: err = %v, want a BuildFailure", attempt, err)
		}
		if !strings.Contains(failure.Summary, "written for an older SDK") || !strings.Contains(failure.Summary, "Context.GetSessionID") || strings.Contains(err.Error(), "\n") {
			t.Fatalf("%s: summary = %q", attempt, failure.Summary)
		}
		symbols := map[string]string{}
		for _, drift := range failure.Drift {
			symbols[drift.Symbol] = drift.Old + " -> " + drift.New
		}
		if symbols["Context.GetSessionID"] != "func() string -> func() (string, error)" || symbols["SendMessageOptions.TriggerTurn"] != "bool -> *bool" {
			t.Fatalf("%s: drift = %+v", attempt, failure.Drift)
		}
		if attempt == "recorded" && !failure.Cached {
			t.Fatalf("the second build compiled again instead of reading the recorded failure")
		}
	}
}

// A compile error that is not an SDK change keeps the compiler's own message.
func TestBuildGoPackedCellUnrelatedErrorIsNotDrift(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	extension := goExtensionFixture(t, "plainerror", `sdk.New("plainerror", "extra")`)
	_, err := BuildGoPackedCell(context.Background(), filepath.Join(configRoot, "cache"), "plain", []GoExtension{extension})
	failure, ok := errors.AsType[*BuildFailure](err)
	if !ok || len(failure.Drift) != 0 || strings.Contains(failure.Summary, "older SDK") {
		t.Fatalf("err = %v, drift = %+v", err, failure)
	}
}

// A packed cell whose build fails on an SDK change in one extension and on an
// unrelated compile error in another is not drift: the unrelated error keeps
// the compiler's own message, so the other extension's failure is not reported
// as written for an older SDK.
func TestBuildGoPackedCellMixedErrorsAreNotDrift(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	drifted := goDriftFixture(t, "mixeddrift")
	broken := goExtensionFixture(t, "mixedplain", `sdk.New("mixedplain", "extra")`)
	_, err := BuildGoPackedCell(context.Background(), filepath.Join(configRoot, "cache"), "mixed", []GoExtension{drifted, broken})
	failure, ok := errors.AsType[*BuildFailure](err)
	if !ok {
		t.Fatalf("err = %v, want a BuildFailure", err)
	}
	if len(failure.Drift) != 0 || strings.Contains(failure.Summary, "older SDK") {
		t.Fatalf("summary = %q, drift = %+v", failure.Summary, failure.Drift)
	}
}
