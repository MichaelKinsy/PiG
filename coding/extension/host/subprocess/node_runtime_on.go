//go:build !pig_strip_node_extensions

package subprocess

import (
	"archive/zip"
	"context"
	_ "embed"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/mod/semver"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pig additive (D92): the embedded Node runtime and the Node preflight that run TypeScript and JavaScript extensions; a Piglet Binary built with pig_strip_node_extensions links node_runtime_off.go instead.

//go:generate go run ./internal/noderuntimegen
//go:embed runtime-node.zip
var nodeRuntimeArchive string

// The archive is opened only on materialization. Warm cache keys use the generated digest without reading or decompressing the runtime.
var nodeRuntimeZip = sync.OnceValues(func() (*zip.Reader, error) {
	reader, err := zip.NewReader(strings.NewReader(nodeRuntimeArchive), int64(len(nodeRuntimeArchive)))
	if err != nil {
		return nil, err
	}
	reader.RegisterDecompressor(zstd.ZipMethodWinZip, zstd.ZipDecompressor())
	return reader, nil
})

var nodeRuntimeFS nodeArchiveFS

type nodeArchiveFS struct{}

func (nodeArchiveFS) Open(name string) (fs.File, error) {
	archive, err := nodeRuntimeZip()
	if err != nil {
		return nil, err
	}
	return archive.Open(name)
}

func (nodeArchiveFS) ReadFile(name string) ([]byte, error) {
	archive, err := nodeRuntimeZip()
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(archive, name)
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
	nodePath, err := exec.LookPath("node")
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
