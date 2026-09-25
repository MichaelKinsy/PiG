//go:build parity

package runner

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

func TestResolvePigBinHonorsFreshBuildOverride(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pig-under-test")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake pig: %v", err)
	}
	t.Setenv("PIG_PARITY_PIG_BIN", bin)
	t.Setenv("PIG_BIN", filepath.Join(t.TempDir(), "wrong-pig"))

	got := ResolvePigBin(t)
	if got.Path != bin {
		t.Fatalf("ResolvePigBin path = %q, want PIG_PARITY_PIG_BIN %q", got.Path, bin)
	}
	if got.Label != "pig" {
		t.Fatalf("ResolvePigBin label = %q", got.Label)
	}
}

func TestResolvePigBinFallsBackToPIGBin(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pig-from-pig-bin")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake pig: %v", err)
	}
	t.Setenv("PIG_PARITY_PIG_BIN", "")
	t.Setenv("PIG_BIN", bin)

	got := ResolvePigBin(t)
	if got.Path != bin {
		t.Fatalf("ResolvePigBin path = %q, want PIG_BIN %q", got.Path, bin)
	}
}

// TestPigBinUnderTestNoInstalledFallback pins that neither env var set resolves
// to empty: the runner must never borrow an installed ~/.local/bin/pig, so a
// bare `go test` skips loudly instead of comparing against user state.
func TestPigBinUnderTestNoInstalledFallback(t *testing.T) {
	t.Setenv("PIG_PARITY_PIG_BIN", "")
	t.Setenv("PIG_BIN", "")
	if got := pigBinUnderTest(); got != "" {
		t.Fatalf("pigBinUnderTest() = %q, want empty (no installed-pig fallback)", got)
	}
}

func TestPigBinUnderTestPrefersParityEnv(t *testing.T) {
	t.Setenv("PIG_PARITY_PIG_BIN", "/parity/pig")
	t.Setenv("PIG_BIN", "/other/pig")
	if got := pigBinUnderTest(); got != "/parity/pig" {
		t.Fatalf("pigBinUnderTest() = %q, want PIG_PARITY_PIG_BIN precedence", got)
	}
}

func TestResolveUpstreamPiBinStagesOptedInRealAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	authPath := filepath.Join(home, ".pig", "agent", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	auth := []byte(`{"github-copilot":{"type":"oauth","access":"token"}}`)
	if err := os.WriteFile(authPath, auth, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_PARITY_REAL_AUTH", authPath)
	bin := filepath.Join(t.TempDir(), "pi-under-test")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' '"+coding.UpstreamVersion+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_PARITY_PI_BIN", bin)
	resolved := ResolveUpstreamPiBin(t)
	var agentDir string
	for _, value := range resolved.Env {
		if after, ok := strings.CutPrefix(value, "PI_CODING_AGENT_DIR="); ok {
			agentDir = after
		}
	}
	got, err := os.ReadFile(filepath.Join(agentDir, "auth.json"))
	if err != nil || !bytes.Equal(got, auth) {
		t.Fatalf("staged auth = %q, %v", got, err)
	}
}

// TestResolveUpstreamPiBinNeverStagesRealAuthWithoutOptIn is the regression
// guard for the privacy bug: real ~/.pig or ~/.pi credentials must never be
// copied into Pi's parity agent directory unless the operator explicitly
// names the file via PIG_PARITY_REAL_AUTH. Before the fix, ResolveUpstreamPiBin
// scanned $HOME/.pi and $HOME/.pig unconditionally and staged whatever it
// found, so this test failed (staged auth present) on the old code.
func TestResolveUpstreamPiBinNeverStagesRealAuthWithoutOptIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PIG_PARITY_REAL_AUTH", "")
	authPath := filepath.Join(home, ".pig", "agent", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	realCreds := []byte(`{"github-copilot":{"type":"oauth","access":"do-not-leak-me"}}`)
	if err := os.WriteFile(authPath, realCreds, 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "pi-under-test")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' '"+coding.UpstreamVersion+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_PARITY_PI_BIN", bin)
	resolved := ResolveUpstreamPiBin(t)
	var agentDir string
	for _, value := range resolved.Env {
		if after, ok := strings.CutPrefix(value, "PI_CODING_AGENT_DIR="); ok {
			agentDir = after
		}
	}
	if _, err := os.Stat(filepath.Join(agentDir, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("real credentials staged without an opt-in: err=%v", err)
	}
}

// versionMismatchHelperEnv selects the TestVersionMismatchHelperProcess
// subprocess entry point used by the Test*VersionMismatch* tests below.
//
// Those tests need to observe whether ResolveUpstreamPiBin actually FAILS the
// test (not just whether it returns), and a failing subtest always marks its
// parent (and the whole test binary) failed too - there is no in-process way
// to call t.Fatalf/t.Skipf and merely observe the outcome. Re-executing this
// same compiled test binary as a child process (the standard os/exec_test.go
// technique) gives a real, isolated pass/fail/skip outcome to assert on.
const versionMismatchHelperEnv = "PIG_TEST_VERSION_MISMATCH_HELPER"

