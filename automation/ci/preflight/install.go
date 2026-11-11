// SPDX-License-Identifier: MIT

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"time"
)

// downloadTimeout bounds one download of a tool.
const downloadTimeout = 5 * time.Minute

// install puts every tool of the config into bin: a release archive is downloaded (or taken from the digest-keyed
// cache), accepted only at its pinned SHA-256, and the tool's binary extracted; a pip tool is installed into its own
// virtual environment from the hash-locked requirements file and linked into bin.
func install(ctx context.Context, cfg config, root, bin, cache string, log io.Writer) error {
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	for _, t := range cfg.Tools {
		start := time.Now()
		var err error
		if t.Pip != "" {
			err = installPip(ctx, t, root, bin)
		} else {
			err = installArchive(ctx, t, bin, cache)
		}
		if err != nil {
			return fmt.Errorf("install %s %s: %w", t.Name, t.Version, err)
		}
		logf(log, "installed %s %s (%s)", t.Name, t.Version, time.Since(start).Round(time.Millisecond))
	}
	return nil
}

func installArchive(ctx context.Context, t tool, bin, cache string) error {
	archive, err := download(ctx, t.URL, t.SHA256, cache)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the archive holds no file %s", t.Name)
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || path.Base(h.Name) != t.Name {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, 512<<20))
		if err != nil {
			return err
		}
		target := filepath.Join(bin, t.Name)
		if err := os.WriteFile(target+".tmp", body, 0o755); err != nil {
			return err
		}
		return os.Rename(target+".tmp", target)
	}
}

// download returns the body at url when its SHA-256 is want, from cache when an earlier run stored it there.
func download(ctx context.Context, url, want, cache string) ([]byte, error) {
	cached := filepath.Join(cache, want)
	if raw, err := os.ReadFile(cached); err == nil && sha256Hex(raw) == want {
		return raw, nil
	}
	client := &http.Client{Timeout: downloadTimeout}
	r, err := retrying(ctx, defaultRetryDelays, nil, "GET "+url, func() (response, error) {
		return exchange(ctx, client, http.MethodGet, url, "", nil, 512<<20)
	})
	if err != nil {
		return nil, err
	}
	if got := sha256Hex(r.body); got != want {
		return nil, fmt.Errorf("%s has SHA-256 %s, pinned %s", url, got, want)
	}
	if err := os.MkdirAll(cache, 0o755); err == nil {
		_ = os.WriteFile(cached, r.body, 0o600)
	}
	return r.body, nil
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func installPip(ctx context.Context, t tool, root, bin string) error {
	venv := filepath.Join(bin, ".venv-"+t.Name)
	steps := [][]string{
		{"python3", "-m", "venv", venv},
		{filepath.Join(venv, "bin", "pip"), "install", "--quiet", "--disable-pip-version-check", "--require-hashes", "-r", filepath.Join(root, filepath.FromSlash(t.Pip))},
	}
	for _, args := range steps {
		//nolint:gosec // G204: python3 and the venv's pip with the hash-locked requirements file of main.
		if out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w\n%s", args[0], err, out)
		}
	}
	link := filepath.Join(bin, t.Name)
	_ = os.Remove(link)
	return os.Symlink(filepath.Join(venv, "bin", t.Name), link)
}
