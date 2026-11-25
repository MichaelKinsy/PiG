// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func baseSyntheticModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod": "module example.com/synth\n\ngo 1.24.0\n",
		"pure/math.go": `package pure

func Add(a, b int) int { return a + b }
`,
		"pure/math_test.go": `package pure

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("bad sum")
	}
}
`,
		"internal/testenv/testenv.go": `package testenv

import (
	"os/exec"
	"testing"
)

func BaselineGoroutines() int { return 0 }
func Bash(t testing.TB) string {
	_, _ = exec.LookPath("bash")
	return "bash"
}
`,
		"internal/pioracle/pioracle.go": `package pioracle

import "testing"

func Bin(t testing.TB) string { return "pi" }
`,
	})
	return root
}

func analyzeOne(t *testing.T, root, pattern string, race bool) PackageResult {
	t.Helper()
	sum, err := Analyze(Config{
		Root:         root,
		Patterns:     []string{pattern},
		Platforms:    DefaultPlatforms,
		IncludeEmpty: true,
		Race:         race,
	})
	if err != nil {
		t.Fatalf("Analyze(%s): %v", pattern, err)
	}
	if len(sum.Packages) != 1 {
		t.Fatalf("Analyze(%s) returned %d packages, want 1", pattern, len(sum.Packages))
	}
	return sum.Packages[0]
}

func TestSyntheticHermeticBaseline(t *testing.T) {
	root := baseSyntheticModule(t)
	res := analyzeOne(t, root, "./pure", false)
	if !res.Hermetic {
		t.Fatalf("pure package should be hermetic, got reasons: %+v", res.Reasons)
	}
	if !res.HasTests {
		t.Fatal("pure package should report HasTests=true")
	}
}

