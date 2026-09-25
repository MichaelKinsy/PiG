package runtimecell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/internal/buildprogress"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// RustExtension describes one factory-style Rust SDK extension that can be
// packed into a generated subprocess runner. The crate must expose a factory
// function returning pig_sdk::Extension, typically:
//
//	pub fn new_extension() -> pig_sdk::Extension
//
// The generated runner still runs as a subprocess and uses one socket per
// contained extension.
type RustExtension struct {
	Name    string
	Root    string
	Package string
	Crate   string
	Factory string
	Hash    string
}

// RustPackedCell describes a generated Rust packed-cell artifact.
type RustPackedCell struct {
	Key           string
	Hash          string
	CacheDir      string
	BinaryPath    string
	Cached        bool
	BuildDuration time.Duration
	Extensions    []RustExtension
}

// BuildRustPackedCell generates and builds a Cargo runner that hosts multiple
// factory-style Rust SDK extensions in one subprocess artifact.
func BuildRustPackedCell(ctx context.Context, cacheRoot, key string, extensions []RustExtension) (*RustPackedCell, error) {
	if len(extensions) == 0 {
		return nil, fmt.Errorf("packed Rust cell requires at least one extension")
	}
	prebuiltExtensions, err := normalizeRustExtensions(extensions, false)
	if err != nil {
		return nil, err
	}
	if bin, ok := resolvePrebuilt(PrebuiltRequest{
		Language: "rust", Key: key, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Extensions: rustPrebuiltExtensions(prebuiltExtensions),
	}); ok {
		return &RustPackedCell{Key: key, Hash: "prebuilt:" + bin, CacheDir: filepath.Dir(bin), BinaryPath: bin, Cached: true, Extensions: prebuiltExtensions}, nil
	}
	normalized, err := normalizeRustExtensions(extensions, true)
	if err != nil {
		return nil, err
	}
	if cell, ok := reuseRustPackedCell(cacheRoot, key, normalized); ok {
		return cell, nil
	}
	lockCandidates := []string{os.Getenv("PIG_SDK_RS_ROOT")}
	for _, extension := range normalized {
		lockCandidates = append(lockCandidates, RustSDKPathOverride(extension.Root))
	}
	lockCandidates = append(lockCandidates, stagedSDKRoots("sdk-rs")...)
	return pigsdklock.WithBuildCandidates(ctx, lockCandidates, func() (*RustPackedCell, error) {
		sdkRoot, err := findRustSDKRoot(normalized)
		if err != nil {
			return nil, err
		}
		return pigsdklock.WithBuildForRoot(ctx, sdkRoot, func() (*RustPackedCell, error) {
			hash := rustPackedCellHash(cacheRoot, key, normalized, sdkRoot)
			if after, ok := strings.CutPrefix(hash, "error:"); ok {
				return nil, fmt.Errorf("hash Rust packed cell: %s", after)
			}
			cellDir := filepath.Join(cacheRoot, "cells", "rust", hash)
			start := time.Now()
			packageName := "pig-generated-packed-cell-" + hash[:16]
			artifactName := packedRunnerName(runtime.GOOS, "rust")
			entry, err := PublishArtifact(ctx, cellDir, artifactName, hash, "rust", func(scratch string) (string, error) {
				if err := os.MkdirAll(filepath.Join(scratch, "src"), 0o755); err != nil {
					return "", fmt.Errorf("create rust cell cache: %w", err)
				}
				if err := os.WriteFile(filepath.Join(scratch, "Cargo.toml"), []byte(renderRustCargoToml(normalized, sdkRoot, packageName)), 0o644); err != nil {
					return "", fmt.Errorf("write generated Cargo.toml: %w", err)
				}
				if err := copyRustSDKLockfile(sdkRoot, scratch); err != nil {
					return "", err
				}
				if err := os.WriteFile(filepath.Join(scratch, "src", "main.rs"), []byte(renderRustRunner(normalized)), 0o644); err != nil {
					return "", fmt.Errorf("write generated Rust runner: %w", err)
				}
				// pig additive (D18): compiler diagnostics are selected only by explicit build callers.
				names := make([]string, len(normalized))
				for i, ext := range normalized {
					names[i] = ext.Name
				}
				buildprogress.Phase(ctx, "Compiling Rust members", strings.Join(names, ", ")+" (dependency resolution, compile, link)")
				cmd := exec.CommandContext(ctx, "cargo", buildprogress.ToolArgs(ctx, "rust", []string{"build", "--release", "--quiet"})...)
				cmd.Dir = scratch
				cmd.Env = cacheBuildEnvironment(scratch)
				if os.Getenv("CARGO_BUILD_JOBS") == "" {
					cmd.Env = append(cmd.Env, "CARGO_BUILD_JOBS=1")
				}
				if out, err := buildprogress.CombinedOutput(ctx, cmd); err != nil {
					invalidateCommandVersion(cacheRoot, "rustc", "--version")
					invalidateCommandVersion(cacheRoot, "cargo", "--version")
					if explained, ok := explainMissingToolchain("rust", err); ok {
						return "", explained
					}
					return "", fmt.Errorf("build generated Rust packed runner: %w\n%s", err, out)
				}
				return filepath.Join(cargoTargetDirectory(scratch), "release", packageName+strings.TrimPrefix(artifactName, "runner")), nil
			})
			if err != nil {
				return nil, err
			}
			var dur time.Duration
			if !entry.Reused {
				dur = time.Since(start)
			}
			return &RustPackedCell{Key: key, Hash: hash, CacheDir: entry.Dir, BinaryPath: entry.ArtifactPath, Cached: entry.Reused, BuildDuration: dur, Extensions: normalized}, nil
		})
	})
}

