package runtimecell

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
	"golang.org/x/term"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/internal/buildprogress"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// GoExtension describes one factory-style Go SDK extension that can be packed
// into a generated subprocess runner. The extension package must expose a
// factory function with signature:
//
//	func Extension() *sdk.Extension
//
// or the Factory name supplied here.
type GoExtension struct {
	Name             string
	Root             string
	ModulePath       string
	Package          string
	Factory          string
	Hash             string
	WorkspaceModules []string
}

// goPackedBuildFlags are the `go build` flags for a packed cell.
// VCS stamping is disabled so the cell does not depend on metadata in the generated build directory or an extension's repository.
// -s -w drop the symbol table and DWARF, which a cell never needs. Go stack traces come from pclntab, so panics stay legible and only debugger attachment is given up.
// Cells are embedded verbatim in Piglet/Piglet Binaries, so the saving is paid back per fused cell.
// These flags are part of the cell hash below because they change the produced binary. A cached cell built with different flags must not be reused.
var goPackedBuildFlags = []string{"-buildvcs=false", "-trimpath", "-ldflags", "-s -w"}

// hasGoCompilerDiagnostic recognizes diagnostics emitted by the Go compiler for
// source that cannot compile. Module resolution, network, toolchain, and other
// setup failures do not have both the package header and file position.
func hasGoCompilerDiagnostic(output []byte) bool {
	sawPackageHeader := false
	for line := range bytes.SplitSeq(output, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("# ")) {
			sawPackageHeader = true
			continue
		}
		if sawPackageHeader && isGoSourceDiagnostic(line) {
			return true
		}
	}
	return false
}

func isGoSourceDiagnostic(line []byte) bool {
	marker := bytes.LastIndex(line, []byte(".go:"))
	if marker < 0 {
		return false
	}
	rest := line[marker+len(".go:"):]
	consumeNumber := func() bool {
		start := len(rest)
		for len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
			rest = rest[1:]
		}
		return len(rest) < start
	}
	if !consumeNumber() || len(rest) == 0 || rest[0] != ':' {
		return false
	}
	rest = rest[1:]
	if consumeNumber() {
		if len(rest) == 0 || rest[0] != ':' {
			return false
		}
		rest = rest[1:]
	}
	return len(bytes.TrimSpace(rest)) > 0
}

// GoPackedCell describes a generated runner artifact. Packing is an execution
// optimization only: the generated binary is still a subprocess and each
// contained extension uses its own host socket.
type GoPackedCell struct {
	Key           string
	Hash          string
	CacheDir      string
	BinaryPath    string
	Cached        bool
	BuildDuration time.Duration
	Extensions    []GoExtension
	// Generation is this spawn attempt's packed-cell generation number
	// (pig additive, CNC-002), claimed by the caller before any build work so
	// an async crash report for a process this attempt is replacing cannot
	// mistake itself for still current. Zero (the default, for callers that
	// never claim one) always compares equal to an un-bumped counter, so
	// leaving it unset preserves prior behavior exactly.
	Generation int
}

// BuildGoPackedCell generates and builds a Go runner that hosts multiple
// factory-style Go SDK extensions in one subprocess artifact. The artifact is
// cached by a deterministic cell hash derived from the extension hashes and
// factory/package metadata.
func BuildGoPackedCell(ctx context.Context, cacheRoot, key string, extensions []GoExtension) (*GoPackedCell, error) {
	return buildGoPackedCell(ctx, cacheRoot, key, extensions, "")
}

// BuildGoPackedCellWithSDKRoot builds a packed cell using preferredSDKRoot when
// the extensions do not declare their own valid SDK replacement. Hosts use
// this to keep packed builds on the running process's resolved config root.
func BuildGoPackedCellWithSDKRoot(ctx context.Context, cacheRoot, key string, extensions []GoExtension, preferredSDKRoot string) (*GoPackedCell, error) {
	return buildGoPackedCell(ctx, cacheRoot, key, extensions, preferredSDKRoot)
}

