package subprocess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/modfile"
	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/internal/buildprogress"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// BuildResult holds the outcome of an auto-build.
type BuildResult struct {
	BinaryPath string // path to the built (or cached) binary
	Cached     bool   // true if cache hit: no rebuild needed
	Hash       string // source content hash used for the cache key
	Language   string // detected build type (go | rust | node)
}

// Builder handles automatic compilation and caching of source-based extensions.
// It detects the build system (Go or Rust) from the source directory and uses
// content-hash caching so unchanged sources produce instant startup.
//
// Cache layout: ~/.pig/cache/ext/<name>-<hash>[.exe]
type Builder struct {
	cacheDir   string
	configRoot string

	// verifyStagedSDK, when set, reports whether a staged SDK directory matches
	// the SDK compiled into this binary. The staged copy lives outside core, so
	// the owner injects the comparison rather than core reaching for it. A
	// mismatch means every extension compiles against an SDK the host does not
	// speak, which surfaces as a fixed bug reappearing.
	verifyStagedSDK func(stagedDir, buildType string) error

	// buildMu prevents concurrent builds of the same extension.
	buildMu sync.Mutex
}

// SetStagedSDKVerifier installs the staged-SDK freshness check. Without one the
// builder only requires the directory to exist.
func (b *Builder) SetStagedSDKVerifier(fn func(stagedDir, buildType string) error) {
	b.verifyStagedSDK = fn
}

// NewBuilder creates a builder with the given cache directory.
// Typically <config-root>/cache/ext/.
func NewBuilder(cacheDir string) *Builder {
	return NewBuilderWithConfigRoot(cacheDir, resolveConfigRoot())
}

// NewBuilderWithConfigRoot creates a builder pinned to the same writable
// config root as the running Pig process. Product assembly should prefer this
// form so an overridden HOME/XDG root cannot diverge from extension builds.
func NewBuilderWithConfigRoot(cacheDir, configRoot string) *Builder {
	if abs, err := filepath.Abs(cacheDir); err == nil {
		cacheDir = abs
	}
	if strings.TrimSpace(configRoot) == "" {
		configRoot = resolveConfigRoot()
	}
	if abs, err := filepath.Abs(configRoot); err == nil {
		configRoot = abs
	}
	return &Builder{cacheDir: cacheDir, configRoot: configRoot}
}

func resolveConfigRoot() string {
	if root := strings.TrimSpace(os.Getenv("PIG_HOME")); root != "" {
		return root
	}
	if root := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); root != "" {
		return filepath.Join(root, "pig")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pig")
}

// Build compiles the extension source at srcDir if needed. Returns the path
// to the binary (cached or freshly built).
//
// Detection:
//   - go.mod present → Go extension (go build)
//   - Cargo.toml present → Rust extension (cargo build --release)
//   - package.json / *.ts / *.js → Node/TypeScript extension (launcher script)
//
// The binary is cached at <cacheDir>/<name>-<hash>. If the hash matches an
// existing cached binary, no build occurs (instant startup).
func (b *Builder) Build(name, srcDir string) (*BuildResult, error) {
	return b.BuildContext(context.Background(), name, srcDir)
}

