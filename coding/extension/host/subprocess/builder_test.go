package subprocess

import (
	"context"
	"debug/buildinfo"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/modfile"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestCargoBuildTargetDirectoryHonorsAbsoluteAndRelativeOverrides(t *testing.T) {
	workingDirectory := t.TempDir()
	absolute := filepath.Join(t.TempDir(), "cargo-target")
	t.Setenv("CARGO_TARGET_DIR", absolute)
	if got := cargoBuildTargetDirectory(workingDirectory); got != absolute {
		t.Fatalf("absolute target = %q, want %q", got, absolute)
	}

	t.Setenv("CARGO_TARGET_DIR", "shared-target")
	want := filepath.Join(workingDirectory, "shared-target")
	if got := cargoBuildTargetDirectory(workingDirectory); got != want {
		t.Fatalf("relative target = %q, want %q", got, want)
	}
}

func TestHostLoadAllStopsBeforeResolutionWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	host := NewHost(t.TempDir())
	_, errs := host.LoadAll(ctx, []ExtConfig{{Name: "cancelled", Source: t.TempDir(), Enabled: true}})
	if len(errs) != 1 || !errors.Is(errs[0], context.Canceled) {
		t.Fatalf("LoadAll errors = %v, want context.Canceled", errs)
	}
}

func TestBuilderBuildContextStopsBeforeMutationWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	builder := NewBuilder(t.TempDir())
	if _, err := builder.BuildContext(ctx, "fixture", t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestBuilder_GoExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in short mode")
	}

	// Create a minimal standalone Go extension (needs its own go.mod).
	srcDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module fixture-ext\ngo 1.26\n"), 0o644)
	_ = os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(`package main

import "fmt"

func main() { fmt.Println("hello from fixture") }
`), 0o644)

	cacheDir := t.TempDir()
	b := NewBuilder(cacheDir)

	// First build: cache miss.
	result, err := b.Build("fixture", srcDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Cached {
		t.Error("first build should not be cached")
	}
	if result.BinaryPath == "" {
		t.Fatal("binary path is empty")
	}
	// Verify the binary exists and is executable.
	info, err := os.Stat(result.BinaryPath)
	if err != nil {
		t.Fatalf("stat binary: %v", err)
	}
	if info.Size() == 0 {
		t.Error("binary is empty")
	}

	// Second build: cache hit (same source, same hash).
	result2, err := b.Build("fixture", srcDir)
	if err != nil {
		t.Fatalf("Build (cached): %v", err)
	}
	if !result2.Cached {
		t.Error("second build should be cached")
	}
	if result2.BinaryPath != result.BinaryPath {
		t.Errorf("paths differ: %s vs %s", result.BinaryPath, result2.BinaryPath)
	}
}

