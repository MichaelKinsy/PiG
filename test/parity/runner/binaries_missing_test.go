//go:build parity

package runner

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// A parity run that cannot find its comparator or its pig must fail, not skip: a skipped TestParity exits 0 with zero scenarios compared, which `make parity-family` used to report as ok. PIG_PARITY_ALLOW_ZERO=1 is the explicit request to accept that outcome, the same opt-out require-parity-ran honors.
const missingBinaryHelperEnv = "PIG_TEST_MISSING_BINARY_HELPER"

// TestMissingBinaryHelperProcess is a subprocess entry point, not a test: a failing or skipping subtest cannot be observed in-process.
func TestMissingBinaryHelperProcess(t *testing.T) {
	switch os.Getenv(missingBinaryHelperEnv) {
	case "pi":
		ResolveUpstreamPiBin(t)
	case "pig":
		ResolvePigBin(t)
	default:
		t.Skip("helper process entry point")
	}
}

func runMissingBinaryHelper(t *testing.T, resolver string, extraEnv ...string) helperOutcome {
	t.Helper()
	dir := ""
	if resolver == "pi" {
		// The resolver finds the source-built oracle by walking up from the working directory to extensions/sdk-ts; a directory outside the repository has none.
		dir = t.TempDir()
	}
	return runTestSubprocess(t, "TestMissingBinaryHelperProcess", dir, append([]string{missingBinaryHelperEnv + "=" + resolver}, extraEnv...))
}

// runTestSubprocess runs one test of this package in a child process with env applied over the inherited environment, and reports how it ended.
func runTestSubprocess(t *testing.T, test, dir string, env []string) helperOutcome {
	t.Helper()
	overridden := map[string]bool{}
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		overridden[key] = true
	}
	childEnv := slices.Clone(env)
	for _, kv := range os.Environ() {
		if key, _, _ := strings.Cut(kv, "="); !overridden[key] {
			childEnv = append(childEnv, kv)
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^"+test+"$", "-test.v")
	cmd.Env = childEnv
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	outcome := helperOutcome{output: string(out)}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		outcome.skipped = strings.Contains(outcome.output, "--- SKIP")
		outcome.passed = !outcome.skipped
	case errors.As(err, &exitErr):
		outcome.failed = true
	default:
		t.Fatalf("run %s in a child process: %v\n%s", test, err, out)
	}
	return outcome
}

// The frame-pacing notice test is the one unit test of this package that needs a built pig. With no binary provided at all it skips and names the missing input; a path that is set but missing still fails, as for TestParity.
func TestFramePacingNoticeTestSkipsOnlyWhenNoPigBinaryIsProvided(t *testing.T) {
	const test = "TestFramePacingNoticeIsTheOnlyDifferenceUnderAdaptivePacing"
	unset := runTestSubprocess(t, test, "", []string{"PIG_PARITY_PIG_BIN=", "PIG_BIN=", "PIG_PARITY_ALLOW_ZERO="})
	if unset.failed || !unset.skipped || !strings.Contains(unset.output, "set PIG_PARITY_PIG_BIN") {
		t.Fatalf("no pig binary provided: %+v, want a skip that names PIG_PARITY_PIG_BIN\n%s", unset, unset.output)
	}
	missing := filepath.Join(t.TempDir(), "no-such-pig")
	set := runTestSubprocess(t, test, "", []string{"PIG_PARITY_PIG_BIN=" + missing, "PIG_BIN=", "PIG_PARITY_ALLOW_ZERO="})
	if !set.failed || set.skipped || set.passed {
		t.Fatalf("PIG_PARITY_PIG_BIN set to a missing path: %+v, want failed=true\n%s", set, set.output)
	}
}

func emptyHomeEnv(t *testing.T) []string {
	home := t.TempDir()
	return []string{"HOME=" + home, "USERPROFILE=" + home, "PIG_PARITY_PI_BIN=", "PIG_PARITY_REAL_AUTH="}
}