// BuildContext builds an extension and cancels compiler subprocesses when ctx
// ends. Build retains the background-context compatibility entry point.
func (b *Builder) BuildContext(ctx context.Context, name, srcDir string) (*BuildResult, error) {
	// Serialize builds to prevent concurrent compilation of the same extension
	// and partial-write corruption of cache entries.
	b.buildMu.Lock()
	defer b.buildMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Resolve srcDir to absolute.
	abs, err := filepath.Abs(srcDir)
	if err != nil {
		return nil, fmt.Errorf("resolve source dir: %w", err)
	}
	srcDir = abs

	// Detect build system.
	buildType, err := detectBuildType(srcDir)
	if err != nil {
		return nil, err
	}
	// reuseOnly returns a published entry, or nil without building or waiting.
	build := func(reuseOnly bool) (*BuildResult, error) {
		stagedSDK, err := b.resolveStagedSDK(srcDir, buildType)
		if err != nil {
			return nil, err
		}

		// Compute content hash of source files.
		hash, err := hashSourceDir(srcDir, buildType)
		if err != nil {
			return nil, fmt.Errorf("hash source: %w", err)
		}
		if stagedSDK != "" {
			hash, err = hashWithStagedSDK(hash, stagedSDK, buildType)
			if err != nil {
				return nil, fmt.Errorf("hash staged %s SDK: %w", buildType, err)
			}
		}

		// A Piglet Binary may embed a prebuilt binary for this source-mode extension.
		// Consulting the resolver here (before any compile) lets a toolchain-less
		// Piglet Binary serve it without cargo/go. Nil resolver (stock pig) falls through.
		if bin, ok := runtimecell.ResolvePrebuilt(runtimecell.PrebuiltRequest{
			Language: buildType, Key: name, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
			Extensions: []runtimecell.PrebuiltExtension{{Name: name, Hash: hash}},
		}); ok {
			return &BuildResult{BinaryPath: bin, Cached: true, Hash: hash, Language: buildType}, nil
		}

		// Content-addressed cache entry: <name>-<fullhash>/bin. Sharing the runtime
		// cell publish primitive gives source-mode extensions the same cross-process
		// dedup as packed cells: parallel Pig instances building the same extension
		// take a per-digest lock, so exactly one compiles and the rest reuse the
		// published binary instead of racing on a shared cache path.
		finalDir := filepath.Join(b.cacheDir, fileNameComponent(name)+"-"+hash)
		// A Node launcher keeps a sibling <bin>.runtime/ tree next to the launcher;
		// every other build type publishes a single self-contained binary.
		if reuseOnly {
			entry, ok := runtimecell.ReusePublishedArtifact(finalDir, runtimecell.EntryIdentity{InputDigest: hash, Artifact: extensionArtifactName(runtime.GOOS, buildType), Language: buildType})
			if !ok || !fileExists(filepath.Join(finalDir, sdkFingerprintFile)) {
				return nil, nil
			}
			return &BuildResult{BinaryPath: entry.ArtifactPath, Cached: true, Hash: hash, Language: buildType}, nil
		}
		artifactName := extensionArtifactName(runtime.GOOS, buildType)
		var keepAlso []string
		if buildType == "node" {
			keepAlso = []string{artifactName + ".runtime"}
		}
		start := time.Now()
		entry, err := runtimecell.PublishArtifact(ctx, finalDir, artifactName, hash, buildType, func(scratch string) (string, error) {
			// Preflight the compiler toolchain before announcing a build, so a
			// toolchain-less host gets actionable guidance instead of an opaque exec
			// error deep in go build / cargo build. Piglet Binaries and prebuilt
			// packages ship compiled binaries served by the resolver above or reused
			// from a warm cache, so neither reaches this build closure.
			if err := ensureBuildToolchain(ctx, buildType); err != nil {
				return "", err
			}
			// pig compiles extensions where upstream pi has no build step; emit the
			// first-run notice only to an interactive stderr so print/json mode and
			// piped/CI output stay byte-clean and match pi.
			// pig additive (D18): explicit builds own the progress display instead of the runtime-load notice.
			if !buildprogress.Enabled(ctx) && term.IsTerminal(int(os.Stderr.Fd())) {
				fmt.Fprintf(os.Stderr, "Building extension %s (first run, will be cached)...\n", name)
			}
			out := filepath.Join(scratch, artifactName)
			switch buildType {
			case "go":
				if err := buildGo(ctx, srcDir, out, stagedSDK); err != nil {
					return "", fmt.Errorf("go build: %w", err)
				}
			case "rust":
				if err := buildRust(ctx, srcDir, out, stagedSDK); err != nil {
					return "", fmt.Errorf("cargo build: %w", err)
				}
			case "node":
				if err := buildNode(srcDir, out); err != nil {
					return "", fmt.Errorf("node launcher: %w", err)
				}
			}
			return out, nil
		}, keepAlso...)
		if err != nil {
			return nil, err
		}
		if !entry.Reused && !buildprogress.Enabled(ctx) && term.IsTerminal(int(os.Stderr.Fd())) {
			fmt.Fprintf(os.Stderr, "Built %s in %s\n", name, time.Since(start).Round(time.Millisecond))
		}
		// Record the SDK this build compiled against. A later SDK change makes the
		// build unreachable (the cache key folds the SDK in), and the fingerprint is
		// what lets a prune prove that rather than guess from age.
		//
		// The SDK reaches a build two ways: injected from the staged copy, or via
		// the extension's own local replace. Both must be fingerprinted. An
		// extension whose replace already resolves never takes the staged path, so
		// keying only on stagedSDK would leave every such build unfingerprinted and
		// indistinguishable from one of unknown provenance.
		if fp, err := b.buildSDKFingerprint(srcDir, stagedSDK, buildType); err == nil && fp != "" {
			marker := filepath.Join(filepath.Dir(entry.ArtifactPath), sdkFingerprintFile)
			_ = os.WriteFile(marker, []byte(fp), 0o644)
		}

		return &BuildResult{BinaryPath: entry.ArtifactPath, Cached: entry.Reused, Hash: hash, Language: buildType}, nil
	}
	if buildNeedsStagedSDK(srcDir, buildType) {
		// A warm entry is found without the staged-SDK lease: its content hash
		// covers the staged SDK, so a hash read during another process's restage
		// names no published entry and falls through to the leased path.
		if result, err := build(true); err == nil && result != nil {
			return result, nil
		}
		return pigsdklock.WithBuild(ctx, b.configRoot, func() (*BuildResult, error) { return build(false) })
	}
	return build(false)
}