func TestBuilderWaitsForSDKStageLock(t *testing.T) {
	configRoot := t.TempDir()
	sdkDir := filepath.Join(configRoot, "state", "pigsdk", "sdk")
	if err := os.MkdirAll(sdkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkDir, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkDir, "sdk.go"), []byte("package sdk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module extension\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\nimport _ \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := pigsdklock.AcquireStage(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	builder := NewBuilderWithConfigRoot(t.TempDir(), configRoot)
	if _, err := builder.BuildContext(ctx, "extension", srcDir); err == nil || !strings.Contains(err.Error(), "SDK transaction lock") {
		t.Fatalf("build error = %v, want SDK transaction lock wait failure", err)
	}
}

// A warm build of a staged-SDK Go extension must not wait for another
// process's SDK stage lock: the cache key covers the staged SDK, so a hit is
// exactly what the leased lookup would return.
func TestBuilderWarmStagedSDKBuildDoesNotWaitForSDKStageLock(t *testing.T) {
	configRoot := t.TempDir()
	sdkDir := filepath.Join(configRoot, "state", "pigsdk", "sdk")
	if err := os.MkdirAll(sdkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkDir, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkDir, "sdk.go"), []byte("package sdk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module extension\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\nimport _ \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	builder := NewBuilderWithConfigRoot(cacheDir, configRoot)
	stagedSDK, err := builder.resolveStagedSDK(srcDir, "go")
	if err != nil || stagedSDK == "" {
		t.Fatalf("resolveStagedSDK = %q, %v", stagedSDK, err)
	}
	hash, err := hashSourceDir(srcDir, "go")
	if err != nil {
		t.Fatal(err)
	}
	if hash, err = hashWithStagedSDK(hash, stagedSDK, "go"); err != nil {
		t.Fatal(err)
	}
	entryDir := filepath.Join(cacheDir, "extension-"+hash)
	artifactName := extensionArtifactName(runtime.GOOS, "go")
	binary := []byte("test executable")
	_, err = runtimecell.PublishArtifact(context.Background(), entryDir, artifactName, hash, "go", func(scratch string) (string, error) {
		binaryPath := filepath.Join(scratch, artifactName)
		if err := os.WriteFile(binaryPath, binary, 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(scratch, sdkFingerprintFile), []byte("fingerprint"), 0o644); err != nil {
			return "", err
		}
		return binaryPath, nil
	}, sdkFingerprintFile)
	if err != nil {
		t.Fatal(err)
	}
	if entry, valid, err := builder.CurrentCacheEntry("extension", srcDir); err != nil || !valid || entry != entryDir {
		t.Fatalf("current extension = %q, %t, %v; want published %s", entry, valid, err, entryDir)
	}

	release, err := pigsdklock.AcquireStage(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := builder.BuildContext(ctx, "extension", srcDir)
	if err != nil {
		t.Fatalf("warm build waited for the SDK stage lock: %v", err)
	}
	wantArtifact := filepath.Join(entryDir, artifactName)
	if !result.Cached || result.BinaryPath != wantArtifact {
		t.Fatalf("warm build = %+v, want cached %s", result, wantArtifact)
	}
}

func TestBuilderWithoutStagedSDKDoesNotTakeSDKLock(t *testing.T) {
	configRoot := t.TempDir()
	release, err := pigsdklock.AcquireStage(context.Background(), configRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module extension\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\nfunc main(\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = NewBuilderWithConfigRoot(t.TempDir(), configRoot).BuildContext(ctx, "extension", srcDir)
	if err == nil {
		t.Fatal("invalid extension unexpectedly built")
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "SDK transaction lock") {
		t.Fatalf("self-contained build waited for unrelated staged SDK lock: %v", err)
	}
}

func TestAC56BuilderUsesStagedGoSDKWithoutCommittedReplace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in short mode")
	}
	configRoot := t.TempDir()
	sdkDir := filepath.Join(configRoot, "state", "pigsdk", "sdk")
	if err := os.MkdirAll(sdkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkDir, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkDir, "sdk.go"), []byte("package sdk\nfunc Name() string { return \"staged\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcDir := t.TempDir()
	goMod := "module relocatable\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := "package main\nimport (\n \"fmt\"\n sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n)\nfunc main(){fmt.Print(sdk.Name())}\n"
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilder(t.TempDir())
	builder.configRoot = configRoot
	result, err := builder.Build("relocatable", srcDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.BinaryPath == "" {
		t.Fatal("builder returned no binary")
	}
	gotMod, err := os.ReadFile(filepath.Join(srcDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotMod) != goMod {
		t.Fatalf("builder mutated committed go.mod:\n%s", gotMod)
	}
	if err := os.WriteFile(filepath.Join(sdkDir, "sdk.go"), []byte("package sdk\nfunc Name() string { return \"staged-v2\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := builder.Build("relocatable", srcDir)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Cached || updated.BinaryPath == result.BinaryPath {
		t.Fatalf("staged SDK change reused cache: first=%+v updated=%+v", result, updated)
	}
}

func TestBuilderMissingStagedGoSDKFailsBeforeCompiler(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module missing-sdk\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\nimport _ \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilder(t.TempDir())
	builder.configRoot = t.TempDir()
	_, err := builder.Build("missing-sdk", srcDir)
	if err == nil || !strings.Contains(err.Error(), "staged Go SDK") || !strings.Contains(err.Error(), "pig reload") {
		t.Fatalf("missing staged SDK error = %v", err)
	}
}

func TestBuilderUsesStagedRustSDKWithoutCommittedPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in short mode")
	}
	cargoPath, err := exec.LookPath("cargo")
	if err != nil {
		t.Skip("cargo is not installed")
	}
	// The staged-SDK contract is independent of a host-wide Cargo source
	// replacement. CI's tools image points crates.io at an offline vendor that
	// intentionally does not contain pig-sdk; isolate Cargo configuration so the
	// build-local patch is the only source-resolution input under test. The same
	// image puts a cargo-auditable launcher first on PATH; its nested
	// `cargo metadata` does not inherit Cargo's command-line patch and is outside
	// this contract.
	t.Setenv("CARGO_HOME", t.TempDir())
	t.Setenv("CARGO_NET_OFFLINE", "true")
	t.Setenv("RUSTC_WRAPPER", "")
	t.Setenv("RUSTC_WORKSPACE_WRAPPER", "")
	t.Setenv("CARGO_BUILD_RUSTC_WRAPPER", "")
	t.Setenv("CARGO_BUILD_RUSTC_WORKSPACE_WRAPPER", "")
	if data, readErr := os.ReadFile(cargoPath); readErr == nil && strings.Contains(string(data), "cargo-auditable") {
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			candidate := filepath.Join(dir, "cargo")
			if candidate == cargoPath {
				continue
			}
			info, statErr := os.Stat(candidate)
			if statErr != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
				continue
			}
			candidateData, candidateErr := os.ReadFile(candidate)
			if candidateErr == nil && !strings.Contains(string(candidateData), "cargo-auditable") {
				cargoPath = candidate
				break
			}
		}
	}
	if data, readErr := os.ReadFile(cargoPath); readErr == nil && strings.Contains(string(data), "cargo-auditable") {
		t.Fatal("an unwrapped cargo executable is required for the staged SDK test")
	}
	t.Setenv("PATH", filepath.Dir(cargoPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	configRoot := t.TempDir()
	sdkDir := filepath.Join(configRoot, "state", "pigsdk", "sdk-rs")
	if err := os.MkdirAll(filepath.Join(sdkDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkDir, "Cargo.toml"), []byte("[package]\nname=\"pig-sdk\"\nversion=\"0.1.0\"\nedition=\"2024\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkDir, "src", "lib.rs"), []byte("pub fn name() -> &'static str { \"staged\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(t.TempDir(), "relocatable-rust")
	if err := os.MkdirAll(filepath.Join(srcDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	cargoToml := "[package]\nname=\"relocatable-rust\"\nversion=\"0.1.0\"\nedition=\"2024\"\n[dependencies]\npig-sdk=\"0.1.0\"\n"
	if err := os.WriteFile(filepath.Join(srcDir, "Cargo.toml"), []byte(cargoToml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "src", "main.rs"), []byte("fn main(){print!(\"{}\", pig_sdk::name());}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilder(t.TempDir())
	builder.configRoot = configRoot
	result, err := builder.Build("relocatable-rust", srcDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.BinaryPath == "" {
		t.Fatal("builder returned no Rust binary")
	}
	gotCargo, err := os.ReadFile(filepath.Join(srcDir, "Cargo.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotCargo) != cargoToml {
		t.Fatalf("builder mutated committed Cargo.toml:\n%s", gotCargo)
	}
}

func TestBuilderExplicitSDKOverridesWin(t *testing.T) {
	t.Run("go replace", func(t *testing.T) {
		srcDir := t.TempDir()
		mod := "module explicit-go\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => ./author-sdk\n"
		if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte(mod), 0o644); err != nil {
			t.Fatal(err)
		}
		// The author vendors their own SDK copy at the replace target. A
		// resolving override must win (no staging); only a dead path stages.
		mustWrite(t, filepath.Join(srcDir, "author-sdk", "go.mod"), "module author-sdk\ngo 1.26\n")
		builder := NewBuilder(t.TempDir())
		builder.configRoot = t.TempDir()
		if staged, err := builder.resolveStagedSDK(srcDir, "go"); err != nil || staged != "" {
			t.Fatalf("explicit Go replacement resolved staged SDK %q, %v", staged, err)
		}
	})

	t.Run("rust path", func(t *testing.T) {
		srcDir := t.TempDir()
		manifest := "[package]\nname=\"explicit-rust\"\nversion=\"0.1.0\"\n[dependencies]\npig-sdk={path=\"./author-sdk\"}\n"
		if err := os.WriteFile(filepath.Join(srcDir, "Cargo.toml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(srcDir, "author-sdk", "Cargo.toml"), "[package]\nname=\"pig-sdk\"\n")
		builder := NewBuilder(t.TempDir())
		builder.configRoot = t.TempDir()
		if staged, err := builder.resolveStagedSDK(srcDir, "rust"); err != nil || staged != "" {
			t.Fatalf("explicit Rust path resolved staged SDK %q, %v", staged, err)
		}
	})

	t.Run("dead go replace stages the SDK", func(t *testing.T) {
		// An installed extension whose replace points at a vanished dev checkout
		// (e.g. ../../../unrelated-checkout/extensions/sdk) must fall back to the
		// staged SDK rather than fail the build on the dead path.
		srcDir := t.TempDir()
		mod := "module dead-go\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => ../../gone/sdk\n"
		mustWrite(t, filepath.Join(srcDir, "go.mod"), mod)
		builder := NewBuilder(t.TempDir())
		configRoot := t.TempDir()
		builder.configRoot = configRoot
		stagedDir := filepath.Join(configRoot, "state", "pigsdk", "sdk")
		mustWrite(t, filepath.Join(stagedDir, "go.mod"), "module github.com/MichaelKinsy/PiG/extensions/sdk\n")
		if staged, err := builder.resolveStagedSDK(srcDir, "go"); err != nil || staged != stagedDir {
			t.Fatalf("dead Go replace resolved staged SDK %q (want %q), %v", staged, stagedDir, err)
		}
	})
}

func TestBuilder_DetectBuildType(t *testing.T) {
	// Go module directory.
	goDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module test\ngo 1.26\n"), 0o644)
	typ, err := detectBuildType(goDir)
	if err != nil {
		t.Fatalf("detect go: %v", err)
	}
	if typ != "go" {
		t.Errorf("type = %q, want go", typ)
	}

	// Rust crate directory.
	rsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(rsDir, "Cargo.toml"), []byte("[package]\nname = \"test\"\n"), 0o644)
	typ, err = detectBuildType(rsDir)
	if err != nil {
		t.Fatalf("detect rust: %v", err)
	}
	if typ != "rust" {
		t.Errorf("type = %q, want rust", typ)
	}

	// Direct TypeScript file.
	tsFile := filepath.Join(t.TempDir(), "fixture.ts")
	_ = os.WriteFile(tsFile, []byte("export default 1\n"), 0o644)
	typ, err = detectBuildType(tsFile)
	if err != nil {
		t.Fatalf("detect node file: %v", err)
	}
	if typ != "node" {
		t.Errorf("type = %q, want node", typ)
	}

	// Node directory.
	nodeDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(nodeDir, "index.ts"), []byte("export default 1\n"), 0o644)
	typ, err = detectBuildType(nodeDir)
	if err != nil {
		t.Fatalf("detect node dir: %v", err)
	}
	if typ != "node" {
		t.Errorf("type = %q, want node", typ)
	}

	// Unknown directory.
	emptyDir := t.TempDir()
	_, err = detectBuildType(emptyDir)
	if err == nil {
		t.Error("expected error for unknown dir")
	}
}

func TestBuilder_NodeHashIncludesSourcePath(t *testing.T) {
	srcA := filepath.Join(t.TempDir(), "fixture.ts")
	srcB := filepath.Join(t.TempDir(), "fixture.ts")
	content := []byte("export default 1\n")
	_ = os.WriteFile(srcA, content, 0o644)
	_ = os.WriteFile(srcB, content, 0o644)

	hashA, err := hashSourceDir(srcA, "node")
	if err != nil {
		t.Fatalf("hash A: %v", err)
	}
	hashB, err := hashSourceDir(srcB, "node")
	if err != nil {
		t.Fatalf("hash B: %v", err)
	}
	if hashA == hashB {
		t.Fatalf("node source hash ignored absolute path; moved checkouts would reuse stale launchers")
	}
}

func TestBuilder_HashChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in short mode")
	}

	// Create a minimal Go extension.
	srcDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module test\ngo 1.26\n"), 0o644)
	_ = os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644)

	cacheDir := t.TempDir()
	b := NewBuilder(cacheDir)

	// Build once.
	r1, err := b.Build("test", srcDir)
	if err != nil {
		t.Fatalf("Build 1: %v", err)
	}
	if r1.Cached {
		t.Error("first build should not be cached")
	}

	// Build again: should be cached.
	r2, err := b.Build("test", srcDir)
	if err != nil {
		t.Fatalf("Build 2: %v", err)
	}
	if !r2.Cached {
		t.Error("second build should be cached")
	}

	// Modify source: cache should miss.
	_ = os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"v2\") }\n"), 0o644)

	r3, err := b.Build("test", srcDir)
	if err != nil {
		t.Fatalf("Build 3: %v", err)
	}
	if r3.Cached {
		t.Error("third build should not be cached (source changed)")
	}
	if r3.BinaryPath == r1.BinaryPath {
		t.Error("different hash should produce different binary path")
	}
}

func mustWriteForHash(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHashSourceDirFollowsSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "extension")
	mustWriteForHash(t, filepath.Join(target, "go.mod"), "module example.com/extension\ngo 1.26\n")
	source := filepath.Join(target, "extension.go")
	mustWriteForHash(t, source, "package extension\nconst version = 1\n")
	absoluteLink := filepath.Join(base, "extension-absolute")
	testenv.Symlink(t, target, absoluteLink)
	relativeLink := filepath.Join(base, "extension-relative")
	testenv.Symlink(t, "extension", relativeLink)
	chainedLink := filepath.Join(base, "extension-chained")
	testenv.Symlink(t, "extension-relative", chainedLink)
	links := map[string]string{
		"absolute": absoluteLink,
		"relative": relativeLink,
		"chained":  chainedLink,
	}

	firstTarget, err := hashSourceDir(target, "go")
	if err != nil {
		t.Fatal(err)
	}
	firstLinks := make(map[string]string, len(links))
	for name, link := range links {
		firstLinks[name], err = hashSourceDir(link, "go")
		if err != nil {
			t.Fatalf("hash %s link: %v", name, err)
		}
		if firstLinks[name] != firstTarget {
			t.Fatalf("%s symlinked extension hash %q differs from target hash %q", name, firstLinks[name], firstTarget)
		}
	}
	mustWriteForHash(t, source, "package extension\nconst version = 2\n")
	secondTarget, err := hashSourceDir(target, "go")
	if err != nil {
		t.Fatal(err)
	}
	for name, link := range links {
		second, err := hashSourceDir(link, "go")
		if err != nil {
			t.Fatalf("hash updated %s link: %v", name, err)
		}
		if second == firstLinks[name] {
			t.Fatalf("%s symlinked extension hash did not change after source changed: %q", name, second)
		}
		if second != secondTarget {
			t.Fatalf("updated %s symlinked extension hash %q differs from target hash %q", name, second, secondTarget)
		}
	}
}

func TestHashSourceDirFollowsSymlinkedLocalReplace(t *testing.T) {
	base := t.TempDir()
	sdkTarget := filepath.Join(base, "sdk")
	mustWriteForHash(t, filepath.Join(sdkTarget, "go.mod"), "module example.com/sdk\ngo 1.26\n")
	sdkSource := filepath.Join(sdkTarget, "protocol.go")
	mustWriteForHash(t, sdkSource, "package sdk\nconst protocol = 1\n")
	sdkLink := filepath.Join(base, "sdk-link")
	testenv.Symlink(t, "sdk", sdkLink)
	extensionRoot := filepath.Join(base, "extension")
	mustWriteForHash(t, filepath.Join(extensionRoot, "go.mod"),
		"module example.com/extension\ngo 1.26\nrequire example.com/sdk v0.0.0\nreplace example.com/sdk => "+modfile.AutoQuote(sdkLink)+"\n")
	mustWriteForHash(t, filepath.Join(extensionRoot, "extension.go"), "package extension\n")

	before, err := hashSourceDir(extensionRoot, "go")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteForHash(t, sdkSource, "package sdk\nconst protocol = 2\n")
	after, err := hashSourceDir(extensionRoot, "go")
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatalf("extension hash did not change after source behind symlinked local replacement changed: %q", after)
	}
}

func TestHashSourceDirFollowsRelativeReplaceFromSymlinkedExtensionRoot(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "project")
	sdk := filepath.Join(project, "sdk")
	mustWriteForHash(t, filepath.Join(sdk, "go.mod"), "module example.com/sdk\ngo 1.26\n")
	sdkSource := filepath.Join(sdk, "protocol.go")
	mustWriteForHash(t, sdkSource, "package sdk\nconst protocol = 1\n")
	extensionRoot := filepath.Join(project, "extension")
	mustWriteForHash(t, filepath.Join(extensionRoot, "go.mod"),
		"module example.com/extension\ngo 1.26\nrequire example.com/sdk v0.0.0\nreplace example.com/sdk => ../sdk\n")
	mustWriteForHash(t, filepath.Join(extensionRoot, "extension.go"), "package extension\n")
	link := filepath.Join(base, "selected-extension")
	testenv.Symlink(t, extensionRoot, link)

	before, err := hashSourceDir(link, "go")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteForHash(t, sdkSource, "package sdk\nconst protocol = 2\n")
	after, err := hashSourceDir(link, "go")
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatalf("symlinked extension hash did not change after its relative replacement changed: %q", after)
	}
}

func TestHashSourceDirFollowsNestedSymlinkedLocalReplace(t *testing.T) {
	outside := t.TempDir()
	mustWriteForHash(t, filepath.Join(outside, "go.mod"), "module example.com/sdk\ngo 1.26\n")
	sdkSource := filepath.Join(outside, "protocol.go")
	mustWriteForHash(t, sdkSource, "package sdk\nconst protocol = 1\n")

	extensionRoot := t.TempDir()
	testenv.Symlink(t, outside, filepath.Join(extensionRoot, "linked-sdk"))
	mustWriteForHash(t, filepath.Join(extensionRoot, "go.mod"),
		"module example.com/extension\ngo 1.26\nrequire example.com/sdk v0.0.0\nreplace example.com/sdk => ./linked-sdk\n")
	mustWriteForHash(t, filepath.Join(extensionRoot, "extension.go"), "package extension\n")

	before, err := hashSourceDir(extensionRoot, "go")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteForHash(t, sdkSource, "package sdk\nconst protocol = 2\n")
	after, err := hashSourceDir(extensionRoot, "go")
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatalf("extension hash did not change after nested symlink replacement changed: %q", after)
	}
}

func TestHashSourceDirFollowsHiddenLocalReplace(t *testing.T) {
	extensionRoot := t.TempDir()
	sdkRoot := filepath.Join(extensionRoot, ".deps", "sdk")
	mustWriteForHash(t, filepath.Join(sdkRoot, "go.mod"), "module example.com/sdk\ngo 1.26\n")
	sdkSource := filepath.Join(sdkRoot, "protocol.go")
	mustWriteForHash(t, sdkSource, "package sdk\nconst protocol = 1\n")
	mustWriteForHash(t, filepath.Join(extensionRoot, "go.mod"),
		"module example.com/extension\ngo 1.26\nrequire example.com/sdk v0.0.0\nreplace example.com/sdk => ./.deps/sdk\n")
	mustWriteForHash(t, filepath.Join(extensionRoot, "extension.go"), "package extension\n")

	before, err := hashSourceDir(extensionRoot, "go")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteForHash(t, sdkSource, "package sdk\nconst protocol = 2\n")
	after, err := hashSourceDir(extensionRoot, "go")
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatalf("extension hash did not change after hidden local replacement changed: %q", after)
	}
}

func TestHashSourceDirRejectsBrokenAndCyclicRootSymlinks(t *testing.T) {
	base := t.TempDir()
	broken := filepath.Join(base, "broken")
	testenv.Symlink(t, "missing", broken)
	cycleA := filepath.Join(base, "cycle-a")
	cycleB := filepath.Join(base, "cycle-b")
	testenv.Symlink(t, "cycle-b", cycleA)
	testenv.Symlink(t, "cycle-a", cycleB)
	for name, root := range map[string]string{"broken": broken, "cyclic": cycleA} {
		if _, err := hashSourceDir(root, "go"); err == nil {
			t.Errorf("%s root hash succeeded, want resolution error", name)
		}
	}
}

func TestHashSourceDirDoesNotTraverseNestedDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	mustWriteForHash(t, filepath.Join(root, "go.mod"), "module example.com/extension\ngo 1.26\n")
	mustWriteForHash(t, filepath.Join(root, "extension.go"), "package extension\n")
	outside := t.TempDir()
	outsideSource := filepath.Join(outside, "outside.go")
	mustWriteForHash(t, outsideSource, "package outside\nconst version = 1\n")
	testenv.Symlink(t, outside, filepath.Join(root, "nested"))
	before, err := hashSourceDir(root, "go")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteForHash(t, outsideSource, "package outside\nconst version = 2\n")
	after, err := hashSourceDir(root, "go")
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("nested directory symlink changed source hash: before=%q after=%q", before, after)
	}
}