// Mutation tests: start from a provably hermetic synthetic package and inject
// each class of non-hermetic side effect. Every mutation MUST immediately flip
// the package from hermetic to non-hermetic (fail-closed).
func TestMutationsFlipHermeticToNonHermetic(t *testing.T) {
	cases := []struct {
		name       string
		relFile    string
		content    string
		wantCode   string
		extraSetup func(t *testing.T, root string)
	}{
		{
			name:    "os/exec.Command in test",
			relFile: "pure/mut_test.go",
			content: `package pure

import (
	"os/exec"
	"testing"
)

func TestExec(t *testing.T) {
	_ = exec.Command("true")
}
`,
			wantCode: "import:os/exec",
		},
		{
			name:    "net.Listen in test",
			relFile: "pure/mut_test.go",
			content: `package pure

import (
	"net"
	"testing"
)

func TestListen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		_ = ln.Close()
	}
}
`,
			wantCode: "import:net",
		},
		{
			name:    "net/http listener in test",
			relFile: "pure/mut_test.go",
			content: `package pure

import (
	"net/http"
	"testing"
)

func TestHTTP(t *testing.T) {
	_ = http.DefaultClient
}
`,
			wantCode: "import:net/http",
		},
		{
			name:    "os.Getenv in test",
			relFile: "pure/mut_test.go",
			content: `package pure

import (
	"os"
	"testing"
)

func TestEnv(t *testing.T) {
	_ = os.Getenv("HOME")
}
`,
			wantCode: "os-side-effect",
		},
		{
			name:    "os.LookupEnv in production file",
			relFile: "pure/mut.go",
			content: `package pure

import "os"

func ReadFlag() (string, bool) {
	return os.LookupEnv("PIG_MODE")
}
`,
			wantCode: "os-side-effect",
		},
		{
			name:    "syscall.ForkExec on linux only",
			relFile: "pure/mut_linux_test.go",
			content: `//go:build linux

package pure

import (
	"syscall"
	"testing"
)

func TestForkExec(t *testing.T) {
	_ = syscall.ForkExec
}
`,
			wantCode: "syscall-side-effect",
		},
		{
			name:    "windows-only os/exec caught by cross-OS scan",
			relFile: "pure/mut_windows_test.go",
			content: `//go:build windows

package pure

import (
	"os/exec"
	"testing"
)

func TestWinExec(t *testing.T) {
	_ = exec.Command("cmd.exe", "/c", "echo")
}
`,
			wantCode: "import:os/exec",
		},
		{
			name:    "darwin-only os.Getenv caught by cross-OS scan",
			relFile: "pure/mut_darwin_test.go",
			content: `//go:build darwin

package pure

import (
	"os"
	"testing"
)

func TestDarwinEnv(t *testing.T) {
	_ = os.Getenv("TERM_PROGRAM")
}
`,
			wantCode: "os-side-effect",
		},
		{
			name:    "Pi oracle package import",
			relFile: "pure/mut_test.go",
			content: `package pure

import (
	"example.com/synth/internal/pioracle"
	"testing"
)

func TestOracle(t *testing.T) {
	_ = pioracle.Bin(t)
}
`,
			wantCode: "oracle",
		},
		{
			name:    "Pi oracle env token reference",
			relFile: "pure/mut_test.go",
			content: `package pure

import "testing"

func TestOracleToken(t *testing.T) {
	_ = "PIG_PARITY_PI_BIN"
}
`,
			wantCode: "external-env-ref",
		},
		{
			name:    "tmux reference in test",
			relFile: "pure/mut_test.go",
			content: `package pure

import "testing"

func TestTmux(t *testing.T) {
	_ = "TMUX"
}
`,
			wantCode: "external-env-ref",
		},
		{
			name:    "non-allowlisted testenv helper",
			relFile: "pure/mut_test.go",
			content: `package pure

import (
	"example.com/synth/internal/testenv"
	"testing"
)

func TestBash(t *testing.T) {
	_ = testenv.Bash(t)
}
`,
			wantCode: "testenv-side-effect",
		},
		{
			name:    "plugin import",
			relFile: "pure/mut_linux_test.go",
			content: `//go:build linux

package pure

import (
	"plugin"
	"testing"
)

func TestPlugin(t *testing.T) {
	_, _ = plugin.Open("missing.so")
}
`,
			wantCode: "import:plugin",
		},
		{
			name:    "runtime/cgo import",
			relFile: "pure/mut_test.go",
			content: `package pure

import (
	_ "runtime/cgo"
	"testing"
)

func TestCgo(t *testing.T) {}
`,
			wantCode: "import:runtime/cgo",
		},
		{
			name:    "transitive non-hermetic dependency",
			relFile: "pure/mut_test.go",
			content: `package pure

import (
	"example.com/synth/helper"
	"testing"
)

func TestViaHelper(t *testing.T) {
	_ = helper.Run()
}
`,
			extraSetup: func(t *testing.T, root string) {
				writeFiles(t, root, map[string]string{
					"helper/helper.go": `package helper

import "os/exec"

func Run() error { return exec.Command("true").Err }
`,
				})
			},
			wantCode: "import:os/exec",
		},
		{
			name:     "on-disk testdata directory fails closed",
			relFile:  "pure/testdata/sample.json",
			content:  `{"k":"v"}`,
			wantCode: "external-testdata",
		},
		{
			name:    "embed external artifact fails closed",
			relFile: "pure/embed.go",
			content: `package pure

import _ "embed"

//go:embed runtime-node.pack
var pack []byte
`,
			extraSetup: func(t *testing.T, root string) {
				writeFiles(t, root, map[string]string{
					"pure/runtime-node.pack": "binary",
				})
			},
			wantCode: "external-embed",
		},
		{
			name:    "parent directory read via os.ReadFile",
			relFile: "pure/mut_test.go",
			content: `package pure

import (
	"os"
	"testing"
)

func TestParentRead(t *testing.T) {
	_, _ = os.ReadFile("../go.mod")
}
`,
			wantCode: "external-fixture",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := baseSyntheticModule(t)
			// Prove baseline is hermetic before mutation.
			before := analyzeOne(t, root, "./pure", false)
			if !before.Hermetic {
				t.Fatalf("baseline unexpectedly non-hermetic: %+v", before.Reasons)
			}
			if tc.extraSetup != nil {
				tc.extraSetup(t, root)
			}
			writeFiles(t, root, map[string]string{tc.relFile: tc.content})

			after := analyzeOne(t, root, "./pure", false)
			if after.Hermetic {
				t.Fatalf("mutation %q remained hermetic; expected fail-closed rejection with code %q", tc.name, tc.wantCode)
			}
			found := false
			for _, r := range after.Reasons {
				if r.Code == tc.wantCode {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("mutation %q reasons = %+v; want code %q", tc.name, after.Reasons, tc.wantCode)
			}
		})
	}
}