func buildNeedsStagedSDK(srcDir, buildType string) bool {
	switch buildType {
	case "go":
		return goNeedsStagedSDK(srcDir)
	case "rust":
		return rustNeedsStagedSDK(srcDir)
	default:
		return false
	}
}

// detectBuildType determines the build system from a source path.
// src may be a directory (Go/Rust/Node package) or a direct .ts/.js file.
func detectBuildType(src string) (string, error) {
	if info, err := os.Stat(src); err == nil && !info.IsDir() {
		if isNodeSourcePath(src) {
			return "node", nil
		}
		return "", fmt.Errorf("cannot detect build type in %s: unsupported source file", src)
	}
	if _, err := os.Stat(filepath.Join(src, "go.mod")); err == nil {
		return "go", nil
	}
	if _, err := os.Stat(filepath.Join(src, "Cargo.toml")); err == nil {
		return "rust", nil
	}
	if hasNodeSource(src) {
		return "node", nil
	}
	return "", fmt.Errorf("cannot detect build type in %s: need go.mod, Cargo.toml, package.json, or a .ts/.js entrypoint", src)
}

// hashSourceDir computes a SHA-256 hash of all relevant source files.
func hashSourceDir(src, buildType string) (string, error) {
	lexicalRoot, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	physicalRoot, err := filepath.EvalSymlinks(lexicalRoot)
	if err != nil {
		return "", err
	}
	src = physicalRoot
	h := sha256.New()
	h.Write([]byte(buildType))
	if buildType == "node" {
		// Node launchers embed the absolute entrypoint path. Include the
		// lexical source path so a moved checkout cannot reuse a stale
		// wrapper that points at the previous location. Source bytes and
		// relative dependencies are read from the resolved physical root.
		h.Write([]byte(lexicalRoot))
		h.Write([]byte(nodeRuntimeVersion))
		h.Write([]byte(nodeLauncherFormat))
		// Hash every embedded runtime file so changes to runtime.mjs,
		// cli.mjs, the loader, or any shim invalidate cached launchers
		// without requiring a manual nodeRuntimeVersion bump.
		_ = fs.WalkDir(nodeRuntimeFS, "runtime-node", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := nodeRuntimeFS.ReadFile(p)
			if err != nil {
				return err
			}
			h.Write([]byte(p))
			h.Write(data)
			return nil
		})
	}

	var exts []string
	switch buildType {
	case "go":
		exts = []string{".go", ".mod", ".sum"}
	case "rust":
		exts = []string{".rs", ".toml", ".lock"}
	case "node":
		exts = []string{".ts", ".js", ".mjs", ".cjs", ".json"}
	}

	info, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(src)
		if err != nil {
			return "", err
		}
		h.Write([]byte(filepath.Base(src)))
		h.Write(data)
		return hex.EncodeToString(h.Sum(nil)), nil
	}

	if err := hashDirSources(h, src, exts); err != nil {
		return "", err
	}

	// Go extensions resolve the pig SDK (and any other local dependency)
	// through `replace` directives that point outside the extension directory.
	// `go build` embeds that replaced source, so a change there produces a
	// different binary. Hash the replaced directories too: without this,
	// editing the SDK leaves the extension's own files untouched, the cache
	// key is unchanged, and pig keeps serving a binary built against the old
	// SDK: which silently breaks the host↔extension wire contract
	// (MaxFrameSize, register fields) the instant the host advances. The
	// replaced path is mixed in as well so relocating the SDK also
	// invalidates the cache.
	if buildType == "go" {
		dirs, err := goLocalReplaceDirs(src)
		if err != nil {
			return "", err
		}
		for _, dir := range dirs {
			h.Write([]byte("\x00replace\x00"))
			h.Write([]byte(dir))
			if err := hashDirSources(h, dir, exts); err != nil {
				return "", err
			}
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashDirSources walks root and mixes every source file whose name ends in one
// of exts into h, keyed by its path relative to root, in deterministic
// filepath.WalkDir order. Hidden, vendor, build-output, and dependency
// directories are skipped to match what the compiler actually reads.
func hashDirSources(h hash.Hash, root string, exts []string) error {
	physicalRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	physicalRoot, err = filepath.EvalSymlinks(physicalRoot)
	if err != nil {
		return err
	}
	// WalkDir does not traverse a symlink used as its root. Resolve only the
	// admitted root; nested symlink entries remain excluded by WalkDir.
	root = physicalRoot
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if path != root && (strings.HasPrefix(base, ".") || base == "target" || base == "vendor" || base == "node_modules" || base == "dist") {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip transient per-build artifacts. buildGo writes a uniquely-named
		// temp go.mod/go.sum (.pig-build-*) into the extension source dir for the
		// staged-SDK build. Hashing it let concurrent builds in separate
		// processes observe each other's transient temp modfiles and mint a
		// divergent cache key per instance for identical source -- cache
		// thrashing and redundant rebuilds when many pig instances share an
		// extension. The temp modfile is not real source; the staged SDK it
		// points at is already hashed separately via hashWithStagedSDK.
		if strings.HasPrefix(d.Name(), ".pig-build-") {
			return nil
		}
		for _, ext := range exts {
			if strings.HasSuffix(path, ext) {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				rel, _ := filepath.Rel(root, path)
				h.Write([]byte(rel))
				h.Write(data)
				break
			}
		}
		return nil
	})
}

// goLocalReplaceDirs returns physical existing directories that srcDir's go.mod
// replaces with a local filesystem path. These hold dependency source, most
// importantly the Pig extension SDK, that the build embeds. An explicit local
// replacement participates even when its lexical path is inside srcDir because
// the general source walk deliberately skips nested symlinks and hidden trees.
func goLocalReplaceDirs(srcDir string) ([]string, error) {
	physicalSource, err := filepath.EvalSymlinks(srcDir)
	if err != nil {
		return nil, err
	}
	srcDir = physicalSource
	modPath := filepath.Join(srcDir, "go.mod")
	data, err := os.ReadFile(modPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// A malformed go.mod is left to `go build` to report; the go.mod bytes are
	// already hashed directly by hashDirSources, so skipping replace resolution
	// here forgoes only the extra invalidation, never something already
	// captured.
	f, err := modfile.Parse(modPath, data, nil)
	if err != nil {
		return nil, nil
	}
	seen := map[string]struct{}{}
	var dirs []string
	for _, r := range f.Replace {
		// Module-to-module replacements (=> other/module v1.2.3) carry a
		// version and are fetched from the module cache, not local source.
		if r.New.Version != "" {
			continue
		}
		target := r.New.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(srcDir, target)
		}
		target = filepath.Clean(target)
		physicalTarget, err := filepath.EvalSymlinks(target)
		if err != nil {
			continue // unresolved replace; the build will surface it
		}
		info, err := os.Stat(physicalTarget)
		if err != nil || !info.IsDir() {
			continue // unresolved replace; the build will surface it
		}
		target = physicalTarget
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		dirs = append(dirs, target)
	}
	slices.Sort(dirs)
	return dirs, nil
}

// isWithin reports whether target is base or a path nested under it.
func isWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

const (
	goSDKModule  = "github.com/MichaelKinsy/PiG/extensions/sdk"
	rustSDKCrate = "pig-sdk"
)

// buildSDKFingerprint identifies the SDK a build compiled against, whether it
// came from the staged copy or from the extension's own resolving replace.
// Fingerprinting content rather than location means a replace pointing at the
// staged SDK (directly or through a symlink) produces the same value, so such a
// build is correctly recognised as current.
func (b *Builder) buildSDKFingerprint(srcDir, stagedSDK, buildType string) (string, error) {
	if stagedSDK != "" {
		return StagedSDKFingerprint(stagedSDK, buildType)
	}
	if buildType != "go" {
		return "", nil
	}
	dirs, err := goLocalReplaceDirs(srcDir)
	if err != nil || len(dirs) == 0 {
		return "", err
	}
	for _, dir := range dirs {
		if fp, err := StagedSDKFingerprint(dir, buildType); err == nil {
			return fp, nil
		}
	}
	return "", nil
}

func (b *Builder) resolveStagedSDK(srcDir, buildType string) (string, error) {
	var needed bool
	var stagedDir string
	var label string
	switch buildType {
	case "go":
		needed = goNeedsStagedSDK(srcDir)
		stagedDir = filepath.Join(b.configRoot, "state", "pigsdk", "sdk")
		label = "Go"
	case "rust":
		needed = rustNeedsStagedSDK(srcDir)
		stagedDir = filepath.Join(b.configRoot, "state", "pigsdk", "sdk-rs")
		label = "Rust"
	default:
		return "", nil
	}
	if !needed {
		return "", nil
	}
	if info, err := os.Stat(stagedDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("staged %s SDK is missing at %s; run `pig reload` with the active config root", label, stagedDir)
	}
	if b.verifyStagedSDK != nil {
		if err := b.verifyStagedSDK(stagedDir, buildType); err != nil {
			return "", err
		}
	}
	return stagedDir, nil
}

func goNeedsStagedSDK(srcDir string) bool {
	path := filepath.Join(srcDir, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	file, err := modfile.Parse(path, data, nil)
	if err != nil {
		return false
	}
	required := false
	for _, requirement := range file.Require {
		if requirement.Mod.Path == goSDKModule {
			required = true
			break
		}
	}
	if !required {
		return false
	}
	for _, replacement := range file.Replace {
		if replacement.Old.Path == goSDKModule {
			// A filesystem replace is authoritative only if its target resolves.
			// In the source tree the scaffold's relative replace points at the
			// real SDK module; once installed to ~/.pig/extensions that path is
			// dead, so the staged SDK must override it (buildGo does this via a
			// temp -modfile, leaving the installed go.mod untouched). A
			// module-version replace is a registry redirect and stands.
			if replacement.New.Version != "" {
				return false
			}
			resolved := replacement.New.Path
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(srcDir, resolved)
			}
			if _, err := os.Stat(resolved); err == nil {
				return false
			}
			return true
		}
	}
	return true
}

func rustNeedsStagedSDK(srcDir string) bool {
	var manifest struct {
		Dependencies map[string]any `toml:"dependencies"`
	}
	if _, err := toml.DecodeFile(filepath.Join(srcDir, "Cargo.toml"), &manifest); err != nil {
		return false
	}
	dependency, ok := manifest.Dependencies[rustSDKCrate]
	if !ok {
		return false
	}
	if config, ok := dependency.(map[string]any); ok {
		if path, _ := config["path"].(string); path != "" {
			// A path dependency is authoritative only if it resolves. In the
			// source tree the scaffold's relative path points at the real SDK
			// crate, so no staging is needed. Once the extension is installed to
			// ~/.pig/extensions that relative path is dead, so the staged SDK
			// must redirect it (buildRust isolates the crate to do so without
			// mutating the shared manifest).
			resolved := path
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(srcDir, resolved)
			}
			if _, err := os.Stat(filepath.Join(resolved, "Cargo.toml")); err == nil {
				return false
			}
		}
	}
	return true
}

