package runtimecell

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/buildprogress"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// lispImageFormat names the shape of the generated image. Bump it when build.lisp or the runner contract changes in a way the SDK tree hash
// does not already cover.
const lispImageFormat = "pig-lisp-image-1"

// LispExtension describes one factory-style Lisp extension that can be built into a generated runner image. The factory is the one
// function NEW-EXTENSION its ASDF system, or its single source file, defines; Package names the system or the file (without .lisp).
type LispExtension struct {
	Name    string
	Root    string
	Package string
	Factory string
	Hash    string
	// Verify, when set, reports an error once the source no longer hashes to Hash: a build runs it before it publishes.
	Verify func() error
}

// LispPackedCell describes a generated Lisp runner image.
type LispPackedCell struct {
	Key           string
	Hash          string
	CacheDir      string
	BinaryPath    string
	Cached        bool
	BuildDuration time.Duration
	Extensions    []LispExtension
}

type lispMember struct {
	Name   string `json:"name"`
	Root   string `json:"root"`
	System string `json:"system,omitempty"`
	File   string `json:"file,omitempty"`
}

func sbclExecutable() string {
	return toolchain.SBCLExecutable(runtime.GOOS, exec.LookPath)
}

func normalizeLispExtensions(extensions []LispExtension) ([]LispExtension, error) {
	out := append([]LispExtension(nil), extensions...)
	for i := range out {
		ext := &out[i]
		ext.Name = strings.TrimSpace(ext.Name)
		ext.Root = strings.TrimSpace(ext.Root)
		ext.Package = strings.TrimSpace(ext.Package)
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
			return nil, fmt.Errorf("extension %q: system or file name is required", ext.Name)
		}
		if ext.Factory != "new-extension" {
			return nil, fmt.Errorf("extension %q: factory must be (defun new-extension () ...)", ext.Name)
		}
		if ext.Hash == "" {
			ext.Hash = "unknown"
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// lispMembers turns the extensions into the build script's member list: an ASDF system when <package>.asd exists at the root, else <package>.lisp.
func lispMembers(extensions []LispExtension) ([]lispMember, error) {
	members := make([]lispMember, len(extensions))
	for i, ext := range extensions {
		members[i] = lispMember{Name: ext.Name, Root: filepath.ToSlash(ext.Root)}
		//perf:ok once per member of an image build, which starts sbcl
		if _, err := os.Stat(filepath.Join(ext.Root, ext.Package+".asd")); err == nil {
			members[i].System = ext.Package
			//perf:ok once per member of an image build, which starts sbcl
		} else if _, err := os.Stat(filepath.Join(ext.Root, ext.Package+".lisp")); err == nil {
			members[i].File = ext.Package + ".lisp"
		} else {
			return nil, fmt.Errorf("extension %q: neither %s.asd nor %s.lisp exists in %s", ext.Name, ext.Package, ext.Package, ext.Root)
		}
	}
	return members, nil
}

func lispCellHash(cacheRoot, key string, extensions []LispExtension, sdkRoot string) string {
	sdkHash := hashTree(sdkRoot)
	if strings.HasPrefix(sdkHash, "error:") {
		return sdkHash
	}
	h := sha256.New()
	_, _ = h.Write([]byte(lispImageFormat + "\x00"))
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte("\x00"))
	hashBuildInput(h, "sdk", sdkHash)
	hashBuildInput(h, "runtime", commandVersion(cacheRoot, sbclExecutable(), "--version"))
	hashBuildInput(h, "quicklisp", quicklispDist+" "+quicklispReleasesSHA256+" "+quicklispSystemsSHA256)
	hashBuildInput(h, "goos", runtime.GOOS+"/"+runtime.GOARCH)
	for _, ext := range extensions {
		for _, part := range []string{ext.Name, ext.Package, ext.Factory, ext.Hash} {
			_, _ = h.Write([]byte(part))
			_, _ = h.Write([]byte("\x00"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func lispExtensionVerifiers(extensions []LispExtension) []func() error {
	var checks []func() error
	for _, extension := range extensions {
		if extension.Verify != nil {
			checks = append(checks, extension.Verify)
		}
	}
	return checks
}

// BuildLispPackedCell builds, or reuses from the cache, the runner image that serves extensions: one SBCL image holding the SDK and every
// member's compiled factory. The host starts it like any packed runner, with one socket per member.
func BuildLispPackedCell(ctx context.Context, cacheRoot, key string, extensions []LispExtension) (*LispPackedCell, error) {
	if len(extensions) == 0 {
		return nil, fmt.Errorf("Lisp cell requires at least one extension")
	}
	normalized, err := normalizeLispExtensions(extensions)
	if err != nil {
		return nil, err
	}
	cell, err := buildLispImage(ctx, cacheRoot, key, normalized)
	if err != nil {
		return nil, err
	}
	return cell, nil
}

// BuildLispBaseImage builds, or reuses, the image that runs an exact standalone Lisp script: the SDK with no member, loading the file
// named on its command line.
func BuildLispBaseImage(ctx context.Context, cacheRoot string) (string, error) {
	cell, err := buildLispImage(ctx, cacheRoot, "standalone-base", nil)
	if err != nil {
		return "", err
	}
	return cell.BinaryPath, nil
}

func buildLispImage(ctx context.Context, cacheRoot, key string, normalized []LispExtension) (*LispPackedCell, error) {
	//perf:ok once per image build, which starts sbcl
	lockCandidates := append([]string{os.Getenv("PIG_SDK_LISP_ROOT")}, stagedSDKRoots("lisp")...)
	return pigsdklock.WithBuildCandidates(ctx, lockCandidates, func() (*LispPackedCell, error) {
		sdkRoot, err := findLispSDKRoot()
		if err != nil {
			return nil, err
		}
		return pigsdklock.WithBuildForRoot(ctx, sdkRoot, func() (*LispPackedCell, error) {
			members, err := lispMembers(normalized)
			if err != nil {
				return nil, err
			}
			hash := lispCellHash(cacheRoot, key, normalized, sdkRoot)
			if after, ok := strings.CutPrefix(hash, "error:"); ok {
				return nil, fmt.Errorf("hash Lisp cell: %s", after)
			}
			cellDir := filepath.Join(cacheRoot, "cells", "lisp", hash)
			//portlint:allow clock measures the build's elapsed time for the log (a duration, not a date)
			start := time.Now()
			artifactName := packedRunnerName(runtime.GOOS, "lisp")
			entry, err := publishArtifactWithFailureCache(ctx, cellDir, artifactName, hash, "lisp", func(scratch string) (string, error) {
				out := filepath.Join(scratch, artifactName)
				sbcl := sbclExecutable()
				memberJSON, err := lispJSON(members)
				if err != nil {
					return "", err
				}
				names := make([]string, len(normalized))
				for i, ext := range normalized {
					names[i] = ext.Name
				}
				extraRoots, err := resolveLispDependencies(ctx, cacheRoot, sbcl, sdkRoot, scratch, memberJSON, members)
				if err != nil {
					return "", err
				}
				extraJSON, err := lispJSON(append([]string{}, extraRoots...))
				if err != nil {
					return "", err
				}
				buildprogress.Phase(ctx, "Building Lisp image", strings.Join(names, ", ")+" (compile, save image)")
				cmd := linkerexec.CommandContext(ctx, sbcl, "--noinform", "--disable-ldb", "--lose-on-corruption", "--end-runtime-options",
					"--non-interactive", "--no-sysinit", "--no-userinit", "--disable-debugger",
					"--load", filepath.Join(sdkRoot, "src", "build.lisp"))
				cmd.Dir = scratch
				cmd.Env = append(cacheBuildEnvironment(scratch),
					"PIG_LISP_SDK_ROOT="+filepath.ToSlash(sdkRoot),
					"PIG_LISP_SCRATCH="+filepath.ToSlash(scratch),
					"PIG_LISP_OUTPUT="+filepath.ToSlash(out),
					"PIG_LISP_EXTRA_ROOTS="+string(extraJSON),
					"PIG_LISP_MEMBERS="+string(memberJSON))
				//portlint:allow clock measures sbcl's elapsed time for the failure record (a duration, not a date); the package's Now seam dates cache entries for collection
				started := time.Now()
				if output, err := buildprogress.CombinedOutput(ctx, cmd); err != nil {
					invalidateCommandVersion(cacheRoot, sbcl, "--version")
					if explained, ok := explainMissingToolchain("lisp", err); ok {
						return "", explained
					}
					failure, recordable := LispBuildFailure(cacheRoot, hash, scratch, err, output, BuildRun{Elapsed: time.Since(started), ContextErr: ctx.Err()})
					if ctx.Err() != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() < 0 || !recordable {
						return "", failure
					}
					return "", cacheBuildFailure(failure)
				}
				if err := cellInputsUnchanged(key, hash, lispCellHash(cacheRoot, key, normalized, sdkRoot), lispExtensionVerifiers(normalized)); err != nil {
					return "", err
				}
				return out, nil
			})
			if err != nil {
				return nil, err
			}
			var duration time.Duration
			if !entry.Reused {
				duration = time.Since(start)
			}
			return &LispPackedCell{Key: key, Hash: hash, CacheDir: entry.Dir, BinaryPath: entry.ArtifactPath, Cached: entry.Reused, BuildDuration: duration, Extensions: normalized}, nil
		})
	})
}

// CurrentLispPackedCellEntry returns the valid cache entry selected by the same normalization and hash inputs as BuildLispPackedCell, without building it.
func CurrentLispPackedCellEntry(cacheRoot, key string, extensions []LispExtension) (string, bool, error) {
	normalized, err := normalizeLispExtensions(extensions)
	if err != nil {
		return "", false, err
	}
	//perf:ok once per cache-entry lookup, not per request
	lockCandidates := append([]string{os.Getenv("PIG_SDK_LISP_ROOT")}, stagedSDKRoots("lisp")...)
	//portlint:allow ctxbackground a lookup of a published entry takes no context and starts no work to cancel
	return pigsdklock.WithBuildEntryCandidates(context.Background(), lockCandidates, func() (string, bool, error) {
		sdkRoot, err := findLispSDKRoot()
		if err != nil {
			return "", false, err
		}
		//portlint:allow ctxbackground a lookup of a published entry takes no context and starts no work to cancel
		return pigsdklock.WithBuildEntryForRoot(context.Background(), sdkRoot, func() (string, bool, error) {
			hash := lispCellHash(cacheRoot, key, normalized, sdkRoot)
			if after, ok := strings.CutPrefix(hash, "error:"); ok {
				return "", false, fmt.Errorf("hash Lisp cell: %s", after)
			}
			entry, ok := ReusePublishedArtifact(filepath.Join(cacheRoot, "cells", "lisp", hash), EntryIdentity{InputDigest: hash, Artifact: packedRunnerName(runtime.GOOS, "lisp"), Language: "lisp"})
			if !ok {
				return filepath.Join(cacheRoot, "cells", "lisp", hash), false, nil
			}
			return entry.Dir, true, nil
		})
	})
}

func findLispSDKRoot() (string, error) {
	//perf:ok once per SDK root lookup on a build
	if root := strings.TrimSpace(os.Getenv("PIG_SDK_LISP_ROOT")); root != "" {
		if abs, err := filepath.Abs(root); err == nil && statHasFile(abs, "pig-sdk.asd") {
			return abs, nil
		}
	}
	// pig additive (D92): only PIG_SDK_LISP_ROOT serves a Lisp SDK the active Piglet strips; this binary staged none.
	if err := StrippedSDKError("lisp"); err != nil {
		return "", err
	}
	for _, root := range stagedSDKRoots("lisp") {
		if statHasFile(root, "pig-sdk.asd") {
			return root, nil
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod")) //nolint:gosec // G304: the path names a file that the user, the project config, or PiG's own state directory designates; the CLI runs as the user with no sandbox, as Pi does, so reading it grants nothing the user lacks
		if err == nil && strings.Contains(string(data), "module github.com/MichaelKinsy/PiG") {
			if cand := filepath.Join(dir, "extensions", "sdk-lisp"); statHasFile(cand, "pig-sdk.asd") {
				return cand, nil
			}
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	for _, root := range installedPigSourceRoots() {
		if cand := filepath.Join(root, "extensions", "sdk-lisp"); statHasFile(cand, "pig-sdk.asd") {
			return cand, nil
		}
	}
	return "", fmt.Errorf("cannot locate github.com/MichaelKinsy/PiG checkout for the Lisp SDK; set PIG_SDK_LISP_ROOT")
}

// LispBuildFailure reports a failed sbcl image build: one summary line, and the complete output in a log under cacheRoot. recordable reports
// whether the same inputs fail again: a compile or load error the build script reported (a failure of the extension's source), not a toolchain or I/O failure.
func LispBuildFailure(cacheRoot, logKey, dir string, buildErr error, output []byte, run BuildRun) (failure *BuildFailure, recordable bool) {
	summary, cause := "sbcl image build failed", "sbcl image build failed"
	if buildErr != nil {
		cause = "sbcl image build failed: " + oneLine(buildErr.Error())
		summary = cause
	}
	//portlint:allow pathseparators every line is trimmed of whitespace, including a trailing carriage return, before it is read
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if message, ok := strings.CutPrefix(strings.TrimSpace(line), "pig-lisp-build: "); ok {
			summary = oneLine("sbcl image build: " + message)
			cause = summary
			recordable = true
			break
		}
	}
	if !recordable {
		for _, line := range slices.Backward(lines) {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				summary = oneLine(cause + ": " + trimmed)
				break
			}
		}
	}
	if run.Elapsed > 0 {
		summary += " after " + run.Elapsed.Round(time.Millisecond).String()
	}
	//portlint:allow clock stamps the build failure log, a diagnostic file, not state read back
	log := fmt.Sprintf("time: %s\ndirectory: %s\nerror: %v\nelapsed: %s\ncontext: %v\n\n--- sbcl output ---\n%s", time.Now().UTC().Format(time.RFC3339), dir, buildErr, run.Elapsed, run.ContextErr, output)
	return &BuildFailure{Summary: summary, Cause: cause, Log: writeBuildLog(cacheRoot, logKey, []byte(log))}, recordable
}

// resolveLispDependencies finds the systems the members need that ASDF cannot find in the SDK and the members' own roots, and resolves them
// from the pinned Quicklisp dist (ResolveQuicklisp). A member that is a single file has none.
func resolveLispDependencies(ctx context.Context, cacheRoot, sbcl, sdkRoot, scratch string, memberJSON []byte, members []lispMember) ([]string, error) {
	hasSystem := false
	for _, member := range members {
		hasSystem = hasSystem || member.System != ""
	}
	if !hasSystem {
		return nil, nil
	}
	scanFile := filepath.Join(scratch, "unresolved.json")
	buildprogress.Phase(ctx, "Scanning Lisp dependencies", "")
	cmd := linkerexec.CommandContext(ctx, sbcl, "--noinform", "--disable-ldb", "--lose-on-corruption", "--end-runtime-options",
		"--non-interactive", "--no-sysinit", "--no-userinit", "--disable-debugger",
		"--load", filepath.Join(sdkRoot, "src", "build.lisp"))
	cmd.Dir = scratch
	cmd.Env = append(cacheBuildEnvironment(scratch),
		"PIG_LISP_SDK_ROOT="+filepath.ToSlash(sdkRoot),
		"PIG_LISP_SCRATCH="+filepath.ToSlash(scratch),
		"PIG_LISP_OUTPUT="+filepath.ToSlash(scanFile),
		"PIG_LISP_MODE=scan",
		"PIG_LISP_MEMBERS="+string(memberJSON))
	if output, err := buildprogress.CombinedOutput(ctx, cmd); err != nil {
		return nil, fmt.Errorf("scan Lisp dependencies: %w: %s", err, oneLine(string(output)))
	}
	data, err := os.ReadFile(scanFile) //nolint:gosec // G304: the path names a file that the user, the project config, or PiG's own state directory designates; the CLI runs as the user with no sandbox, as Pi does, so reading it grants nothing the user lacks
	if err != nil {
		return nil, fmt.Errorf("scan Lisp dependencies: %w", err)
	}
	var unresolved []string
	if err := json.Unmarshal(data, &unresolved); err != nil {
		return nil, fmt.Errorf("scan Lisp dependencies: %w", err)
	}
	if len(unresolved) == 0 {
		return nil, nil
	}
	buildprogress.Phase(ctx, "Fetching Lisp dependencies", "Quicklisp "+quicklispDist+": "+strings.Join(unresolved, ", "))
	roots, err := ResolveQuicklisp(ctx, cacheRoot, unresolved)
	if err != nil {
		return nil, fmt.Errorf("resolve Lisp dependencies %s: %w", strings.Join(unresolved, ", "), err)
	}
	for i := range roots {
		roots[i] = filepath.ToSlash(roots[i])
	}
	return roots, nil
}

// lispJSON encodes v for the SDK's own JSON reader, which reads \u003c as readily as <, so no escaping of HTML characters is needed.
func lispJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
