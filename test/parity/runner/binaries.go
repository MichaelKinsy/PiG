//go:build parity

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// BinaryRef describes one binary to run a scenario against.
type BinaryRef struct {
	Label string   // "pig" or "pi"
	Path  string   // absolute path
	Args  []string // base args prepended to every scenario invocation
	Env   []string // base env vars (KEY=value)
}

// upstreamPiInstallDirs lists the mise install directories we accept as
// homes for the upstream pi comparator binary, in priority order.
//
// The upstream package was renamed when the project moved from
// earendil-works/pi to earendil-works/pi in May 2026. The OLD npm package
// (@mariozechner/pi-coding-agent) stopped publishing at v0.73.1; v0.74+
// only exists under the NEW name (@earendil-works/pi-coding-agent).
// We prefer the new path. Old path is kept as fallback for legacy installs.
var upstreamPiInstallDirs = []string{
	"npm-earendil-works-pi-coding-agent",
	"npm-mariozechner-pi-coding-agent",
}

// pigBinUnderTest resolves the explicitly provided pig binary: PIG_PARITY_PIG_BIN
// then PIG_BIN. It returns "" when neither is set, and intentionally has no
// installed-pig fallback so the runner never reads user state.
func pigBinUnderTest() string {
	if bin := os.Getenv("PIG_PARITY_PIG_BIN"); bin != "" {
		return bin
	}
	return os.Getenv("PIG_BIN")
}

// ResolvePigBin returns a BinaryRef for the pig binary under test.
//
// Quality-control rule: parity runs against an explicitly provided, freshly
// built pig, never a silently borrowed install. Resolve PIG_PARITY_PIG_BIN then
// PIG_BIN; if neither is set, skip rather than fall back to ~/.local/bin/pig, so
// the runner cannot compare upstream pi against stale or unrelated user state.
// requireFreshPig still guards an explicitly provided binary against staleness.
func ResolvePigBin(t *testing.T) BinaryRef {
	t.Helper()
	bin := pigBinUnderTest()
	if bin == "" {
		t.Skip("no pig binary under test: build one (`go build -o bin/pig-parity ./cmd/pig`) " +
			"and set PIG_PARITY_PIG_BIN, or set PIG_BIN; `make parity` does this. The runner " +
			"never falls back to an installed pig so it cannot silently compare against user state.")
	}
	if !filepath.IsAbs(bin) {
		abs, err := filepath.Abs(bin)
		if err == nil {
			bin = abs
		}
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("pig binary not found at %s: build it and set PIG_PARITY_PIG_BIN or PIG_BIN", bin)
	}

	// Guard default pig state the same way ResolveUpstreamPiBin guards pi
	// state. Parity scenarios must never read or mutate the user's real ~/.pig
	// unless a scenario explicitly overrides PIG_HOME/PIG_CODING_AGENT_DIR.
	//
	// The runner never reads the operator's real ~/.pig or ~/.pi credentials
	// here by default: requires-auth scenarios would otherwise silently see
	// them on any machine where the operator happens to be logged in. Stage
	// real credentials only when PIG_PARITY_REAL_AUTH names the exact
	// auth.json to use; see realAuthSourcePath.
	safeHome := t.TempDir()
	safeAgentDir := filepath.Join(safeHome, "agent")
	_ = os.MkdirAll(safeAgentDir, 0o700)
	if src := realAuthSourcePath(); src != "" {
		if data, err := os.ReadFile(src); err == nil {
			_ = os.WriteFile(filepath.Join(safeAgentDir, "auth.json"), data, 0o600)
		}
	}

	return BinaryRef{
		Label: "pig",
		Path:  bin,
		Env: []string{
			"PIG_USE_PI_DIRS=", // Sharing is selected only by a scenario, never by the operator's environment.
			"PIG_QUIET_STARTUP=1",
			"PIG_HOME=" + safeHome,
			"PIG_CODING_AGENT_DIR=", // Derive from PIG_HOME unless the scenario explicitly overrides the agent directory.
			// The same startup release-check switch the Pi oracle receives, so an
			// operator's PIG_UPDATE_URL cannot draw a D39 update notice and both
			// binaries hand extensions the same value.
			"PI_SKIP_VERSION_CHECK=1",
		},
	}
}