func hashWithStagedSDK(baseHash, sdkDir, buildType string) (string, error) {
	h := sha256.New()
	_, _ = io.WriteString(h, baseHash)
	_, _ = io.WriteString(h, "\x00staged-sdk\x00"+buildType+"\x00")
	exts := []string{".go", ".mod", ".sum"}
	if buildType == "rust" {
		exts = []string{".rs", ".toml", ".lock"}
	}
	if err := hashDirSources(h, sdkDir, exts); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ensureBuildToolchain fails early with actionable guidance when the toolchain a
// source extension needs is not installed. pig compiles extensions (upstream pi
// does not), so on a bare host the build otherwise dies with an opaque
// "exec: \"go\": executable file not found in $PATH". The remedy is to install
// the toolchain or run the extension from a Piglet Binary or prebuilt package, which
// ship a compiled binary and need no toolchain. Unknown build types are a no-op.
func ensureBuildToolchain(ctx context.Context, buildType string) error {
	if buildType == "node" {
		_, err := ensureNodeRuntime(ctx)
		return err
	}
	tool := map[string]string{"go": "go", "rust": "cargo"}[buildType]
	if tool == "" {
		return nil
	}
	if _, err := exec.LookPath(tool); err != nil {
		return fmt.Errorf("cannot build %s extension: the %q toolchain is not installed; install it, or run this extension from a Piglet Binary or prebuilt package (which need no toolchain)", buildType, tool)
	}
	return nil
}

// buildGo compiles a Go module to the given output path.
// VCS stamping is disabled so an extension build depends only on its source and toolchain, not on the availability or health of repository metadata around the source.
// Uses a temp file + rename to avoid partial binaries on failure.
func buildGo(ctx context.Context, srcDir, outPath, stagedSDK string) error {
	tmpPath := outPath + ".tmp"
	args := []string{"build", "-buildvcs=false", "-trimpath", "-ldflags", "-s -w", "-o", tmpPath}
	cleanup := func() {}
	if stagedSDK != "" {
		modPath, remove, err := stagedGoModFile(srcDir, stagedSDK)
		if err != nil {
			return err
		}
		cleanup = remove
		args = append(args, "-modfile="+modPath)
	}
	defer cleanup()
	args = append(args, ".")
	goCommand, err := toolchain.Go()
	if err != nil {
		return err
	}
	buildprogress.Phase(ctx, "Compiling Go member", srcDir+" (module resolution, compile, link)")
	cmd := exec.CommandContext(ctx, goCommand, buildprogress.ToolArgs(ctx, "go", args)...)
	cmd.Dir = srcDir
	cmd.Env = append(goBuildEnvironment(srcDir, os.Environ()), "CGO_ENABLED=0", "GOWORK=off")
	out, err := buildprogress.CombinedOutput(ctx, cmd)
	if err != nil {
		_ = os.Remove(tmpPath) // Clean up partial.
		return fmt.Errorf("%w\n%s", err, out)
	}
	if err := rejectNonExecutableArtifact(tmpPath, srcDir); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	// Atomic rename: no partial binary visible at outPath.
	return os.Rename(tmpPath, outPath)
}

func goBuildEnvironment(source string, environment []string) []string {
	for _, value := range environment {
		if workTree, ok := strings.CutPrefix(value, "GIT_WORK_TREE="); ok && isWithin(workTree, source) {
			return append([]string(nil), environment...)
		}
	}
	return withoutGitEnvironmentOverrides(environment)
}

func withoutGitEnvironmentOverrides(environment []string) []string {
	return slices.DeleteFunc(append([]string(nil), environment...), func(value string) bool {
		return strings.HasPrefix(value, "GIT_DIR=") || strings.HasPrefix(value, "GIT_WORK_TREE=")
	})
}

// archiveMagic opens a Unix ar archive, which is what the Go toolchain writes
// when asked to build a package that declares something other than `main`.
const archiveMagic = "!<arch>\n"

// isArchive reports whether a build artifact is a static library rather than a
// program.
func isArchive(data []byte) bool {
	return strings.HasPrefix(string(data), archiveMagic)
}

// rejectNonExecutableArtifact turns a silently useless build into a build error.
//
// `go build -o out .` exits 0 for a package that is not `main`, writing a
// library archive to the output path. Nothing downstream inspects the artifact,
// so the extension is cached as built and the problem first appears when the
// host executes it, reported as a launch failure that names neither the package
// nor the fix.
func rejectNonExecutableArtifact(path, srcDir string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read build output: %w", err)
	}
	defer func() { _ = f.Close() }()
	header := make([]byte, len(archiveMagic))
	if _, err := io.ReadFull(f, header); err != nil {
		return fmt.Errorf("read build output: %w", err)
	}
	if !isArchive(header) {
		return nil
	}
	return fmt.Errorf(
		"%s compiled to a library archive, not a program: its package is not `main`.\n"+
			"An extension built this way cannot be executed. Either give the package "+
			"`package main` with a `func main()`, or expose "+
			"`func Extension() *sdk.Extension` so Pig builds it through the factory path",
		srcDir)
}

