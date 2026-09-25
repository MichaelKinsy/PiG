//go:build parity

package runner

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// snapshotEnvKeys lists every env key whose value is a directory that
// must be ephemeral-copied before the binary runs. When a scenario
// declares one of these keys in its [env] section, snapshotAgentDirs
// resolves the path relative to the scenario file, copies it to a
// t.TempDir, and rewrites the env var to point at the copy.
//
// Add new keys here when a binary reads/writes a home or agent dir
// from an env var. The snapshot mechanism is the only thing preventing
// committed testdata from being mutated by the binary under test.
var snapshotEnvKeys = []string{
	"PIG_CODING_AGENT_DIR",
	"PI_CODING_AGENT_DIR",
	"PIG_HOME",
	"PI_HOME",
}

func scenarioAuthModes(sc *Scenario) (preserve, inject bool) {
	inject = sc.HasTag("requires-auth")
	preserve = inject || sc.HasTag("fixture-auth")
	return preserve, inject
}

// snapshotAgentDirs creates ephemeral copies of any agent/home dirs
// referenced in the scenario's env overrides. preserveAuth retains explicit
// non-secret fixture credentials; injectAuth overlays ambient test credentials
// for requires-auth scenarios. The copies are registered with t.Cleanup.
//
// This prevents the binary from mutating committed testdata (e.g. pi
// persists defaultModel into settings.json on /model switch, or pig
// writes session files under PIG_HOME).
//
// FATAL on missing source: if the scenario declares a dir that doesn't
// exist on disk, the test fails immediately. This catches typo'd paths
// that would otherwise silently fall through to the user's real home
// dir, producing non-hermetic, non-reproducible results.
//
// Returns updated env var slices with the paths rewritten to temp copies.
func snapshotAgentDirs(t *testing.T, sourcePath string, pigEnv, piEnv []string, preserveAuth, injectAuth bool) ([]string, []string) {
	t.Helper()
	pigOut := snapshotEnvDirs(t, sourcePath, pigEnv, preserveAuth, injectAuth)
	piOut := snapshotEnvDirs(t, sourcePath, piEnv, preserveAuth, injectAuth)
	return pigOut, piOut
}

// snapshotBinaryEnv isolates the fallback home directories carried by a
// BinaryRef. Every driver must use it because drivers run concurrently and the
// binaries create runtime cache and lock files in those homes.
func snapshotBinaryEnv(t *testing.T, sourcePath string, env []string, preserveAuth, injectAuth bool) []string {
	t.Helper()
	return snapshotEnvDirs(t, sourcePath, env, preserveAuth, injectAuth)
}

// snapshotEnvDirs scans envVars for any key in snapshotEnvKeys and
// snapshots the referenced directory. Returns the updated env slice.
func snapshotEnvDirs(t *testing.T, sourcePath string, envVars []string, preserveAuth, injectAuth bool) []string {
	t.Helper()
	out := make([]string, len(envVars))
	copy(out, envVars)

	for i, kv := range out {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || v == "" {
			continue
		}
		if !isSnapshotKey(k) {
			continue
		}

		// Resolve the source dir relative to the scenario file.
		srcDir := v
		if !filepath.IsAbs(srcDir) {
			srcDir = filepath.Join(filepath.Dir(sourcePath), srcDir)
		}
		if _, err := os.Stat(srcDir); err != nil {
			t.Fatalf("snapshot: env %s=%s resolves to %s which does not exist: %v\n"+
				"The scenario declares a testdata directory that is missing on disk.\n"+
				"Fix the relative path in the scenario's [env] section.",
				k, v, srcDir, err)
		}

		// Create ephemeral copy.
		// PIG_HOME holds pig's documentation bundle, whose path the system
		// prompt carries; see "Documentation paths in the system prompt".
		root := os.TempDir()
		if k == "PIG_HOME" {
			root = promptPathRoot
		}
		tmp, err := mkdirFixed(root, fmt.Sprintf("parity-snap-%s-", k))
		if err != nil {
			t.Fatalf("snapshot: mkdirtemp for %s: %v", k, err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(tmp); err != nil {
				t.Error(err)
			}
		})

		if err := copyDir(srcDir, tmp); err != nil {
			t.Fatalf("snapshot: copy %s → %s: %v", srcDir, tmp, err)
		}
		if !preserveAuth {
			_ = os.Remove(filepath.Join(tmp, "auth.json"))
			_ = os.Remove(filepath.Join(tmp, "agent", "auth.json"))
		}

		// On the pi side, re-pin the changelog-seen version to the
		// upstream tag under test so pi's "what's new" overlay stays
		// suppressed across pin bumps. See seedPiChangelogVersion.
		if strings.HasPrefix(k, "PI_") {
			seedPiChangelogVersion(t, tmp)
		}
		switch k {
		case "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR":
			seedCheckoutUntrusted(t, checkoutTrustRoot(sourcePath), tmp)
		case "PIG_HOME":
			seedCheckoutUntrusted(t, checkoutTrustRoot(sourcePath), filepath.Join(tmp, "agent"))
		}
		if injectAuth && (k == "PIG_CODING_AGENT_DIR" || k == "PI_CODING_AGENT_DIR") {
			label := "pig"
			if k == "PI_CODING_AGENT_DIR" {
				label = "pi"
			}
			if _, err := EnsureTestdataAuth(tmp, label); err != nil {
				t.Logf("snapshot auth for %s: %v", label, err)
			}
		}

		// Rewrite the env var to point to the snapshot.
		out[i] = k + "=" + tmp
	}
	return out
}