// TestHashSourceDir_LocalReplaceInvalidatesOnSDKChange is the regression for
// the stale-binary bug: a Go extension resolves the pig SDK through a local
// `replace`, so editing that SDK changes the compiled binary. The cache key
// must change too. Before the fix, hashSourceDir hashed only the extension's
// own files, so this test's two hashes were equal and pig served a binary
// built against the old SDK (skewed MaxFrameSize / register fields).
func TestHashSourceDir_LocalReplaceInvalidatesOnSDKChange(t *testing.T) {
	sdkDir := t.TempDir()
	mustWriteForHash(t, filepath.Join(sdkDir, "go.mod"), "module example.com/sdk\ngo 1.26\n")
	sdkFile := filepath.Join(sdkDir, "protocol.go")
	mustWriteForHash(t, sdkFile, "package sdk\n\nconst MaxFrameSize = 16 << 20\n")

	srcDir := t.TempDir()
	mustWriteForHash(t, filepath.Join(srcDir, "go.mod"),
		"module fixture-ext\ngo 1.26\nrequire example.com/sdk v0.0.0\nreplace example.com/sdk => "+modfile.AutoQuote(sdkDir)+"\n")
	mustWriteForHash(t, filepath.Join(srcDir, "main.go"), "package main\n\nfunc main() {}\n")

	before, err := hashSourceDir(srcDir, "go")
	if err != nil {
		t.Fatalf("hash before: %v", err)
	}

	// Change ONLY the replaced SDK source. The extension's own files are
	// byte-for-byte unchanged.
	mustWriteForHash(t, sdkFile, "package sdk\n\nconst MaxFrameSize = 128 << 20\n")

	after, err := hashSourceDir(srcDir, "go")
	if err != nil {
		t.Fatalf("hash after: %v", err)
	}
	if before == after {
		t.Fatalf("hash unchanged after the replaced SDK source changed (%s);\n"+
			"a stale binary built against the old SDK would be served from cache", before)
	}
}