// buildRust compiles a Rust crate and copies the binary to outPath.
// Uses a temp file + rename to avoid partial binaries on failure.
func buildRust(ctx context.Context, srcDir, outPath, stagedSDK string) error {
	buildDir := srcDir
	args := []string{}
	if stagedSDK != "" {
		// Redirect the pig-sdk path dependency (the SDK scaffold's canonical
		// form) to the staged SDK. Cargo has no `--modfile` equivalent to Go's
		// staged go.mod, and [patch.crates-io] cannot redirect a path dependency
		// (cargo reads the stale/relative dead path before patch resolution),
		// which is why an installed crate whose Cargo.toml points at a moved
		// checkout fails to build. When the crate declares an inline pig-sdk path
		// dep, build an isolated copy with a rewritten manifest in this build's
		// private scratch dir (the parent of outPath). This never mutates the
		// shared installed crate, so concurrent Pig builds and manifest readers
		// stay safe. A registry-form `pig-sdk = "x.y"` dep needs no copy and is
		// handled by the crates-io patch below.
		if hasInlinePigSDKPath(srcDir) {
			buildDir = filepath.Join(filepath.Dir(outPath), "rust-crate")
			if err := copyCrateWithStagedSDK(srcDir, buildDir, stagedSDK); err != nil {
				return err
			}
		}
		args = append(args, "--config", "patch.crates-io."+rustSDKCrate+".path="+strconv.Quote(filepath.ToSlash(stagedSDK)))
	}
	args = append(args, "build", "--release", "--quiet")
	buildprogress.Phase(ctx, "Compiling Rust member", srcDir+" (dependency resolution, compile, link)")
	cmd := exec.CommandContext(ctx, "cargo", buildprogress.ToolArgs(ctx, "rust", args)...)
	cmd.Dir = buildDir
	cmd.Env = withoutGitEnvironmentOverrides(os.Environ())
	out, err := buildprogress.CombinedOutput(ctx, cmd)
	if err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}

	var manifest struct {
		Package struct {
			Name string `toml:"name"`
		} `toml:"package"`
	}
	if _, err := toml.DecodeFile(filepath.Join(srcDir, "Cargo.toml"), &manifest); err != nil {
		return fmt.Errorf("read Rust standalone package name: %w", err)
	}
	if manifest.Package.Name == "" {
		return fmt.Errorf("Rust standalone %s has no Cargo package name", srcDir)
	}
	binarySuffix := strings.TrimPrefix(extensionArtifactName(runtime.GOOS, "rust"), "bin")
	builtBin := filepath.Join(cargoBuildTargetDirectory(buildDir), "release", manifest.Package.Name+binarySuffix)
	if _, err := os.Stat(builtBin); err != nil {
		return fmt.Errorf("built binary not found at %s", builtBin)
	}

	// Copy to temp, then rename (atomic).
	tmpPath := outPath + ".tmp"
	data, err := os.ReadFile(builtBin)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpPath, data, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpPath, outPath)
}