// TestVersionMismatchHelperProcess is not a real test; it is invoked only as
// a subprocess by runVersionMismatchHelper.
func TestVersionMismatchHelperProcess(t *testing.T) {
	if os.Getenv(versionMismatchHelperEnv) != "1" {
		t.Skip("helper process entry point")
	}
	ResolveUpstreamPiBin(t)
}

// helperOutcome is the observable result of running the helper subprocess:
// exactly one of failed/skipped/passed is true.
type helperOutcome struct {
	failed, skipped, passed bool
	output                  string
}

func runVersionMismatchHelper(t *testing.T, extraEnv ...string) helperOutcome {
	t.Helper()
	overridden := map[string]bool{}
	for _, kv := range extraEnv {
		if key, _, ok := strings.Cut(kv, "="); ok {
			overridden[key] = true
		}
	}
	env := []string{versionMismatchHelperEnv + "=1"}
	for _, kv := range os.Environ() {
		if key, _, ok := strings.Cut(kv, "="); ok && overridden[key] {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, extraEnv...)

	cmd := exec.Command(os.Args[0], "-test.run", "^TestVersionMismatchHelperProcess$", "-test.v")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	outcome := helperOutcome{output: string(out)}
	if err == nil {
		outcome.passed = !strings.Contains(outcome.output, "--- SKIP")
		outcome.skipped = strings.Contains(outcome.output, "--- SKIP")
	} else {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run version-mismatch helper: %v\n%s", err, out)
		}
		outcome.failed = true
	}
	return outcome
}

// TestResolveUpstreamPiBinFailsOnVersionMismatchByDefault is the regression
// guard for the false-green bug: a reference pi binary that fails its version
// check must FAIL the run, not skip it, so a broken or mismatched oracle
// cannot silently produce a green parity run. Before the fix this called
// t.Skipf, so this test failed (the helper skipped instead of failing) on the
// old code.
func TestResolveUpstreamPiBinFailsOnVersionMismatchByDefault(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pi-wrong-version")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '0.0.0-not-the-pin\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outcome := runVersionMismatchHelper(t, "PIG_PARITY_PI_BIN="+bin, "PIG_PARITY_ALLOW_VERSION_SKEW=")
	if !outcome.failed || outcome.skipped || outcome.passed {
		t.Fatalf("version mismatch: %+v, want failed=true (a mismatched reference pi must fail the run, not skip it)\n%s",
			outcome, outcome.output)
	}
}

// TestResolveUpstreamPiBinAllowsVersionSkewWithOptOut proves the explicit
// opt-out still works: PIG_PARITY_ALLOW_VERSION_SKEW=1 turns the failure back
// into a skip.
func TestResolveUpstreamPiBinAllowsVersionSkewWithOptOut(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pi-wrong-version")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '0.0.0-not-the-pin\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outcome := runVersionMismatchHelper(t, "PIG_PARITY_PI_BIN="+bin, "PIG_PARITY_ALLOW_VERSION_SKEW=1")
	if outcome.failed || !outcome.skipped {
		t.Fatalf("version mismatch with opt-out: %+v, want skipped=true\n%s", outcome, outcome.output)
	}
}

// TestResolveUpstreamPiBinFailsWhenVersionCheckErrorsByDefault covers the
// other half of the same guard: the binary exists but --version itself fails
// to run (a "broken" reference pi), which must also fail, not skip.
func TestResolveUpstreamPiBinFailsWhenVersionCheckErrorsByDefault(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pi-broken")
	// No shebang and not a valid executable format: exec fails outright.
	if err := os.WriteFile(bin, []byte("not an executable\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outcome := runVersionMismatchHelper(t, "PIG_PARITY_PI_BIN="+bin, "PIG_PARITY_ALLOW_VERSION_SKEW=")
	if !outcome.failed || outcome.skipped || outcome.passed {
		t.Fatalf("broken reference pi: %+v, want failed=true\n%s", outcome, outcome.output)
	}
}

func TestResolveUpstreamPiBinHonorsExplicitOverride(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pi-under-test")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' '"+coding.UpstreamVersion+"'\n"), 0o755); err != nil {
		t.Fatalf("write fake pi: %v", err)
	}
	t.Setenv("PIG_PARITY_PI_BIN", bin)

	got := ResolveUpstreamPiBin(t)
	if got.Path != bin {
		t.Fatalf("ResolveUpstreamPiBin path = %q, want PIG_PARITY_PI_BIN %q", got.Path, bin)
	}
	if got.Label != "pi" {
		t.Fatalf("ResolveUpstreamPiBin label = %q", got.Label)
	}
}
