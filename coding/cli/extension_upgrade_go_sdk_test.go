//go:build !pig_strip_extension_sdk_go

package cli

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The real analysis and rebuild: an extension written for an old SDK upgrades, builds, and is then current.
func TestUpgraderUpgradesAnOldGoExtensionEndToEnd(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	root, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SDK_GO_ROOT", root)
	dir := filepath.Join(t.TempDir(), "ask")
	writeOldExtension(t, dir)
	configs := pathToExtConfigs(dir)
	if len(configs) != 1 || configs[0].ResolveError() != nil {
		t.Fatalf("configs = %+v", configs)
	}
	upgrader := newExtensionUpgrader(configRoot)
	report := upgrader.upgrade(t.Context(), configs[0], false)
	if !report.Ok() || report.Status != upgradeUpgraded {
		var out bytes.Buffer
		printUpgradeReport(&out, report, false)
		t.Fatalf("report:\n%s", out.String())
	}
	source := readFile(t, filepath.Join(dir, "extension.go"))
	for _, want := range []string{"id, err := ctx.GetSessionID()", "return err", "TriggerTurn: sdk.Bool(true)"} {
		if !strings.Contains(source, want) {
			t.Errorf("upgraded source lacks %q:\n%s", want, source)
		}
	}
	if again := upgrader.upgrade(t.Context(), configs[0], false); again.Status != upgradeUpToDate {
		t.Fatalf("a second upgrade: %+v", again)
	}
}

// With no mechanical rewrite the extension is reported, not edited.
func TestUpgraderLeavesAnExtensionItCannotRewrite(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	root, _ := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	t.Setenv("PIG_SDK_GO_ROOT", root)
	dir := filepath.Join(t.TempDir(), "editor")
	source := strings.Replace(oldExtensionSource, "id := ctx.GetSessionID()\n\t\treturn ctx.SendMessage(\"id\", id, true, sdk.SendMessageOptions{TriggerTurn: true})", "return ctx.SetEditorComponent(42)", 1)
	writeStartupFixtureFile(t, filepath.Join(dir, "extension.go"), source)
	writeStartupFixtureFile(t, filepath.Join(dir, "go.mod"), "module example.com/ask\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1\n")
	configs := pathToExtConfigs(dir)
	report := newExtensionUpgrader(configRoot).upgrade(t.Context(), configs[0], false)
	if report.Status != upgradeManual || report.Ok() || report.Backup != "" || readFile(t, filepath.Join(dir, "extension.go")) != source {
		t.Fatalf("report = %+v", report)
	}
	var out bytes.Buffer
	printUpgradeReport(&out, report, true)
	if !strings.Contains(out.String(), "still to change by hand: Context.SetEditorComponent: func(any) error -> func(EditorFactory) error") {
		t.Fatalf("report:\n%s", out.String())
	}
}

// A real run applies every rewrite that has one safe form and lists the rest with file, line and the form to write. The extension then builds only once the listed uses are changed, so the report says so and exits non-zero; it does not restore the files it rewrote. (Before, one use with no safe form restored every file, and the next attempt met the next class of change.)
func TestUpgraderKeepsTheSafeRewritesAndListsWhatItCannotChange(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	root, _ := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	t.Setenv("PIG_SDK_GO_ROOT", root)
	dir := filepath.Join(t.TempDir(), "home")
	source := `package home

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func save(cwd, home string) error { return nil }

func agents(home string) string { return home + "/agents" }

func agentDir(ctx sdk.Context) string { return agents(ctx.ConfigHome()) }

func Extension() *sdk.Extension {
	ext := sdk.New("home")
	ext.Command("save", "Save", func(ctx sdk.Context, args string) error {
		if err := save(ctx.Cwd(), ctx.ConfigHome()); err != nil {
			return err
		}
		for i := 0; i < len(ctx.ConfigHome()); i++ {
		}
		ctx.Notify(agentDir(ctx), "info")
		return ctx.SetEditorComponent(42)
	})
	return ext
}
`
	writeStartupFixtureFile(t, filepath.Join(dir, "extension.go"), source)
	writeStartupFixtureFile(t, filepath.Join(dir, "go.mod"), "module example.com/home\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1\n")
	configs := pathToExtConfigs(dir)
	upgrader := newExtensionUpgrader(configRoot)
	loopLine := 0
	for i, line := range strings.Split(source, "\n") {
		if strings.Contains(line, "for i := 0") {
			loopLine = i + 1
		}
	}
	wantListed := []string{
		fmt.Sprintf("extension.go:%d: edit by hand (Context.ConfigHome): the statement is not one pig moves a call out of", loopLine),
		"write: configHome, err := ctx.ConfigHome(); handle err",
		"still to change by hand: Context.SetEditorComponent: func(any) error -> func(EditorFactory) error",
	}

	preview := upgrader.upgrade(t.Context(), configs[0], true)
	var out bytes.Buffer
	printUpgradeReport(&out, preview, true)
	if preview.Status != upgradePreviewPartial || preview.Ok() || preview.Backup != "" || readFile(t, filepath.Join(dir, "extension.go")) != source {
		t.Fatalf("dry run:\n%s", out.String())
	}
	for _, want := range append([]string{"would partly upgrade", "build of the rewritten extension: fails only on the changes listed above"}, wantListed...) {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run lacks %q:\n%s", want, out.String())
		}
	}

	report := upgrader.upgrade(t.Context(), configs[0], false)
	out.Reset()
	printUpgradeReport(&out, report, true)
	if report.Status != upgradePartial || report.Ok() || report.Built || report.Backup == "" {
		t.Fatalf("report:\n%s", out.String())
	}
	upgraded := readFile(t, filepath.Join(dir, "extension.go"))
	for _, want := range []string{
		"configHome, err := ctx.ConfigHome()",
		"ctx.Notify(\"Context.ConfigHome failed: \"+err.Error(), \"error\")",
		"cwd2 := ctx.Cwd()",
		"len(ctx.ConfigHome())",
		"ctx.SetEditorComponent(42)",
	} {
		if !strings.Contains(upgraded, want) {
			t.Errorf("the extension lacks %q:\n%s", want, upgraded)
		}
	}
	for _, want := range wantListed {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
	if line := upgradeSummaryLine(report); !strings.Contains(line, `partly upgraded extension "home"`) || strings.Contains(line, "\n") {
		t.Errorf("summary = %q", line)
	}

	// What is left is a plain listing: a second run changes nothing and lists the same uses.
	again := upgrader.upgrade(t.Context(), configs[0], false)
	if again.Status != upgradeManual || readFile(t, filepath.Join(dir, "extension.go")) != upgraded {
		t.Fatalf("second run = %+v", again)
	}
}