func buildGoPackedCell(ctx context.Context, cacheRoot, key string, extensions []GoExtension, preferredSDKRoot string) (*GoPackedCell, error) {
	if len(extensions) == 0 {
		return nil, fmt.Errorf("packed cell requires at least one extension")
	}
	prebuiltExtensions, err := normalizeGoExtensions(extensions, false)
	if err != nil {
		return nil, err
	}
	if bin, ok := resolvePrebuilt(PrebuiltRequest{
		Language: "go", Key: key, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Extensions: goPrebuiltExtensions(prebuiltExtensions),
	}); ok {
		return &GoPackedCell{Key: key, Hash: "prebuilt:" + bin, CacheDir: filepath.Dir(bin), BinaryPath: bin, Cached: true, Extensions: prebuiltExtensions}, nil
	}
	normalized, err := normalizeGoExtensions(extensions, true)
	if err != nil {
		return nil, err
	}
	if cell, ok := reuseGoPackedCell(cacheRoot, key, normalized, preferredSDKRoot); ok {
		return cell, nil
	}
	lockCandidates := []string{preferredSDKRoot, os.Getenv("PIG_SDK_GO_ROOT")}
	for _, extension := range normalized {
		lockCandidates = append(lockCandidates, GoSDKPathOverride(extension.Root))
	}
	lockCandidates = append(lockCandidates, stagedGoSDKRoots()...)
	return pigsdklock.WithBuildCandidates(ctx, lockCandidates, func() (*GoPackedCell, error) {
		sdkRoot, err := findSDKRootWithPreferred(normalized, preferredSDKRoot)
		if err != nil {
			return nil, err
		}
		return pigsdklock.WithBuildForRoot(ctx, sdkRoot, func() (*GoPackedCell, error) {
			hash := goPackedCellHash(cacheRoot, key, normalized, sdkRoot)
			if after, ok := strings.CutPrefix(hash, "error:"); ok {
				return nil, fmt.Errorf("hash Go packed cell: %s", after)
			}
			cellDir := filepath.Join(cacheRoot, "cells", "go", hash)
			start := time.Now()
			artifactName := packedRunnerName(runtime.GOOS, "go")
			entry, err := publishArtifactWithFailureCache(ctx, cellDir, artifactName, hash, "go", func(scratch string) (string, error) {
				names := make([]string, len(normalized))
				for i, ext := range normalized {
					names[i] = ext.Name
				}
				// pig additive (D18): explicit builds own their progress display; runtime loads retain their normal diagnostics.
				if !buildprogress.Enabled(ctx) && term.IsTerminal(int(os.Stderr.Fd())) {
					fmt.Fprintf(os.Stderr, "Building extensions %s (first run, will be cached)...\n", strings.Join(names, ", "))
				}
				// Build generated source outside the cache root. A user's home can contain
				// an unrelated or malformed .git directory; Go VCS discovery must not bind
				// a generated cell to that parent repository. The finished artifact is
				// still published atomically from the cache-local scratch directory.
				buildDir, err := os.MkdirTemp("", "pig-go-cell-build-*")
				if err != nil {
					return "", fmt.Errorf("create generated Go build directory: %w", err)
				}
				defer func() { _ = os.RemoveAll(buildDir) }()
				if requiresLegacyGoSDK(normalized) {
					if err := stageLegacyGoSDK(sdkRoot, filepath.Join(buildDir, legacyGoSDKDir)); err != nil {
						return "", err
					}
				}
				if err := os.WriteFile(filepath.Join(buildDir, "go.mod"), []byte(renderGoMod(normalized, sdkRoot)), 0o644); err != nil {
					return "", fmt.Errorf("write generated go.mod: %w", err)
				}
				if goSum := mergeGoSum(normalized); goSum != "" {
					if err := os.WriteFile(filepath.Join(buildDir, "go.sum"), []byte(goSum), 0o644); err != nil {
						return "", fmt.Errorf("write merged go.sum: %w", err)
					}
				}
				if err := os.WriteFile(filepath.Join(buildDir, "main.go"), []byte(renderGoRunner(normalized)), 0o644); err != nil {
					return "", fmt.Errorf("write generated runner: %w", err)
				}
				out := filepath.Join(scratch, artifactName)
				goCommand, err := toolchain.Go()
				if err != nil {
					explained, _ := explainMissingToolchain("go", err)
					return "", explained
				}
				buildprogress.Phase(ctx, "Compiling Go members", strings.Join(names, ", ")+" (module resolution, compile, link)")
				args := append(append([]string{"build"}, goPackedBuildFlags...), "-o", out, ".")
				cmd := exec.CommandContext(ctx, goCommand, buildprogress.ToolArgs(ctx, "go", args)...)
				cmd.Dir = buildDir
				cmd.Env = append(cacheBuildEnvironment(buildDir), "CGO_ENABLED=0", "GOWORK=off")
				if combined, err := buildprogress.CombinedOutput(ctx, cmd); err != nil {
					invalidateCommandVersion(cacheRoot, goCommand, "version")
					if explained, ok := explainMissingToolchain("go", err); ok {
						return "", explained
					}
					generatedMod, _ := os.ReadFile(filepath.Join(buildDir, "go.mod"))
					buildErr := fmt.Errorf("build generated packed runner: %w\n%s\ngo.mod:\n%s", err, combined, generatedMod)
					if ctx.Err() != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() < 0 || !hasGoCompilerDiagnostic(combined) {
						return "", buildErr
					}
					return "", cacheBuildFailure(buildErr)
				}
				return out, nil
			})
			if err != nil {
				return nil, err
			}
			var dur time.Duration
			if !entry.Reused {
				dur = time.Since(start)
				if !buildprogress.Enabled(ctx) && term.IsTerminal(int(os.Stderr.Fd())) {
					fmt.Fprintf(os.Stderr, "Built extensions in %s (cached for next run)\n", dur.Round(time.Millisecond))
				}
			}
			return &GoPackedCell{Key: key, Hash: hash, CacheDir: entry.Dir, BinaryPath: entry.ArtifactPath, Cached: entry.Reused, BuildDuration: dur, Extensions: normalized}, nil
		})
	})
}