func TestAllowlistedTestenvSymbolStaysHermetic(t *testing.T) {
	root := baseSyntheticModule(t)
	writeFiles(t, root, map[string]string{
		"pure/goroutines_test.go": `package pure

import (
	"example.com/synth/internal/testenv"
	"testing"
)

func TestGoroutines(t *testing.T) {
	_ = testenv.BaselineGoroutines()
}
`,
	})
	res := analyzeOne(t, root, "./pure", false)
	if !res.Hermetic {
		t.Fatalf("allowlisted testenv helper should stay hermetic, got reasons: %+v", res.Reasons)
	}
}

func TestRaceForcesEveryPackageNonHermetic(t *testing.T) {
	root := baseSyntheticModule(t)
	res := analyzeOne(t, root, "./pure", true)
	if res.Hermetic {
		t.Fatal("expected -race mode to force pure package non-hermetic")
	}
	if len(res.Reasons) == 0 || res.Reasons[0].Code != "race" {
		t.Fatalf("unexpected reasons under -race: %+v", res.Reasons)
	}
}

func TestCLIModesListCheckAndJSON(t *testing.T) {
	root := baseSyntheticModule(t)
	writeFiles(t, root, map[string]string{
		"impure/impure.go": `package impure

func Hello() string { return "hi" }
`,
		"impure/impure_test.go": `package impure

import (
	"os/exec"
	"testing"
)

func TestImpure(t *testing.T) { _ = exec.Command("true") }
`,
	})

	// --list-hermetic --rel
	var out, errBuf bytes.Buffer
	if code := run([]string{"--root", root, "--list-hermetic", "--rel"}, &out, &errBuf); code != 0 {
		t.Fatalf("--list-hermetic exited %d: %s", code, errBuf.String())
	}
	if strings.TrimSpace(out.String()) != "./pure" {
		t.Fatalf("--list-hermetic = %q, want ./pure", out.String())
	}

	// --list-nonhermetic --rel
	out.Reset()
	errBuf.Reset()
	if code := run([]string{"--root", root, "--list-nonhermetic", "--rel"}, &out, &errBuf); code != 0 {
		t.Fatalf("--list-nonhermetic exited %d: %s", code, errBuf.String())
	}
	if strings.TrimSpace(out.String()) != "./impure" {
		t.Fatalf("--list-nonhermetic = %q, want ./impure", out.String())
	}

	// --check passes on ./pure, fails on ./impure
	out.Reset()
	errBuf.Reset()
	if code := run([]string{"--root", root, "--check", "./pure"}, &out, &errBuf); code != 0 {
		t.Fatalf("--check ./pure exited %d: %s", code, errBuf.String())
	}
	out.Reset()
	errBuf.Reset()
	if code := run([]string{"--root", root, "--check", "./impure"}, &out, &errBuf); code != 1 {
		t.Fatalf("--check ./impure exited %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "NON-HERMETIC ./impure") {
		t.Fatalf("expected NON-HERMETIC diagnostic, got: %s", errBuf.String())
	}

	// --json emits valid Summary JSON
	out.Reset()
	errBuf.Reset()
	if code := run([]string{"--root", root, "--json"}, &out, &errBuf); code != 0 {
		t.Fatalf("--json exited %d: %s", code, errBuf.String())
	}
	var sum Summary
	if err := json.Unmarshal(out.Bytes(), &sum); err != nil {
		t.Fatalf("invalid json output: %v\n%s", err, out.String())
	}
	if sum.HermeticPackages != 1 || sum.NonHermeticPackages != 1 {
		t.Fatalf("summary counts = (%d hermetic, %d non-hermetic), want (1, 1)", sum.HermeticPackages, sum.NonHermeticPackages)
	}
}

// Verify automation/ci/test-grouped.sh opt-in hermetic cache behavior:
//  1. Default CI run (without PIG_GO_HERMETIC_TEST_CACHE=1): EVERY package keeps -count=1.
//  2. Opt-in PR run (PIG_GO_HERMETIC_TEST_CACHE=1, GITHUB_EVENT_NAME=pull_request):
//     only hermetic packages omit -count=1; non-hermetic packages keep -count=1.
//  3. Nightly / release / main / schedule runs ALWAYS keep -count=1 even with PIG_GO_HERMETIC_TEST_CACHE=1.
func TestGroupedScriptHermeticCachePolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash script fixture test runs on Unix")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	copyScript := func(rel string) {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{
		"automation/ci/test-grouped.sh",
		"automation/ci/test-shard-pattern.sh",
		"automation/ci/check-shard-union.sh",
		"automation/ci/assert-clean-tmp.sh",
		"automation/ci/agent-dir-guard.sh",
		"automation/ci/reap-test-processes.sh",
		"automation/ci/race-timeout.sh",
		"automation/ci/plain-timeout.sh",
	} {
		copyScript(rel)
	}
	writeFiles(t, root, map[string]string{
		"piglets/porter/extensions/pig-porter/.keep": "",
		"durable/core/driver/fault_core_test.go":     "//go:build pigfault\n\npackage driver\n\nfunc TestCoreFaultHeld(t *testing.T) {}\n",
		"automation/ci/test-fixtures.sh":             "#!/bin/sh\nprintf 'export CI_TEST_FIXTURES=ready\\n'\n",
		"hermetic.list":                              "./alpha\n",
	})
	if err := os.Chmod(filepath.Join(root, "automation/ci/test-fixtures.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGo := `#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  list)
    shift
    if [[ "$*" == "./..." ]]; then
      printf '%s\n' ./alpha ./beta ./coding/cli ./coding/extension/host/subprocess ./test/extension-conformance ./test/perfgate
    else
      printf '%s\n' "$@"
    fi
    ;;
  test)
    printf '%s\n' "$*" >> "$ARGS_LOG"
    ;;
  *) exit 0 ;;
