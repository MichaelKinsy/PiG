package codingagent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	semver "github.com/Masterminds/semver/v3"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
)

// DefaultUpdateURL is the built-in update-manifest URL. It is empty in stock
// pig and is set at build time for a product binary via:
//
//	-ldflags "-X github.com/MichaelKinsy/PiG/internal/codingagent.DefaultUpdateURL=<url>"
//
// so a product release can configure its update source. Stock Pig and Piglet
// builds never set it: update transport is product/environment policy.
var DefaultUpdateURL string

// DefaultUpdateTrustRoot is an optional build-time Ed25519 public key in PEM
// form. Distributor installers normally seed the same public key through the
// update-trust.pem sidecar beside update-url.
var DefaultUpdateTrustRoot string

// UpdateSignatureHeader carries the base64 Ed25519 signature over the exact
// update manifest response bytes.
const UpdateSignatureHeader = "X-Pig-Release-Signature"

// PigletBinaryRelease is the baked Piglet/Piglet Binary release version. It
// is empty for stock Pig and set via ldflags for a Piglet Binary. When set it
// marks the running binary as an immutable baked release whose composition
// cannot be drifted in place (see ResolveSelfUpdateTier). It is the single
// source of truth for the baked release identity; cmd/pig copies its ldflags
// value here at startup.
var PigletBinaryRelease string

// maxUpdateBinaryBytes bounds a downloaded self-update binary.
const maxUpdateBinaryBytes = 512 << 20

// UpdateManifest is the document an update source returns: the latest version
// plus a per-platform binary URL and checksum. Host-agnostic so any static
// host or marketplace can serve it. Binaries is keyed by "<goos>/<goarch>".
type UpdateManifest struct {
	Version     string                  `json:"version"`
	PackageName string                  `json:"packageName"`
	Notes       string                  `json:"notes,omitempty"`
	Binaries    map[string]UpdateBinary `json:"binaries"`
}

// UpdateBinary is one platform's download URL and expected SHA256 (hex).
type UpdateBinary struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// BinaryUpdate describes an available newer release for the current platform.
type BinaryUpdate struct {
	CurrentVersion string
	LatestVersion  string
	Notes          string
	Binary         UpdateBinary
	Command        string // the command that applies it, e.g. "pig update self"
}

// UpdateSourceURL resolves the update-manifest URL. Resolution order:
//  1. PIG_UPDATE_URL env (explicit per-invocation override);
//  2. a sidecar file at <config-root>/update-url, written by an installer that
//     knows its origin at install time (a generic, transport-neutral seam: any
//     distributor may write it; pig reads only the URL, never product data);
//  3. the build-time embedded DefaultUpdateURL, set by a product release build;
//  4. empty (no update source configured).
//
// The sidecar lets an installer seed the update source for a downloaded binary
// without a Go toolchain (the binary is not rebuilt) and without modifying the
// user's shell rc. It carries one URL line; pig never writes it.
func UpdateSourceURL() string {
	if v := strings.TrimSpace(os.Getenv("PIG_UPDATE_URL")); v != "" {
		return v
	}
	if v := readUpdateURLSidecar(); v != "" {
		return v
	}
	return strings.TrimSpace(DefaultUpdateURL)
}