// allowParityVersionSkew reports whether the operator explicitly opted into
// running parity against a reference pi binary that failed its version
// check. See failOrSkipVersionMismatch.
func allowParityVersionSkew() bool {
	return os.Getenv("PIG_PARITY_ALLOW_VERSION_SKEW") != ""
}

// failOrSkipVersionMismatch reports a broken or mismatched reference pi
// binary. It FAILS the run by default: a version check that only skips (as it
// did before) lets a broken or mismatched pi produce a green-looking parity
// run, because TestParity returns before any scenario executes and `go test`
// still exits 0 (see require-parity-ran in automation/make/parity.mk, which
// treats that as a hard failure only via the results file, not via the test's
// own exit code). Set PIG_PARITY_ALLOW_VERSION_SKEW=1 to accept a version
// mismatch on purpose (mirrors PIG_PARITY_ALLOW_ZERO's opt-out pattern for
// require-parity-ran).
func failOrSkipVersionMismatch(t *testing.T, reason string) {
	t.Helper()
	if allowParityVersionSkew() {
		t.Skipf("%s (PIG_PARITY_ALLOW_VERSION_SKEW set: accepting on purpose)", reason)
		return
	}
	t.Fatalf("%s; a broken or mismatched reference pi must fail parity, not silently skip it. "+
		"Set PIG_PARITY_ALLOW_VERSION_SKEW=1 to accept this on purpose.", reason)
}

