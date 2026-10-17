package aotruntime

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
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
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Bounds on what an install reads: the signed checksum list, its signature, an archive and the extracted compiler. Pi-Bolt 0.7.4's largest runtime
// archive is 44 MB and its largest compiler 106 MB.
const (
	maxChecksumsSize  = 64 << 10
	maxArchiveSize    = 256 << 20
	maxExecutableSize = 384 << 20
	maxNoticeSize     = 1 << 20
	downloadTimeout   = 20 * time.Minute
	versionTimeout    = 15 * time.Second
)

// toolchainFiles are the files of a runtime archive this Piglet keeps: the compiler and the notices that travel with it.
var toolchainFiles = map[string]fileKind{
	"bun":                    kindExecutable,
	"bun.exe":                kindExecutable,
	"LICENSE":                kindNotice,
	"LICENSE.pi":             kindNotice,
	"THIRD_PARTY_NOTICES.md": kindNotice,
}

type fileKind int

const (
	kindExecutable fileKind = iota
	kindNotice
)

// toolchainRecord is <toolchain dir>/toolchain.json, written after every file is in place.
type toolchainRecord struct {
	Version    string `json:"version"`
	Platform   string `json:"platform"`
	Archive    string `json:"archive"`
	ArchiveSHA string `json:"archiveSha256"`
	ExeSHA     string `json:"executableSha256"`
	Source     string `json:"corresponding_source"`
}

// toolchainDir is where the compiler for platform lives under the provider directory.
func toolchainDir(providerDir, platform string) string {
	return filepath.Join(providerDir, "toolchain", "pi-bolt-"+toolchainVersion+"-"+platform)
}

// installer downloads the Pi-Bolt compiler on the user's machine. PiG, the Piglet and its releases carry no copy of it.
type installer struct {
	providerDir string
	platform    string
	baseURL     string
	client      *http.Client
	// key and archives are the pins; nil means this Piglet's.
	key      ed25519.PublicKey
	archives []archive
}

func (in installer) pins() (ed25519.PublicKey, []archive) {
	if in.key == nil {
		return releaseKey, archives
	}
	return in.key, in.archives
}

// install downloads the platform's runtime archive and the signed checksum list. It verifies the signature with the pinned key, the archive's
// digest against both the signed list and the pin, and the extracted compiler against the pin, and then publishes the toolchain directory by
// rename with toolchain.json written last. It returns the compiler's path.
func (in installer) install(ctx context.Context) (string, error) {
	key, pinned := in.pins()
	var a archive
	for _, candidate := range pinned {
		if candidate.platform == in.platform {
			a = candidate
		}
	}
	if a.name == "" {
		return "", fmt.Errorf("Pi-Bolt %s has no compiler for %s", toolchainVersion, in.platform)
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	checksums, err := in.fetch(ctx, checksumsFile, maxChecksumsSize)
	if err != nil {
		return "", err
	}
	signature, err := in.fetch(ctx, signatureFile, ed25519.SignatureSize)
	if err != nil {
		return "", err
	}
	if len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, checksums, signature) {
		return "", fmt.Errorf("%s: the Ed25519 signature does not verify with the key pinned in this Piglet", checksumsFile)
	}
	listed, err := signedDigest(string(checksums), a.name)
	if err != nil {
		return "", err
	}
	if listed != a.sha256 {
		return "", fmt.Errorf("the signed checksum of %s is %s, this Piglet pins %s", a.name, listed, a.sha256)
	}
	parent := filepath.Join(in.providerDir, "toolchain")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(parent, ".install-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	archivePath := filepath.Join(staging, a.name)
	if err := in.download(ctx, a.name, archivePath, a.sha256); err != nil {
		return "", err
	}
	unpacked := filepath.Join(staging, "unpacked")
	if err := os.Mkdir(unpacked, 0o755); err != nil {
		return "", err
	}
	exe, err := extractToolchain(archivePath, unpacked, a.exeSHA)
	if err != nil {
		return "", err
	}
	if err := os.Remove(archivePath); err != nil {
		return "", err
	}
	record, err := json.MarshalIndent(toolchainRecord{Version: toolchainVersion, Platform: in.platform, Archive: a.name, ArchiveSHA: a.sha256, ExeSHA: a.exeSHA, Source: sourcesURL}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(unpacked, "toolchain.json"), append(record, '\n'), 0o644); err != nil {
		return "", err
	}
	target := toolchainDir(in.providerDir, in.platform)
	if err := replaceDir(unpacked, target); err != nil {
		return "", err
	}
	return filepath.Join(target, exe), nil
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

func (in installer) download(ctx context.Context, name, dest, digest string) error {
	body, err := in.open(ctx, name)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	file, err := os.Create(dest)
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
	client := in.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", name, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("download %s: %s", name, resp.Status)
	}
	return resp.Body, nil
}

// signedDigest returns the digest the signed checksum list gives for name.
func signedDigest(list, name string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(list))
	for scanner.Scan() {
		digest, file, ok := strings.Cut(scanner.Text(), "  ")
		if ok && file == name {
			return digest, nil
		}
	}
	return "", fmt.Errorf("the signed checksums do not list %s", name)
}

