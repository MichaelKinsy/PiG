package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// The four extension shapes of one real failure: an SDK that changed Context.ConfigHome and SendMessageOptions.TriggerTurn under extensions that call them in
// statement and in expression position, and the Context getters that gained an error, in functions that cannot return it. The sources are written for this test.
var oldShapeExtensions = map[string]string{
	"ask": `package ask

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func agents(home string) string { return home + "/agents" }

func agentDir(ctx sdk.Context) string { return agents(ctx.ConfigHome()) }

func Extension() *sdk.Extension {
	ext := sdk.New("ask")
	ext.Command("dir", "Show the agent directory", func(ctx sdk.Context, args string) error {
		ctx.Notify(agentDir(ctx), "info")
		return nil
	})
	return ext
}
`,
	"web-search": `package websearch

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	ext := sdk.New("web-search")
	ext.Command("search", "Search the web", func(ctx sdk.Context, args string) error {
		return ctx.SendMessage("search", args, true, sdk.SendMessageOptions{TriggerTurn: true})
	})
	return ext
}
`,
	"context-info": `package contextinfo

import (
	"fmt"
	"path/filepath"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func overlayPath(ctx sdk.Context) string { return filepath.Join(ctx.ConfigHome(), "prompt-overlays.json") }

func footer(ctx sdk.Context) string {
	usage := ctx.GetContextUsage()
	active := ctx.GetActiveTools()
	all := ctx.GetAllTools()
	thinking := ctx.GetThinkingLevel()
	if usage == nil {
		return thinking
	}
	return fmt.Sprint(len(active), len(all), thinking, overlayPath(ctx))
}

func Extension() *sdk.Extension {
	ext := sdk.New("context-info")
	ext.Command("context", "Show the context", func(ctx sdk.Context, args string) error {
		ctx.Notify(footer(ctx), "info")
		return nil
	})
	return ext
}
`,
	"subagent": `package subagent

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

type dispatch struct{ ParentSessionID string }

func loadHistory(home, id string) (int, error) { return len(home) + len(id), nil }

func launch(ctx sdk.Context, d *dispatch) {
	if d.ParentSessionID == "" {
		d.ParentSessionID = ctx.GetSessionID()
	}
	go func() {
		if n, err := loadHistory(ctx.ConfigHome(), ctx.GetSessionID()); err != nil || n < 0 {
			ctx.Notify("history", "warning")
		}
	}()
	for !ctx.IsIdle() {
	}
}

func Extension() *sdk.Extension {
	ext := sdk.New("subagent")
	ext.Command("launch", "Launch an agent", func(ctx sdk.Context, args string) error {
		launch(ctx, &dispatch{})
		return nil
	})
	return ext
}
`,
}

// writeOldShapeExtensions writes each shape as a Go extension module under dir, written for SDK 0.4.1, and returns the directories in name order.
func writeOldShapeExtensions(t *testing.T, dir string) []string {
	t.Helper()
	var dirs []string
	for _, name := range []string{"ask", "context-info", "subagent", "web-search"} {
		extension := filepath.Join(dir, name)
		writeStartupFixtureFile(t, filepath.Join(extension, "extension.go"), oldShapeExtensions[name])
		writeStartupFixtureFile(t, filepath.Join(extension, "go.mod"), "module example.com/"+name+"\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1\n")
		dirs = append(dirs, extension)
	}
	return dirs
}

// upgradeCommandFor is the command the notice must print for these extension directories: each path as the shell of the platform reads it. A path outside the plain set (a space, or the "~" of a Windows 8.3 temp directory such as RUNNER~1) is quoted, so the printed words are not the raw paths.
func upgradeCommandFor(dirs []string) string {
	words := []string{"pig", "extension", "upgrade"}
	for _, dir := range dirs {
		words = append(words, commandWord(dir))
	}
	return strings.Join(words, " ")
}

func useRepositorySDK(t *testing.T) string {
	t.Helper()
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	root, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SDK_GO_ROOT", root)
	return configRoot
}

