package subprocess

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/host/tsruntime"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

const nodeRuntimeVersion = "v2"

// minimumNodeRuntimeVersion is the oldest Node release PiG supports for the extension runtime. Pi 0.87.1 itself declares Node 22.19 or newer.
const minimumNodeRuntimeVersion = "v22.13.0"

// nodeLauncherFormat identifies the generated launcher shape, including the
// entry file recorded beside the runtime. It is part of the cache key so a
// format change never reuses a launcher built by a prior pig for the same
// source.
const nodeLauncherFormat = "direct-node-v3"

// nodeEntryFile, inside the launcher's runtime directory, records the absolute
// extension entry the launcher runs.
const nodeEntryFile = "entry"

var nodeRuntimeDigest = func() []byte { return []byte(nodeRuntimeHash) }

// pig additive (D92): the embedded Node runtime (nodeRuntimeFS, ensureNodeRuntime) lives in node_runtime_on.go, which a Piglet Binary compiles out with pig_strip_node_extensions.
func errNodeExtensionsStripped() error {
	return pigstrip.Error("The TypeScript and JavaScript extension runtime", pigstrip.ListFeatures, pigstrip.NodeExtensions)
}

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
	_, ok := extsource.NodeDirectoryImport(dir)
	return ok
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
	declared, missing, hasManifest, err := extsource.NodeManifestEntries(src)
	if err != nil {
		return "", err
	}
	if len(declared) == 1 {
		// Upstream keeps a declared directory, such as "./", as the extension
		// path and jiti imports it; Node refuses a directory import.
		if info, err := os.Stat(declared[0]); err == nil && info.IsDir() {
			file, ok := extsource.NodeDirectoryImport(declared[0])
			if !ok {
				return "", fmt.Errorf("package %s: pi.extensions directory %s cannot be imported: it has no index file or package.json main", src, declared[0])
			}
			return file, nil
		}
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
	if hasManifest {
		// Upstream loads only what pi.extensions names; a declared directory
		// with no entry file contributes nothing, and the root's own index is
		// never consulted in its place.
		return "", fmt.Errorf("package %s declares pi.extensions directories with no extension entry file", src)
	}
	for _, name := range []string{"index.ts", "index.js", "main.ts", "main.js", "extension.ts", "extension.js"} {
		candidate := filepath.Join(src, name)
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	// A directory with no manifest entries and no index.ts or index.js is itself the extension path (package-manager.ts:1379-1384), and the loader's jiti import of it resolves the file it loads, such as index.mjs.
	if file, ok := extsource.NodeDirectoryImport(src); ok {
		return file, nil
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

// nodeLauncherCommand runs the node command of a published launcher directly,
// with the arguments the launcher script would exec. Spawning the launcher
// costs a shell and a dirname process before node starts. It reports false for
// any other artifact. The host preflights Node before calling it. The
// --import value is a module specifier, so the loader is named by its file
// URL; a path that has none fails the command's Start.
func nodeLauncherCommand(ctx context.Context, binPath string, sel tsruntime.Selection) (*exec.Cmd, bool) {
	entry, ok := nodeLauncherEntry(binPath)
	if !ok {
		return nodePackedLauncherCommand(ctx, binPath, sel)
	}
	return typeScriptRuntimeCommand(ctx, sel, binPath+".runtime", "cli.mjs", "node extension loader", []string{entry}), true
}

func buildNode(ctx context.Context, cacheRoot, srcPath, outPath string) error {
	entry, err := resolveNodeEntrypoint(srcPath)
	if err != nil {
		return err
	}
	runtimeDir := outPath + ".runtime"
	_ = os.RemoveAll(runtimeDir)
	if err := os.MkdirAll(filepath.Join(runtimeDir, "shims"), 0o755); err != nil {
		return err
	}
	if err := materializeNodeRuntime(ctx, cacheRoot, runtimeDir); err != nil {
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
