package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// pinnedArchive is one download an agent bundle carries, pinned by URL, size and sha256.
type pinnedArchive struct {
	url    string
	size   int64
	sha256 string
}

// cpythonRelease is the standalone CPython an agent bundle carries for workloads written in Python (bench/evals'
// pigbench): python-build-standalone's stripped install-only build, which runs from any directory on any glibc Linux
// and needs nothing from the base image. The release is more than seven days old when it is pinned; its sizes and
// sha256 are the release's own (SHA256SUMS).
const cpythonRelease = "3.12.15+20261001"

const cpythonPython = "python/bin/python3.12"

var cpythonArchives = map[string]pinnedArchive{
	"amd64": {
		url:    "https://github.com/astral-sh/python-build-standalone/releases/download/20261001/cpython-3.12.15%2B20261001-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz",
		size:   34289539,
		sha256: "7bb1659e3235077b7f63d5b6eb6ce653c6fcd6c5041e9d5f73b42ce10421464d",
	},
	"arm64": {
		url:    "https://github.com/astral-sh/python-build-standalone/releases/download/20261001/cpython-3.12.15%2B20261001-aarch64-unknown-linux-gnu-install_only_stripped.tar.gz",
		size:   29212084,
		sha256: "0b35f4dc08d58534eb82e024989e2db9873885dccd4f2a316ff55c0cec146123",
	},
}

// fetchPinned writes a pinned archive to dst: from local when it is set (an offline copy), else from its URL. Either
// way the bytes must have exactly the pinned size and sha256; a longer body is refused without reading past one byte
// over the size, and dst is removed on any failure.
func fetchPinned(ctx context.Context, client *http.Client, a pinnedArchive, local, dst string) (err error) {
	var src io.Reader
	if local != "" {
		f, err := os.Open(local)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		src = f
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: %s", a.url, resp.Status)
		}
		src = resp.Body
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(src, a.size+1))
	if err != nil {
		return err
	}
	name := a.url
	if local != "" {
		name = local
	}
	if n != a.size {
		return fmt.Errorf("%s: %d bytes, want the pinned %d", name, n, a.size)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != a.sha256 {
		return fmt.Errorf("%s: sha256 %s, want the pinned %s", name, sum, a.sha256)
	}
	return nil
}

// addCPython unpacks the pinned CPython for arch into dir/python and checks that the interpreter is there.
func addCPython(ctx context.Context, dir, arch, local string, stderr io.Writer) error {
	a, ok := cpythonArchives[arch]
	if !ok {
		return fmt.Errorf("no pinned CPython for linux/%s", arch)
	}
	archive := filepath.Join(dir, "cpython.tar.gz")
	from := a.url
	if local != "" {
		from = local
	}
	_, _ = fmt.Fprintf(stderr, "k8sbench bundle: CPython %s from %s\n", cpythonRelease, from)
	if err := fetchPinned(ctx, http.DefaultClient, a, local, archive); err != nil {
		return err
	}
	if _, err := output(ctx, dir, nil, "tar", "-xzf", archive, "--no-same-owner"); err != nil {
		return err
	}
	if err := os.Remove(archive); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, cpythonPython)); err != nil {
		return fmt.Errorf("the CPython archive has no %s: %w", strings.TrimPrefix(cpythonPython, "python/"), err)
	}
	return nil
}

// nodeArchives are the official Node.js builds of nodeVersion a trial bundle carries: a task image need not have
// Node, so the Pi arm runs the bundle's own. Sizes and sha256 are the release's own (SHASUMS256.txt).
var nodeArchives = map[string]pinnedArchive{
	"amd64": {
		url:    "https://nodejs.org/dist/v" + nodeVersion + "/node-v" + nodeVersion + "-linux-x64.tar.gz",
		size:   57409532,
		sha256: "f625d97cd707df4ff96254916fbc5ff014f09c09effe5a1e0ca8f6d41a8789d4",
	},
	"arm64": {
		url:    "https://nodejs.org/dist/v" + nodeVersion + "/node-v" + nodeVersion + "-linux-arm64.tar.gz",
		size:   57128466,
		sha256: "d28c8a5bf0a808f0ed434a1dce8c54ae98f0371c0bd86ac58abc613f73e6643f",
	},
}

const nodeBinary = "node/bin/node"

// addNode unpacks the pinned Node.js for arch into dir/node.
func addNode(ctx context.Context, dir, arch, local string, stderr io.Writer) error {
	a, ok := nodeArchives[arch]
	if !ok {
		return fmt.Errorf("no pinned Node.js for linux/%s", arch)
	}
	archive := filepath.Join(dir, "node.tar.gz")
	from := a.url
	if local != "" {
		from = local
	}
	_, _ = fmt.Fprintf(stderr, "k8sbench bundle: Node.js %s from %s\n", nodeVersion, from)
	if err := fetchPinned(ctx, http.DefaultClient, a, local, archive); err != nil {
		return err
	}
	defer func() { _ = os.Remove(archive) }()
	if err := os.MkdirAll(filepath.Join(dir, "node"), 0o755); err != nil {
		return err
	}
	if _, err := output(ctx, dir, nil, "tar", "-xzf", archive, "--no-same-owner", "--strip-components=1", "-C", "node"); err != nil {
		return err
	}
	got, err := linuxArch(filepath.Join(dir, nodeBinary))
	if err != nil {
		return err
	}
	if got != arch {
		return fmt.Errorf("%s is linux/%s, not linux/%s", nodeBinary, got, arch)
	}
	return nil
}