func TestResolveUpstreamPiBinFailsWhenNotInstalledByDefault(t *testing.T) {
	outcome := runMissingBinaryHelper(t, "pi", append(emptyHomeEnv(t), "PIG_PARITY_ALLOW_ZERO=")...)
	if !outcome.failed || outcome.skipped || outcome.passed {
		t.Fatalf("no installed pi: %+v, want failed=true (a missing comparator must not skip the parity run)\n%s", outcome, outcome.output)
	}
}

func TestResolveUpstreamPiBinFailsWhenOverrideIsMissingByDefault(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-pi")
	outcome := runMissingBinaryHelper(t, "pi", "PIG_PARITY_PI_BIN="+missing, "PIG_PARITY_ALLOW_ZERO=")
	if !outcome.failed || outcome.skipped || outcome.passed {
		t.Fatalf("missing PIG_PARITY_PI_BIN: %+v, want failed=true\n%s", outcome, outcome.output)
	}
}

func TestResolveUpstreamPiBinMissingSkipsOnlyWithExplicitOptOut(t *testing.T) {
	outcome := runMissingBinaryHelper(t, "pi", append(emptyHomeEnv(t), "PIG_PARITY_ALLOW_ZERO=1")...)
	if outcome.failed || !outcome.skipped {
		t.Fatalf("no installed pi with PIG_PARITY_ALLOW_ZERO=1: %+v, want skipped=true\n%s", outcome, outcome.output)
	}
}

func TestResolvePigBinFailsWhenUnsetByDefault(t *testing.T) {
	outcome := runMissingBinaryHelper(t, "pig", "PIG_PARITY_PIG_BIN=", "PIG_BIN=", "PIG_PARITY_ALLOW_ZERO=")
	if !outcome.failed || outcome.skipped || outcome.passed {
		t.Fatalf("no pig binary under test: %+v, want failed=true\n%s", outcome, outcome.output)
	}
}

func TestResolvePigBinFailsWhenMissingByDefault(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-pig")
	outcome := runMissingBinaryHelper(t, "pig", "PIG_PARITY_PIG_BIN="+missing, "PIG_BIN=", "PIG_PARITY_ALLOW_ZERO=")
	if !outcome.failed || outcome.skipped || outcome.passed {
		t.Fatalf("missing pig binary: %+v, want failed=true\n%s", outcome, outcome.output)
	}
}

func TestResolvePigBinMissingSkipsOnlyWithExplicitOptOut(t *testing.T) {
	outcome := runMissingBinaryHelper(t, "pig", "PIG_PARITY_PIG_BIN=", "PIG_BIN=", "PIG_PARITY_ALLOW_ZERO=1")
	if outcome.failed || !outcome.skipped {
		t.Fatalf("no pig binary with PIG_PARITY_ALLOW_ZERO=1: %+v, want skipped=true\n%s", outcome, outcome.output)
	}
}

// A published Pi that mise installed must not become the oracle on its own: a run without PIG_PARITY_PI_PUBLISHED=1 and without a
// source-built oracle fails instead of comparing against the published release (the silent fallback an unset PIG_PARITY_PI_BIN used to take).
func TestResolveUpstreamPiBinNeverFallsBackToAMiseInstallWithoutOptIn(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "share", "mise", "installs", upstreamPiInstallDirs[0], coding.UpstreamPackageVersion, "bin", "pi")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho "+coding.UpstreamPackageVersion+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "PIG_PARITY_PI_BIN=", "PIG_PARITY_REAL_AUTH=", "PIG_PARITY_ALLOW_ZERO="}
	if outcome := runMissingBinaryHelper(t, "pi", append(env, "PIG_PARITY_PI_PUBLISHED=")...); !outcome.failed {
		t.Fatalf("a mise-installed Pi was used without the opt-in: %+v\n%s", outcome, outcome.output)
	}
	if coding.UpstreamVersion == coding.UpstreamPackageVersion {
		if outcome := runMissingBinaryHelper(t, "pi", append(env, "PIG_PARITY_PI_PUBLISHED=1")...); outcome.failed {
			t.Fatalf("the mise-installed Pi was refused despite the opt-in: %+v\n%s", outcome, outcome.output)
		}
	}
}