// reuseGoPackedCell returns an already-built cell without the staged-SDK
// lease. The cell hash covers the SDK tree, so a hash read while another
// process restages the SDK names no published entry and the caller falls back
// to the leased path. A hit therefore proves the entry was built from exactly
// the SDK content just read, which is what the leased lookup would return.
func reuseGoPackedCell(cacheRoot, key string, normalized []GoExtension, preferredSDKRoot string) (*GoPackedCell, bool) {
	sdkRoot, err := findSDKRootWithPreferred(normalized, preferredSDKRoot)
	if err != nil {
		return nil, false
	}
	hash := goPackedCellHash(cacheRoot, key, normalized, sdkRoot)
	if strings.HasPrefix(hash, "error:") {
		return nil, false
	}
	entry, ok := ReusePublishedArtifact(filepath.Join(cacheRoot, "cells", "go", hash), EntryIdentity{InputDigest: hash, Artifact: packedRunnerName(runtime.GOOS, "go"), Language: "go"})
	if !ok {
		return nil, false
	}
	return &GoPackedCell{Key: key, Hash: hash, CacheDir: entry.Dir, BinaryPath: entry.ArtifactPath, Cached: true, Extensions: normalized}, true
}

func normalizeGoExtensions(extensions []GoExtension, requireFactory bool) ([]GoExtension, error) {
	out := append([]GoExtension(nil), extensions...)
	for i := range out {
		ext := &out[i]
		ext.Name = strings.TrimSpace(ext.Name)
		ext.Root = strings.TrimSpace(ext.Root)
		ext.ModulePath = strings.TrimSpace(ext.ModulePath)
		ext.Package = strings.TrimSpace(ext.Package)
		ext.Factory = strings.TrimSpace(ext.Factory)
		ext.Hash = strings.TrimSpace(ext.Hash)
		for j := range ext.WorkspaceModules {
			ext.WorkspaceModules[j] = filepath.Clean(ext.WorkspaceModules[j])
			if physical, err := filepath.EvalSymlinks(ext.WorkspaceModules[j]); err == nil {
				ext.WorkspaceModules[j] = physical
			}
		}
		sort.Strings(ext.WorkspaceModules)
		if ext.Name == "" {
			return nil, fmt.Errorf("extension[%d]: name is required", i)
		}
		if ext.Root == "" {
			return nil, fmt.Errorf("extension %q: root is required", ext.Name)
		}
		abs, err := filepath.Abs(ext.Root)
		if err != nil {
			return nil, fmt.Errorf("extension %q: resolve root: %w", ext.Name, err)
		}
		ext.Root = abs
		if physical, err := filepath.EvalSymlinks(ext.Root); err == nil {
			ext.Root = physical
		}
		if ext.ModulePath == "" {
			return nil, fmt.Errorf("extension %q: module path is required", ext.Name)
		}
		if ext.Package == "" {
			ext.Package = ext.ModulePath
		}
		if requireFactory && ext.Factory != "Extension" {
			return nil, fmt.Errorf("extension %q: factory must be func Extension() *sdk.Extension", ext.Name)
		}
		if ext.Hash == "" {
			ext.Hash = "unknown"
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func goPackedCellHash(cacheRoot, key string, extensions []GoExtension, sdkRoot string) string {
	sdkHash := hashTree(sdkRoot)
	if strings.HasPrefix(sdkHash, "error:") {
		return sdkHash
	}
	h := sha256.New()
	h.Write([]byte("pig-go-packed-cell\x00"))
	h.Write([]byte(key))
	h.Write([]byte("\x00"))
	hashBuildInput(h, "sdk", sdkHash)
	goCommand, err := toolchain.Go()
	if err != nil {
		return "error:" + err.Error()
	}
	hashBuildInput(h, "runtime", commandVersion(cacheRoot, goCommand, "version"))
	hashBuildInput(h, "buildflags", strings.Join(goPackedBuildFlags, " "))
	hashBuildInput(h, "template", renderGoRunner(extensions))
	if requiresLegacyGoSDK(extensions) {
		hashBuildInput(h, "sdk-alias", extsource.LegacyGoSDKModulePath)
	}
	for _, ext := range extensions {
		for _, part := range []string{ext.Name, ext.ModulePath, ext.Package, ext.Factory, ext.Hash} {
			h.Write([]byte(part))
			h.Write([]byte("\x00"))
		}
		for _, moduleRoot := range ext.WorkspaceModules {
			workspaceHash := hashTree(moduleRoot)
			if after, ok := strings.CutPrefix(workspaceHash, "error:"); ok {
				return "error:hash Go workspace module " + moduleRoot + ": " + after
			}
			hashBuildInput(h, "workspace:"+moduleRoot, workspaceHash)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// findSDKRoot locates the Go SDK source directory. Explicit author overrides
// win; otherwise packed builds use the running Pig config root's staged SDK,
// matching ordinary source builds. Checkout/source-tree fallbacks serve tests
// and development binaries that have not staged an SDK yet. Every candidate is
// validated for its go.mod before use: a stale override (a moved checkout, a
// dead PIG_SDK_GO_ROOT) falls through to the stable staged SDK instead of
// baking a dead replace path into the generated go.mod.
func findSDKRoot(extensions []GoExtension) (string, error) {
	return findSDKRootWithPreferred(extensions, "")
}

func findSDKRootWithPreferred(extensions []GoExtension, preferred string) (string, error) {
	for _, ext := range extensions {
		if candidate := GoSDKPathOverride(ext.Root); candidate != "" {
			return candidate, nil
		}
	}
	if preferred = strings.TrimSpace(preferred); preferred != "" {
		if abs, err := filepath.Abs(preferred); err == nil {
			if _, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil {
				return abs, nil
			}
		}
	}
	if root := strings.TrimSpace(os.Getenv("PIG_SDK_GO_ROOT")); root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			if _, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil {
				return abs, nil
			}
		}
	}
	for _, sdk := range stagedGoSDKRoots() {
		if _, err := os.Stat(filepath.Join(sdk, "go.mod")); err == nil {
			return sdk, nil
		}
	}
	wd, err := os.Getwd()
	if err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
			if err == nil && strings.Contains(string(data), "module github.com/MichaelKinsy/PiG") {
				if sdk := filepath.Join(dir, "extensions", "sdk"); statHasFile(sdk, "go.mod") {
					return sdk, nil
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	for _, root := range installedPigSourceRoots() {
		sdk := filepath.Join(root, "extensions", "sdk")
		if _, err := os.Stat(filepath.Join(sdk, "go.mod")); err == nil {
			return sdk, nil
		}
	}
	return "", fmt.Errorf("cannot locate github.com/MichaelKinsy/PiG SDK; install pig (so ~/.pig/source exists), run inside the pig source tree, or set PIG_SDK_GO_ROOT")
}

// GoSDKPathOverride returns an extension module's explicit local SDK replace.
func GoSDKPathOverride(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	file, err := modfile.Parse(filepath.Join(root, "go.mod"), data, nil)
	if err != nil {
		return ""
	}
	for _, replacement := range file.Replace {
		if replacement.Old.Path != "github.com/MichaelKinsy/PiG/extensions/sdk" || replacement.New.Version != "" {
			continue
		}
		candidate := replacement.New.Path
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, candidate)
		}
		candidate = filepath.Clean(candidate)
		if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
			return candidate
		}
	}
	return ""
}

func stagedGoSDKRoots() []string {
	return stagedSDKRoots("sdk")
}

// statHasFile reports whether dir/name exists. It validates a candidate SDK root
// by its marker file (go.mod / Cargo.toml / pig_sdk/__init__.py) before the root
// is baked into a generated manifest, so a stale override never emits a dead
// replace/path directive.
func statHasFile(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func stagedSDKRoots(name string) []string {
	if root := strings.TrimSpace(os.Getenv("PIG_HOME")); root != "" {
		return []string{filepath.Join(root, "state", "pigsdk", name)}
	}
	if root := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); root != "" {
		return []string{filepath.Join(root, "pig", "state", "pigsdk", name)}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return []string{filepath.Join(home, ".pig", "state", "pigsdk", name)}
	}
	return nil
}

// installedPigSourceRoots returns candidate pig source roots staged outside the
// build cwd: PIG_SOURCE_ROOT and the data-dir copy at ~/.pig/source that pig
// writes at install time. These let an installed pig resolve its SDK with no
// per-extension configuration.
func installedPigSourceRoots() []string {
	var roots []string
	if r := strings.TrimSpace(os.Getenv("PIG_SOURCE_ROOT")); r != "" {
		roots = append(roots, r)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, ".pig", "source"))
	}
	return roots
}

func renderGoMod(extensions []GoExtension, sdkRoot string) string {
	var b strings.Builder
	b.WriteString("module pig.generated/packed-cell\n\ngo 1.26.0\n\n")
	fmt.Fprintf(&b, "require github.com/MichaelKinsy/PiG/extensions/sdk %s // indirect\n", sdkVersionFor(extensions))
	legacySDK := requiresLegacyGoSDK(extensions)
	if legacySDK {
		fmt.Fprintf(&b, "require %s v0.0.0 // indirect\n", extsource.LegacyGoSDKModulePath)
	}
	modules := make(map[string]string, len(extensions))
	for _, ext := range extensions {
		if !extsource.IsGoSDKModulePath(ext.ModulePath) {
			modules[ext.ModulePath] = ext.Root
		}
		for _, moduleRoot := range ext.WorkspaceModules {
			if modulePath, err := modulePathAt(moduleRoot); err == nil && !extsource.IsGoSDKModulePath(modulePath) {
				modules[modulePath] = moduleRoot
			}
		}
	}
	modulePaths := make([]string, 0, len(modules))
	for modulePath := range modules {
		modulePaths = append(modulePaths, modulePath)
	}
	sort.Strings(modulePaths)
	for _, modulePath := range modulePaths {
		fmt.Fprintf(&b, "require %s v0.0.0\n", modulePath)
	}
	// Promote transitive requirements and local replacements from extension
	// modules into the generated main module. Go ignores replacement directives
	// declared by dependency modules.
	extraReqs, extraReps := collectExtensionDeps(extensions)
	extraVersions := map[string]string{}
	for _, requirement := range extraReqs {
		fields := strings.Fields(requirement)
		if len(fields) < 2 {
			continue
		}
		if _, localModule := modules[fields[0]]; localModule {
			continue
		}
		if current := extraVersions[fields[0]]; current == "" || semver.Compare(fields[1], current) > 0 {
			extraVersions[fields[0]] = fields[1]
		}
	}
	extraModules := make([]string, 0, len(extraVersions))
	for modulePath := range extraVersions {
		extraModules = append(extraModules, modulePath)
	}
	sort.Strings(extraModules)
	for _, modulePath := range extraModules {
		fmt.Fprintf(&b, "require %s %s // indirect\n", modulePath, extraVersions[modulePath])
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "replace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", modfile.AutoQuote(filepath.ToSlash(sdkRoot)))
	// A legacy SDK import resolves to a build-local copy of the current SDK
	// (see stageLegacyGoSDK). The extension's own legacy replace is dropped with
	// its other SDK lines: it names the SDK staged by an older Pig, whose wire
	// may not match this host.
	if legacySDK {
		fmt.Fprintf(&b, "replace %s => ./%s\n", extsource.LegacyGoSDKModulePath, legacyGoSDKDir)
	}
	for _, modulePath := range modulePaths {
		fmt.Fprintf(&b, "replace %s => %s\n", modulePath, modfile.AutoQuote(filepath.ToSlash(modules[modulePath])))
	}
	for _, rep := range extraReps {
		if fields := strings.Fields(rep); len(fields) > 0 {
			if _, localModule := modules[fields[0]]; localModule {
				continue
			}
		}
		fmt.Fprintf(&b, "replace %s\n", rep)
	}
	return b.String()
}

func sdkVersionFor(extensions []GoExtension) string {
	version := "v0.0.0"
	for _, ext := range extensions {
		data, err := os.ReadFile(filepath.Join(ext.Root, "go.mod"))
		if err != nil {
			continue
		}
		file, err := modfile.Parse(filepath.Join(ext.Root, "go.mod"), data, nil)
		if err != nil {
			continue
		}
		for _, requirement := range file.Require {
			if requirement.Mod.Path == "github.com/MichaelKinsy/PiG/extensions/sdk" && semver.Compare(requirement.Mod.Version, version) > 0 {
				version = requirement.Mod.Version
			}
		}
	}
	return version
}

// legacyGoSDKDir is the generated-module directory holding the current SDK
// under its legacy module path. Go rejects one directory replacing two module
// paths, so the legacy path needs its own copy.
const legacyGoSDKDir = "legacy-sdk"

// requiresLegacyGoSDK reports whether any extension module (or one of its
// workspace modules) requires the legacy Go SDK module path.
func requiresLegacyGoSDK(extensions []GoExtension) bool {
	for _, ext := range extensions {
		for _, root := range append([]string{ext.Root}, ext.WorkspaceModules...) {
			data, err := os.ReadFile(filepath.Join(root, "go.mod"))
			if err != nil {
				continue
			}
			file, err := modfile.Parse(filepath.Join(root, "go.mod"), data, nil)
			if err != nil {
				continue
			}
			for _, requirement := range file.Require {
				if requirement.Mod.Path == extsource.LegacyGoSDKModulePath {
					return true
				}
			}
		}
	}
	return false
}

// stageLegacyGoSDK copies the current SDK package from sdkRoot into dst and
// declares it as the legacy module path, so a legacy import compiles against
// the SDK that speaks this host's protocol.
func stageLegacyGoSDK(sdkRoot, dst string) error {
	entries, err := os.ReadDir(sdkRoot)
	if err != nil {
		return fmt.Errorf("read Go SDK for legacy module path: %w", err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create legacy Go SDK copy: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sdkRoot, name))
		if err != nil {
			return fmt.Errorf("copy Go SDK for legacy module path: %w", err)
		}
		if name == "go.mod" {
			file, err := modfile.Parse(filepath.Join(sdkRoot, name), data, nil)
			if err != nil {
				return fmt.Errorf("parse Go SDK module: %w", err)
			}
			if err := file.AddModuleStmt(extsource.LegacyGoSDKModulePath); err != nil {
				return fmt.Errorf("declare legacy Go SDK module: %w", err)
			}
			if data, err = file.Format(); err != nil {
				return fmt.Errorf("format legacy Go SDK module: %w", err)
			}
		}
		if err := os.WriteFile(filepath.Join(dst, name), data, 0o644); err != nil {
			return fmt.Errorf("write legacy Go SDK copy: %w", err)
		}
	}
	return nil
}