// snapshotCWD copies the scenario's CWD fixture into a fresh directory under a canonical, context-free temporary root so concurrent pi/pig invocations get independent copies.
// Returns the absolute canonical path of the snapshot. Mirrors snapshotEnvDirs
// for env-referenced agent/home dirs, but for the binary's working
// directory (declared via a driver's cwd field in the scenario TOML).
//
// Required for scenarios that mutate CWD files (edit-tool diff, write,
// etc.) so the two binaries cannot race on the same fixture file.
func snapshotCWD(t *testing.T, src string) (string, error) {
	t.Helper()
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("cwd source missing: %s: %w", src, err)
	}
	tmp, err := mkdirTempFixed("parity-snap-cwd-")
	if err != nil {
		return "", err
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(tmp); err != nil {
			t.Error(err)
		}
	})
	if err := copyDir(src, tmp); err != nil {
		return "", err
	}
	if err := rewriteSnapshotSessionCwds(tmp); err != nil {
		return "", fmt.Errorf("rewrite snapshot session cwds: %w", err)
	}
	return tmp, nil
}

// snapshotExtensionPath copies an extension source into a per-binary writable
// root. File extensions bring their sibling directory so relative imports keep
// working; directory extensions copy that directory directly. This prevents a
// reload/self-edit scenario from mutating checked-in fixtures or changing the
// source subsequently observed by the other binary.
func snapshotExtensionPath(t *testing.T, src string) (string, error) {
	t.Helper()
	info, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		tmp, err := os.MkdirTemp("", "parity-snap-extension-*")
		if err != nil {
			return "", err
		}
		t.Cleanup(func() { _ = os.RemoveAll(tmp) })
		if err := copyDir(src, tmp); err != nil {
			return "", err
		}
		return tmp, nil
	}
	parent := filepath.Dir(src)
	tmp, err := os.MkdirTemp("", "parity-snap-extension-*")
	if err != nil {
		return "", err
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	if err := copyDir(parent, tmp); err != nil {
		return "", err
	}
	return filepath.Join(tmp, filepath.Base(src)), nil
}