// readUpdateURLSidecar reads the optional update-URL sidecar at
// <config-root>/update-url. Returns "" when absent, unreadable, or blank.
func readUpdateURLSidecar() string {
	data, err := os.ReadFile(filepath.Join(ConfigRoot(), updateURLSidecarName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
}

// updateURLSidecarName is the sidecar file an installer writes to seed the
// update source for a downloaded binary.
const updateURLSidecarName = "update-url"

const updateTrustRootSidecarName = "update-trust.pem"

// updateTransportCASidecarName is the installer-owned TLS trust supplement for
// the configured update origin. It is distinct from update-trust.pem, which
// authenticates release metadata rather than the HTTPS transport.
const updateTransportCASidecarName = "update-ca.pem"

var updatePackageNamePattern = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)

// updateTrustRoots returns every Ed25519 public key the client trusts to sign a
// release. A bundle of more than one key is what makes key rotation survivable:
// during an overlap window the publisher signs with both the outgoing and
// incoming keys, so clients holding either one still verify. Without that,
// rotating a key silently breaks self-update for every client already installed.
//
// Every key here is already trusted by definition -- it came from the build, an
// operator-set path, or the sidecar an installer wrote. Nothing is adopted from
// the network.
func updateTrustRoots() ([]ed25519.PublicKey, error) {
	var data []byte
	if path := strings.TrimSpace(os.Getenv("PIG_UPDATE_TRUST_ROOT")); path != "" {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read update trust root %s: %w", path, err)
		}
	} else if sidecar, err := os.ReadFile(filepath.Join(ConfigRoot(), updateTrustRootSidecarName)); err == nil {
		data = sidecar
	} else {
		data = []byte(DefaultUpdateTrustRoot)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("no update trust root is configured")
	}
	var keys []ed25519.PublicKey
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "PUBLIC KEY" {
			return nil, fmt.Errorf("update trust root must contain only PEM public keys")
		}
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse update trust root: %w", err)
		}
		publicKey, ok := key.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("update trust root must be an Ed25519 public key")
		}
		keys = append(keys, publicKey)
	}
	if len(keys) == 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("update trust root must contain one or more PEM public keys")
	}
	return keys, nil
}

// verifyReleaseSignature reports whether any signature in the header verifies
// against any trusted key. The header carries one or more comma-separated
// base64 Ed25519 signatures over the exact manifest bytes; a publisher mid
// key-rotation emits one per active key so clients on either side of the
// rotation can verify. A malformed or unverifiable entry is skipped rather than
// fatal, because an old client must tolerate a signature from a key it does not
// know -- but at least one entry must verify or the release is refused.
func verifyReleaseSignature(data []byte, header string, trustRoots []ed25519.PublicKey) bool {
	for encoded := range strings.SplitSeq(header, ",") {
		signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil || len(signature) != ed25519.SignatureSize {
			continue
		}
		for _, key := range trustRoots {
			if ed25519.Verify(key, data, signature) {
				return true
			}
		}
	}
	return false
}

func updateDevelopmentHTTPAllowed() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PIG_UPDATE_ALLOW_LOOPBACK_HTTP")))
	return value == "1" || value == "true" || value == "yes"
}

func validateUpdateURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid update URL %q", raw)
	}
	if parsed.Scheme == "https" {
		return parsed, nil
	}
	if parsed.Scheme == "http" && updateDevelopmentHTTPAllowed() {
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		if strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback() {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("update URL %s must use HTTPS; loopback HTTP requires PIG_UPDATE_ALLOW_LOOPBACK_HTTP=1", raw)
}

func doUpdateRequest(client *http.Client, req *http.Request) (*http.Response, error) {
	trustedClient, err := clientWithUpdateTransportCA(client)
	if err != nil {
		return nil, err
	}
	clone := *trustedClient
	previousCheck := clone.CheckRedirect
	clone.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && next.URL.Scheme != "https" {
			return fmt.Errorf("refusing HTTPS downgrade redirect to %s", next.URL)
		}
		if _, err := validateUpdateURL(next.URL.String()); err != nil {
			return err
		}
		if previousCheck != nil {
			return previousCheck(next, via)
		}
		return nil
	}
	return clone.Do(req)
}

func readUpdateTransportCA() ([]byte, error) {
	root := ConfigRoot()
	rootInfo, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) || err == nil && !rootInfo.IsDir() {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat update config root %s: %w", root, err)
	}
	path := filepath.Join(root, updateTransportCASidecarName)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read update transport CA %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat update transport CA %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("update transport CA %s must be a regular file", path)
	}
	if ownerOnly, err := ownerfile.OwnerOnly(path, info); err != nil || !ownerOnly {
		return nil, fmt.Errorf("update transport CA %s must be owner-only", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read update transport CA %s: %w", path, err)
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("update transport CA %s exceeds 1048576-byte limit", path)
	}
	return data, nil
}

