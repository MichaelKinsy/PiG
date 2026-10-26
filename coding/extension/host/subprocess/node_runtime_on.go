//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess/internal/noderuntimepack"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// pig additive (D92): the embedded Node runtime and the Node preflight that run TypeScript and JavaScript extensions; a Piglet Binary built with pig_strip_node_extensions links node_runtime_off.go instead.

//go:generate go run ./internal/noderuntimegen
//go:embed runtime-node.pack
var nodeRuntimeArchive string

// extractNodeRuntime writes the runtime tree to dst. The archive is decoded only here, so warm cache keys, which use the generated digest, never read or decompress it.
func extractNodeRuntime(dst string) error {
	return noderuntimepack.Extract(nodeRuntimeArchive, func(name string, data []byte) error {
		target := filepath.Join(dst, filepath.FromSlash(strings.TrimPrefix(name, "runtime-node/")))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// InstallNodeRuntime writes the runtime tree into dir under the content-addressed name an installed binary reads it from
// ([installedNodeRuntime]), with a marker that records the runtime's identity, and returns the tree's directory. dir must not hold the tree
// already.
// pig additive (D201): the packaging step for a read-only TypeScript extension runtime.
func InstallNodeRuntime(dir string) (string, error) {
	target := filepath.Join(dir, nodeRuntimeDirName())
	//perf:ok packaging step, run once when a package is built
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("%s already exists; install into an empty directory", target)
	}
	staging, err := os.MkdirTemp(dir, ".stage-"+filepath.Base(target)+"-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := os.Chmod(staging, 0o755); err != nil {
		return "", err
	}
	if err := extractNodeRuntime(staging); err != nil {
		return "", fmt.Errorf("install TypeScript extension runtime: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, nodeRuntimeMarker), []byte(nodeRuntimeIdentity()+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(staging, target); err != nil {
		return "", err
	}
	return target, nil
}

// nodeRuntimeUnavailable reports why this process cannot run a Node extension, or nil when it can. Stock PiG never strips the runtime.
func nodeRuntimeUnavailable() error {
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.NodeExtensions) {
		return errNodeExtensionsStripped()
	}
	return nil
}

func ensureNodeRuntime(ctx context.Context) (string, error) {
	if err := nodeRuntimeUnavailable(); err != nil {
		return "", err
	}
	requirement := fmt.Sprintf("TypeScript extensions need Node.js %s or newer", minimumNodeRuntimeDisplay())
	nodePath, err := toolchain.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("%s; node was not found on PATH", requirement)
	}
	output, err := linkerexec.CommandContext(ctx, nodePath, "--version").CombinedOutput()
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
