package bunruntime

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/ed25519"
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
	"time"
)

// runtimeID is the slot directory and the name users select with extensionsTypescriptRuntime or PIG_TS_RUNTIME.
const runtimeID = "bun"

// provider names this Piglet in runtime.json.
const provider = "pig-standard/bun-runtime"

// Bounds on what an install reads: the signed checksum file, an archive and the extracted executable. Bun 1.4.2's largest archive
// is 44 MB and its largest executable 89 MB.
const (
	maxChecksumsSize  = 64 << 10
	maxArchiveSize    = 192 << 20
	maxExecutableSize = 256 << 20
	downloadTimeout   = 15 * time.Minute
	versionTimeout    = 10 * time.Second
)

// manifest is PiG's runtime slot manifest (docs/site/docs/typescript-runtimes.md) for a Bun runtime.
type manifest struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Provider string `json:"provider"`
	Image    image  `json:"image"`
}

type image struct {
	Executable string `json:"executable"`
	SHA256     string `json:"sha256"`
}

// slotRoot is the runtime slot root PiG reads: PIG_TS_RUNTIMES_DIR, else <agent dir>/runtimes/typescript.
func slotRoot(agentDir func() (string, error)) (string, error) {
	if root := os.Getenv("PIG_TS_RUNTIMES_DIR"); root != "" {
		return root, nil
	}
	dir, err := agentDir()
	if err != nil {
		return "", fmt.Errorf("resolve the agent directory: %w", err)
	}
	return filepath.Join(dir, "runtimes", "typescript"), nil
}

// platform names this machine as PiG's release targets do, and reports whether it uses musl.
func platform() (string, bool) {
	goos := runtime.GOOS
	if goos == "linux" && os.Getenv("TERMUX_VERSION") != "" {
		goos = "android"
	}
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	if arch == "" {
		arch = runtime.GOARCH
	}
	musl := false
	if goos == "linux" {
		matches, _ := filepath.Glob("/lib/ld-musl-*.so.1")
		musl = len(matches) > 0
	}
	return goos + "-" + arch, musl
}

// installer installs Bun into a slot root.
type installer struct {
	root     string
	platform string
	musl     bool
	baseURL  string
	client   *http.Client
	// key, fingerprint and assets are the pins; nil means this Piglet's.
	key         ed25519.PublicKey
	fingerprint []byte
	assets      []asset
}

func (in installer) pins() (ed25519.PublicKey, []byte, []asset) {
	if in.key == nil {
		return signingKey, signingFingerprint, assets
	}
	return in.key, in.fingerprint, in.assets
}

// install downloads the platform's archive and the signed checksums, verifies the signature with the pinned key, the archive's
// digest against both the signed list and the pin, and the extracted executable against the pin, then replaces <root>/bun with a
// directory whose runtime.json is written last.
func (in installer) install(ctx context.Context) (string, error) {
	key, fingerprint, pinned := in.pins()
	a, ok := assetFor(pinned, in.platform, in.musl)
	if !ok {
		return "", fmt.Errorf("Bun %s has no build for %s", bunVersion, in.platform)
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	armored, err := in.fetch(ctx, "SHASUMS256.txt.asc", maxChecksumsSize)
	if err != nil {
		return "", err
	}
	signed, err := verifyClearsigned(armored, key, fingerprint)
	if err != nil {
		return "", fmt.Errorf("SHASUMS256.txt.asc: %w", err)
	}
	listed, err := signedDigest(signed, a.name+".zip")
	if err != nil {
		return "", err
	}
	if listed != a.zipSHA {
		return "", fmt.Errorf("the signed checksum of %s.zip is %s, this Piglet pins %s", a.name, listed, a.zipSHA)
	}
	if err := os.MkdirAll(in.root, 0o755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(in.root, ".bun-install-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	archive := filepath.Join(staging, a.name+".zip")
	if err := in.download(ctx, a.name+".zip", archive, a.zipSHA); err != nil {
		return "", err
	}
	if err := extractExecutable(archive, a.name+"/"+a.exeName, filepath.Join(staging, a.exeName), a.exeSHA); err != nil {
		return "", err
	}
	if err := os.Remove(archive); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(manifest{ID: runtimeID, Kind: "bun", Version: bunVersion, Platform: in.platform, Provider: provider, Image: image{Executable: a.exeName, SHA256: a.exeSHA}}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(staging, "runtime.json"), append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	target := filepath.Join(in.root, runtimeID)
	// Another process may be installing, removing or checking the runtime: the replacement is rename-aside then rename-in, and a reader between the two renames finds no <root>/bun.
	err = withInstallLock(ctx, in.root, true, func() error {
		removeStaleReplacements(target)
		return replaceDir(staging, target)
	})
	if err != nil {
		return "", err
	}
	return filepath.Join(target, a.exeName), nil
}

func (in installer) fetch(ctx context.Context, name string, limit int64) ([]byte, error) {
	body, err := in.open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download %s: larger than %d bytes", name, limit)
	}
	return data, nil
}

func (in installer) download(ctx context.Context, name, path, digest string) error {
	body, err := in.open(ctx, name)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(body, maxArchiveSize+1))
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	if n > maxArchiveSize {
		return fmt.Errorf("download %s: larger than %d bytes", name, maxArchiveSize)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != digest {
		return fmt.Errorf("download %s: SHA-256 %s, want %s", name, got, digest)
	}
	return nil
}

func (in installer) open(ctx context.Context, name string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.baseURL+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	resp, err := in.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", name, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("download %s: %s", name, resp.Status)
	}
	return resp.Body, nil
}

// signedDigest returns the digest the signed SHASUMS256.txt lists for name.
func signedDigest(signed, name string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(signed))
	for scanner.Scan() {
		digest, file, ok := strings.Cut(scanner.Text(), "  ")
		if ok && file == name {
			return digest, nil
		}
	}
	return "", fmt.Errorf("the signed checksums do not list %s", name)
}

// extractExecutable writes the archive member to path and checks its digest.
func extractExecutable(archive, member, path, digest string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(archive), err)
	}
	defer func() { _ = reader.Close() }()
	for _, file := range reader.File {
		if file.Name != member {
			continue
		}
		if file.UncompressedSize64 > maxExecutableSize {
			return fmt.Errorf("%s is larger than %d bytes", member, maxExecutableSize)
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		defer func() { _ = src.Close() }()
		dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(src, maxExecutableSize+1))
		if err := errors.Join(copyErr, dst.Close()); err != nil {
			return fmt.Errorf("extract %s: %w", member, err)
		}
		if n > maxExecutableSize {
			return fmt.Errorf("%s is larger than %d bytes", member, maxExecutableSize)
		}
		if got := hex.EncodeToString(hash.Sum(nil)); got != digest {
			return fmt.Errorf("%s: SHA-256 %s, want %s", member, got, digest)
		}
		return nil
	}
	return fmt.Errorf("%s has no %s", filepath.Base(archive), member)
}