// (1) Detection reads the types. A failure the classifier cannot account for on its own (a recorded failure with no drift, the compile errors the go command stops
// printing after ten, an extension's own errors beside the SDK changes) still offers the extension, with every changed API it uses; an extension with only its own
// errors is not offered.
func TestSDKDriftOfOffersEveryExtensionTheUpgradeWouldTouch(t *testing.T) {
	configRoot := useRepositorySDK(t)
	root := t.TempDir()
	dirs := writeOldShapeExtensions(t, root)
	own := filepath.Join(root, "own-errors")
	writeStartupFixtureFile(t, filepath.Join(own, "extension.go"), "package own\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\nfunc Extension() *sdk.Extension { return sdk.New(\"own\") }\n\nvar _ int = \"not an int\"\n")
	writeStartupFixtureFile(t, filepath.Join(own, "go.mod"), "module example.com/own\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1\n")
	dirs = append(dirs, own)
	var configs []subprocess.ExtConfig
	var errs []error
	for _, dir := range dirs {
		found := pathToExtConfigs(dir)
		if len(found) != 1 || found[0].ResolveError() != nil {
			t.Fatalf("%s: %+v", dir, found)
		}
		configs = append(configs, found[0])
		// What startup holds for each: a build failure that names no SDK change.
		errs = append(errs, &subprocess.ExtensionLoadError{Name: found[0].Name, Path: dir, Err: &runtimecell.BuildFailure{Summary: "go build: extension.go:1:1: something the classifier does not know", Cause: "go build: unknown"}})
	}
	items := sdkDriftOf(t.Context(), newExtensionUpgrader(configRoot), errs, configs, nil)
	got := map[string][]string{}
	for _, item := range items {
		for _, drift := range item.Drift {
			got[item.Name] = append(got[item.Name], drift.Symbol)
		}
		if item.Plan == nil {
			t.Errorf("%s: no plan", item.Name)
		}
	}
	want := map[string][]string{
		"ask":          {"Context.ConfigHome"},
		"context-info": {"Context.ConfigHome", "Context.GetActiveTools", "Context.GetAllTools", "Context.GetContextUsage", "Context.GetThinkingLevel"},
		"subagent":     {"Context.ConfigHome", "Context.GetSessionID", "Context.IsIdle"},
		"web-search":   {"SendMessageOptions.TriggerTurn"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("offered = %v, want %v", got, want)
	}
	// Offered with a notice that names each API and the one command for all of them.
	notice := sdkDriftNotice(items)
	for _, name := range []string{"ask", "context-info", "subagent", "web-search"} {
		if !strings.Contains(notice, fmt.Sprintf("Extension %q was written for an older SDK:", name)) {
			t.Errorf("notice lacks %s:\n%s", name, notice)
		}
	}
	if !strings.Contains(notice, "Run: "+upgradeCommandFor(dirs[:4])+"\n") {
		t.Errorf("notice lacks the one command:\n%s", notice)
	}
}

// (3) One run upgrades every extension of the four shapes, so the next run finds nothing to do and each extension builds: before, each attempt stopped at the next class of change and restored the files.
func TestUpgradeAllFourShapesInOneRun(t *testing.T) {
	configRoot := useRepositorySDK(t)
	dirs := writeOldShapeExtensions(t, t.TempDir())
	var configs []subprocess.ExtConfig
	for _, dir := range dirs {
		configs = append(configs, pathToExtConfigs(dir)...)
	}
	upgrader := newExtensionUpgrader(configRoot)

	// (4) The dry run builds the result and says so.
	var out bytes.Buffer
	if status := upgradeExtensions(t.Context(), &out, upgrader, configs, true, false); status != 0 {
		t.Fatalf("dry run exit %d:\n%s", status, out.String())
	}
	if got := strings.Count(out.String(), ": would upgrade\n"); got != 4 || strings.Count(out.String(), "build of the rewritten extension: ok") != 4 {
		t.Fatalf("dry run:\n%s", out.String())
	}

	out.Reset()
	if status := upgradeExtensions(t.Context(), &out, upgrader, configs, false, false); status != 0 {
		t.Fatalf("exit %d:\n%s", status, out.String())
	}
	if got := strings.Count(out.String(), ": upgraded\n"); got != 4 || strings.Count(out.String(), "rebuilt: ok") != 4 || strings.Contains(out.String(), "edit by hand") {
		t.Fatalf("upgrade:\n%s", out.String())
	}
	subagent := readFile(t, filepath.Join(dirs[2], "extension.go"))
	for _, want := range []string{"pigUpgradeIsIdle(ctx)", "ctx.Notify(\"Context.ConfigHome failed: \"+err.Error(), \"error\")"} {
		if !strings.Contains(subagent, want) {
			t.Errorf("subagent lacks %q:\n%s", want, subagent)
		}
	}
	if !strings.Contains(readFile(t, filepath.Join(dirs[2], "pig_upgrade_getters.go")), "func pigUpgradeIsIdle(ctx sdk.Context) bool {") {
		t.Error("the helper file is missing")
	}

	out.Reset()
	if status := upgradeExtensions(t.Context(), &out, upgrader, configs, false, true); status != 0 || strings.Count(out.String(), "needs no upgrade") != 4 {
		t.Fatalf("second run exit %d:\n%s", status, out.String())
	}
}

// (4) A dry run does not overpromise: an extension whose rewritten tree does not build is not "would be upgraded". Its own compile error is listed, and the run exits non-zero.
func TestDryRunReportsWhatTheRewrittenTreeDoes(t *testing.T) {
	configRoot := useRepositorySDK(t)
	dir := filepath.Join(t.TempDir(), "broken")
	writeStartupFixtureFile(t, filepath.Join(dir, "extension.go"), oldExtensionSource+"\nvar _ int = \"not an int\"\n")
	writeStartupFixtureFile(t, filepath.Join(dir, "go.mod"), "module example.com/ask\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1\n")
	configs := pathToExtConfigs(dir)
	var out bytes.Buffer
	status := upgradeExtensions(t.Context(), &out, newExtensionUpgrader(configRoot), configs, true, false)
	for _, want := range []string{"broken: would partly upgrade", "compile error: ", `cannot use "not an int"`, "build of the rewritten extension: fails only on the changes listed above"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run lacks %q:\n%s", want, out.String())
		}
	}
	if status != 1 || strings.Contains(out.String(), "would upgrade\n") {
		t.Fatalf("exit %d:\n%s", status, out.String())
	}
	if readFile(t, filepath.Join(dir, "extension.go")) != oldExtensionSource+"\nvar _ int = \"not an int\"\n" {
		t.Fatal("a dry run wrote a file")
	}
}

