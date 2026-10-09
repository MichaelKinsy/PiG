package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// oldExtensionSource is an extension written for SDK 0.2.0: its getter returns one value and its option is a bool.
const oldExtensionSource = `package ask

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	ext := sdk.New("ask")
	ext.Command("id", "Show the session", func(ctx sdk.Context, args string) error {
		id := ctx.GetSessionID()
		return ctx.SendMessage("id", id, true, sdk.SendMessageOptions{TriggerTurn: true})
	})
	return ext
}
`

func writeOldExtension(t *testing.T, dir string) {
	t.Helper()
	writeStartupFixtureFile(t, filepath.Join(dir, "extension.go"), oldExtensionSource)
	writeStartupFixtureFile(t, filepath.Join(dir, "go.mod"), "module example.com/ask\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1\n")
}

func driftFailure(files ...string) error {
	var drifts []upgrade.Drift
	for _, file := range files {
		drifts = append(drifts, upgrade.Drift{Rule: upgrade.RuleContextGetters, Symbol: "Context.GetSessionID", Old: "func() string", New: "func() (string, error)", Since: "0.3.0", Rewrites: true, File: file, Line: 9, Count: 1})
	}
	return &runtimecell.BuildFailure{Summary: "go build: written for an older SDK (Context.GetSessionID changed)", Cause: "go build: written for an older SDK (Context.GetSessionID)", Drift: drifts}
}

// A packed cell fails as one build, and every extension in it gets that failure. Only the extension whose file the compiler named is written for an older SDK.
func TestSDKDriftNamesOnlyTheExtensionWhoseFileFailed(t *testing.T) {
	root := t.TempDir()
	ask, healthy := filepath.Join(root, "ask"), filepath.Join(root, "healthy")
	configs := []subprocess.ExtConfig{{Name: "ask", Source: ask}, {Name: "healthy", Source: healthy}}
	failure := driftFailure(filepath.Join(ask, "extension.go"))
	errs := []error{
		&subprocess.ExtensionLoadError{Name: "ask", Path: ask, Err: failure},
		&subprocess.ExtensionLoadError{Name: "healthy", Path: healthy, Err: failure},
		errors.New("embedded cell: unrelated"),
	}
	items := sdkDriftOf(errs, configs)
	if len(items) != 1 || items[0].Name != "ask" || items[0].Source != ask {
		t.Fatalf("items = %+v, want only ask", items)
	}
	// A failure with no drift is not drift.
	plain := []error{&subprocess.ExtensionLoadError{Name: "ask", Path: ask, Err: &runtimecell.BuildFailure{Summary: "go build: x", Cause: "x"}}}
	if items := sdkDriftOf(plain, configs); len(items) != 0 {
		t.Fatalf("items = %+v", items)
	}
}

// Two copies of one extension share a name; the drift is the copy whose file the compiler named.
func TestSDKDriftFindsTheSameNamedCopyThatFailed(t *testing.T) {
	root := t.TempDir()
	current, stale := filepath.Join(root, "current", "ask"), filepath.Join(root, "stale", "ask")
	configs := []subprocess.ExtConfig{{Name: "ask", Source: current}, {Name: "ask", Source: stale}}
	failure := driftFailure(filepath.Join(stale, "extension.go"))
	errs := []error{&subprocess.ExtensionLoadError{Name: "ask", Path: current, Err: failure}, &subprocess.ExtensionLoadError{Name: "ask", Path: stale, Err: failure}}
	if items := sdkDriftOf(errs, configs); len(items) != 1 || items[0].Source != stale {
		t.Fatalf("items = %+v, want only the stale copy", items)
	}
}

func TestSDKDriftNoticeNamesChangesAndTheExactCommand(t *testing.T) {
	ask := filepath.Join(t.TempDir(), "my ext")
	items := []extensionDrift{{Name: "ask", Source: ask, Drift: []upgrade.Drift{
		{Symbol: "Context.GetSessionID", Old: "func() string", New: "func() (string, error)", Since: "0.3.0", Rewrites: true},
		{Symbol: "Context.SetEditorComponent", Old: "func(any) error", New: "func(EditorFactory) error", Since: "0.4.2"},
	}}}
	notice := sdkDriftNotice(items)
	for _, want := range []string{
		`Extension "ask" was written for an older SDK:`,
		"Context.GetSessionID: func() string -> func() (string, error) (SDK 0.3.0)",
		"Context.SetEditorComponent: func(any) error -> func(EditorFactory) error (SDK 0.4.2): needs a manual change",
		"Run: pig extension upgrade " + commandWord(ask) + "\n",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice lacks %q:\n%s", want, notice)
		}
	}
	if !strings.Contains(notice, "'") && !strings.Contains(notice, `"`) {
		t.Errorf("a path with a space is not quoted:\n%s", notice)
	}
}