// TestHashSourceDir_LocalReplacePathMixedIn proves relocating the SDK (same
// content, different path) also invalidates the cache, so a moved checkout
// cannot reuse a binary that embedded the previous absolute path.
func TestHashSourceDir_LocalReplacePathMixedIn(t *testing.T) {
	const sdkGoMod = "module example.com/sdk\ngo 1.26\n"
	const sdkSrc = "package sdk\n\nconst V = 1\n"

	sdkA := t.TempDir()
	mustWriteForHash(t, filepath.Join(sdkA, "go.mod"), sdkGoMod)
	mustWriteForHash(t, filepath.Join(sdkA, "sdk.go"), sdkSrc)

	sdkB := t.TempDir()
	mustWriteForHash(t, filepath.Join(sdkB, "go.mod"), sdkGoMod)
	mustWriteForHash(t, filepath.Join(sdkB, "sdk.go"), sdkSrc)

	hashWith := func(sdkDir string) string {
		srcDir := t.TempDir()
		mustWriteForHash(t, filepath.Join(srcDir, "go.mod"),
			"module fixture-ext\ngo 1.26\nrequire example.com/sdk v0.0.0\nreplace example.com/sdk => "+modfile.AutoQuote(sdkDir)+"\n")
		mustWriteForHash(t, filepath.Join(srcDir, "main.go"), "package main\n\nfunc main() {}\n")
		h, err := hashSourceDir(srcDir, "go")
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		return h
	}

	if hashWith(sdkA) == hashWith(sdkB) {
		t.Fatal("hash identical for SDKs at different paths; relocating the SDK must invalidate the cache")
	}
}