// (4) When the rewritten tree fails to build for a reason the plan did not predict, nothing is written: a dry run says it would fail, and a real run that has uses to list says the same instead of leaving a file changed.
func TestUpgradeWritesNothingWhenTheRewriteWouldAddACompileError(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dryRun=%v", dryRun), func(t *testing.T) {
			upgrader, _ := fakeUpgrader(t, upgradeUpgraded, true)
			config := driftedExtensions(t, "ask")[0].Config
			analyze := upgrader.analyze
			upgrader.analyze = func(ctx context.Context, config subprocess.ExtConfig) (*upgrade.Result, error) {
				result, err := analyze(ctx, config)
				result.Remaining = []upgrade.Drift{{Rule: upgrade.RuleEditorComponent, Symbol: "Context.SetEditorComponent"}}
				return result, err
			}
			upgrader.check = func(context.Context, subprocess.ExtConfig, *upgrade.Result) (rewriteCheck, error) {
				return rewriteCheck{Unexpected: []string{"extension.go:4: undefined: sessionID"}}, nil
			}
			report := upgrader.upgrade(t.Context(), config, dryRun)
			if report.Status != upgradeFailed || report.Backup != "" || readFile(t, filepath.Join(config.Source, "extension.go")) != "before\n" {
				t.Fatalf("report = %+v", report)
			}
			var out bytes.Buffer
			printUpgradeReport(&out, report, false)
			if !strings.Contains(out.String(), "build error: extension.go:4: undefined: sessionID") || !strings.Contains(out.String(), "no file was changed") {
				t.Fatalf("report:\n%s", out.String())
			}
		})
	}
}