func modulePathAt(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if modulePath, ok := strings.CutPrefix(line, "module "); ok && strings.TrimSpace(modulePath) != "" {
			return strings.TrimSpace(modulePath), nil
		}
	}
	return "", fmt.Errorf("%s has no module directive", filepath.Join(root, "go.mod"))
}

func renderGoRunner(extensions []GoExtension) string {
	var b strings.Builder
	b.WriteString("package main\n\nimport (\n")
	b.WriteString("\t\"fmt\"\n\t\"net\"\n\t\"os\"\n\t\"slices\"\n\t\"strings\"\n\t\"sync\"\n\n")
	for _, ext := range extensions {
		pkgName := extPackageName(ext.Name)
		fmt.Fprintf(&b, "\t%s %q\n", pkgName, ext.Package)
	}
	b.WriteString(")\n\n")
	b.WriteString("func main() {\n")
	b.WriteString("\ttype item struct { name string; env string; run func(string) error }\n")
	b.WriteString("\titems := []item{\n")
	for _, ext := range extensions {
		pkgName := extPackageName(ext.Name)
		fmt.Fprintf(&b, "\t\t{name: %q, env: %q, run: func(sock string) error { return %s.%s().RunWithSocket(sock) }},\n", ext.Name, SocketEnvName(ext.Name), pkgName, ext.Factory)
	}
	b.WriteString("\t}\n")
	b.WriteString("\tvar wg sync.WaitGroup\n\terrs := make(chan error, len(items))\n")
	b.WriteString("\tactive := os.Getenv(\"PIG_EXT_ACTIVE_MEMBERS\")\n")
	b.WriteString("\tfor _, it := range items {\n\t\tit := it\n\t\tif active != \"\" && !slices.Contains(strings.Split(active, \",\"), it.name) { continue }\n\t\tsock := os.Getenv(it.env)\n\t\tif sock == \"\" && len(items) == 1 { sock = os.Getenv(\"PIG_EXT_SOCKET\") }\n\t\tif sock == \"\" { errs <- fmt.Errorf(\"%s not set for %s\", it.env, it.name); continue }\n\t\twg.Add(1)\n\t\tgo func() { defer wg.Done(); if err := it.run(sock); err != nil && !strings.Contains(err.Error(), \"use of closed network connection\") { fmt.Fprintf(os.Stderr, \"%s: %v\\n\", it.name, err); if c, dialErr := net.Dial(\"unix\", sock); dialErr == nil { _ = c.Close() }; errs <- fmt.Errorf(\"%s: %w\", it.name, err) } }()\n\t}\n")
	b.WriteString("\twg.Wait()\n\tclose(errs)\n\tfor err := range errs { if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) } }\n")
	b.WriteString("}\n")
	return b.String()
}

