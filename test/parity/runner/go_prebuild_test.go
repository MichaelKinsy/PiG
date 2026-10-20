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
	programScript := filepath.Join(t.TempDir(), "program.sh")
	program := "set -euo pipefail\n./test/parity/testdata/prebuilt-go-run.sh " + fixtures + "program a b\n"
	if err := os.WriteFile(programScript, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	// A Node script reaches the helpers as a program with its operands in a list.
	nodeScript := filepath.Join(t.TempDir(), "probe.mjs")
	node := "import { spawnSync } from 'node:child_process';\n" +
		"const test = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['" + fixtures + "probe', '-run', '^TestProbe$', '-count=1', '-v'], { encoding: 'utf8' });\n" +
		"const program = spawnSync('./test/parity/testdata/prebuilt-go-run.sh', ['" + fixtures + "program', 'a', 'b'], { encoding: 'utf8' });\n" +
		"process.stdout.write(test.stdout.split('\\n').filter((line) => line.startsWith('PROBE ')).join('') + '\\n' + program.stdout);\n" +
		"process.exit(test.status || program.status);\n"
	if err := os.WriteFile(nodeScript, []byte(node), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want                                             string
		pigArgs, goTests, piArgs, piGoTests, pigProgs, piProgs []string
	}{
		{name: "go-run", want: "PROGRAM a b", pigArgs: []string{"go", "run", fixtures + "program", "a", "b"}},
		{name: "go-test", want: "PROBE ok", pigArgs: []string{"bash", script}, goTests: []string{fixtures + "probe"}},
		{name: "go-program-from-a-script", want: "PROGRAM a b", pigArgs: []string{"bash", programScript}, pigProgs: []string{fixtures + "program"}},
		{name: "pi-side-node-script", want: "PROBE ok\nPROGRAM a b", pigArgs: []string{"echo", "PROBE ok\nPROGRAM a b"}, piArgs: []string{"node", nodeScript}, piGoTests: []string{fixtures + "probe"}, piProgs: []string{fixtures + "program"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			piArgs := tc.piArgs
			if piArgs == nil {
				piArgs = []string{"echo", tc.want}
			}
			scenario := &Scenario{
				Name: "go-prebuild-" + tc.name, Driver: "cli-mode", SourcePath: filepath.Join(repoRoot, "test/parity/scenarios/go-prebuild.toml"),
				Env: EnvOverrides{
					OverrideBaseArgs: true, PigBin: posixEnvLauncher, PiBin: posixEnvLauncher,
					PigArgs: tc.pigArgs, PiArgs: piArgs, PigGoTests: tc.goTests, PiGoTests: tc.piGoTests,
					PigGoPrograms: tc.pigProgs, PiGoPrograms: tc.piProgs,
					Pig: []string{"PATH=" + failingGo + string(os.PathListSeparator) + os.Getenv("PATH")},
					Pi:  []string{"PATH=" + failingGo + string(os.PathListSeparator) + os.Getenv("PATH")},
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

// goToolchainSpawn matches the Go toolchain started as a program with its arguments in a list, as Node's child_process and Python's
// subprocess do: ("go", ["test", ...]) or ["go", "test", ...].
var goToolchainSpawn = regexp.MustCompile(`['"]go['"]\s*,\s*(?:\[\s*)?['"](build|install|run|test|tool|vet)['"]`)

// quotedShellWord matches a quoted shell word, such as a -run pattern whose alternatives contain |.
var quotedShellWord = regexp.MustCompile(`'[^']*'|"[^"]*"`)

// prebuiltGoTestPackage matches a ./-relative package operand.
var prebuiltGoTestPackage = regexp.MustCompile(`(?:^|\s)(\./[A-Za-z0-9_./-]*[A-Za-z0-9_])`)

// prebuiltHelperCall matches a script's call of a prebuilt helper as a program with its operands in a list, as Node's child_process and Python's
// subprocess call it: ("./test/parity/testdata/prebuilt-go-test.sh", ["./PACKAGE", ...]). Group 1 names the helper, group 2 is the list.
var prebuiltHelperCall = regexp.MustCompile(`prebuilt-go-(test|run)\.sh['"]\s*,\s*\[([^\]]*)\]`)

// prebuiltHelperList matches the same call written as one list, as Python's subprocess takes it: ["./test/parity/testdata/prebuilt-go-test.sh", "--binary", "./PACKAGE"].
var prebuiltHelperList = regexp.MustCompile(`\[\s*['"][^'"]*prebuilt-go-(test|run)\.sh['"]\s*,([^\]]*)\]`)

// quotedPackage matches a ./-relative package in a quoted string of a list.
var quotedPackage = regexp.MustCompile(`['"](\./[A-Za-z0-9_./-]*[A-Za-z0-9_])['"]`)

// Every cli-mode scenario keeps the Go toolchain out of its timed runs, on both sides: a pig side may be `go run ./PACKAGE ARGS`, which
// RunScenario builds first; a script of either side, in any language, runs Go tests only through prebuilt-go-test.sh and Go programs only
// through prebuilt-go-run.sh, for exactly the packages its side lists in pig_go_tests, pi_go_tests, pig_go_programs or pi_go_programs.
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
		cwd := resolveScenarioCWD(sc.SourcePath, sc.CLI.CWD)
		pigCLI, piCLI := sc.CLI.Args, sc.CLI.Args
		if sc.CLI.PigArgs != nil {
			pigCLI = sc.CLI.PigArgs
		}
		if sc.CLI.PiArgs != nil {
			piCLI = sc.CLI.PiArgs
		}
		for _, side := range []struct {
			label, bin       string
			argv             []string
			goTests, goProgs []string
		}{
			{"pig", sc.Env.PigBin, slices.Concat(sc.Env.PigArgs, pigCLI), sc.Env.PigGoTests, sc.Env.PigGoPrograms},
			{"pi", sc.Env.PiBin, slices.Concat(sc.Env.PiArgs, piCLI), sc.Env.PiGoTests, sc.Env.PiGoPrograms},
		} {
			argv := side.argv
			run := -1
			if side.label == "pig" {
				runArgs, index := goRunArgs(sc)
				run = index
				if runArgs == &sc.CLI.PigArgs {
					run += len(sc.Env.PigArgs)
				}
			}
			if filepath.Base(side.bin) == "go" || (slices.Contains(argv, "go") && slices.Index(argv, "go") != run) {
				t.Errorf("%s: %s_bin %q with args %q starts the Go toolchain inside the timed run", sc.SourcePath, side.label, side.bin, argv)
			}
			if run >= 0 && (run+2 >= len(argv) || !isLocalGoPackage(argv[run+2])) {
				t.Errorf("%s: pig args %q: write `go run ./PACKAGE ARGS`, which the runner builds before the timed runs", sc.SourcePath, argv)
			}
			tested, programs := map[string]bool{}, map[string]bool{}
			// A side may run a helper directly: pi_args = ["./test/parity/testdata/prebuilt-go-run.sh", "./PACKAGE", ARGS...].
			for i, arg := range argv {
				helper := filepath.Base(arg)
				if (helper == "prebuilt-go-test.sh" || helper == "prebuilt-go-run.sh") && i+1 < len(argv) {
					if helper == "prebuilt-go-run.sh" {
						programs[argv[i+1]] = true
					} else {
						tested[argv[i+1]] = true
					}
				}
			}
			for _, arg := range append([]string{side.bin}, argv...) {
				script := probeScript(sc, cwd, arg)
				if script == "" {
					continue
				}
				checked++
				data, err := os.ReadFile(script)
				if err != nil {
					t.Fatal(err)
				}
				comment := "#"
				if strings.HasSuffix(script, ".mjs") || strings.HasSuffix(script, ".js") || strings.HasSuffix(script, ".ts") {
					comment = "//"
				}
				for i, line := range strings.Split(string(data), "\n") {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, comment) || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*") {
						continue
					}
					if goToolchainCommand.MatchString(line) || goToolchainSpawn.MatchString(line) {
						t.Errorf("%s:%d: starts the Go toolchain inside the timed run; run Go tests through prebuilt-go-test.sh and Go programs through prebuilt-go-run.sh", script, i+1)
					}
					for _, match := range append(prebuiltHelperCall.FindAllStringSubmatch(line, -1), prebuiltHelperList.FindAllStringSubmatch(line, -1)...) {
						target := tested
						if match[1] == "run" {
							target = programs
						}
						for _, pkg := range quotedPackage.FindAllStringSubmatch(match[2], -1) {
							target[pkg[1]] = true
							if match[1] == "run" {
								break // the operands after a program's package are its arguments
							}
						}
					}
					_, operands, ok := strings.Cut(line, "prebuilt-go-test.sh")
					if !ok || prebuiltHelperCall.MatchString(line) || prebuiltHelperList.MatchString(line) {
						continue
					}
					operands, _, _ = strings.Cut(quotedShellWord.ReplaceAllString(operands, "''"), "|")
					for _, match := range prebuiltGoTestPackage.FindAllStringSubmatch(operands, -1) {
						tested[match[1]] = true
					}
				}
			}
			for _, kind := range []struct {
				name   string
				found  map[string]bool
				listed []string
			}{{side.label + "_go_tests", tested, side.goTests}, {side.label + "_go_programs", programs, side.goProgs}} {
				for pkg := range kind.found {
					if !slices.Contains(kind.listed, pkg) {
						t.Errorf("%s: a %s script runs %s through its prebuilt helper: add it to [env] %s", sc.SourcePath, side.label, pkg, kind.name)
					}
				}
				for _, pkg := range kind.listed {
					if !kind.found[pkg] {
						t.Errorf("%s: [env] %s lists %s, which no %s script runs", sc.SourcePath, kind.name, pkg, side.label)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no cli-mode script found; the guard no longer sees the scripts")
	}
}

// probeScript returns the script file arg names for a side of sc (shell, Node, Python, or any file with a shebang), resolved as the scenario
// and its cwd resolve it, or "" when arg names none.
func probeScript(sc *Scenario, cwd, arg string) string {
	if arg == "" || arg == posixEnvLauncher || strings.HasPrefix(arg, "-") || strings.Contains(arg, "{{") {
		return ""
	}
	candidates := []string{resolveScenarioCWD(sc.SourcePath, arg)}
	if cwd != "" && !filepath.IsAbs(arg) {
		candidates = append(candidates, filepath.Join(cwd, arg))
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		switch filepath.Ext(path) {
		case ".sh", ".mjs", ".js", ".py":
			return path
		}
		if strings.HasPrefix(string(data), "#!") {
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