// rewriteSnapshotSessionCwds rewrites the header `cwd` of every
// sessions/*.jsonl under the snapshot to the snapshot's canonical path.
//
// Resume scenarios ship a session fixture with a fixed placeholder cwd
// (e.g. "/tmp"). Since v0.78.1, `--continue` with a custom `--session-dir`
// filters candidate sessions to those whose header cwd matches the runtime
// cwd (upstream continueRecent's filterCwd; pig mirrors it). The runtime
// cwd is this random snapshot dir, so the placeholder never matches and the
// session is skipped, leaving both binaries on the startup banner. Aligning
// the header cwd to the snapshot's resolved path makes the session loadable
// by both pig and pi exactly as if it had been created here. Only the first
// (header) line is rewritten; scenarios without a sessions/ dir are a no-op.
func rewriteSnapshotSessionCwds(snap string) error {
	sessionsDir := filepath.Join(snap, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	canonical, err := filepath.EvalSymlinks(snap)
	if err != nil {
		canonical = snap
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if err := rewriteSessionHeaderCwd(filepath.Join(sessionsDir, e.Name()), canonical); err != nil {
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
	}
	return nil
}

// rewriteSessionHeaderCwd parses the first JSONL line of a session file as
// a header object. A cwd beginning with {{SNAPSHOT}} keeps its relative suffix
// under newCwd; every other session header is set to newCwd for the established
// same-cwd resume fixture behavior. Every subsequent line remains untouched.
func rewriteSessionHeaderCwd(path, newCwd string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil
	}
	var header map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		return nil // not a JSON header; leave the file alone
	}
	if header["type"] != "session" {
		return nil
	}
	if fixtureCwd, ok := header["cwd"].(string); ok && strings.HasPrefix(fixtureCwd, "{{SNAPSHOT}}") {
		relative := strings.TrimPrefix(fixtureCwd, "{{SNAPSHOT}}")
		relative = strings.TrimLeft(relative, `/\`)
		header["cwd"] = filepath.Join(newCwd, filepath.FromSlash(relative))
	} else {
		header["cwd"] = newCwd
	}
	encoded, err := json.Marshal(header)
	if err != nil {
		return err
	}
	lines[0] = string(encoded)
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

func isSnapshotKey(k string) bool {
	for _, sk := range snapshotEnvKeys {
		if k == sk {
			return true
		}
	}
	return false
}

// seedPiChangelogVersion re-pins lastChangelogVersion in an ephemeral pi
// agent dir's settings.json to coding.UpstreamVersion (the pi tag under
// test). pi renders a "what's new" changelog overlay on first interactive
// launch whenever its version exceeds the recorded lastChangelogVersion;
// scenarios suppress it by recording the current version. Hardcoding that
// version in each fixture is brittle: a pin bump leaves every fixture
// behind and the overlay re-appears, masking the real UI in every
// interactive scenario. Seeding from the pin here fixes the whole class in
// one place and keeps the suite resilient to the next bump.
//
// pig's own bundled changelog tops at its embedded version and its fixtures
// record that, so the pig side stays inert; only the pi side is reseeded.
//
// A missing settings.json is a no-op (nothing to suppress). Invalid JSON is
// fatal so a corrupt fixture surfaces loudly instead of silently failing to
// suppress.
func seedPiChangelogVersion(t *testing.T, agentDir string) {
	t.Helper()
	settingsPath := filepath.Join(agentDir, "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("seed changelog: %s is not valid JSON: %v", settingsPath, err)
	}
	settings["lastChangelogVersion"] = coding.UpstreamVersion
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatalf("seed changelog: re-marshal %s: %v", settingsPath, err)
	}
	if err := os.WriteFile(settingsPath, encoded, 0o600); err != nil {
		t.Fatalf("seed changelog: write %s: %v", settingsPath, err)
	}
}

// checkoutTrustRoot returns the canonical path of the PiG checkout that holds
// the scenario, in the form both binaries use as a trust.json key.
func checkoutTrustRoot(sourcePath string) string {
	root := mustRepoRootForArtifact(sourcePath)
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		return canonical
	}
	return root
}

// seedCheckoutUntrusted records "do not trust" for the checkout root in
// agentDir/trust.json. The checkout tracks .agents/skills, and both binaries
// walk up from a cwd inside the checkout, find it, and open the "Trust project
// folder?" prompt (upstream hasTrustRequiringProjectResources). A stored
// decision for the root answers that prompt the way a hermetic run needs: no
// checkout skills or .pi resources load. Context files are not trust-gated.
//
// Stored decisions inherit from the nearest ancestor (upstream
// findNearestTrustEntry), so the seed is skipped when the fixture already
// decides the root or one of its ancestors. A scenario that exercises project
// trust runs its project from a snapshot outside the checkout, where the seed
// never applies.
func seedCheckoutUntrusted(t *testing.T, root, agentDir string) {
	t.Helper()
	path := filepath.Join(agentDir, "trust.json")
	data := map[string]*bool{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatalf("seed trust: %s is not a trust store: %v", path, err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("seed trust: read %s: %v", path, err)
	}
	for dir := root; ; dir = filepath.Dir(dir) {
		if data[dir] != nil {
			return
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	untrusted := false
	data[root] = &untrusted
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("seed trust: encode %s: %v", path, err)
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatalf("seed trust: create %s: %v", agentDir, err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("seed trust: write %s: %v", path, err)
	}
}

// copyDir copies the contents of src into dst (which must exist).
// Only copies regular files and directories (no symlinks).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o600)
		if info.Mode()&0o111 != 0 {
			mode = 0o700
		}
		return os.WriteFile(target, data, mode)
	})
}