func cargoBuildTargetDirectory(workingDirectory string) string {
	target := strings.TrimSpace(os.Getenv("CARGO_TARGET_DIR"))
	if target == "" {
		return filepath.Join(workingDirectory, "target")
	}
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(workingDirectory, target)
}

// pigSDKInlinePathRe matches an inline `pig-sdk = { ... path = "..." }`
// dependency, capturing everything up to (and including) the opening quote of
// the path value so it can be replaced. Registry-form deps (`pig-sdk = "x.y"`)
// and section-form `[dependencies.pig-sdk]` tables do not match; the SDK
// scaffolds emit the inline path form.
var pigSDKInlinePathRe = regexp.MustCompile(`(pig-sdk\s*=\s*\{[^}]*?path\s*=\s*)"[^"]*"`)

// rewritePigSDKPath redirects an inline pig-sdk path dependency to newPath,
// returning the rewritten manifest and whether a replacement occurred.
func rewritePigSDKPath(manifest, newPath string) (string, bool) {
	loc := pigSDKInlinePathRe.FindStringSubmatchIndex(manifest)
	if loc == nil {
		return manifest, false
	}
	// loc[3] ends capture group 1 (through `path = `); loc[1] ends the whole
	// match (just after the old quoted value).
	quoted := strconv.Quote(filepath.ToSlash(newPath))
	return manifest[:loc[3]] + quoted + manifest[loc[1]:], true
}

