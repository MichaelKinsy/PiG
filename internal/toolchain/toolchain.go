// Package toolchain locates the build tools PiG uses for compiled extension
// cells and Piglet Binaries, and installs a PiG-managed Go toolchain for users
// who installed only the pig binary.
package toolchain

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// GoDownloadBase is the official Go distribution site. Its JSON index
// publishes the SHA-256 of every archive.
const GoDownloadBase = "https://go.dev/dl/"

// maxArchiveBytes bounds a Go distribution download. Current archives are
// about 70 MB.
const maxArchiveBytes = 512 << 20

// ConfigRoot is the PiG configuration root: $PIG_HOME, else
// $XDG_CONFIG_HOME/pig, else ~/.pig, with a leading ~ expanded. It mirrors
// codingagent.ConfigRoot, which imports this package.
func ConfigRoot() (string, error) {
	if root := os.Getenv("PIG_HOME"); root != "" {
		return expandTilde(root)
	}
	if root := os.Getenv("XDG_CONFIG_HOME"); root != "" {
		root, err := expandTilde(root)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "pig"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".pig"), nil
}

// expandTilde mirrors codingagent.ExpandTildePath.
func expandTilde(path string) (string, error) {
	rest, ok := strings.CutPrefix(path, "~/")
	if path != "~" && !ok {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, rest), nil
}

// ManagedGoRoot is the GOROOT of the PiG-managed Go toolchain.
func ManagedGoRoot(configRoot string) string {
	return filepath.Join(configRoot, "toolchains", "go")
}

func exeName(name string) string {
	return exeNameFor(runtime.GOOS, name)
}

// exeNameFor is the file name of executable name on goos.
func exeNameFor(goos, name string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

// Go returns the go command to run: the one on PATH, else the PiG-managed
// toolchain. The error names both places it looked and the setup command.
func Go() (string, error) {
	if path, err := exec.LookPath("go"); err == nil {
		return path, nil
	}
	root, err := ConfigRoot()
	if err == nil {
		managed := filepath.Join(ManagedGoRoot(root), "bin", exeName("go"))
		if info, statErr := os.Stat(managed); statErr == nil && !info.IsDir() {
			return managed, nil
		}
	}
	return "", fmt.Errorf("go is not on PATH and no PiG-managed Go toolchain is installed; run `pig setup go`: %w", exec.ErrNotFound)
}

// ContainerRuntimes lists the container engines PiG can drive, in preference
// order.
var ContainerRuntimes = []string{"docker", "podman", "nerdctl"}

// ContainerRuntime returns the first container engine found on PATH.
func ContainerRuntime() (name, path string, ok bool) {
	for _, candidate := range ContainerRuntimes {
		if found, err := exec.LookPath(candidate); err == nil {
			return candidate, found, true
		}
	}
	return "", "", false
}

type goRelease struct {
	Version string   `json:"version"`
	Files   []goFile `json:"files"`
}

type goFile struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Kind     string `json:"kind"`
}