func clientWithUpdateTransportCA(client *http.Client) (*http.Client, error) {
	path := filepath.Join(ConfigRoot(), updateTransportCASidecarName)
	data, err := readUpdateTransportCA()
	if err != nil {
		return nil, err
	}
	if data == nil {
		return client, nil
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	httpTransport, ok := transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("update transport CA requires an HTTP transport")
	}
	transportClone := httpTransport.Clone()
	var roots *x509.CertPool
	if transportClone.TLSClientConfig != nil && transportClone.TLSClientConfig.RootCAs != nil {
		roots = transportClone.TLSClientConfig.RootCAs.Clone()
	} else {
		roots, err = x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("load system certificate roots: %w", err)
		}
		if roots == nil {
			roots = x509.NewCertPool()
		}
	}
	certificates := 0
	rest := data
	for len(bytes.TrimSpace(rest)) > 0 {
		block, next := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("update transport CA %s must contain only PEM certificates", path)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse update transport CA %s: %w", path, err)
		}
		roots.AddCert(certificate)
		certificates++
		rest = next
	}
	if certificates == 0 {
		return nil, fmt.Errorf("update transport CA %s contains no certificates", path)
	}

	if transportClone.TLSClientConfig == nil {
		transportClone.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	} else {
		transportClone.TLSClientConfig = transportClone.TLSClientConfig.Clone()
		transportClone.TLSClientConfig.RootCAs = roots
		if transportClone.TLSClientConfig.MinVersion < tls.VersionTLS12 {
			transportClone.TLSClientConfig.MinVersion = tls.VersionTLS12
		}
	}
	clone := *client
	clone.Transport = transportClone
	return &clone, nil
}

// SelfUpdateFallback is the actionable instruction shown when self-update
// cannot run automatically (no source, unreachable, unsupported platform, or a
// read-only install). It degrades gracefully: point at the working command when
// a source is configured, otherwise the ladder of set-a-URL / download / pull
// the container image.
func SelfUpdateFallback() string {
	if src := UpdateSourceURL(); src != "" {
		return fmt.Sprintf(
			"Automatic update from %s could not be applied. Download a new %s binary from your provider's releases, "+
				"or pull the container image (log in to your registry first) and re-deploy.",
			src, AppName)
	}
	return fmt.Sprintf(
		"No update source is configured. Set PIG_UPDATE_URL=<manifest-url> and run `%s update`, "+
			"or download a new %s binary from your provider's releases, "+
			"or pull the container image (log in to your registry first) and re-deploy.",
		AppName, AppName)
}

