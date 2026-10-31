//go:build parity

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeD217Manifest writes tui/testdata/pi-d217/manifest.json (and nothing else) under a scratch repository root.
func writeD217Manifest(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(d217HookDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// fakePiPackage lays out a pi-coding-agent package with a bundled bin and an unbundled entry, and returns the bin's path.
func fakePiPackage(t *testing.T) (bin, entry string) {
	t.Helper()
	pkg := filepath.Join(t.TempDir(), "pi-coding-agent")
	for path, content := range map[string]string{
		"package.json":         `{"name":"@earendil-works/pi-coding-agent"}`,
		"dist/bundle/cli.js":   "// bundle\n",
		"dist/cli.js":          "// unbundled\n",
		"dist/bundle/other.js": "// chunk\n",
	} {
		full := filepath.Join(pkg, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(pkg, "dist", "bundle", "cli.js"), filepath.Join(pkg, "dist", "cli.js")
}

// The hook is loaded while the manifest swaps a module, and only then: an empty manifest, or no hook directory at all (the pin has passed the
// carried fixes), leaves the pinned Pi as it is.
func TestD217HookFollowsTheManifest(t *testing.T) {
	root := writeD217Manifest(t, `[{ "suffix": "/a.js", "pinnedSha256": "0", "file": "a.js" }]`)
	got, err := d217Hook(root)
	if err != nil {
		t.Fatal(err)
	}
	want := fileURL(filepath.Join(root, "tui", "testdata", "pi-d217", "register.mjs"))
	if got != want || !strings.HasPrefix(got, "file:///") || !strings.HasSuffix(got, "/tui/testdata/pi-d217/register.mjs") {
		t.Fatalf("hook = %q, want %q", got, want)
	}
	for name, manifest := range map[string]string{"empty": `[]`, "empty with whitespace": " [\n]\n"} {
		root := writeD217Manifest(t, manifest)
		if got, err := d217Hook(root); err != nil || got != "" {
			t.Errorf("%s manifest: hook = %q, %v; want none", name, got, err)
		}
	}
	if got, err := d217Hook(t.TempDir()); err != nil || got != "" {
		t.Errorf("no hook directory: hook = %q, %v; want none", got, err)
	}
	if _, err := d217Hook(writeD217Manifest(t, `{"not": "a list"}`)); err == nil {
		t.Error("a manifest that is not a list was accepted")
	}
}

func TestFileURLPrefixesAWindowsDrive(t *testing.T) {
	if got, want := fileURL(`C:\repo\a b\register.mjs`), "file:///C:/repo/a%20b/register.mjs"; filepath.Separator == '\\' && got != want {
		t.Fatalf("fileURL = %q, want %q", got, want)
	}
	if got, want := fileURL("/repo/a b/register.mjs"), "file:///repo/a%20b/register.mjs"; got != want {
		t.Fatalf("fileURL = %q, want %q", got, want)
	}
}

// While the hook swaps a module the real pi runs as node --import <hook> <package>/dist/cli.js; the resolved bin stays the package's bundle for
// everything that reads it (the version check, the package root), and a scenario that names its own pi_bin runs that program as before.
func TestD217LaunchStartsTheUnbundledEntryUnderTheHook(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	bin, entry := fakePiPackage(t)
	root := writeD217Manifest(t, `[{ "suffix": "/a.js", "pinnedSha256": "0", "file": "a.js" }]`)
	hook, err := d217Hook(root)
	if err != nil {
		t.Fatal(err)
	}
	pi := BinaryRef{Label: "pi", Path: bin, Args: []string{"--no-extensions"}}
	got := d217Launch(t, root, pi)
	if got.Path != bin || !slices.Equal(got.Args, pi.Args) {
		t.Fatalf("Path = %q, Args = %q; want the resolved bin and its base args unchanged", got.Path, got.Args)
	}
	if got.Launch == nil || got.Launch.Program != node || !slices.Equal(got.Launch.Args, []string{"--import", hook, entry}) {
		t.Fatalf("Launch = %+v, want %s --import %s %s", got.Launch, node, hook, entry)
	}
	if program, lead := got.program(); program != node || !slices.Equal(lead, []string{"--import", hook, entry}) {
		t.Fatalf("program = %q %q, want the Launch", program, lead)
	}
	if _, handedOut := got.program(); len(handedOut) > 0 {
		handedOut[0] = "mutated"
	}
	if got.Launch.Args[0] != "--import" {
		t.Fatal("program hands out the Launch's own argument slice")
	}
	if kept := got.forScenario(&Scenario{}); kept.Launch == nil {
		t.Error("a scenario that runs the resolved pi lost the launch")
	}
	if own := got.forScenario(&Scenario{Env: EnvOverrides{PiBin: "/usr/bin/env"}}); own.Launch != nil {
		t.Errorf("a scenario with its own pi_bin kept the launch %+v", own.Launch)
	}
	if bundled := got.forScenario(&Scenario{Env: EnvOverrides{PiLaunch: "bundled"}}); bundled.Launch != nil || bundled.Path != bin {
		t.Errorf("pi_launch = bundled got Launch %+v, Path %q; want the bundle", bundled.Launch, bundled.Path)
	}
	pig := BinaryRef{Label: "pig", Path: "pig", Launch: &Launch{Program: node}}
	for _, env := range []EnvOverrides{{PiBin: "/usr/bin/env"}, {PiLaunch: "bundled"}} {
		if kept := pig.forScenario(&Scenario{Env: env}); kept.Launch == nil {
			t.Errorf("%+v cleared pig's launch", env)
		}
	}

	off := d217Launch(t, writeD217Manifest(t, `[]`), pi)
	if off.Launch != nil || off.Path != bin {
		t.Fatalf("with an empty manifest the bundle must run unchanged, got %+v", off)
	}
	if program, lead := off.program(); program != bin || len(lead) != 0 {
		t.Fatalf("program = %q %q, want the bin alone", program, lead)
	}
}

// Every driver that runs a real pi starts the Launch first, then the base args unless the scenario overrides them, then the scenario's args.
func TestDriversStartTheLaunchBeforeTheArgs(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	argv := &Launch{Program: node, Args: []string{"-e", `process.stdout.write("argv:" + process.argv.slice(1).join(" ") + ":end\n")`, "--"}}
	for _, override := range []bool{false, true} {
		want := "argv:--no-extensions scenario:end"
		if override {
			want = "argv:scenario:end"
		}
		t.Run(fmt.Sprintf("cli-mode/override=%t", override), func(t *testing.T) {
			cwd := t.TempDir()
			sc := &Scenario{
				Name: "launch-cli", SourcePath: filepath.Join(cwd, "scenario.toml"),
				Env: EnvOverrides{OverrideBaseArgs: override},
				CLI: CLIDriverConfig{CWD: cwd, Args: []string{"scenario"}, TimeoutSeconds: 10},
			}
			result := (cliModeDriver{}).Run(t.Context(), t, BinaryRef{Label: "pi", Path: "unused", Args: []string{"--no-extensions"}, Launch: argv}, sc)
			if result.Err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Output) != want {
				t.Fatalf("output = %q, exit code %d, err %v; want %q", result.Output, result.ExitCode, result.Err, want)
			}
		})
		t.Run(fmt.Sprintf("print-mode/override=%t", override), func(t *testing.T) {
			cwd := t.TempDir()
			sc := &Scenario{
				Name: "launch-print", SourcePath: filepath.Join(cwd, "scenario.toml"),
				Env:   EnvOverrides{OverrideBaseArgs: override, PiArgs: []string{"scenario"}},
				Print: PrintDriverConfig{Prompt: "p", CWD: cwd, TimeoutSeconds: 10},
			}
			result := (printModeDriver{}).Run(t.Context(), t, BinaryRef{Label: "pi", Path: "unused", Args: []string{"--no-extensions"}, Launch: argv}, sc)
			want := strings.Replace(want, ":end", " --print p:end", 1)
			if result.Err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Output) != want {
				t.Fatalf("output = %q, exit code %d, err %v; want %q", result.Output, result.ExitCode, result.Err, want)
			}
		})
		t.Run(fmt.Sprintf("rpc-mode/override=%t", override), func(t *testing.T) {
			cwd := t.TempDir()
			echo := &Launch{Program: node, Args: []string{"-e", `
require("node:readline").createInterface({ input: process.stdin }).on("line", () => {
  process.stdout.write("argv:" + process.argv.slice(1).join(" ") + ":end\n");
});`, "--"}}
			sc := &Scenario{
				Name: "launch-rpc", SourcePath: filepath.Join(cwd, "scenario.toml"),
				Env: EnvOverrides{OverrideBaseArgs: override, PiArgs: []string{"scenario"}},
				RPC: RPCDriverConfig{CWD: cwd, Steps: []RPCInputStep{{Line: `{}`, WaitContains: []string{":end"}}}},
			}
			result := (rpcModeDriver{}).Run(t.Context(), t, BinaryRef{Label: "pi", Path: "unused", Args: []string{"--no-extensions"}, Launch: echo}, sc)
			want := "argv:--no-extensions scenario --mode rpc:end"
			if override {
				want = "argv:scenario --mode rpc:end"
			}
			if result.Err != nil || !strings.Contains(result.Output, want) {
				t.Fatalf("output = %q, err %v; want it to contain %q", result.Output, result.Err, want)
			}
		})
		t.Run(fmt.Sprintf("interactive-tmux/override=%t", override), func(t *testing.T) {
			sc := &Scenario{
				Name: "launch-tmux",
				Env:  EnvOverrides{OverrideBaseArgs: override, PiArgs: []string{"scenario"}},
				Tmux: TmuxDriverConfig{Width: 80, Height: 8, ReadyPatternPi: "argv:", ReadyTimeoutSeconds: 10},
			}
			launch := &Launch{Program: "sh", Args: []string{"-c", `printf 'argv:%s:end\n' "$*"; sleep 999`, "sh"}}
			result := (tmuxDriver{}).Run(t.Context(), t, BinaryRef{Label: "pi", Path: "unused", Args: []string{"--no-extensions"}, Launch: launch}, sc)
			want := "argv:--no-extensions scenario:end"
			if override {
				want = "argv:scenario:end"
			}
			if result.Err != nil || !strings.Contains(result.Output, want) {
				t.Fatalf("output = %q, err %v; want it to contain %q", result.Output, result.Err, want)
			}
		})
	}
}

func TestPiLaunchIsValidated(t *testing.T) {
	valid := &Scenario{Name: "n", Driver: "cli-mode", Covers: []string{"c"}, Env: EnvOverrides{PiLaunch: "bundled"}}
	if err := validate(valid); err != nil {
		t.Fatalf("pi_launch = bundled: %v", err)
	}
	for name, env := range map[string]EnvOverrides{
		"another value":   {PiLaunch: "unbundled"},
		"with its pi_bin": {PiLaunch: "bundled", PiBin: "/usr/bin/env"},
	} {
		sc := *valid
		sc.Env = env
		if err := validate(&sc); err == nil {
			t.Errorf("%s: %+v was accepted", name, env)
		}
	}
}

// pi_launch = "bundled" is for a scenario that asserts the launch identity the unbundled entry changes and renders none of the code the D217 hook
// swaps. A scenario added to this list is that claim made again: review it against the hook's manifest.
func TestPiLaunchBundledHasOneUser(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	scenarios, err := DiscoverScenarios(filepath.Join(root, "test", "parity", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	var users []string
	for _, sc := range scenarios {
		if sc.Env.PiLaunch == "bundled" {
			users = append(users, sc.Name)
		}
	}
	if want := []string{"34-extension-process-identity"}; !slices.Equal(users, want) {
		t.Fatalf("scenarios with pi_launch = bundled: %q, want %q", users, want)
	}
}