// TestHashSourceDir_IgnoresUndeclaredOutsideDir guards against over-broad
// hashing: only directories the extension actually replaces participate. An
// edit to an unrelated outside directory must not invalidate the cache.
func TestHashSourceDir_IgnoresUndeclaredOutsideDir(t *testing.T) {
	other := t.TempDir()
	otherFile := filepath.Join(other, "junk.go")
	mustWriteForHash(t, otherFile, "package junk\n")

	srcDir := t.TempDir()
	mustWriteForHash(t, filepath.Join(srcDir, "go.mod"), "module fixture-ext\ngo 1.26\n")
	mustWriteForHash(t, filepath.Join(srcDir, "main.go"), "package main\n\nfunc main() {}\n")

	before, err := hashSourceDir(srcDir, "go")
	if err != nil {
		t.Fatalf("hash before: %v", err)
	}
	mustWriteForHash(t, otherFile, "package junk\n\nvar X = 1\n")
	after, err := hashSourceDir(srcDir, "go")
	if err != nil {
		t.Fatalf("hash after: %v", err)
	}
	if before != after {
		t.Fatal("hash changed for an edit to a directory the extension does not replace")
	}
}

// TestGoLocalReplaceDirs covers the replace classifier: version-less local
// targets are included, while module-to-module replacements and missing
// targets are excluded. An explicit target inside the extension remains in the
// result because the general source walk may skip its symlink or hidden parent.
func TestGoLocalReplaceDirs(t *testing.T) {
	sdk := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	srcDir := t.TempDir()
	mustWriteForHash(t, filepath.Join(srcDir, "internal", "local", "x.go"), "package local\n")
	gomod := "module fixture-ext\ngo 1.26\n" +
		"replace example.com/sdk => " + modfile.AutoQuote(sdk) + "\n" +
		"replace example.com/pinned => other.com/pinned v1.4.0\n" +
		"replace example.com/missing => " + modfile.AutoQuote(missing) + "\n" +
		"replace example.com/inside => ./internal/local\n"
	mustWriteForHash(t, filepath.Join(srcDir, "go.mod"), gomod)

	dirs, err := goLocalReplaceDirs(srcDir)
	if err != nil {
		t.Fatalf("goLocalReplaceDirs: %v", err)
	}
	physicalSDK, err := filepath.EvalSymlinks(sdk)
	if err != nil {
		t.Fatal(err)
	}
	physicalInside, err := filepath.EvalSymlinks(filepath.Join(srcDir, "internal", "local"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{physicalSDK: true, physicalInside: true}
	if len(dirs) != len(want) {
		t.Fatalf("expected %v, got %v", want, dirs)
	}
	for _, dir := range dirs {
		if !want[dir] {
			t.Fatalf("unexpected local replacement %s in %v", dir, dirs)
		}
	}
}

func TestRewritePigSDKPath(t *testing.T) {
	// An installed Rust extension's Cargo.toml declares pig-sdk as a path
	// dependency pointing at a source-tree checkout. Once installed to
	// ~/.pig/extensions, that relative path is dead, and [patch.crates-io]
	// cannot redirect a path dep, so the build fails. stageRustSDKPath rewrites
	// the inline path to the staged SDK. This pins that rewrite.
	const staged = "/Users/x/.pig/state/pigsdk/sdk-rs"
	cases := []struct {
		name    string
		in      string
		want    string
		changed bool
	}{
		{
			"inline path dep",
			`pig-sdk = { path = "../../../../unrelated-checkout/extensions/sdk-rs" }`,
			`pig-sdk = { path = "/Users/x/.pig/state/pigsdk/sdk-rs" }`,
			true,
		},
		{
			"inline path dep with trailing keys",
			`pig-sdk = { path = "../x", version = "0.1" }`,
			`pig-sdk = { path = "/Users/x/.pig/state/pigsdk/sdk-rs", version = "0.1" }`,
			true,
		},
		{
			"registry dep left untouched",
			`pig-sdk = "0.1"`,
			`pig-sdk = "0.1"`,
			false,
		},
		{
			"no pig-sdk dep",
			`serde_json = "1"`,
			`serde_json = "1"`,
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := rewritePigSDKPath(tc.in, staged)
			if got != tc.want || changed != tc.changed {
				t.Fatalf("rewritePigSDKPath(%q) = (%q, %v), want (%q, %v)", tc.in, got, changed, tc.want, tc.changed)
			}
		})
	}
}

func TestCopyCrateWithStagedSDK(t *testing.T) {
	src := t.TempDir()
	const origManifest = "[package]\nname = \"rust-factory\"\nversion = \"0.1.0\"\nedition = \"2024\"\n\n[dependencies]\npig-sdk = { path = \"../../../../unrelated-checkout/extensions/sdk-rs\" }\nserde_json = \"1\"\n"
	mustWrite(t, filepath.Join(src, "Cargo.toml"), origManifest)
	mustWrite(t, filepath.Join(src, "src", "main.rs"), "fn main() {}\n")
	// A build-artifact dir that must NOT be copied.
	mustWrite(t, filepath.Join(src, "target", "release", "junk"), "stale\n")

	dst := filepath.Join(t.TempDir(), "rust-crate")
	const staged = "/Users/x/.pig/state/pigsdk/sdk-rs"
	if err := copyCrateWithStagedSDK(src, dst, staged); err != nil {
		t.Fatal(err)
	}

	// Parallel-safety invariant: the shared installed manifest is untouched.
	if got := readFile(t, filepath.Join(src, "Cargo.toml")); got != origManifest {
		t.Fatalf("source Cargo.toml mutated:\n%s", got)
	}
	// The copy points at the staged SDK and preserves other deps.
	gotCopy := readFile(t, filepath.Join(dst, "Cargo.toml"))
	if !strings.Contains(gotCopy, `pig-sdk = { path = "`+staged+`" }`) {
		t.Fatalf("copied Cargo.toml not redirected:\n%s", gotCopy)
	}
	if !strings.Contains(gotCopy, `serde_json = "1"`) {
		t.Fatalf("copied Cargo.toml dropped serde_json:\n%s", gotCopy)
	}
	// Source files are copied; build artifacts are not.
	if got := readFile(t, filepath.Join(dst, "src", "main.rs")); got != "fn main() {}\n" {
		t.Fatalf("src/main.rs not copied: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "target")); !os.IsNotExist(err) {
		t.Fatalf("target/ was copied (err=%v); it must be skipped", err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
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

func TestRustNeedsStagedSDK(t *testing.T) {
	// The staging gate must fire for an installed crate whose pig-sdk path
	// dependency is dead (the SDK-path bug), stay off when the author's path
	// resolves (source tree), and fire for a registry-form dep.
	realSDK := t.TempDir()
	mustWrite(t, filepath.Join(realSDK, "Cargo.toml"), "[package]\nname = \"pig-sdk\"\n")

	cases := []struct {
		name     string
		manifest string
		want     bool
	}{
		{"dead relative path dep", `[dependencies]` + "\n" + `pig-sdk = { path = "../../nonexistent/sdk-rs" }`, true},
		{"resolving path dep", `[dependencies]` + "\n" + `pig-sdk = { path = ` + strconv.Quote(realSDK) + ` }`, false},
		{"registry version dep", `[dependencies]` + "\n" + `pig-sdk = "0.1"`, true},
		{"no pig-sdk dep", `[dependencies]` + "\n" + `serde_json = "1"`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "Cargo.toml"), "[package]\nname = \"x\"\n\n"+tc.manifest+"\n")
			if got := rustNeedsStagedSDK(dir); got != tc.want {
				t.Fatalf("rustNeedsStagedSDK = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGoNeedsStagedSDK(t *testing.T) {
	realSDK := t.TempDir()
	mkGoMod := func(replaceLine string) string {
		return "module x\n\ngo 1.26\n\nrequire " + goSDKModule + " v0.0.0\n" + replaceLine
	}
	cases := []struct {
		name    string
		replace string
		want    bool
	}{
		{"require only, no replace", "", true},
		{"dead relative replace", "\nreplace " + goSDKModule + " => ../../nonexistent/sdk\n", true},
		{"resolving replace", "\nreplace " + goSDKModule + " => " + modfile.AutoQuote(realSDK) + "\n", false},
		{"module-version replace", "\nreplace " + goSDKModule + " => example.com/fork v1.2.3\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "go.mod"), mkGoMod(tc.replace))
			if got := goNeedsStagedSDK(dir); got != tc.want {
				t.Fatalf("goNeedsStagedSDK = %v, want %v", got, tc.want)
			}
		})
	}
	// A crate without the SDK requirement never needs staging.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module x\n\ngo 1.26\n")
	if goNeedsStagedSDK(dir) {
		t.Fatal("goNeedsStagedSDK = true for a module without the SDK require")
	}
}

func TestBuildGoIgnoresBrokenVCSMetadata(t *testing.T) {
	root := t.TempDir()
	mustWriteForHash(t, filepath.Join(root, "go.mod"), "module example.com/unstamped\n\ngo 1.26\n")
	mustWriteForHash(t, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
	git := exec.Command("git", "init", "--quiet")
	git.Dir = root
	git.Env = withoutGitEnvironmentOverrides(os.Environ())
	if output, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	mustWriteForHash(t, filepath.Join(root, ".git", "index"), "broken index")

	output := filepath.Join(t.TempDir(), "unstamped")
	if err := buildGo(t.Context(), root, output, ""); err != nil {
		t.Fatal(err)
	}
	info, err := buildinfo.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range info.Settings {
		if strings.HasPrefix(setting.Key, "vcs.") {
			t.Fatalf("built extension contains VCS-dependent setting %q=%q", setting.Key, setting.Value)
		}
	}
}