// updateChecksOffline reports the environment values that suppress bounded
// startup update probes. Explicit `pig update` does not use this guard: its
// errors must remain actionable. PI_OFFLINE mirrors Pi; PIG_OFFLINE is Pig's
// product-neutral alias.
func updateChecksOffline() bool {
	if strings.TrimSpace(os.Getenv("PI_OFFLINE")) != "" {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PIG_OFFLINE")))
	return value == "1" || value == "true" || value == "yes"
}

// CheckForBinaryUpdate fetches the update manifest and reports a newer release
// for the current platform, or nil when up to date, no source is configured, or
// the source is unreachable. It never surfaces an error: a startup update check
// is best-effort and must not disrupt the session.
func CheckForBinaryUpdate(ctx context.Context, client *http.Client, currentVersion string) *BinaryUpdate {
	if updateChecksOffline() {
		return nil
	}
	src := UpdateSourceURL()
	if src == "" {
		return nil
	}
	manifest, err := FetchUpdateManifest(ctx, client, src)
	if err != nil || manifest == nil {
		return nil
	}
	if CompareVersions(currentVersion, manifest.Version) >= 0 {
		return nil
	}
	binary, ok := manifest.PlatformBinary()
	if !ok {
		return nil
	}
	return &BinaryUpdate{
		CurrentVersion: currentVersion,
		LatestVersion:  manifest.Version,
		Notes:          manifest.Notes,
		Binary:         binary,
		Command:        AppName + " update",
	}
}

// FetchUpdateManifest reads, authenticates, and parses the exact update
// manifest at rawURL.
func FetchUpdateManifest(ctx context.Context, client *http.Client, rawURL string) (*UpdateManifest, error) {
	manifestURL, err := validateUpdateURL(rawURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := doUpdateRequest(client, req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update source %s: %s", rawURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("update manifest exceeds 1048576-byte limit")
	}
	trustRoots, err := updateTrustRoots()
	if err != nil {
		return nil, err
	}
	if !verifyReleaseSignature(data, resp.Header.Get(UpdateSignatureHeader), trustRoots) {
		return nil, fmt.Errorf("update manifest release signature verification failed")
	}
	var manifest UpdateManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("parse update manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("parse update manifest: trailing content")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return nil, fmt.Errorf("update manifest has no version")
	}
	if _, err := semver.StrictNewVersion(manifest.Version); err != nil {
		return nil, fmt.Errorf("update manifest has invalid release version %q: %w", manifest.Version, err)
	}
	if !updatePackageNamePattern.MatchString(manifest.PackageName) {
		return nil, fmt.Errorf("update manifest has invalid package name %q", manifest.PackageName)
	}
	for platform, binary := range manifest.Binaries {
		binaryURL, err := url.Parse(binary.URL)
		if err != nil {
			return nil, fmt.Errorf("update manifest binary %s has invalid URL: %w", platform, err)
		}
		resolved := resp.Request.URL.ResolveReference(binaryURL)
		if _, err := validateUpdateURL(resolved.String()); err != nil {
			return nil, fmt.Errorf("update manifest binary %s: %w", platform, err)
		}
		binary.URL = resolved.String()
		manifest.Binaries[platform] = binary
	}
	return &manifest, nil
}

// PlatformBinary returns the manifest's binary for the current platform.
func (m *UpdateManifest) PlatformBinary() (UpdateBinary, bool) {
	bin, ok := m.Binaries[platformKey()]
	if !ok || strings.TrimSpace(bin.URL) == "" {
		return UpdateBinary{}, false
	}
	checksum := strings.TrimSpace(bin.SHA256)
	if len(checksum) != sha256.Size*2 {
		return UpdateBinary{}, false
	}
	if _, err := hex.DecodeString(checksum); err != nil {
		return UpdateBinary{}, false
	}
	return bin, true
}

// PlatformKey returns the "<goos>/<goarch>" manifest key for the current
// platform.
func PlatformKey() string { return runtime.GOOS + "/" + runtime.GOARCH }

func platformKey() string { return PlatformKey() }

// SelfReplace downloads bin, verifies its SHA256, and atomically replaces the
// running executable. Unix-only: replacing a running .exe in place needs the
// Windows quarantine dance pig does not implement, so on Windows it returns an
// error directing the user to reinstall.
func SelfReplace(ctx context.Context, client *http.Client, bin UpdateBinary) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return SelfReplaceAt(ctx, client, bin, exe)
}

// SelfReplaceAt downloads bin, verifies its SHA256, and atomically replaces the
// executable at exePath. It is the standalone-tier replacement entry point used
// after tier resolution has proven exePath. Unix-only: on Windows it returns an
// error directing the user to reinstall.
func SelfReplaceAt(ctx context.Context, client *http.Client, bin UpdateBinary, exePath string) error {
	return selfReplaceAt(ctx, client, bin, exePath, nil)
}

// SelfReplaceAtWithCommit replaces exePath and then commits its ownership
// metadata. If commit fails, the previous executable is restored before the
// error returns, so a failed receipt update cannot strand a new binary with
// stale provenance.
func SelfReplaceAtWithCommit(
	ctx context.Context,
	client *http.Client,
	bin UpdateBinary,
	exePath string,
	commit func() error,
) error {
	if commit == nil {
		return fmt.Errorf("standalone update commit is unavailable")
	}
	return selfReplaceAt(ctx, client, bin, exePath, commit)
}

func selfReplaceAt(ctx context.Context, client *http.Client, bin UpdateBinary, exePath string, commit func() error) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("in-place self-update is not supported on Windows; reinstall from the update source")
	}
	if strings.TrimSpace(bin.URL) == "" {
		return fmt.Errorf("update manifest has no binary for %s", platformKey())
	}
	want := strings.TrimSpace(bin.SHA256)
	if len(want) != sha256.Size*2 {
		return fmt.Errorf("update manifest has no valid SHA256 checksum: refusing to install")
	}
	if _, err := hex.DecodeString(want); err != nil {
		return fmt.Errorf("update manifest has invalid SHA256 checksum: refusing to install")
	}
	dir := filepath.Dir(exePath)
	tmpPath, sum, err := downloadBinaryToFile(ctx, client, bin.URL, dir, maxUpdateBinaryBytes)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpPath) }()
	if !strings.EqualFold(sum, want) {
		return fmt.Errorf("checksum mismatch: refusing to install")
	}
	if commit == nil {
		if err := os.Rename(tmpPath, exePath); err != nil {
			return fmt.Errorf("replace %s: %w", exePath, err)
		}
		return nil
	}

	backupFile, err := os.CreateTemp(dir, ".pig-update-backup-*")
	if err != nil {
		return fmt.Errorf("stage executable rollback: %w", err)
	}
	backupPath := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		_ = os.Remove(backupPath)
		return fmt.Errorf("stage executable rollback: %w", err)
	}
	if err := os.Remove(backupPath); err != nil {
		return fmt.Errorf("stage executable rollback: %w", err)
	}
	if err := os.Link(exePath, backupPath); err != nil {
		return fmt.Errorf("preserve current executable for rollback: %w", err)
	}
	removeBackup := true
	defer func() {
		if removeBackup {
			_ = os.Remove(backupPath)
		}
	}()
	if err := os.Rename(tmpPath, exePath); err != nil {
		return fmt.Errorf("replace %s: %w", exePath, err)
	}
	if err := commit(); err != nil {
		if restoreErr := os.Rename(backupPath, exePath); restoreErr != nil {
			removeBackup = false
			return fmt.Errorf("commit standalone ownership: %w; restore previous executable: %w; previous executable preserved at %s", err, restoreErr, backupPath)
		}
		return fmt.Errorf("commit standalone ownership: %w; previous executable restored", err)
	}
	return nil
}

