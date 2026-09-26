package subprocess

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
)

const nodeRuntimeVersion = "v2"

// minimumNodeRuntimeVersion is the first Node release that exposes
// node:module.stripTypeScriptTypes, which the embedded loader imports.
const minimumNodeRuntimeVersion = "v22.13.0"

// nodeLauncherFormat identifies the generated launcher shape, including the
// entry file recorded beside the runtime. It is part of the cache key so a
// format change never reuses a launcher built by a prior pig for the same
// source.
const nodeLauncherFormat = "direct-node-v3"

// nodeEntryFile, inside the launcher's runtime directory, records the absolute
// extension entry the launcher runs.
const nodeEntryFile = "entry"

//go:embed runtime-node/*.mjs runtime-node/shims/*.mjs runtime-node/shims/get-east-asian-width runtime-node/shims/pi-dist runtime-node/shims/yaml
var nodeRuntimeFS embed.FS

func isNodeSourcePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".ts", ".js", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}

func hasNodeSource(dir string) bool {
	if fileExists(filepath.Join(dir, "package.json")) {
		return true
	}
	for _, name := range []string{"index.ts", "index.js", "main.ts", "main.js", "extension.ts", "extension.js"} {
		if fileExists(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

func resolveNodeEntrypoint(src string) (string, error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		if isNodeSourcePath(src) {
			return src, nil
		}
		return "", fmt.Errorf("unsupported node extension entrypoint: %s", src)
	}
	// A pi package names its entry files in package.json "pi.extensions", and
	// upstream resolveExtensionEntries (core/extensions/loader.ts) consults
	// that before the conventional file names. Without this pig cannot load a
	// package in its published form, only one that happens to keep an index at
	// the root.
	declared, missing, err := piPackageEntrypoints(src)
	if err != nil {
		return "", err
	}
	if len(declared) == 1 {
		return declared[0], nil
	}
	if len(declared) > 1 {
		return "", fmt.Errorf(
			"package %s declares %d pi.extensions entries; build each entry as its own extension",
			src, len(declared))
	}
	if len(missing) > 0 {
		// Distributed packages commonly point pi.extensions at a build output,
		// so saying "no entrypoint" would send the user looking for the wrong
		// problem.
		return "", fmt.Errorf(
			"package %s declares pi.extensions %v but none exist; build the package first",
			src, missing)
	}
	for _, name := range []string{"index.ts", "index.js", "main.ts", "main.js", "extension.ts", "extension.js"} {
		candidate := filepath.Join(src, name)
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot resolve node extension entrypoint in %s", src)
}

func nodeLauncherEntry(binPath string) (string, bool) {
	entry, err := os.ReadFile(filepath.Join(binPath+".runtime", nodeEntryFile))
	if err != nil || len(entry) == 0 {
		return "", false
	}
	return string(entry), true
}

func usesNodeRuntime(binPath, runtimeLanguage string) bool {
	if runtimeLanguage == "node" {
		return true
	}
	if _, ok := nodeLauncherEntry(binPath); ok {
		return true
	}
	_, ok := nodePackedLauncherManifest(binPath)
	return ok
}

func minimumNodeRuntimeDisplay() string {
	return strings.TrimSuffix(strings.TrimPrefix(minimumNodeRuntimeVersion, "v"), ".0")
}

func ensureNodeRuntime(ctx context.Context) (string, error) {
	requirement := fmt.Sprintf("TypeScript extensions need Node.js %s or newer", minimumNodeRuntimeDisplay())
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("%s; node was not found on PATH", requirement)
	}
	output, err := exec.CommandContext(ctx, nodePath, "--version").CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("%s; node --version failed: %s", requirement, detail)
	}
	found := strings.TrimSpace(string(output))
	normalized := found
	if !strings.HasPrefix(normalized, "v") {
		normalized = "v" + normalized
	}
	if !semver.IsValid(normalized) {
		return "", fmt.Errorf("%s; node --version returned %q", requirement, found)
	}
	if semver.Compare(normalized, minimumNodeRuntimeVersion) < 0 {
		return "", fmt.Errorf("%s; found %s", requirement, found)
	}
	return nodePath, nil
}

// nodeLauncherCommand runs the node command of a published launcher directly,
// with the arguments the launcher script would exec. Spawning the launcher
// costs a shell and a dirname process before node starts. It reports false for
// any other artifact. The host preflights Node before calling it. The
// --import value is a module specifier, so the loader is named by its file
// URL; a path that has none fails the command's Start.
func nodeLauncherCommand(ctx context.Context, binPath string) (*exec.Cmd, bool) {
	entry, ok := nodeLauncherEntry(binPath)
	if !ok {
		return nodePackedLauncherCommand(ctx, binPath)
	}
	runtimeDir := binPath + ".runtime"
	loaderPath := filepath.Join(runtimeDir, "register-loader.mjs")
	loaderURL, urlErr := nodeFileURL(loaderPath)
	cmd := exec.CommandContext(ctx, "node", "--import", loaderURL, filepath.Join(runtimeDir, "cli.mjs"), entry)
	if urlErr != nil && cmd.Err == nil {
		cmd.Err = fmt.Errorf("node extension loader %s: %w", loaderPath, urlErr)
	}
	return cmd, true
}

func buildNode(srcPath, outPath string) error {
	entry, err := resolveNodeEntrypoint(srcPath)
	if err != nil {
		return err
	}
	runtimeDir := outPath + ".runtime"
	_ = os.RemoveAll(runtimeDir)
	if err := os.MkdirAll(filepath.Join(runtimeDir, "shims"), 0o755); err != nil {
		return err
	}
	if err := copyEmbeddedTree(nodeRuntimeFS, "runtime-node", runtimeDir); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, nodeEntryFile), []byte(entry), 0o644); err != nil {
		return err
	}
	// The host reads the sibling runtime tree and starts Node directly. Keep a
	// small platform-neutral primary artifact so cache publication retains the
	// same single-artifact shape without requiring /bin/sh or a shebang runner.
	launcher := []byte("pig-node-launcher:" + nodeLauncherFormat + "\n")
	tmpPath := outPath + ".tmp"
	if err := os.WriteFile(tmpPath, launcher, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, outPath)
}

func copyEmbeddedTree(efs embed.FS, root, dst string) error {
	return fs.WalkDir(efs, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, root)
		rel = strings.TrimPrefix(rel, string(filepath.Separator))
		if rel == "" {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := efs.ReadFile(path)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		if runtime.GOOS != "windows" && strings.HasSuffix(target, ".mjs") {
			mode = 0o644
		}
		return os.WriteFile(target, data, mode)
	})
}

// piPackageEntrypoints returns the existing files named by package.json
// "pi.extensions", resolved against dir. A package without the field, or with a
// malformed one, yields no entries so resolution falls through to the
// conventional names, matching upstream readPiManifest returning null.
func piPackageEntrypoints(dir string) (entries, missing []string, err error) {
	packageJSON := filepath.Join(dir, "package.json")
	if _, err := os.Stat(packageJSON); err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	manifest := packagecontent.ReadPiManifest(packageJSON)
	if manifest == nil {
		return nil, nil, nil
	}
	for _, rel := range manifest.Extensions {
		candidate := filepath.Join(dir, filepath.FromSlash(rel))
		if fileExists(candidate) {
			entries = append(entries, candidate)
			continue
		}
		missing = append(missing, rel)
	}
	return entries, missing, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