var nonEnv = regexp.MustCompile(`[^A-Za-z0-9_]`)

// SocketEnvName returns the environment variable used to pass one extension's
// socket into a packed-cell runner.
func SocketEnvName(name string) string {
	name = strings.ToUpper(nonEnv.ReplaceAllString(name, "_"))
	return "PIG_EXT_SOCKET_" + name
}

// extPackageName converts an extension name to a valid Go package name for
// the local subpackage used in packed cells. Hyphens become underscores.
var nonIdent = regexp.MustCompile(`[^a-zA-Z0-9_]`)

func extPackageName(name string) string {
	return "ext_" + nonIdent.ReplaceAllString(strings.ReplaceAll(name, "-", "_"), "")
}

// ExtPackageName returns a deterministic import alias for a generated packed
// or fused extension registry.
func ExtPackageName(name string) string { return extPackageName(name) }

// collectExtensionDeps reads each extension's go.mod and collects the require
// and replace directives that do not name the SDK, formatted as go.mod
// directive bodies. This ensures third-party dependencies work in the packed
// cell. A relative local replacement is made absolute against the extension
// root, and a path that needs quoting (for example one containing a space) is
// quoted.
func collectExtensionDeps(extensions []GoExtension) (requires, replaces []string) {
	seenReq := map[string]bool{}
	seenRep := map[string]bool{}
	for _, ext := range extensions {
		if ext.Root == "" {
			continue
		}
		path := filepath.Join(ext.Root, "go.mod")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		file, err := modfile.Parse(path, data, nil)
		if err != nil {
			continue
		}
		for _, requirement := range file.Require {
			if extsource.IsGoSDKModulePath(requirement.Mod.Path) {
				continue
			}
			value := requirement.Mod.Path + " " + requirement.Mod.Version
			if requirement.Indirect {
				value += " // indirect"
			}
			if !seenReq[value] {
				seenReq[value] = true
				requires = append(requires, value)
			}
		}
		for _, replacement := range file.Replace {
			if extsource.IsGoSDKModulePath(replacement.Old.Path) || extsource.IsGoSDKModulePath(replacement.New.Path) {
				continue
			}
			value := extensionReplaceDirective(ext.Root, replacement)
			if !seenRep[value] {
				seenRep[value] = true
				replaces = append(replaces, value)
			}
		}
	}
	sort.Strings(requires)
	sort.Strings(replaces)
	return
}