// downloadBinaryToFile streams a verified candidate into the target directory
// while hashing it. The caller must compare the returned digest before rename;
// the returned path is always a temporary file that must be removed on every
// failure. Streaming keeps the update's memory use independent of the binary
// size while the limit protects both declared and chunked responses.
func downloadBinaryToFile(ctx context.Context, client *http.Client, rawURL, dir string, limit int64) (string, string, error) {
	if limit <= 0 {
		return "", "", fmt.Errorf("invalid update size limit")
	}
	downloadURL, err := validateUpdateURL(rawURL)
	if err != nil {
		return "", "", err
	}
	tmp, err := os.CreateTemp(dir, ".pig-update-*")
	if err != nil {
		return "", "", fmt.Errorf("stage update (is %s writable?): %w", dir, err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if tmp != nil {
			_ = tmp.Close()
		}
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL.String(), nil)
	if err != nil {
		return "", "", err
	}
	resp, err := doUpdateRequest(client, req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download %s: %s", rawURL, resp.Status)
	}
	if resp.ContentLength > limit {
		return "", "", fmt.Errorf("download %s exceeds %d-byte limit", rawURL, limit)
	}

	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, digest), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", "", err
	}
	if written > limit {
		return "", "", fmt.Errorf("download %s exceeds %d-byte limit", rawURL, limit)
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		return "", "", fmt.Errorf("download %s truncated: received %d of %d bytes", rawURL, written, resp.ContentLength)
	}
	if err := tmp.Chmod(0o755); err != nil {
		return "", "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", "", fmt.Errorf("sync update: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", "", err
	}
	tmp = nil
	removeTemp = false
	return tmpPath, hex.EncodeToString(digest.Sum(nil)), nil
}

// CompareVersions compares strict semantic versions. Returns -1 if a<b, 1 if
// a>b, and 0 when equal or when either side is malformed. Callers parsing
// release metadata must reject malformed remote versions before comparison;
// the zero fallback here keeps development/CI local versions non-disruptive.
func CompareVersions(a, b string) int {
	pa, err := semver.StrictNewVersion(strings.TrimSpace(a))
	if err != nil {
		return 0
	}
	pb, err := semver.StrictNewVersion(strings.TrimSpace(b))
	if err != nil {
		return 0
	}
	return pa.Compare(pb)
}