// hasInlinePigSDKPath reports whether srcDir/Cargo.toml declares an inline
// pig-sdk path dependency that must be redirected to the staged SDK.
func hasInlinePigSDKPath(srcDir string) bool {
	data, err := os.ReadFile(filepath.Join(srcDir, "Cargo.toml"))
	if err != nil {
		return false
	}
	return pigSDKInlinePathRe.Match(data)
}

// copyCrateWithStagedSDK copies the Rust crate at srcDir into dstDir (skipping
// build artifacts and VCS metadata) and rewrites the copied Cargo.toml's inline
// pig-sdk path dependency to stagedSDK. Building the copy leaves the shared
// installed crate untouched, keeping concurrent Pig builds and manifest readers
// safe.
func copyCrateWithStagedSDK(srcDir, dstDir, stagedSDK string) error {
	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dstDir, 0o755)
		}
		if d.IsDir() {
			if base := filepath.Base(rel); base == "target" || base == ".git" {
				return fs.SkipDir
			}
			return os.MkdirAll(filepath.Join(dstDir, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks and other non-regular files
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dstDir, rel), data, info.Mode().Perm())
	}
	if err := filepath.WalkDir(srcDir, walk); err != nil {
		return fmt.Errorf("copy rust crate: %w", err)
	}
	manifestPath := filepath.Join(dstDir, "Cargo.toml")
	orig, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	rewritten, changed := rewritePigSDKPath(string(orig), stagedSDK)
	if !changed {
		return nil
	}
	return os.WriteFile(manifestPath, []byte(rewritten), 0o644)
}

