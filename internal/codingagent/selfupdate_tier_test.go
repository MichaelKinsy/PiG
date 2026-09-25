package codingagent

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// fakeCmdRunner is a deterministic cmdRunner for package-manager root probes.
// Each entry maps "name arg1 arg2..." to its stdout.
type fakeCmdRunner struct {
	outputs map[string]string
}

func (f fakeCmdRunner) Output(name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	if out, ok := f.outputs[key]; ok {
		return out, nil
	}
	return "", exec.ErrNotFound
}

// ownExe returns the path of the running test binary, suitable as a writable
// standalone target that os.Executable() will resolve to.
func ownExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// writeFakeExe creates a writable executable in a temp dir and returns its path.
func writeFakeExe(t *testing.T, dir string) string {
	t.Helper()
	exe := filepath.Join(dir, "pig")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func writeStandaloneTestReceipt(t *testing.T, exe string) {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_UPDATE_URL", "https://updates.example/manifest")
	originalVersion := InstalledPigVersion
	t.Cleanup(func() { InstalledPigVersion = originalVersion })
	InstalledPigVersion = "test-version"
	if err := WriteStandaloneReceipt(exe, InstalledPigVersion, UpdateSourceURL()); err != nil {
		t.Fatalf("write standalone receipt: %v", err)
	}
}

// withExecutable overrides os.Executable for a test by setting the canonical
// path the resolver evaluates. Because ResolveSelfUpdateTier calls
// os.Executable directly, tests instead call resolveSelfUpdateTierWith against a
// crafted exe path via resolveTierForExe.
func resolveTierForExe(t *testing.T, exe string, runner cmdRunner) (*SelfUpdateProvenance, error) {
	t.Helper()
	return resolveSelfUpdateTierForExe(exe, runner)
}

func TestResolveSelfUpdateTier_StandaloneWritableBinary(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	dir := t.TempDir()
	exe := writeFakeExe(t, dir)
	writeStandaloneTestReceipt(t, exe)
	// No package-manager roots resolve, no baked release, not Windows.
	prov, err := resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Tier != tierStandalone {
		t.Fatalf("tier = %s, want standalone", prov.Tier)
	}
	if prov.ExePath != canonicalPath(exe) {
		t.Fatalf("exe = %q, want canonical path %q", prov.ExePath, canonicalPath(exe))
	}
}

func TestAC9StandaloneReceiptControlsOwnership(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	t.Run("agreeing_receipt", func(t *testing.T) {
		exe := writeFakeExe(t, t.TempDir())
		writeStandaloneTestReceipt(t, exe)
		prov, err := resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierStandalone {
			t.Fatalf("tier = %s, want standalone", prov.Tier)
		}
	})
	t.Run("transport_ca_digest_mismatch", func(t *testing.T) {
		dir := t.TempDir()
		exe := writeFakeExe(t, dir)
		t.Setenv("PIG_HOME", t.TempDir())
		t.Setenv("PIG_UPDATE_URL", "https://updates.example/manifest")
		originalVersion := InstalledPigVersion
		t.Cleanup(func() { InstalledPigVersion = originalVersion })
		InstalledPigVersion = "test-version"
		caPath := filepath.Join(ConfigRoot(), updateTransportCASidecarName)
		if err := os.WriteFile(caPath, []byte("first-ca"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := WriteStandaloneReceipt(exe, InstalledPigVersion, UpdateSourceURL()); err != nil {
			t.Fatal(err)
		}
		prov, err := resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierStandalone {
			t.Fatalf("tier = %s, want standalone for receipt-bound transport CA", prov.Tier)
		}
		if err := os.WriteFile(caPath, []byte("changed-ca"), 0o600); err != nil {
			t.Fatal(err)
		}
		prov, err = resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierUnsupported {
			t.Fatalf("tier = %s, want unsupported for changed transport CA", prov.Tier)
		}
	})
	t.Run("unowned_transport_ca", func(t *testing.T) {
		exe := writeFakeExe(t, t.TempDir())
		writeStandaloneTestReceipt(t, exe)
		if err := os.WriteFile(filepath.Join(ConfigRoot(), updateTransportCASidecarName), []byte("added-ca"), 0o600); err != nil {
			t.Fatal(err)
		}
		prov, err := resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierUnsupported {
			t.Fatalf("tier = %s, want unsupported for unowned transport CA", prov.Tier)
		}
	})
	t.Run("digest_mismatch", func(t *testing.T) {
		exe := writeFakeExe(t, t.TempDir())
		writeStandaloneTestReceipt(t, exe)
		if err := os.WriteFile(exe, []byte("changed"), 0o755); err != nil {
			t.Fatal(err)
		}
		prov, err := resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierUnsupported {
			t.Fatalf("tier = %s, want unsupported", prov.Tier)
		}
	})
	t.Run("moved_executable", func(t *testing.T) {
		dir := t.TempDir()
		exe := writeFakeExe(t, dir)
		writeStandaloneTestReceipt(t, exe)
		moved := filepath.Join(dir, "moved-pig")
		if err := os.Rename(exe, moved); err != nil {
			t.Fatal(err)
		}
		prov, err := resolveTierForExe(t, moved, fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierUnsupported {
			t.Fatalf("tier = %s, want unsupported", prov.Tier)
		}
	})
	t.Run("update_source_mismatch", func(t *testing.T) {
		exe := writeFakeExe(t, t.TempDir())
		writeStandaloneTestReceipt(t, exe)
		t.Setenv("PIG_UPDATE_URL", "https://other.example/manifest")
		prov, err := resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierUnsupported {
			t.Fatalf("tier = %s, want unsupported", prov.Tier)
		}
	})
}

func TestResolveSelfUpdateTier_ReadOnlyBinaryIsUnsupported(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	dir := t.TempDir()
	exe := writeFakeExe(t, dir)
	testenv.ReadOnlyDir(t, dir)
	prov, err := resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Tier != tierUnsupported {
		t.Fatalf("tier = %s, want unsupported for read-only binary", prov.Tier)
	}
}

// TestResolveSelfUpdateTier_SymlinkedBinaryResolvesToTarget proves a symlinked
// executable resolves through to its writable target as a standalone binary.
// Mirrors upstream realpathSync usage in isManagedByGlobalPackageManager.
func TestResolveSelfUpdateTier_SymlinkedBinaryResolvesToTarget(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	dir := t.TempDir()
	target := writeFakeExe(t, dir)
	writeStandaloneTestReceipt(t, target)
	link := filepath.Join(dir, "pig-link")
	testenv.Symlink(t, target, link)
	prov, err := resolveTierForExe(t, link, fakeCmdRunner{outputs: map[string]string{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Tier != tierStandalone {
		t.Fatalf("tier = %s, want standalone for symlinked writable binary", prov.Tier)
	}
	// The resolver follows the symlink to the real target (canonicalized).
	if prov.ExePath != canonicalPath(target) {
		t.Fatalf("exe = %q, want canonical symlink target %q", prov.ExePath, canonicalPath(target))
	}
}

// TestAC2ResolveSelfUpdateTier is the spec-named AC-2 test: across writable,
// read-only, symlinked, package-managed, immutable Binary, OCI image, and
// unknown installations, exactly one tier (or an ambiguity failure) results
// before any mutation.
func TestAC2ResolveSelfUpdateTier(t *testing.T) {
	t.Run("writable_receipted_standalone", func(t *testing.T) {
		requireStandaloneSelfUpdateTier(t)
		dir := t.TempDir()
		exe := writeFakeExe(t, dir)
		writeStandaloneTestReceipt(t, exe)
		prov, err := resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierStandalone {
			t.Fatalf("tier = %s, want standalone", prov.Tier)
		}
	})
	t.Run("writable_unreceipted_is_unsupported", func(t *testing.T) {
		t.Setenv("PIG_HOME", t.TempDir())
		dir := t.TempDir()
		prov, err := resolveTierForExe(t, writeFakeExe(t, dir), fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierUnsupported {
			t.Fatalf("tier = %s, want unsupported", prov.Tier)
		}
	})
	t.Run("read_only_unsupported", func(t *testing.T) {
		requireStandaloneSelfUpdateTier(t)
		dir := t.TempDir()
		exe := writeFakeExe(t, dir)
		testenv.ReadOnlyDir(t, dir)
		prov, err := resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierUnsupported {
			t.Fatalf("tier = %s, want unsupported", prov.Tier)
		}
	})
	t.Run("symlinked_resolves_to_standalone", func(t *testing.T) {
		requireStandaloneSelfUpdateTier(t)
		dir := t.TempDir()
		target := writeFakeExe(t, dir)
		writeStandaloneTestReceipt(t, target)
		link := filepath.Join(dir, "pig-link")
		testenv.Symlink(t, target, link)
		prov, err := resolveTierForExe(t, link, fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierStandalone {
			t.Fatalf("tier = %s, want standalone", prov.Tier)
		}
	})
	t.Run("package_managed", func(t *testing.T) {
		root := t.TempDir()
		npmRoot := filepath.Join(root, "node_modules")
		binDir := filepath.Join(npmRoot, "pig", "bin")
		_ = os.MkdirAll(binDir, 0o755)
		prov, err := resolveTierForExe(t, writeFakeExe(t, binDir), fakeCmdRunner{outputs: map[string]string{
			"npm root -g": npmRoot,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierPackageManager {
			t.Fatalf("tier = %s, want package-manager", prov.Tier)
		}
	})
	t.Run("package_managed_read_only_is_unsupported", func(t *testing.T) {
		root := t.TempDir()
		npmRoot := filepath.Join(root, "node_modules")
		binDir := filepath.Join(npmRoot, "pig", "bin")
		_ = os.MkdirAll(binDir, 0o755)
		exe := writeFakeExe(t, binDir)
		testenv.ReadOnlyDir(t, binDir)
		prov, err := resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{
			"npm root -g": npmRoot,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierUnsupported {
			t.Fatalf("tier = %s, want unsupported for read-only package install", prov.Tier)
		}
	})
	t.Run("immutable_binary", func(t *testing.T) {
		orig := PigletBinaryRelease
		t.Cleanup(func() { PigletBinaryRelease = orig })
		PigletBinaryRelease = "1.0.0"
		prov, err := resolveTierForExe(t, ownExe(t), fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierImmutableBinary {
			t.Fatalf("tier = %s, want immutable-binary", prov.Tier)
		}
	})
	t.Run("oci_container", func(t *testing.T) {
		t.Setenv("PIG_INSTALL_TIER", "container")
		prov, err := resolveTierForExe(t, ownExe(t), fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierContainer {
			t.Fatalf("tier = %s, want container", prov.Tier)
		}
	})
	t.Run("ambiguous_rejected", func(t *testing.T) {
		root := t.TempDir()
		shared := filepath.Join(root, "node_modules")
		binDir := filepath.Join(shared, "pig", "bin")
		_ = os.MkdirAll(binDir, 0o755)
		_, err := resolveTierForExe(t, writeFakeExe(t, binDir), fakeCmdRunner{outputs: map[string]string{
			"npm root -g":  shared,
			"pnpm root -g": shared,
		}})
		if err == nil {
			t.Fatal("ambiguous ownership resolved without error")
		}
	})
}

func TestResolveSelfUpdateTier_PackageManagerOwnershipDetected(t *testing.T) {
	// Place the fake exe inside a simulated npm global root.
	root := t.TempDir()
	npmRoot := filepath.Join(root, "node_modules")
	binDir := filepath.Join(npmRoot, "pig", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := writeFakeExe(t, binDir)
	runner := fakeCmdRunner{outputs: map[string]string{
		"npm root -g": npmRoot,
	}}
	prov, err := resolveTierForExe(t, exe, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Tier != tierPackageManager {
		t.Fatalf("tier = %s, want package-manager", prov.Tier)
	}
	if prov.PackageOwner != ownerNPM {
		t.Fatalf("owner = %s, want npm", prov.PackageOwner)
	}
	if prov.PackageName != "pig" {
		t.Fatalf("package = %q, want pig", prov.PackageName)
	}
}

func TestResolveSelfUpdateTier_PNPMPackageIdentityUsesLinkedPackage(t *testing.T) {
	root := t.TempDir()
	pnpmRoot := filepath.Join(root, "node_modules")
	binDir := filepath.Join(
		pnpmRoot,
		".pnpm",
		"@example+pig@1.2.3",
		"node_modules",
		"@example",
		"pig",
		"bin",
	)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prov, err := resolveTierForExe(t, writeFakeExe(t, binDir), fakeCmdRunner{outputs: map[string]string{
		"pnpm root -g": pnpmRoot,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if prov.Tier != tierPackageManager || prov.PackageOwner != ownerPNPM {
		t.Fatalf("provenance = %#v, want pnpm package-manager", prov)
	}
	if prov.PackageName != "@example/pig" {
		t.Fatalf("package = %q, want @example/pig", prov.PackageName)
	}
}

func TestResolveSelfUpdateTier_AmbiguousOwnershipIsRejected(t *testing.T) {
	// The same exe path is claimed by both npm and pnpm global roots.
	root := t.TempDir()
	sharedRoot := filepath.Join(root, "node_modules")
	binDir := filepath.Join(sharedRoot, "pig", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := writeFakeExe(t, binDir)
	runner := fakeCmdRunner{outputs: map[string]string{
		"npm root -g":  sharedRoot,
		"pnpm root -g": sharedRoot,
	}}
	_, err := resolveTierForExe(t, exe, runner)
	if err == nil {
		t.Fatal("ambiguous ownership resolved without error")
	}
	var te *TierError
	if !errors.As(err, &te) {
		t.Fatalf("error is not a *TierError: %v", err)
	}
	if te.Tier != tierPackageManager {
		t.Fatalf("tier = %s, want package-manager", te.Tier)
	}
	if !strings.Contains(te.Remediation, "more than one package manager") {
		t.Fatalf("remediation missing ambiguity message: %s", te.Remediation)
	}
}

func TestResolveSelfUpdateTier_ImmutableBinaryWhenReleaseBaked(t *testing.T) {
	orig := PigletBinaryRelease
	t.Cleanup(func() { PigletBinaryRelease = orig })
	PigletBinaryRelease = "1.2.3"
	// Even a writable exe is classified immutable when a release is baked.
	prov, err := resolveTierForExe(t, ownExe(t), fakeCmdRunner{outputs: map[string]string{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Tier != tierImmutableBinary {
		t.Fatalf("tier = %s, want immutable-binary", prov.Tier)
	}
}

func TestResolveSelfUpdateTier_ContainerViaEnvOverride(t *testing.T) {
	for _, val := range []string{"container", "image"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("PIG_INSTALL_TIER", val)
			prov, err := resolveTierForExe(t, ownExe(t), fakeCmdRunner{outputs: map[string]string{}})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if prov.Tier != tierContainer {
				t.Fatalf("tier = %s, want container for PIG_INSTALL_TIER=%s", prov.Tier, val)
			}
		})
	}
}

func TestResolveSelfUpdateTier_UnknownEnvOverrideIsRejected(t *testing.T) {
	t.Setenv("PIG_INSTALL_TIER", "bogus")
	_, err := resolveTierForExe(t, ownExe(t), fakeCmdRunner{outputs: map[string]string{}})
	if err == nil {
		t.Fatal("unknown PIG_INSTALL_TIER resolved without error")
	}
	var te *TierError
	if !errors.As(err, &te) {
		t.Fatalf("error is not a *TierError: %v", err)
	}
	if !strings.Contains(te.Remediation, "unknown PIG_INSTALL_TIER value") {
		t.Fatalf("remediation missing unknown-tier message: %s", te.Remediation)
	}
}

// pig123 is the release plan that updates pig to 1.2.3 without a rename.
var pig123 = SelfUpdatePackageTarget{PackageName: "pig", InstallSpec: "pig@1.2.3"}

func TestPackageManagerUpdateCommand_MirrorsUpstreamShape(t *testing.T) {
	cases := []struct {
		owner    PackageManagerOwner
		wantCmd  string
		wantArgs []string
	}{
		{ownerNPM, "npm", []string{"install", "-g", "--ignore-scripts", "--min-release-age=0", "pig@1.2.3"}},
		{ownerPNPM, "pnpm", []string{"install", "-g", "--ignore-scripts", "--config.minimumReleaseAge=0", "pig@1.2.3"}},
		{ownerYarn, "yarn", []string{"global", "add", "--ignore-scripts", "pig@1.2.3"}},
		{ownerBun, "bun", []string{"install", "-g", "--ignore-scripts", "--minimum-release-age=0", "pig@1.2.3"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.owner), func(t *testing.T) {
			cmd := PackageManagerUpdateCommand(tc.owner, "pig", nil, pig123)
			if cmd == nil {
				t.Fatal("nil command")
			}
			if cmd.Command != tc.wantCmd {
				t.Fatalf("command = %q, want %q", cmd.Command, tc.wantCmd)
			}
			if strings.Join(cmd.Args, "\x00") != strings.Join(tc.wantArgs, "\x00") {
				t.Fatalf("args = %v, want %v", cmd.Args, tc.wantArgs)
			}
			if !strings.Contains(cmd.Display, "--ignore-scripts") {
				t.Fatalf("display missing --ignore-scripts: %s", cmd.Display)
			}
			// Upstream uninstalls first only when the release renames the
			// package (getSelfUpdateCommandForMethod compares
			// target.packageName, not the install spec).
			if len(cmd.Steps) != 0 {
				t.Fatalf("steps = %q, want only the install step for an unrenamed package", cmd.Display)
			}
		})
	}
	if PackageManagerUpdateCommand(PackageManagerOwner("unknown"), "pig", nil, pig123) != nil {
		t.Fatal("unknown owner returned a command")
	}
}

func TestRemediationMessagesAreNonLoopingAndMentionExe(t *testing.T) {
	exe := "/usr/local/bin/pig"
	if msg := ImmutableBinaryRemediation(exe); !strings.Contains(msg, exe) || strings.Contains(msg, "pig update") {
		t.Fatalf("immutable remediation loops or omits exe: %s", msg)
	}
	if msg := ContainerRemediation(exe); !strings.Contains(msg, exe) || strings.Contains(msg, "pig update") {
		t.Fatalf("container remediation loops or omits exe: %s", msg)
	}
	if msg := UnsupportedRemediation(exe); !strings.Contains(msg, exe) || strings.Contains(msg, "pig update") {
		t.Fatalf("unsupported remediation loops or omits exe: %s", msg)
	}
}

func TestContainerRemediationUsesImageRefWhenSet(t *testing.T) {
	t.Setenv("PIG_IMAGE_REF", "registry.example/pig:1.2.3")
	msg := ContainerRemediation("/x/pig")
	if !strings.Contains(msg, "registry.example/pig:1.2.3") {
		t.Fatalf("container remediation omits PIG_IMAGE_REF: %s", msg)
	}
	if !strings.Contains(msg, "pull registry.example/pig:1.2.3") {
		t.Fatalf("container remediation omits pull command: %s", msg)
	}
}

// TestContainerRemediationRendersProductRedeployInstruction proves that a
// managed deployment supplies the exact redeploy operation through the generic
// seam. Pig renders it instead of asserting a generic `docker pull` that an
// operator cannot run against the managed environment.
func TestContainerRemediationRendersProductRedeployInstruction(t *testing.T) {
	t.Setenv("PIG_IMAGE_REF", "registry.example/pig-agent:1.2.3")
	t.Setenv("PIG_REDEPLOY_INSTRUCTION", "Roll the managed agent deployment.")
	msg := ContainerRemediation("/usr/local/bin/pig")

	if !strings.Contains(msg, "Roll the managed agent deployment") {
		t.Fatalf("remediation dropped the product redeploy instruction: %s", msg)
	}
	if strings.Contains(msg, "docker pull") {
		t.Fatalf("remediation asserted a generic docker pull over the product instruction: %s", msg)
	}
	// Pig's own invariants must survive: the artifact identity, the executable,
	// and the no-in-place-rewrite guarantee.
	for _, want := range []string{
		"registry.example/pig-agent:1.2.3",
		"/usr/local/bin/pig",
		"not rewritten",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("remediation dropped %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "pig update") {
		t.Fatalf("remediation loops back to the failed command: %s", msg)
	}
}

// TestApplySelfUpdateTier_NoFallthroughAfterStandaloneStarts proves that once
// the standalone tier starts and fails, the result is a standalone failure and
// never a fallthrough to package-manager, container, or refused. This is the
// AC-7 no-fallthrough guard: mutate ApplySelfUpdateTier to retry another tier
// after a standalone failure and this test goes red.
func TestApplySelfUpdateTier_NoFallthroughAfterStandaloneStarts(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	dir := t.TempDir()
	exe := writeFakeExe(t, dir)
	writeStandaloneTestReceipt(t, exe)
	called := false
	apply := func(string) error {
		called = true
		return errStandaloneSimulated
	}
	// Force standalone classification by resolving against the writable fake exe
	// with no package-manager roots and no baked release.
	result := applyTierForExe(t, exe, apply)
	if !called {
		t.Fatal("standalone apply was not invoked; tier did not start")
	}
	if result.Action != "standalone-failed" {
		t.Fatalf("action = %q, want standalone-failed (no fallthrough)", result.Action)
	}
	if !errors.Is(result.Cause, errStandaloneSimulated) {
		t.Fatalf("cause = %v, want the standalone failure preserved", result.Cause)
	}
}

// TestApplySelfUpdateTier_ImmutableRefusesWithoutAttemptingDownload proves the
// immutable tier never starts a download. Mutate the tier to fall through to
// standalone on immutable and this test goes red.
func TestApplySelfUpdateTier_ImmutableRefusesWithoutAttemptingDownload(t *testing.T) {
	orig := PigletBinaryRelease
	t.Cleanup(func() { PigletBinaryRelease = orig })
	PigletBinaryRelease = "9.9.9"
	apply := func(string) error {
		t.Fatal("standalone download attempted on an immutable binary")
		return nil
	}
	result := applyTierForExe(t, ownExe(t), apply)
	if result.Action != "refused" {
		t.Fatalf("action = %q, want refused", result.Action)
	}
	if !strings.Contains(result.Message, "immutable") {
		t.Fatalf("refusal missing immutable remediation: %s", result.Message)
	}
}

// errStandaloneSimulated is a sentinel for the no-fallthrough test so the
// asserted cause is distinct from any real error.
var errStandaloneSimulated = errors.New("simulated standalone failure")

// applyTierForExe resolves the tier for a specific exe (bypassing
// os.Executable) and applies it, for tests that must control the executable
// path.
func applyTierForExe(t *testing.T, exe string, apply func(string) error) SelfUpdateActionResult {
	t.Helper()
	prov, err := resolveSelfUpdateTierForExe(exe, fakeCmdRunner{outputs: map[string]string{}})
	if err != nil {
		// Mirror ApplySelfUpdateTier's error handling so refusal paths are
		// exercised here too.
		if te, ok := errors.AsType[*TierError](err); ok {
			return SelfUpdateActionResult{Action: "refused", Message: te.Error(), Cause: te}
		}
		return SelfUpdateActionResult{Action: "error", Message: err.Error(), Cause: err}
	}
	return applyProvenance(prov, apply, nil)
}

// TestAC4PackageManagerUpdateOwnsMutation proves the package-manager tier runs
// the exact owner command and that a failure surfaces from this tier with no
// standalone fallback. The standalone apply callback must never be invoked.
func TestAC4PackageManagerUpdateOwnsMutation(t *testing.T) {
	var ran []*SelfUpdateCommand
	orig := packageManagerRunner
	t.Cleanup(func() { packageManagerRunner = orig })
	packageManagerRunner = func(cmd *SelfUpdateCommand) error {
		ran = append(ran, cmd)
		return nil
	}
	standaloneCalled := false
	prov := &SelfUpdateProvenance{
		Tier:         tierPackageManager,
		ExePath:      "/managed/pig",
		PackageOwner: ownerPNPM,
		PackageName:  "pig",
	}
	result := applyProvenance(prov, func(string) error {
		standaloneCalled = true
		return nil
	}, func(prov *SelfUpdateProvenance) error {
		return RunPackageManagerUpdate(PackageManagerUpdateCommand(prov.PackageOwner, prov.PackageName, nil, pig123))
	})
	if standaloneCalled {
		t.Fatal("standalone apply was invoked; package-manager tier fell back to standalone")
	}
	if !result.Done || result.Action != "package-manager-updated" {
		t.Fatalf("result = %+v, want package-manager-updated", result)
	}
	if len(ran) != 1 || ran[0].Command != "pnpm" {
		t.Fatalf("ran = %#v, want one pnpm command", ran)
	}
	if !strings.Contains(ran[0].Display, "--ignore-scripts") {
		t.Fatalf("command missing --ignore-scripts: %s", ran[0].Display)
	}
}

// TestAC4PackageManagerFailureSurfacesNoFallback proves a package-manager
// failure reports the owner command failure and never starts a standalone
// download.
func TestAC4PackageManagerFailureSurfacesNoFallback(t *testing.T) {
	orig := packageManagerRunner
	t.Cleanup(func() { packageManagerRunner = orig })
	packageManagerRunner = func(cmd *SelfUpdateCommand) error {
		return errors.New("pnpm exited 1")
	}
	standaloneCalled := false
	prov := &SelfUpdateProvenance{
		Tier:         tierPackageManager,
		ExePath:      "/managed/pig",
		PackageOwner: ownerPNPM,
		PackageName:  "pig",
	}
	result := applyProvenance(prov, func(string) error {
		standaloneCalled = true
		return nil
	}, func(prov *SelfUpdateProvenance) error {
		return RunPackageManagerUpdate(PackageManagerUpdateCommand(prov.PackageOwner, prov.PackageName, nil, pig123))
	})
	if standaloneCalled {
		t.Fatal("standalone apply invoked after package-manager failure")
	}
	if result.Action != "package-manager-failed" {
		t.Fatalf("action = %q, want package-manager-failed", result.Action)
	}
	if !strings.Contains(result.Message, "pnpm exited 1") {
		t.Fatalf("message missing owner failure: %s", result.Message)
	}
}

// Upstream self-updates npm and pnpm installs on Windows and refuses every
// other install method there with "self-update on Windows is only supported
// for npm and pnpm installs" (package-manager-cli.ts). A standalone pig.exe
// stays unsupported (D39).
func TestResolveSelfUpdateTierOnWindowsFollowsInstallMethod(t *testing.T) {
	t.Setenv("PIG_INSTALL_TIER", "")
	type install struct {
		outputs map[string]string
		binDir  string
	}
	layouts := map[PackageManagerOwner]func(root string) install{
		ownerNPM: func(root string) install {
			global := filepath.Join(root, "node_modules")
			return install{map[string]string{"npm root -g": global}, filepath.Join(global, "pig", "bin")}
		},
		ownerPNPM: func(root string) install {
			global := filepath.Join(root, "pnpm", "node_modules")
			return install{map[string]string{"pnpm root -g": global}, filepath.Join(global, "pig", "bin")}
		},
		ownerYarn: func(root string) install {
			global := filepath.Join(root, "yarn", "global")
			return install{map[string]string{"yarn global dir": global}, filepath.Join(global, "node_modules", "pig", "bin")}
		},
		ownerBun: func(root string) install {
			return install{
				map[string]string{"bun pm bin -g": filepath.Join(root, "bun", "bin")},
				filepath.Join(root, "bun", "install", "global", "node_modules", "pig", "bin"),
			}
		},
	}
	for _, tc := range []struct {
		goos    string
		owner   PackageManagerOwner
		refused bool
	}{
		{"windows", ownerNPM, false},
		{"windows", ownerPNPM, false},
		{"windows", ownerYarn, true},
		{"windows", ownerBun, true},
		{"linux", ownerYarn, false},
		{"linux", ownerBun, false},
	} {
		t.Run(tc.goos+"/"+string(tc.owner), func(t *testing.T) {
			layout := layouts[tc.owner](t.TempDir())
			if err := os.MkdirAll(layout.binDir, 0o755); err != nil {
				t.Fatal(err)
			}
			exe := writeFakeExe(t, layout.binDir)
			prov, err := resolveSelfUpdateTierOn(tc.goos, exe, fakeCmdRunner{outputs: layout.outputs})
			if tc.refused {
				want := "pig self-update on Windows is only supported for npm and pnpm installs.\nDetected install method: " + string(tc.owner) + ". Update pig manually."
				te, ok := errors.AsType[*TierError](err)
				if !ok || te.Error() != want {
					t.Fatalf("resolve = %#v, %v; want the refusal %q", prov, err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if prov.Tier != tierPackageManager || prov.PackageOwner != tc.owner || prov.PackageName != "pig" {
				t.Fatalf("provenance = %#v, want the %s package-manager tier for pig", prov, tc.owner)
			}
		})
	}
	t.Run("windows/standalone", func(t *testing.T) {
		exe := writeFakeExe(t, t.TempDir())
		writeStandaloneTestReceipt(t, exe)
		prov, err := resolveSelfUpdateTierOn("windows", exe, fakeCmdRunner{outputs: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		if prov.Tier != tierUnsupported {
			t.Fatalf("tier = %s, want unsupported for a standalone Windows binary", prov.Tier)
		}
	})
}