// fakeUpgrader records upgrades and answers from a table.
func fakeUpgrader(t *testing.T, status extensionUpgradeStatus, built bool) (*extensionUpgrader, *[]string) {
	t.Helper()
	var upgraded []string
	u := newExtensionUpgrader(t.TempDir())
	u.now = func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }
	u.analyze = func(_ context.Context, config subprocess.ExtConfig) (*upgrade.Result, error) {
		upgraded = append(upgraded, config.Name)
		path := filepath.Join(config.Source, "extension.go")
		return &upgrade.Result{Files: []upgrade.FileChange{{Path: path, Before: []byte("before\n"), After: []byte("after\n"), Rewrites: []upgrade.Rewrite{{Rule: upgrade.RuleContextGetters, Symbol: "Context.GetSessionID", Line: 3}}}}}, nil
	}
	u.rebuild = func(context.Context, subprocess.ExtConfig) error {
		if !built {
			return errors.New("compile failed")
		}
		return nil
	}
	_ = status
	return u, &upgraded
}

func driftedExtensions(t *testing.T, names ...string) []extensionDrift {
	t.Helper()
	var items []extensionDrift
	for _, name := range names {
		dir := filepath.Join(t.TempDir(), name)
		writeStartupFixtureFile(t, filepath.Join(dir, "extension.go"), "before\n")
		config := subprocess.ExtConfig{Name: name, Source: dir, RuntimeLanguage: "go", EntrypointKind: "factory"}
		items = append(items, extensionDrift{Name: name, Source: dir, Config: config, Drift: []upgrade.Drift{{Symbol: "Context.GetSessionID", Old: "func() string", New: "func() (string, error)", Since: "0.3.0", Rewrites: true}}})
	}
	return items
}

func TestStartupAsksBeforeUpgradingAndUpgradesOnYes(t *testing.T) {
	for _, tc := range []struct {
		answer      string
		wantUpgrade bool
	}{{"y\n", true}, {"YES\n", true}, {"\n", false}, {"n\n", false}, {"", false}, {"yep\n", false}} {
		t.Run(fmt.Sprintf("%q", tc.answer), func(t *testing.T) {
			upgrader, upgraded := fakeUpgrader(t, upgradeUpgraded, true)
			items := driftedExtensions(t, "ask", "notes")
			var out bytes.Buffer
			restart := upgradeDriftedExtensions(t.Context(), items, startupUpgradeOptions{Interactive: true, Upgrader: upgrader, In: strings.NewReader(tc.answer), Out: &out})
			if restart != tc.wantUpgrade || (len(*upgraded) == 2) != tc.wantUpgrade || (len(*upgraded) != 0 && !tc.wantUpgrade) {
				t.Fatalf("restart=%v upgraded=%v", restart, *upgraded)
			}
			if !strings.Contains(out.String(), `Extension "ask" was written for an older SDK`) || !strings.Contains(out.String(), "now? [y/N] ") {
				t.Fatalf("the prompt does not list the extensions and ask:\n%s", out.String())
			}
			if tc.wantUpgrade && !strings.Contains(out.String(), "ask: upgraded") {
				t.Fatalf("the upgrade report is missing:\n%s", out.String())
			}
		})
	}
}

// Print, RPC and JSON modes never prompt and never write: the failure is reported as it always was.
func TestStartupDoesNotPromptOrUpgradeWhenNotInteractive(t *testing.T) {
	upgrader, upgraded := fakeUpgrader(t, upgradeUpgraded, true)
	var out bytes.Buffer
	restart := upgradeDriftedExtensions(t.Context(), driftedExtensions(t, "ask"), startupUpgradeOptions{Upgrader: upgrader, In: strings.NewReader("y\n"), Out: &out})
	if restart || len(*upgraded) != 0 || out.Len() != 0 {
		t.Fatalf("restart=%v upgraded=%v output=%q", restart, *upgraded, out.String())
	}
}

func TestStartupAutoUpgradeIsOneLinePerExtension(t *testing.T) {
	upgrader, upgraded := fakeUpgrader(t, upgradeUpgraded, true)
	var out bytes.Buffer
	restart := upgradeDriftedExtensions(t.Context(), driftedExtensions(t, "ask", "notes"), startupUpgradeOptions{Auto: true, Upgrader: upgrader, In: strings.NewReader(""), Out: &out})
	if !restart || len(*upgraded) != 2 {
		t.Fatalf("restart=%v upgraded=%v", restart, *upgraded)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `upgraded extension "ask"`) || !strings.Contains(lines[0], "1 changes") || !strings.Contains(lines[0], "extension-upgrade") {
		t.Fatalf("output = %q", out.String())
	}
}