// extractToolchain unpacks the archive's files into dir, rejects every other entry, checks the compiler against digest and returns its file name.
func extractToolchain(archivePath, dir, digest string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	zr, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", filepath.Base(archivePath), err)
	}
	tr := tar.NewReader(zr)
	exeName := ""
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read %s: %w", filepath.Base(archivePath), err)
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		name := path.Base(header.Name)
		kind, known := toolchainFiles[name]
		if header.Typeflag != tar.TypeReg || !known {
			return "", fmt.Errorf("%s holds an unexpected entry %q", filepath.Base(archivePath), header.Name)
		}
		limit := int64(maxNoticeSize)
		mode := os.FileMode(0o644)
		if kind == kindExecutable {
			limit, mode, exeName = maxExecutableSize, 0o755, name
		}
		if header.Size > limit {
			return "", fmt.Errorf("%s is larger than %d bytes", header.Name, limit)
		}
		dst, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return "", err
		}
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(tr, limit+1))
		if err := errors.Join(copyErr, dst.Close()); err != nil {
			return "", fmt.Errorf("extract %s: %w", header.Name, err)
		}
		if n > limit {
			return "", fmt.Errorf("%s is larger than %d bytes", header.Name, limit)
		}
		if kind == kindExecutable {
			if got := hex.EncodeToString(hash.Sum(nil)); got != digest {
				return "", fmt.Errorf("%s: SHA-256 %s, want %s", header.Name, got, digest)
			}
		}
	}
	if exeName == "" {
		return "", fmt.Errorf("%s has no compiler", filepath.Base(archivePath))
	}
	return exeName, nil
}

// replaceDir moves staging to target, removing an earlier target after the move.
func replaceDir(staging, target string) error {
	var old string
	if _, err := os.Lstat(target); err == nil {
		old = target + ".old-" + filepath.Base(filepath.Dir(staging))
		if err := os.Rename(target, old); err != nil {
			return fmt.Errorf("replace %s: %w", target, err)
		}
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

// verified caches the verification of one compiler state for the life of the process.
var verified sync.Map

type verifiedKey struct {
	path    string
	size    int64
	mtimeNs int64
}

// verifyToolchain checks an installed toolchain as the build does before it runs the compiler: the record, the executable's digest against the
// pin and its --version. It returns the compiler's path.
func verifyToolchain(ctx context.Context, providerDir, platform string, pinned []archive) (string, error) {
	dir := toolchainDir(providerDir, platform)
	data, err := os.ReadFile(filepath.Join(dir, "toolchain.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("the Pi-Bolt compiler is not installed in %s", dir)
	}
	if err != nil {
		return "", err
	}
	var record toolchainRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return "", fmt.Errorf("toolchain.json: %w", err)
	}
	var a archive
	for _, candidate := range pinned {
		if candidate.platform == platform {
			a = candidate
		}
	}
	if a.name == "" || record.Version != toolchainVersion || record.Platform != platform || record.ExeSHA != a.exeSHA {
		return "", fmt.Errorf("toolchain.json does not describe Pi-Bolt %s for %s", toolchainVersion, platform)
	}
	exe := ""
	for name, kind := range toolchainFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); kind == kindExecutable && err == nil {
			exe = filepath.Join(dir, name)
		}
	}
	if exe == "" {
		return "", fmt.Errorf("%s has no compiler", dir)
	}
	info, err := os.Stat(exe)
	if err != nil {
		return "", err
	}
	key := verifiedKey{path: exe, size: info.Size(), mtimeNs: info.ModTime().UnixNano()}
	if _, ok := verified.Load(key); ok {
		return exe, nil
	}
	got, err := fileSHA256(exe)
	if err != nil {
		return "", err
	}
	if got != a.exeSHA {
		return "", fmt.Errorf("%s: SHA-256 %s, want %s", exe, got, a.exeSHA)
	}
	vctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(vctx, exe, "--version")
	ownProcessGroup(cmd)
	output, err := cmd.Output()
	endProcessGroup(cmd)
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", exe, err)
	}
	if strings.TrimSpace(string(output)) == "" {
		return "", fmt.Errorf("%s --version printed nothing", exe)
	}
	verified.Store(key, struct{}{})
	return exe, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// removeToolchain deletes the installed compiler of platform.
func removeToolchain(providerDir, platform string) (bool, error) {
	dir := toolchainDir(providerDir, platform)
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return true, os.RemoveAll(dir)
}