func extensionReplaceDirective(root string, replacement *modfile.Replace) string {
	old := modfile.AutoQuote(replacement.Old.Path)
	if replacement.Old.Version != "" {
		old += " " + replacement.Old.Version
	}
	target := replacement.New.Path
	if replacement.New.Version == "" && !filepath.IsAbs(target) && strings.HasPrefix(target, ".") {
		target = filepath.ToSlash(filepath.Clean(filepath.Join(root, target)))
	}
	target = modfile.AutoQuote(target)
	if replacement.New.Version != "" {
		target += " " + replacement.New.Version
	}
	return old + " => " + target
}

// mergeGoSum reads go.sum from all extensions and returns the union of all
// entries. This avoids needing network access (go mod tidy/download) during
// packed cell builds.
func mergeGoSum(extensions []GoExtension) string {
	seen := map[string]bool{}
	var lines []string
	for _, ext := range extensions {
		if ext.Root == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ext.Root, "go.sum"))
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// CurrentGoPackedCellEntry returns the valid cache entry selected by the same
// normalization and hash inputs as BuildGoPackedCell, without building it.
func CurrentGoPackedCellEntry(cacheRoot, key string, extensions []GoExtension) (string, bool, error) {
	normalized, err := normalizeGoExtensions(extensions, true)
	if err != nil {
		return "", false, err
	}
	lockCandidates := []string{os.Getenv("PIG_SDK_GO_ROOT")}
	for _, extension := range normalized {
		lockCandidates = append(lockCandidates, GoSDKPathOverride(extension.Root))
	}
	lockCandidates = append(lockCandidates, stagedGoSDKRoots()...)
	return pigsdklock.WithBuildEntryCandidates(context.Background(), lockCandidates, func() (string, bool, error) {
		sdkRoot, err := findSDKRoot(normalized)
		if err != nil {
			return "", false, err
		}
		return pigsdklock.WithBuildEntryForRoot(context.Background(), sdkRoot, func() (string, bool, error) {
			hash := goPackedCellHash(cacheRoot, key, normalized, sdkRoot)
			if after, ok := strings.CutPrefix(hash, "error:"); ok {
				return "", false, fmt.Errorf("hash Go packed cell: %s", after)
			}
			entry := filepath.Join(cacheRoot, "cells", "go", hash)
			_, valid := validCellEntry(entry, EntryIdentity{InputDigest: hash, Artifact: packedRunnerName(runtime.GOOS, "go"), Language: "go"})
			return entry, valid, nil
		})
	})
}