// afterReplaceAside runs between replaceDir's two renames, while <root>/bun does not exist; a test replaces it to observe that window.
var afterReplaceAside = func() {}

// replaceDir moves staging to target, removing an earlier target after the move.
func replaceDir(staging, target string) error {
	var old string
	if _, err := os.Lstat(target); err == nil {
		old = target + ".old-" + filepath.Base(staging)
		if err := os.Rename(target, old); err != nil {
			return fmt.Errorf("replace %s: %w", target, err)
		}
		afterReplaceAside()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(staging, target); err != nil {
		if old != "" {
			err = errors.Join(err, os.Rename(old, target))
		}
		return fmt.Errorf("install %s: %w", target, err)
	}
	if old != "" {
		return os.RemoveAll(old)
	}
	return nil
}

// removeStaleReplacements deletes the directories an install that was killed between its two renames left beside target. The caller holds the install lock, so none belongs to a replacement in progress.
func removeStaleReplacements(target string) {
	stale, _ := filepath.Glob(target + ".old-*")
	for _, dir := range stale {
		_ = os.RemoveAll(dir)
	}
}

// verifyInstalled checks the installed runtime as PiG does before it runs it: the manifest, the executable's digest against this
// Piglet's pin and its --version.
func verifyInstalled(ctx context.Context, root, platform string, pinned []asset) (string, error) {
	dir := filepath.Join(root, runtimeID)
	data, err := os.ReadFile(filepath.Join(dir, "runtime.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("Bun is not installed in %s", dir)
	}
	if err != nil {
		return "", err
	}
	var m manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return "", fmt.Errorf("runtime.json: %w", err)
	}
	known := false
	for _, a := range pinned {
		known = known || (a.platform == m.Platform && a.exeName == m.Image.Executable && a.exeSHA == m.Image.SHA256)
	}
	if m.ID != runtimeID || m.Kind != "bun" || m.Version != bunVersion || m.Platform != platform || !known {
		return "", fmt.Errorf("runtime.json does not describe Bun %s for %s", bunVersion, platform)
	}
	executable := filepath.Join(dir, m.Image.Executable)
	file, err := os.Open(executable)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != m.Image.SHA256 {
		return "", fmt.Errorf("%s: SHA-256 %s, want %s", executable, got, m.Image.SHA256)
	}
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", executable, err)
	}
	if version := strings.TrimSpace(string(output)); version != bunVersion {
		return "", fmt.Errorf("%s --version printed %q", executable, version)
	}
	return executable, nil
}

// remove deletes the installed runtime.
func remove(ctx context.Context, root string) (removed bool, err error) {
	err = withInstallLock(ctx, root, false, func() error {
		dir := filepath.Join(root, runtimeID)
		if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		removed = true
		return os.RemoveAll(dir)
	})
	return removed, err
}

// verifyInstalledLocked is verifyInstalled while no other process replaces the runtime.
func verifyInstalledLocked(ctx context.Context, root, platform string, pinned []asset) (executable string, err error) {
	err = withInstallLock(ctx, root, false, func() (err error) {
		executable, err = verifyInstalled(ctx, root, platform, pinned)
		return err
	})
	return executable, err
}