func cargoTargetDirectory(workingDirectory string) string {
	target := strings.TrimSpace(os.Getenv("CARGO_TARGET_DIR"))
	if target == "" {
		return filepath.Join(workingDirectory, "target")
	}
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(workingDirectory, target)
}

func normalizeRustExtensions(extensions []RustExtension, requireFactory bool) ([]RustExtension, error) {
	out := append([]RustExtension(nil), extensions...)
	for i := range out {
		ext := &out[i]
		ext.Name = strings.TrimSpace(ext.Name)
		ext.Root = strings.TrimSpace(ext.Root)
		ext.Package = strings.TrimSpace(ext.Package)
		ext.Crate = strings.TrimSpace(ext.Crate)
		ext.Factory = strings.TrimSpace(ext.Factory)
		ext.Hash = strings.TrimSpace(ext.Hash)
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
		if ext.Package == "" {
			return nil, fmt.Errorf("extension %q: package is required", ext.Name)
		}
		if ext.Crate == "" {
			ext.Crate = sanitizeRustIdent(ext.Package)
		}
		if requireFactory && ext.Factory != "new_extension" {
			return nil, fmt.Errorf("extension %q: factory must be pub fn new_extension() -> Extension", ext.Name)
		}
		if ext.Hash == "" {
			ext.Hash = "unknown"
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// reuseRustPackedCell returns an already-built cell without the staged-SDK
// lease; see reuseGoPackedCell for why a content-hash hit is safe.
func reuseRustPackedCell(cacheRoot, key string, normalized []RustExtension) (*RustPackedCell, bool) {
	sdkRoot, err := findRustSDKRoot(normalized)
	if err != nil {
		return nil, false
	}
	hash := rustPackedCellHash(cacheRoot, key, normalized, sdkRoot)
	if strings.HasPrefix(hash, "error:") {
		return nil, false
	}
	entry, ok := ReusePublishedArtifact(filepath.Join(cacheRoot, "cells", "rust", hash), EntryIdentity{InputDigest: hash, Artifact: packedRunnerName(runtime.GOOS, "rust"), Language: "rust"})
	if !ok {
		return nil, false
	}
	return &RustPackedCell{Key: key, Hash: hash, CacheDir: entry.Dir, BinaryPath: entry.ArtifactPath, Cached: true, Extensions: normalized}, true
}

func rustPackedCellHash(cacheRoot, key string, extensions []RustExtension, sdkRoot string) string {
	sdkHash := hashTree(sdkRoot)
	if strings.HasPrefix(sdkHash, "error:") {
		return sdkHash
	}
	h := sha256.New()
	_, _ = h.Write([]byte("pig-rust-packed-cell\x00"))
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte("\x00"))
	hashBuildInput(h, "sdk", sdkHash)
	hashBuildInput(h, "runtime", commandVersion(cacheRoot, "rustc", "--version"))
	hashBuildInput(h, "cargo", commandVersion(cacheRoot, "cargo", "--version"))
	hashBuildInput(h, "template", renderRustRunner(extensions))
	for _, ext := range extensions {
		for _, part := range []string{ext.Name, ext.Package, ext.Crate, ext.Factory, ext.Hash} {
			_, _ = h.Write([]byte(part))
			_, _ = h.Write([]byte("\x00"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func findRustSDKRoot(extensions []RustExtension) (string, error) {
	for _, ext := range extensions {
		if root := RustSDKPathOverride(ext.Root); root != "" {
			return root, nil
		}
	}
	if root := strings.TrimSpace(os.Getenv("PIG_SDK_RS_ROOT")); root != "" {
		if abs, err := filepath.Abs(root); err == nil && statHasFile(abs, "Cargo.toml") {
			return abs, nil
		}
	}
	for _, root := range stagedSDKRoots("sdk-rs") {
		if _, err := os.Stat(filepath.Join(root, "Cargo.toml")); err == nil {
			return root, nil
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module github.com/MichaelKinsy/PiG") {
			if cand := filepath.Join(dir, "extensions", "sdk-rs"); statHasFile(cand, "Cargo.toml") {
				return cand, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	// Staged copy under the config-root data dir (~/.pig/source) or
	// PIG_SOURCE_ROOT, written by Stock Pig's SDK staging step. This is what
	// lets an installed pig resolve the Rust SDK on a clean host with no checkout
	// and no env config.
	for _, root := range installedPigSourceRoots() {
		cand := filepath.Join(root, "extensions", "sdk-rs")
		if _, err := os.Stat(filepath.Join(cand, "Cargo.toml")); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("cannot locate github.com/MichaelKinsy/PiG checkout for Rust SDK; set PIG_SDK_RS_ROOT")
}

// RustSDKPathOverride returns an extension crate's explicit local SDK path.
func RustSDKPathOverride(root string) string {
	var manifest struct {
		Dependencies map[string]any `toml:"dependencies"`
	}
	if _, err := toml.DecodeFile(filepath.Join(root, "Cargo.toml"), &manifest); err != nil {
		return ""
	}
	dependency, ok := manifest.Dependencies["pig-sdk"].(map[string]any)
	if !ok {
		return ""
	}
	path, _ := dependency["path"].(string)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	if _, err := os.Stat(filepath.Join(path, "Cargo.toml")); err != nil {
		return ""
	}
	return path
}

func copyRustSDKLockfile(sdkRoot, cellDir string) error {
	data, err := os.ReadFile(filepath.Join(sdkRoot, "Cargo.lock"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read Rust SDK Cargo.lock: %w", err)
	}
	if err := os.WriteFile(filepath.Join(cellDir, "Cargo.lock"), data, 0o644); err != nil {
		return fmt.Errorf("write generated Cargo.lock: %w", err)
	}
	return nil
}

func renderRustCargoToml(extensions []RustExtension, sdkRoot, packageName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[package]\nname = %q\nversion = \"0.0.0\"\nedition = \"2024\"\n\n[dependencies]\n", packageName)
	fmt.Fprintf(&b, "pig-sdk = { path = %q }\n", filepath.ToSlash(sdkRoot))
	for _, ext := range extensions {
		fmt.Fprintf(&b, "%s = { package = %q, path = %q }\n", ext.Crate, ext.Package, filepath.ToSlash(ext.Root))
	}
	b.WriteString("\n[patch.crates-io]\n")
	fmt.Fprintf(&b, "pig-sdk = { path = %q }\n", filepath.ToSlash(sdkRoot))
	return b.String()
}

func renderRustRunner(extensions []RustExtension) string {
	var b strings.Builder
	b.WriteString("use std::env;\nuse std::process;\nuse std::thread;\n\n")
	b.WriteString("struct Item { name: &'static str, env: &'static str, run: fn(String) -> std::io::Result<()> }\n\n")
	b.WriteString("fn main() {\n")
	b.WriteString("    let items: Vec<Item> = vec![\n")
	for _, ext := range extensions {
		fmt.Fprintf(&b, "        Item { name: %q, env: %q, run: |sock| %s::%s().run_with_socket(&sock) },\n", ext.Name, SocketEnvName(ext.Name), ext.Crate, ext.Factory)
	}
	b.WriteString("    ];\n")
	b.WriteString("    let mut handles = Vec::new();\n")
	b.WriteString("    let active = env::var(\"PIG_EXT_ACTIVE_MEMBERS\").ok();\n")
	b.WriteString("    for item in items {\n")
	b.WriteString("        if active.as_ref().is_some_and(|value| !value.split(',').any(|name| name == item.name)) { continue; }\n")
	b.WriteString("        let sock = match env::var(item.env).or_else(|_| if handles.is_empty() { env::var(\"PIG_EXT_SOCKET\") } else { Err(env::VarError::NotPresent) }) {\n")
	b.WriteString("            Ok(sock) => sock,\n")
	b.WriteString("            Err(_) => { eprintln!(\"{} not set for {}\", item.env, item.name); process::exit(1); }\n")
	b.WriteString("        };\n")
	// A member that fails while the shared process lives on connects to its
	// socket and closes it, so the host's wait for that member ends with a
	// visible load error instead of waiting for a connection that never comes.
	b.WriteString("        handles.push(thread::spawn(move || {\n")
	b.WriteString("            let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| (item.run)(sock.clone())));\n")
	b.WriteString("            let result = match result { Ok(result) => result, Err(_) => Err(std::io::Error::other(\"extension panicked\")) };\n")
	b.WriteString("            if let Err(err) = &result { eprintln!(\"{}: {}\", item.name, err); let _ = pig_sdk::report_load_failure(&sock); }\n")
	b.WriteString("            (item.name, result)\n")
	b.WriteString("        }));\n")
	b.WriteString("    }\n")
	b.WriteString("    let mut failed = false;\n")
	b.WriteString("    for handle in handles {\n")
	b.WriteString("        match handle.join() {\n")
	b.WriteString("            Ok((name, Ok(()))) => {},\n")
	b.WriteString("            Ok((name, Err(err))) => { eprintln!(\"{}: {}\", name, err); failed = true; },\n")
	b.WriteString("            Err(_) => { eprintln!(\"extension thread panicked\"); failed = true; },\n")
	b.WriteString("        }\n")
	b.WriteString("    }\n")
	b.WriteString("    if failed { process::exit(1); }\n")
	b.WriteString("}\n")
	return b.String()
}

func sanitizeRustIdent(value string) string {
	var identifier strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' {
			identifier.WriteRune(char)
		} else {
			identifier.WriteByte('_')
		}
	}
	result := identifier.String()
	if result == "" || result[0] >= '0' && result[0] <= '9' {
		result = "ext_" + result
	}
	return result
}

// CurrentRustPackedCellEntry returns the valid cache entry selected by the same
// normalization and hash inputs as BuildRustPackedCell, without building it.
func CurrentRustPackedCellEntry(cacheRoot, key string, extensions []RustExtension) (string, bool, error) {
	normalized, err := normalizeRustExtensions(extensions, true)
	if err != nil {
		return "", false, err
	}
	lockCandidates := []string{os.Getenv("PIG_SDK_RS_ROOT")}
	for _, extension := range normalized {
		lockCandidates = append(lockCandidates, RustSDKPathOverride(extension.Root))
	}
	lockCandidates = append(lockCandidates, stagedSDKRoots("sdk-rs")...)
	return pigsdklock.WithBuildEntryCandidates(context.Background(), lockCandidates, func() (string, bool, error) {
		sdkRoot, err := findRustSDKRoot(normalized)
		if err != nil {
			return "", false, err
		}
		return pigsdklock.WithBuildEntryForRoot(context.Background(), sdkRoot, func() (string, bool, error) {
			hash := rustPackedCellHash(cacheRoot, key, normalized, sdkRoot)
			if after, ok := strings.CutPrefix(hash, "error:"); ok {
				return "", false, fmt.Errorf("hash Rust packed cell: %s", after)
			}
			entry := filepath.Join(cacheRoot, "cells", "rust", hash)
			_, valid := validCellEntry(entry, EntryIdentity{InputDigest: hash, Artifact: packedRunnerName(runtime.GOOS, "rust"), Language: "rust"})
			return entry, valid, nil
		})
	})
}