// Startup restarts only when every drifted extension builds after the upgrade.
func TestStartupDoesNotRestartWhenAnUpgradedExtensionStillFailsToBuild(t *testing.T) {
	upgrader, _ := fakeUpgrader(t, upgradeUpgraded, false)
	var out bytes.Buffer
	if upgradeDriftedExtensions(t.Context(), driftedExtensions(t, "ask"), startupUpgradeOptions{Auto: true, Upgrader: upgrader, Out: &out}) {
		t.Fatal("startup restarts although the upgraded extension does not build")
	}
	if !strings.Contains(out.String(), `could not upgrade extension "ask": the upgraded extension does not build: compile failed`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestUpgraderBacksUpBeforeWritingAndRebuilds(t *testing.T) {
	upgrader, _ := fakeUpgrader(t, upgradeUpgraded, true)
	items := driftedExtensions(t, "ask")
	config := items[0].Config
	file := filepath.Join(config.Source, "extension.go")
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatal(err)
	}

	preview := upgrader.upgrade(t.Context(), config, true)
	if preview.Status != upgradePreview || readFile(t, file) != "before\n" || preview.Backup != "" {
		t.Fatalf("a dry run changed something: %+v", preview)
	}

	report := upgrader.upgrade(t.Context(), config, false)
	if report.Status != upgradeUpgraded || !report.Built || !report.Ok() {
		t.Fatalf("report = %+v", report)
	}
	if got := readFile(t, file); got != "after\n" {
		t.Fatalf("file = %q", got)
	}
	// Windows has no POSIX mode bits: Chmod only toggles read-only, and Stat reports 0o666 or 0o444.
	if info, err := os.Stat(file); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o640) {
		t.Fatalf("mode = %v, %v", info.Mode(), err)
	}
	var manifest backupManifest
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(report.Backup, "manifest.json"))), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 1 || manifest.Files[0].Path != file || readFile(t, manifest.Files[0].Backup) != "before\n" {
		t.Fatalf("manifest = %+v", manifest)
	}
	var out bytes.Buffer
	printUpgradeReport(&out, report, true)
	for _, want := range []string{"ask: upgraded", "-before", "+after", "original files: " + report.Backup, "rebuilt: ok"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}

// Two copies of one extension share a name. Upgraded in the same second, each keeps its own backup.
func TestUpgraderKeepsEveryBackupOfSameNamedExtensions(t *testing.T) {
	upgrader, _ := fakeUpgrader(t, upgradeUpgraded, true)
	first, second := driftedExtensions(t, "ask")[0].Config, driftedExtensions(t, "ask")[0].Config
	reports := []extensionUpgradeReport{upgrader.upgrade(t.Context(), first, false), upgrader.upgrade(t.Context(), second, false)}
	if reports[0].Backup == "" || reports[0].Backup == reports[1].Backup {
		t.Fatalf("backups = %q and %q, want two directories", reports[0].Backup, reports[1].Backup)
	}
	for i, config := range []subprocess.ExtConfig{first, second} {
		var manifest backupManifest
		if err := json.Unmarshal([]byte(readFile(t, filepath.Join(reports[i].Backup, "manifest.json"))), &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Source != config.Source || len(manifest.Files) != 1 || readFile(t, manifest.Files[0].Backup) != "before\n" {
			t.Fatalf("backup %d = %+v, want the files of %s", i, manifest, config.Source)
		}
	}
}

// An interrupt that arrives while the extension is read leaves every file and the backup root untouched.
func TestUpgraderWritesNothingAfterCancel(t *testing.T) {
	upgrader, _ := fakeUpgrader(t, upgradeUpgraded, true)
	config := driftedExtensions(t, "ask")[0].Config
	ctx, cancel := context.WithCancel(t.Context())
	analyze := upgrader.analyze
	upgrader.analyze = func(ctx context.Context, config subprocess.ExtConfig) (*upgrade.Result, error) {
		result, err := analyze(ctx, config)
		cancel()
		return result, err
	}
	report := upgrader.upgrade(ctx, config, false)
	if report.Status != upgradeFailed || report.Backup != "" || !strings.Contains(report.Note, "cancelled") {
		t.Fatalf("report = %+v", report)
	}
	if got := readFile(t, filepath.Join(config.Source, "extension.go")); got != "before\n" {
		t.Fatalf("file = %q", got)
	}
	if _, err := os.Stat(filepath.Join(upgrader.configRoot, "state", "extension-upgrade")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup root exists: %v", err)
	}
}

func TestUpgraderReportsARebuildThatFails(t *testing.T) {
	upgrader, _ := fakeUpgrader(t, upgradeUpgraded, false)
	config := driftedExtensions(t, "ask")[0].Config
	report := upgrader.upgrade(t.Context(), config, false)
	if report.Ok() || report.Status != upgradeUpgraded || !strings.Contains(report.Note, "does not build") || report.Backup == "" {
		t.Fatalf("report = %+v", report)
	}
}

func TestUpgraderSkipsNonGoExtensions(t *testing.T) {
	upgrader, upgraded := fakeUpgrader(t, upgradeUpgraded, true)
	report := upgrader.upgrade(t.Context(), subprocess.ExtConfig{Name: "py", Source: t.TempDir(), RuntimeLanguage: "python"}, false)
	if report.Status != upgradeSkipped || len(*upgraded) != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

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

// The command resolves a directory, upgrades it, and exits 0.
func TestExtensionUpgradeCommandOnADirectory(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	dir := filepath.Join(home, "ask")
	writeOldExtension(t, dir)
	root, _ := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	run := func(args ...string) (string, string, error) {
		cmd := exec.Command(binary, append([]string{"extension", "upgrade"}, args...)...)
		cmd.Dir = home
		cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, "pig"), "PI_HOME="+filepath.Join(home, "pi"), "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_SDK_GO_ROOT="+root)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}
	out, errOut, err := run("--dry-run", dir)
	if err != nil || !strings.Contains(out, "ask: would upgrade") || !strings.Contains(out, "+\t\tid, err := ctx.GetSessionID()") || readFile(t, filepath.Join(dir, "extension.go")) != oldExtensionSource {
		t.Fatalf("dry run: err=%v\n%s\n%s", err, out, errOut)
	}
	out, errOut, err = run(dir)
	if err != nil || !strings.Contains(out, "ask: upgraded") || !strings.Contains(out, "rebuilt: ok") || !strings.Contains(out, "original files:") {
		t.Fatalf("upgrade: err=%v\n%s\n%s", err, out, errOut)
	}
	out, _, err = run(dir)
	if err != nil || !strings.Contains(out, "ask: up to date") {
		t.Fatalf("second upgrade: err=%v\n%s", err, out)
	}
	if _, _, err := run("no-such-extension"); err == nil {
		t.Fatal("an unknown name succeeds")
	}
}