// ResolveUpstreamPiBin finds the highest-versioned upstream pi binary
// installed by mise.
//
// The oracle runs with PI_SKIP_VERSION_CHECK=1 (pi utils/version-check.ts
// checkForNewPiVersion). Otherwise Pi fetches https://pi.dev/api/latest-version
// and draws an "Update Available" box whenever the live release is newer than
// the pinned version, so a capture that outlasts the reply differs from run to
// run for reasons unrelated to PiG. A scenario that needs the check overrides
// the variable in its own [env].
func ResolveUpstreamPiBin(t *testing.T) BinaryRef {
	t.Helper()
	home, _ := os.UserHomeDir()
	want := coding.UpstreamVersion

	// Guard: default PI_HOME and PI_CODING_AGENT_DIR to ephemeral temp
	// dirs so scenarios that forget to declare them in their [env]
	// section can never pollute the user's real ~/.pi/ config.
	// PI_CODING_AGENT_DIR is the env var pi actually reads (not PI_HOME).
	// Scenarios that DO set these override via the driver's env-append
	// (last-wins), so the guard is invisible to well-written scenarios.
	safeHome := t.TempDir()
	safeAgentDir := filepath.Join(safeHome, "agent")
	_ = os.MkdirAll(safeAgentDir, 0o700)

	// The runner never reads the operator's real ~/.pi or ~/.pig credentials
	// here by default: requires-auth scenarios would otherwise silently see
	// them on any machine where the operator happens to be logged in. Stage
	// real credentials only when PIG_PARITY_REAL_AUTH names the exact
	// auth.json to use; see realAuthSourcePath.
	if src := realAuthSourcePath(); src != "" {
		if data, err := os.ReadFile(src); err == nil {
			_ = os.WriteFile(filepath.Join(safeAgentDir, "auth.json"), data, 0o600)
		}
	}

	// PIG_PARITY_PI_VERSION carries the exact pin to pi_bin wrapper scripts
	// (e.g. pi-ai-copilot-wrapper.mjs) that locate a pi-ai build by scanning
	// mise install dirs. Without it they pick the highest-installed version,
	// which silently diverges from the oracle pin once a newer pi is installed
	// (e.g. during a bump) - the exact skew this function forbids for the
	// direct pi path.
	piEnv := []string{"PI_HOME=" + safeHome, "PI_CODING_AGENT_DIR=" + safeAgentDir, "PIG_PARITY_PI_VERSION=" + want, "PI_SKIP_VERSION_CHECK=1"}

	// CI parity images bake the exact upstream oracle outside mise's writable
	// home. Prefer the explicit binary when provided, but keep the same exact
	// version guard so oracle skew cannot pass silently.
	if bin := os.Getenv("PIG_PARITY_PI_BIN"); bin != "" {
		if !filepath.IsAbs(bin) {
			if abs, err := filepath.Abs(bin); err == nil {
				bin = abs
			}
		}
		if _, err := os.Stat(bin); err != nil {
			t.Skipf("PIG_PARITY_PI_BIN not found at %s", bin)
		}
		out, err := exec.Command(bin, "--version").Output()
		if err != nil {
			failOrSkipVersionMismatch(t, fmt.Sprintf("PIG_PARITY_PI_BIN version check failed at %s: %v", bin, err))
		} else if got := strings.TrimSpace(string(out)); got != want {
			failOrSkipVersionMismatch(t, fmt.Sprintf("PIG_PARITY_PI_BIN version = %s, want %s", got, want))
		}
		return BinaryRef{
			Label: "pi",
			Path:  bin,
			Args:  []string{"--no-extensions"},
			Env:   piEnv,
		}
	}

	// Try each known install dir, prefer the new earendil-works path.
	// Require an EXACT match for coding.UpstreamVersion so version skew
	// is impossible to ignore. If the pin is at 0.73.1, only mise's
	// 0.73.1 dir is acceptable: not 0.73.0, not 0.74.0, not "latest".
	var tried []string
	for _, dirName := range upstreamPiInstallDirs {
		bin := filepath.Join(home, ".local", "share", "mise", "installs", dirName, want, "bin", "pi")
		tried = append(tried, bin)
		if _, err := os.Stat(bin); err == nil {
			return BinaryRef{
				Label: "pi",
				Path:  bin,
				// Upstream needs --no-extensions by default so scenarios see a
				// minimal baseline matching pig's startup. Scenarios that need
				// extensions can override via Args in the scenario file.
				Args: []string{"--no-extensions"},
				Env:  piEnv,
			}
		}
	}

	t.Skipf("upstream pi v%s not installed (required by coding.UpstreamVersion). Tried:\n  %s\nInstall with:\n  mise install npm:@earendil-works/pi-coding-agent@%s",
		want, joinPaths(tried), want)
	return BinaryRef{} // unreachable after t.Skipf
}

func joinPaths(ps []string) string {
	out := ""
	for i, p := range ps {
		if i > 0 {
			out += "\n  "
		}
		out += p
	}
	return out
}

// UpstreamVersion returns the pi version label this run is comparing
// against. With the version-pinned resolver above this always equals
// coding.UpstreamVersion, but we still derive it from the binary path
// so a future resolver change shows through immediately.
func UpstreamVersion(ref BinaryRef) string {
	parts := splitPath(ref.Path)
	for i, p := range parts {
		for _, dirName := range upstreamPiInstallDirs {
			if p == dirName && i+1 < len(parts) {
				return parts[i+1]
			}
		}
	}
	return "unknown"
}

// splitPath returns p's names, root excluded. The walk ends at a relative
// path's first name or at the root, which filepath.Split returns unchanged:
// "/", a volume such as C:\, or a UNC share.
func splitPath(p string) []string {
	var out []string
	for {
		dir, base := filepath.Split(p)
		if base != "" {
			out = append([]string{base}, out...)
		}
		next := filepath.Clean(dir)
		if dir == "" || next == p {
			break
		}
		p = next
	}
	return out
}

// MustExist fatals if the binary is missing. Use in cmd/coverage which
// doesn't have t.Skipf.
func MustExist(ref BinaryRef) error {
	if _, err := os.Stat(ref.Path); err != nil {
		return fmt.Errorf("binary %s not found at %s", ref.Label, ref.Path)
	}
	return nil
}