// InstallGo downloads the Go archive for version, goos, and goarch from base,
// verifies its size and the SHA-256 published in the index, and replaces the
// managed toolchain under configRoot. It returns the installed go command.
func InstallGo(ctx context.Context, client *http.Client, base, version, goos, goarch, configRoot string) (string, error) {
	if !strings.HasPrefix(version, "go") || strings.ContainsAny(version, `/\ `) {
		return "", fmt.Errorf("invalid Go version %q", version)
	}
	file, err := findGoArchive(ctx, client, base, version, goos, goarch)
	if err != nil {
		return "", err
	}
	toolchains := filepath.Join(configRoot, "toolchains")
	if err := os.MkdirAll(toolchains, 0o755); err != nil {
		return "", fmt.Errorf("create toolchain directory: %w", err)
	}
	archive, err := os.CreateTemp(toolchains, ".go-download-*")
	if err != nil {
		return "", fmt.Errorf("create download file: %w", err)
	}
	// Cleanup is best effort: the archive is a verified temporary copy.
	defer func() { _ = os.Remove(archive.Name()) }()
	defer func() { _ = archive.Close() }()
	if err := download(ctx, client, base+file.Filename, file, archive); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(toolchains, ".go-staging-*")
	if err != nil {
		return "", fmt.Errorf("create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if strings.HasSuffix(file.Filename, ".zip") {
		err = extractZip(archive, file.Size, staging)
	} else {
		err = extractTarGz(archive, staging)
	}
	if err != nil {
		return "", fmt.Errorf("extract %s: %w", file.Filename, err)
	}
	extracted := filepath.Join(staging, "go")
	goBinary := exeNameFor(goos, "go")
	if _, err := os.Stat(filepath.Join(extracted, "bin", goBinary)); err != nil {
		return "", fmt.Errorf("archive %s has no go/bin/%s", file.Filename, goBinary)
	}
	target := ManagedGoRoot(configRoot)
	if err := os.RemoveAll(target); err != nil {
		return "", fmt.Errorf("remove previous managed Go: %w", err)
	}
	if err := os.Rename(extracted, target); err != nil {
		return "", fmt.Errorf("install managed Go: %w", err)
	}
	return filepath.Join(target, "bin", goBinary), nil
}

func findGoArchive(ctx context.Context, client *http.Client, base, version, goos, goarch string) (goFile, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?mode=json&include=all", nil)
	if err != nil {
		return goFile{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return goFile{}, fmt.Errorf("fetch Go release index: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return goFile{}, fmt.Errorf("fetch Go release index: %s", response.Status)
	}
	var releases []goRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&releases); err != nil {
		return goFile{}, fmt.Errorf("decode Go release index: %w", err)
	}
	for _, release := range releases {
		if release.Version != version {
			continue
		}
		for _, file := range release.Files {
			if file.Kind == "archive" && file.OS == goos && file.Arch == goarch {
				if len(file.SHA256) != 64 || file.Size <= 0 || file.Size > maxArchiveBytes || file.Filename != filepath.Base(file.Filename) {
					return goFile{}, fmt.Errorf("the Go release index entry for %s is malformed", file.Filename)
				}
				return file, nil
			}
		}
		return goFile{}, fmt.Errorf("Go %s publishes no archive for %s/%s", version, goos, goarch)
	}
	return goFile{}, fmt.Errorf("Go release %s is not in the official index", version)
}

func download(ctx context.Context, client *http.Client, url string, file goFile, out *os.File) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download %s: %w", file.Filename, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", file.Filename, response.Status)
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(response.Body, file.Size+1))
	if err != nil {
		return fmt.Errorf("download %s: %w", file.Filename, err)
	}
	if written != file.Size {
		return fmt.Errorf("download %s: got %d bytes, the index lists %d", file.Filename, written, file.Size)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != strings.ToLower(file.SHA256) {
		return fmt.Errorf("download %s: SHA-256 %s does not match the published %s", file.Filename, got, file.SHA256)
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return nil
}

// safeJoin resolves an archive entry name inside root. It rejects absolute
// names and any name containing "..", which Go toolchain archives never use.
func safeJoin(root, name string) (string, error) {
	if strings.Contains(name, "..") || !filepath.IsLocal(filepath.FromSlash(name)) {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	return filepath.Join(root, filepath.Clean(filepath.FromSlash(name))), nil
}

func extractTarGz(archive io.Reader, root string) error {
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(root, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(target, reader, header.FileInfo().Mode().Perm()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive entry %q has unsupported type %q", header.Name, header.Typeflag)
		}
	}
}

func extractZip(archive *os.File, size int64, root string) error {
	reader, err := zip.NewReader(archive, size)
	if err != nil {
		return err
	}
	for _, entry := range reader.File {
		target, err := safeJoin(root, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return fmt.Errorf("archive entry %q is not a regular file", entry.Name)
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		err = writeFile(target, source, entry.Mode().Perm())
		_ = source.Close() // A read-side close cannot lose extracted data.
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(target string, source io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, source); err != nil {
		_ = file.Close() // The copy error is the one to report.
		return err
	}
	return file.Close()
}