func TestUpgradeAfterUpdateRunsOnlyWhenEnabled(t *testing.T) {
	runs := 0
	upgradeExtensionsAfterUpdate(false, func() error { runs++; return nil })
	if runs != 0 {
		t.Fatal("the upgrade ran with the setting off")
	}
	upgradeExtensionsAfterUpdate(true, func() error { runs++; return nil })
	if runs != 1 {
		t.Fatalf("runs = %d, want 1", runs)
	}
	// A failed upgrade is a warning, not a panic or an exit.
	upgradeExtensionsAfterUpdate(true, func() error { return errors.New("no go toolchain") })
}

func TestUpgradeSummaryLines(t *testing.T) {
	u, _ := fakeUpgrader(t, upgradeUpgraded, true)
	config := driftedExtensions(t, "ask")[0].Config
	for _, tc := range []struct {
		report extensionUpgradeReport
		want   string
	}{
		{u.upgrade(t.Context(), config, true), `would be upgraded`},
		{u.upgrade(t.Context(), subprocess.ExtConfig{Name: "py", Source: t.TempDir(), RuntimeLanguage: "python"}, false), `extension "py" skipped: not a Go source extension`},
		{extensionUpgradeReport{Name: "ask", Status: upgradeUpToDate}, `extension "ask" needs no upgrade`},
		{extensionUpgradeReport{Name: "ask", Status: upgradeManual, Source: "/x/ask", Result: &upgrade.Result{Remaining: []upgrade.Drift{{Symbol: "Context.SetEditorComponent"}}}}, `1 SDK change(s) need a manual edit; run pig extension upgrade /x/ask`},
	} {
		if got := upgradeSummaryLine(tc.report); !strings.Contains(got, tc.want) || strings.Contains(got, "\n") {
			t.Errorf("summary = %q, want %q", got, tc.want)
		}
	}
}

// --all finds the extensions the settings and the agent directory load, and
// --summary prints one line each, as the upgrade after `pig update` does.
func TestExtensionUpgradeAllDiscoversAgentExtensions(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	ask := filepath.Join(agentDir, "extensions", "ask")
	writeOldExtension(t, ask)
	cmd := exec.Command(binary, "extension", "upgrade", "--all", "--summary")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, "pig"), "PI_HOME="+filepath.Join(home, "pi"), "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v\n%s\n%s", err, stdout.String(), stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `upgraded extension "ask" for the current SDK (2 changes;`) {
		t.Fatalf("output:\n%s\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(readFile(t, filepath.Join(ask, "extension.go")), "sdk.Bool(true)") {
		t.Fatal("the extension was not upgraded")
	}
}
