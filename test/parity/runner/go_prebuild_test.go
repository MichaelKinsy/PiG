//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// A timed pig run starts the probe binary, not the Go toolchain: the scenario's PATH puts a failing go first, and only
// the runner's build before the timed runs uses the real one.
func TestRunScenarioBuildsGoProbesBeforeTheTimedRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the failing go is a POSIX shell script")
	}
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := goPrebuilds.removeAll(); err != nil {
			t.Errorf("remove the prebuilt Go probes: %v", err)
		}
	})
	failingGo := t.TempDir()
	if err := os.WriteFile(filepath.Join(failingGo, "go"), []byte("#!/bin/sh\necho 'go ran inside the timed run' >&2\nexit 97\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	const fixtures = "./test/parity/runner/testdata/goprebuild/"
	script := filepath.Join(t.TempDir(), "probe.sh")
	probe := "set -euo pipefail\n./test/parity/testdata/prebuilt-go-test.sh " + fixtures + "probe -run '^TestProbe$' -count=1 -v | grep '^PROBE '\n"
	if err := os.WriteFile(script, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want       string
		pigArgs, goTests []string
	}{
		{name: "go-run", want: "PROGRAM a b", pigArgs: []string{"go", "run", fixtures + "program", "a", "b"}},
		{name: "go-test", want: "PROBE ok", pigArgs: []string{"bash", script}, goTests: []string{fixtures + "probe"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scenario := &Scenario{
				Name: "go-prebuild-" + tc.name, Driver: "cli-mode", SourcePath: filepath.Join(repoRoot, "test/parity/scenarios/go-prebuild.toml"),
				Env: EnvOverrides{
					OverrideBaseArgs: true, PigBin: posixEnvLauncher, PiBin: posixEnvLauncher,
					PigArgs: tc.pigArgs, PiArgs: []string{"echo", tc.want}, PigGoTests: tc.goTests,
					Pig: []string{"PATH=" + failingGo + string(os.PathListSeparator) + os.Getenv("PATH")},
				},
				CLI:    CLIDriverConfig{CWD: repoRoot, TimeoutSeconds: 60},
				Assert: AssertSpec{ExitCode: new(int), OutputEqual: true, Runs: 2},
			}
			outcome := RunScenario(t.Context(), t, scenario, BinaryRef{Label: "pig"}, BinaryRef{Label: "pi"}, false)
			if !outcome.Passed() {
				var output string
				if runs := outcome.Pig.Runs; len(runs) > 0 {
					output = runs[len(runs)-1].Output
				}
				t.Fatalf("failures=%q pig output=%q", outcome.Failures, output)
			}
		})
	}
}

// goToolchainCommand matches a shell line that starts the Go toolchain.
var goToolchainCommand = regexp.MustCompile("(^|[\\s;&|(`$])go\\s+(build|install|run|test|tool|vet)\\b")

// quotedShellWord matches a quoted shell word, such as a -run pattern whose alternatives contain |.
var quotedShellWord = regexp.MustCompile(`'[^']*'|"[^"]*"`)

// prebuiltGoTestPackage matches a ./-relative package operand.
var prebuiltGoTestPackage = regexp.MustCompile(`(?:^|\s)(\./[A-Za-z0-9_./-]*[A-Za-z0-9_])`)

// Every cli-mode scenario keeps the Go toolchain out of its timed pig runs: its pig side may be `go run ./PACKAGE ARGS`,
// which RunScenario builds first, and its scripts run Go tests only through prebuilt-go-test.sh, for exactly the
// packages its pig_go_tests lists.
func TestParityGoProbesAreBuiltBeforeTheTimedRun(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	scenarios, err := DiscoverScenarios(filepath.Join(repoRoot, "test/parity/scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, sc := range scenarios {
		if sc.Driver != "cli-mode" {
			continue
		}
		cliArgs := sc.CLI.Args
		if sc.CLI.PigArgs != nil {
			cliArgs = sc.CLI.PigArgs
		}
		argv := slices.Concat(sc.Env.PigArgs, cliArgs)
		runArgs, run := goRunArgs(sc)
		if runArgs == &sc.CLI.PigArgs {
			run += len(sc.Env.PigArgs)
		}
		if filepath.Base(sc.Env.PigBin) == "go" || (slices.Contains(argv, "go") && slices.Index(argv, "go") != run) {
			t.Errorf("%s: pig_bin %q with args %q starts the Go toolchain inside the timed run", sc.SourcePath, sc.Env.PigBin, argv)
		}
		if run >= 0 && (run+2 >= len(argv) || !isLocalGoPackage(argv[run+2])) {
			t.Errorf("%s: pig args %q: write `go run ./PACKAGE ARGS`, which the runner builds before the timed runs", sc.SourcePath, argv)
		}
		cwd := resolveScenarioCWD(sc.SourcePath, sc.CLI.CWD)
		tested := map[string]bool{}
		for _, arg := range append([]string{sc.Env.PigBin}, argv...) {
			script := pigScript(sc, cwd, arg)
			if script == "" {
				continue
			}
			checked++
			data, err := os.ReadFile(script)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "#") {
					continue
				}
				if goToolchainCommand.MatchString(line) {
					t.Errorf("%s:%d: starts the Go toolchain inside the timed run; run Go tests through prebuilt-go-test.sh", script, i+1)
				}
				_, operands, ok := strings.Cut(line, "prebuilt-go-test.sh")
				if !ok {
					continue
				}
				operands, _, _ = strings.Cut(quotedShellWord.ReplaceAllString(operands, "''"), "|")
				for _, match := range prebuiltGoTestPackage.FindAllStringSubmatch(operands, -1) {
					tested[match[1]] = true
				}
			}
		}
		for pkg := range tested {
			if !slices.Contains(sc.Env.PigGoTests, pkg) {
				t.Errorf("%s: a pig script tests %s: add it to [env] pig_go_tests", sc.SourcePath, pkg)
			}
		}
		for _, pkg := range sc.Env.PigGoTests {
			if !tested[pkg] {
				t.Errorf("%s: [env] pig_go_tests lists %s, which no pig script tests", sc.SourcePath, pkg)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no cli-mode pig script found; the guard no longer sees the scripts")
	}
}

// pigScript returns the shell script arg names for sc's pig side, resolved as the scenario and its cwd resolve it,
// or "" when arg names none.
func pigScript(sc *Scenario, cwd, arg string) string {
	if arg == "" || arg == posixEnvLauncher || strings.HasPrefix(arg, "-") || strings.Contains(arg, "{{") {
		return ""
	}
	candidates := []string{resolveScenarioCWD(sc.SourcePath, arg)}
	if cwd != "" && !filepath.IsAbs(arg) {
		candidates = append(candidates, filepath.Join(cwd, arg))
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil && (strings.HasSuffix(path, ".sh") || strings.HasPrefix(string(data), "#!")) {
			return path
		}
	}
	return ""
}

func TestGoRunArgsFindsEnvLaunchedGoRun(t *testing.T) {
	for _, tc := range []struct {
		name             string
		bin              string
		envArgs, cliArgs []string
		inCLI            bool
		want             int
	}{
		{name: "env pig_args", bin: posixEnvLauncher, envArgs: []string{"go", "run", "./x", "arg"}, want: 0},
		{name: "env -u operand", bin: posixEnvLauncher, envArgs: []string{"-u", "AWS_BEARER_TOKEN_BEDROCK", "go", "run", "./x"}, want: 2},
		{name: "env assignment", bin: posixEnvLauncher, envArgs: []string{"NAME=value", "go", "run", "./x"}, want: 1},
		{name: "cli pig_args", bin: posixEnvLauncher, cliArgs: []string{"go", "run", "./x", "arg"}, inCLI: true, want: 0},
		{name: "cli after env operand", bin: posixEnvLauncher, envArgs: []string{"-u", "NAME"}, cliArgs: []string{"go", "run", "./x"}, inCLI: true, want: 0},
		{name: "go test", bin: posixEnvLauncher, envArgs: []string{"go", "test", "./x"}, want: -1},
		{name: "script", bin: posixEnvLauncher, envArgs: []string{"bash", "./probe.sh"}, cliArgs: []string{"go", "run", "./x"}, want: -1},
		{name: "other launcher", bin: "/bin/sh", envArgs: []string{"go", "run", "./x"}, want: -1},
	} {
		sc := &Scenario{Driver: "cli-mode", Env: EnvOverrides{OverrideBaseArgs: true, PigBin: tc.bin, PigArgs: tc.envArgs}, CLI: CLIDriverConfig{PigArgs: tc.cliArgs}}
		args, got := goRunArgs(sc)
		if got != tc.want || (got >= 0 && (args == &sc.CLI.PigArgs) != tc.inCLI) {
			t.Errorf("%s: goRunArgs = (cli %t, %d), want (cli %t, %d)", tc.name, args == &sc.CLI.PigArgs, got, tc.inCLI, tc.want)
		}
	}
}

func TestIsLocalGoPackage(t *testing.T) {
	for pkg, want := range map[string]bool{
		"./coding": true, "./internal/codingagent/tools": true,
		"coding": false, "./": false, "./coding/": false, "./a/../b": false, "./..": false, "./../x": false,
		"./...": false, "./coding/...": false, `./a\b`: false,
	} {
		if got := isLocalGoPackage(pkg); got != want {
			t.Errorf("isLocalGoPackage(%q) = %t, want %t", pkg, got, want)
		}
	}
}

func TestValidateRejectsPigGoTestsOutsideCLIMode(t *testing.T) {
	base := Scenario{Name: "s", Covers: []string{"x.ts"}, Driver: "cli-mode", CLI: CLIDriverConfig{CWD: "../../../.."}}
	for name, edit := range map[string]func(*Scenario){
		"print-mode":     func(sc *Scenario) { sc.Driver = "print-mode" },
		"no cwd":         func(sc *Scenario) { sc.CLI.CWD = "" },
		"temporary cwd":  func(sc *Scenario) { sc.CLI.CWD = "{{TEMP}}" },
		"module pattern": func(sc *Scenario) { sc.Env.PigGoTests = []string{"./..."} },
	} {
		sc := base
		sc.Env.PigGoTests = []string{"./coding"}
		edit(&sc)
		if err := validate(&sc); err == nil {
			t.Errorf("%s: validate accepted pig_go_tests %q", name, sc.Env.PigGoTests)
		}
	}
	sc := base
	sc.Env.PigGoTests = []string{"./coding"}
	if err := validate(&sc); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