func stagedGoModFile(srcDir, stagedSDK string) (string, func(), error) {
	path := filepath.Join(srcDir, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", func() {}, err
	}
	file, err := modfile.Parse(path, data, nil)
	if err != nil {
		return "", func() {}, fmt.Errorf("parse go.mod for staged SDK: %w", err)
	}
	if err := file.AddReplace(goSDKModule, "", filepath.ToSlash(stagedSDK), ""); err != nil {
		return "", func() {}, fmt.Errorf("add staged SDK replacement: %w", err)
	}
	encoded, err := file.Format()
	if err != nil {
		return "", func() {}, fmt.Errorf("format build-local go.mod: %w", err)
	}
	temp, err := os.CreateTemp(srcDir, ".pig-build-*.mod")
	if err != nil {
		return "", func() {}, err
	}
	tempPath := temp.Name()
	sumPath := strings.TrimSuffix(tempPath, ".mod") + ".sum"
	cleanup := func() {
		_ = os.Remove(tempPath)
		_ = os.Remove(sumPath)
	}
	if _, err := temp.Write(encoded); err != nil {
		_ = temp.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	// Go resolves the module checksum file next to the -modfile (same base name,
	// .sum). Seed it with the extension's existing go.sum so third-party
	// dependencies verify offline; without this, a -modfile build reports
	// "missing go.sum entry" for every module the extension imports. Extensions
	// with only the local SDK replace and stdlib have no go.sum and need none.
	if sum, err := os.ReadFile(filepath.Join(srcDir, "go.sum")); err == nil {
		if err := os.WriteFile(sumPath, sum, 0o644); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return tempPath, cleanup, nil
}

// CurrentCacheEntry returns the valid source-build cache entry selected by the
// same source and staged-SDK hash as BuildContext, without building it.
func (b *Builder) CurrentCacheEntry(name, srcDir string) (string, bool, error) {
	absolute, err := filepath.Abs(srcDir)
	if err != nil {
		return "", false, err
	}
	buildType, err := detectBuildType(absolute)
	if err != nil {
		return "", false, err
	}
	stagedSDK, err := b.resolveStagedSDK(absolute, buildType)
	if err != nil {
		return "", false, err
	}
	hash, err := hashSourceDir(absolute, buildType)
	if err != nil {
		return "", false, err
	}
	if stagedSDK != "" {
		hash, err = hashWithStagedSDK(hash, stagedSDK, buildType)
		if err != nil {
			return "", false, err
		}
	}
	entry := filepath.Join(b.cacheDir, fileNameComponent(name)+"-"+hash)
	_, valid := runtimecell.ValidEntry(entry, runtimecell.EntryIdentity{InputDigest: hash, Artifact: extensionArtifactName(runtime.GOOS, buildType), Language: buildType})
	return entry, valid, nil
}