// (5) A file the upgrade adds is part of its backup and of its restore: when the rebuild fails for a reason the plan does not list, the added file goes with the rewrites.
func TestUpgradeRestoreRemovesAFileItAdded(t *testing.T) {
	upgrader, _ := fakeUpgrader(t, upgradeUpgraded, false)
	config := driftedExtensions(t, "ask")[0].Config
	added := filepath.Join(config.Source, "pig_upgrade_getters.go")
	analyze := upgrader.analyze
	upgrader.analyze = func(ctx context.Context, config subprocess.ExtConfig) (*upgrade.Result, error) {
		result, err := analyze(ctx, config)
		result.Files = append(result.Files, upgrade.FileChange{Path: added, After: []byte("package ask\n"), Created: true})
		return result, err
	}
	report := upgrader.upgrade(t.Context(), config, false)
	if report.Status != upgradeFailed || !strings.Contains(report.Note, "the original files are restored") {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(added); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the added file is still there: %v", err)
	}
	if got := readFile(t, filepath.Join(config.Source, "extension.go")); got != "before\n" {
		t.Fatalf("extension.go = %q", got)
	}
}

// (5) The command exits non-zero when uses are left, applies the rest, and prints the line and the form of each.
func TestExtensionUpgradeCommandAppliesWhatItCanAndListsTheRest(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	dir := filepath.Join(home, "partial")
	writeStartupFixtureFile(t, filepath.Join(dir, "extension.go"), strings.Replace(oldExtensionSource, "id := ctx.GetSessionID()\n\t\treturn ctx.SendMessage(\"id\", id, true, sdk.SendMessageOptions{TriggerTurn: true})", "id := ctx.GetSessionID()\n\t\t_ = id\n\t\treturn ctx.SetEditorComponent(42)", 1))
	writeStartupFixtureFile(t, filepath.Join(dir, "go.mod"), "module example.com/ask\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1\n")
	root, _ := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	cmd := exec.Command(binary, "extension", "upgrade", dir)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, "pig"), "PI_HOME="+filepath.Join(home, "pi"), "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_SDK_GO_ROOT="+root)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("err = %v\n%s\n%s", err, stdout.String(), stderr.String())
	}
	for _, want := range []string{"partial: partly upgraded", "id, err := ctx.GetSessionID()", "still to change by hand: Context.SetEditorComponent", "extension.go:"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, stdout.String())
		}
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "extension.go")), "id, err := ctx.GetSessionID()") {
		t.Fatal("the safe rewrite was not applied")
	}
}

// A compile error the extension already had is listed with its path, and a Windows path holds a colon of its own (the drive), so the message a rewrite build repeats is not the
// text after the third colon: the build of the rewritten extension must still know it, on every platform.
func TestRewriteBuildKnowsAnExistingCompileErrorUnderAWindowsPath(t *testing.T) {
	const message = `cannot use "not an int" (untyped string constant) as int value in variable declaration`
	result := &upgrade.Result{Errors: []string{`C:\Users\runneradmin\Temp\broken\extension.go:14:13: ` + message}}
	output := []byte("# example.com/ask\n" + `C:\Users\RUNNER~1\Temp\pig-upgrade-check-1\0.go:17:13: ` + message + "\n")
	check := classifyRewriteBuild(errors.New("exit status 1"), output, t.TempDir(), nil, result)
	if len(check.Unexpected) != 0 {
		t.Fatalf("the compile error the extension already had was unexpected: %v", check.Unexpected)
	}
	output = append(output, []byte(`C:\Users\RUNNER~1\Temp\pig-upgrade-check-1\0.go:18:2: a new error`+"\n")...)
	if check = classifyRewriteBuild(errors.New("exit status 1"), output, t.TempDir(), nil, result); len(check.Unexpected) != 1 {
		t.Fatalf("a new compile error must stay unexpected: %v", check.Unexpected)
	}
}