esac
`
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}

	runCase := func(name string, extraEnv []string) []string {
		t.Helper()
		argsLog := filepath.Join(root, name+".log")
		_ = os.Remove(argsLog)
		cmd := exec.Command("bash", filepath.Join(root, "automation/ci/test-grouped.sh"), "fast-parallel")
		cmd.Dir = root
		env := append([]string{}, os.Environ()...)
		env = append(env,
			"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"ARGS_LOG="+argsLog,
			"CI=true",
			"PIG_HERMETIC_PKGS_FILE="+filepath.Join(root, "hermetic.list"),
		)
		env = append(env, extraEnv...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		data, err := os.ReadFile(argsLog)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}

	// 1. PR with opt-in enabled: ./alpha omits -count=1, ./beta keeps -count=1.
	prLines := runCase("pr-enabled", []string{
		"PIG_GO_HERMETIC_TEST_CACHE=1",
		"GITHUB_EVENT_NAME=pull_request",
		"GITHUB_REF=refs/pull/42/merge",
	})
	var sawAlphaCached, sawBetaCount1 bool
	for _, line := range prLines {
		if strings.HasSuffix(line, " ./alpha") {
			if strings.Contains(line, "-count=1") {
				t.Errorf("PR opt-in: hermetic ./alpha unexpectedly passed -count=1: %s", line)
			}
			sawAlphaCached = true
		}
		if strings.HasSuffix(line, " ./beta") {
			if !strings.Contains(line, "-count=1") {
				t.Errorf("PR opt-in: non-hermetic ./beta missing -count=1: %s", line)
			}
			sawBetaCount1 = true
		}
	}
	if !sawAlphaCached || !sawBetaCount1 {
		t.Fatalf("PR opt-in did not split ./alpha and ./beta as expected:\n%s", strings.Join(prLines, "\n"))
	}

	// 2. Nightly schedule with opt-in set MUST still enforce -count=1 everywhere.
	for _, tc := range []struct {
		name, event, ref string
	}{
		{"schedule", "schedule", "refs/heads/main"},
		{"release-push", "push", "refs/heads/release/0.8.0"},
		{"main-push", "push", "refs/heads/main"},
		{"opt-in-off", "pull_request", "refs/pull/42/merge"},
	} {
		opt := "PIG_GO_HERMETIC_TEST_CACHE=1"
		if tc.name == "opt-in-off" {
			opt = "PIG_GO_HERMETIC_TEST_CACHE=0"
		}
		lines := runCase(tc.name, []string{opt, "GITHUB_EVENT_NAME=" + tc.event, "GITHUB_REF=" + tc.ref})
		for _, line := range lines {
			if !strings.Contains(line, "-count=1") {
				t.Errorf("%s: expected -count=1 on every go test call, got: %s", tc.name, line)
			}
		}
	}
}
