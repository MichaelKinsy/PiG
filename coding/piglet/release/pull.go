package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/net/idna"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

const (
	maxIndexBytes  = 1 << 20
	maxBinaryBytes = 512 << 20
	pullTimeout    = 5 * time.Minute
)

// Options selects one release target and applies explicit signer rotation.
type Options struct {
	Client       *http.Client
	Target       string
	Version      string
	AcceptSigner string
	Now          func() time.Time
}

// Result identifies the installed artifact and its durable signed receipt.
type Result struct {
	Piglet      string
	Version     string
	Target      string
	SignerKeyID string
	Artifact    string
	Receipt     string
}

// Receipt retains the signed release index and signed binary manifest that
// selected one installed artifact.
type Receipt struct {
	IndexURL      string             `json:"indexUrl"`
	IndexEnvelope signature.Envelope `json:"indexEnvelope"`
	Manifest      signature.Manifest `json:"manifest"`
	Artifact      ReceiptArtifact    `json:"artifact"`
	InstalledAt   string             `json:"installedAt"`
}

// ReceiptArtifact binds a receipt to a path under the managed artifact root.
type ReceiptArtifact struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type currentPointer struct {
	Artifact    string `json:"artifact"`
	Receipt     string `json:"receipt"`
	SignerKeyID string `json:"signerKeyId"`
}

// Pull verifies a signed release index and target binary before publishing any
// managed file. The current pointer is the atomic commit point and also pins
// the first accepted signer for subsequent pulls. Publication revalidates that
// pin under a cross-process store lock and refuses symlinked managed paths.
func Pull(ctx context.Context, ref string, options Options) (Result, error) {
	indexURL, refVersion, err := resolveIndexURL(ref, options.Version)
	if err != nil {
		return Result{}, err
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: pullTimeout}
	}
	indexData, err := fetchBounded(ctx, client, indexURL, maxIndexBytes)
	if err != nil {
		return Result{}, fmt.Errorf("fetch Piglet release index: %w", err)
	}
	verified, err := Verify(indexData)
	if err != nil {
		return Result{}, err
	}
	index := verified.Index
	if refVersion != "" && refVersion != index.Version {
		return Result{}, fmt.Errorf("Piglet release index is version %s, requested %s", index.Version, refVersion)
	}
	target := options.Target
	if target == "" {
		target = runtime.GOOS + "/" + runtime.GOARCH
	}
	binary, exists := index.Binaries[target]
	if !exists {
		return Result{}, fmt.Errorf("Piglet release %s %s has no binary for target %s", index.Piglet, index.Version, target)
	}
	trust, err := signature.LoadTrust(signature.TrustDir())
	if err != nil {
		return Result{}, fmt.Errorf("Piglet trust store: %w", err)
	}
	if err := checkIndexTrust(verified, trust); err != nil {
		return Result{}, err
	}
	if err := checkSignerContinuity(index.Piglet, index.Signer.KeyID, options.AcceptSigner); err != nil {
		return Result{}, err
	}
	binaryURL, err := resolveBinaryURL(indexURL, binary.URL)
	if err != nil {
		return Result{}, fmt.Errorf("Piglet release target %s: %w", target, err)
	}
	stage, digest, size, err := downloadBinary(ctx, client, binaryURL, binary)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = os.Remove(stage) }()
	status, err := signature.Check(stage, signature.Policy{Trust: trust})
	if err != nil {
		return Result{}, fmt.Errorf("verify downloaded Piglet Binary: %w", err)
	}
	if !status.Signed {
		return Result{}, fmt.Errorf("downloaded Piglet Binary is unsigned")
	}
	if err := matchBinaryManifest(status, verified, target); err != nil {
		return Result{}, err
	}
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	artifacts, err := openStore("artifacts", true)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = artifacts.Close() }()
	lock, err := lockStore()
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = lock.Close() }()
	// pig additive (D18): revalidate the signer under cross-process ownership immediately before publishing any release files.
	if err := checkSignerContinuity(index.Piglet, index.Signer.KeyID, options.AcceptSigner); err != nil {
		return Result{}, err
	}
	return commitPull(stage, indexURL, verified, status.Manifest, digest, size, target, now().UTC(), artifacts)
}

func checkIndexTrust(verified VerifiedIndex, trust signature.Trust) error {
	id := verified.Index.Signer.KeyID
	if trust.Revoked[id] {
		return fmt.Errorf("Piglet release index is signed by %s, a key revoked in your Piglet trust store", id)
	}
	trusted, known := trust.Keys[id]
	if known && !trusted.Equal(verified.PublicKey) {
		return fmt.Errorf("Piglet trust store key %s does not match the release index public key", id)
	}
	if trust.RequireSignature && !known {
		return fmt.Errorf("Piglet release index is signed by %s, which is not in your Piglet trust store, and your policy requires a trusted signature", id)
	}
	return nil
}

func checkSignerContinuity(piglet, signer, accepted string) error {
	if accepted != "" && accepted != signer {
		return fmt.Errorf("--accept-signer names %s, but the release index is signed by %s", accepted, signer)
	}
	current, err := readCurrent(piglet)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read current Piglet release for %s: %w", piglet, err)
	}
	if current.SignerKeyID == signer {
		return nil
	}
	if accepted == signer {
		return nil
	}
	return fmt.Errorf("Piglet %s is pinned to signer %s; release signer %s is refused (pass --accept-signer %s to change it)", piglet, current.SignerKeyID, signer, signer)
}

func matchBinaryManifest(status signature.Status, verified VerifiedIndex, target string) error {
	index := verified.Index
	if status.KeyID != index.Signer.KeyID {
		return fmt.Errorf("downloaded Piglet Binary is signed by %s, release index is signed by %s", status.KeyID, index.Signer.KeyID)
	}
	manifest := status.Manifest
	for _, check := range []struct{ name, signed, indexed string }{
		{"Piglet", manifest.Piglet, index.Piglet},
		{"release version", manifest.ReleaseVersion, index.Version},
		{"target", manifest.Target, target},
		{"Pig version", manifest.PigVersion, index.PigVersion},
	} {
		if check.signed != check.indexed {
			return fmt.Errorf("downloaded Piglet Binary manifest names %s %q, release index names %q", check.name, check.signed, check.indexed)
		}
	}
	for name, digest := range map[string]string{
		"Piglet definition": manifest.PigletDigest,
		"Piglet source":     manifest.SourceDigest,
		"resolution record": manifest.ResolutionDigest,
		"component plan":    manifest.ComponentPlanDigest,
		"executable":        manifest.Executable.Digest,
	} {
		if !validDigest(digest) {
			return fmt.Errorf("downloaded Piglet Binary manifest has invalid %s digest %q", name, digest)
		}
	}
	return nil
}

func downloadBinary(ctx context.Context, client *http.Client, rawURL string, binary Binary) (string, string, int64, error) {
	if err := checkBinarySize(binary.Size); err != nil {
		return "", "", 0, err
	}
	response, err := doRequest(ctx, client, rawURL)
	if err != nil {
		return "", "", 0, fmt.Errorf("download Piglet Binary: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", "", 0, fmt.Errorf("download Piglet Binary: %s", response.Status)
	}
	if response.ContentLength > binary.Size {
		return "", "", 0, fmt.Errorf("downloaded Piglet Binary exceeds declared size %d", binary.Size)
	}
	file, err := os.CreateTemp("", ".piglet-pull-*.binary")
	if err != nil {
		return "", "", 0, err
	}
	path := file.Name()
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, binary.Size+1))
	if copyErr != nil {
		return "", "", 0, fmt.Errorf("download Piglet Binary: %w", copyErr)
	}
	if written != binary.Size {
		return "", "", 0, fmt.Errorf("downloaded Piglet Binary size is %d, release index says %d", written, binary.Size)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if digest != binary.SHA256 {
		return "", "", 0, fmt.Errorf("downloaded Piglet Binary SHA256 is %s, release index says %s", digest, binary.SHA256)
	}
	if err := file.Chmod(0o755); err != nil {
		return "", "", 0, err
	}
	if err := file.Close(); err != nil {
		return "", "", 0, err
	}
	remove = false
	return path, "sha256:" + digest, written, nil
}

func fetchBounded(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	response, err := doRequest(ctx, client, rawURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d-byte limit", limit)
	}
	return data, nil
}

func doRequest(ctx context.Context, client *http.Client, rawURL string) (*http.Response, error) {
	if _, err := validateRemoteURL(rawURL); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil) //nolint:gosec // G704: this CLI downloads the caller-selected release; URL policy is checked above and on every redirect.
	if err != nil {
		return nil, requestFailure{cause: err}
	}
	clone := *client
	previous := clone.CheckRedirect
	clone.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return urlPolicyError("stopped after 10 redirects")
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && next.URL.Scheme != "https" {
			return urlPolicyError("refusing HTTPS downgrade redirect")
		}
		if _, err := validateRemoteURL(next.URL.String()); err != nil {
			return err
		}
		if previous != nil {
			return previous(next, via)
		}
		return nil
	}
	response, err := clone.Do(request) //nolint:gosec // G704: caller-selected distribution URLs are intentional CLI input, validated before requests and redirects.
	if err != nil {
		if policy, ok := errors.AsType[urlPolicyError](err); ok {
			return response, policy
		}
		return response, requestFailure{cause: err}
	}
	return response, nil
}

type urlPolicyError string

func (e urlPolicyError) Error() string { return string(e) }

// pig additive (D18): HTTP client and redirect errors can contain private URL queries. Diagnostics are static; callers can still inspect the original error with errors.Is or errors.As.
type requestFailure struct{ cause error }

func (e requestFailure) Error() string {
	switch {
	case errors.Is(e.cause, context.Canceled):
		return context.Canceled.Error()
	case errors.Is(e.cause, context.DeadlineExceeded):
		return context.DeadlineExceeded.Error()
	default:
		return "release HTTP request failed; check network connectivity, redirect policy, and TLS configuration"
	}
}

func (e requestFailure) Unwrap() error { return e.cause }

func resolveIndexURL(ref, version string) (string, string, error) {
	requestedVersion := strings.TrimPrefix(version, "v")
	if strings.HasPrefix(ref, "https://") || strings.HasPrefix(ref, "http://") {
		if _, err := validateRemoteURL(ref); err != nil {
			return "", "", err
		}
		if requestedVersion != "" && !validReleaseVersion(requestedVersion) {
			return "", "", fmt.Errorf("invalid Piglet release version %q", version)
		}
		return ref, requestedVersion, nil
	}
	github, ok := strings.CutPrefix(ref, "github:")
	if !ok {
		return "", "", fmt.Errorf("Piglet release has no recorded release index; use an HTTPS index URL or github:owner/repo@version")
	}
	repository, releaseVersion, ok := strings.Cut(github, "@")
	if ok {
		releaseVersion = strings.TrimPrefix(releaseVersion, "v")
	} else {
		releaseVersion = requestedVersion
	}
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || !validGitHubPart(owner) || !validGitHubPart(name) || !validReleaseVersion(releaseVersion) {
		return "", "", fmt.Errorf("invalid GitHub Piglet release ref; want github:owner/repo@version")
	}
	if requestedVersion != "" && requestedVersion != releaseVersion {
		return "", "", fmt.Errorf("GitHub release ref version %s does not match --version %s", releaseVersion, version)
	}
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/piglet-release.json", owner, name, releaseVersion), releaseVersion, nil
}

func validGitHubPart(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func resolveBinaryURL(indexURL, raw string) (string, error) {
	base, err := url.Parse(indexURL)
	if err != nil {
		return "", urlPolicyError("invalid Piglet release index URL")
	}
	reference, err := url.Parse(raw)
	if err != nil {
		return "", urlPolicyError("invalid Piglet Binary URL")
	}
	resolved := base.ResolveReference(reference).String()
	if _, err := validateRemoteURL(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func validateRemoteURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, urlPolicyError("invalid Piglet release URL; use an absolute HTTPS URL without credentials or a fragment")
	}
	if parsed.Scheme == "https" {
		host := parsed.Hostname()
		if strings.IndexFunc(host, func(r rune) bool { return r > 127 }) >= 0 {
			host, err = idna.Lookup.ToASCII(host)
			if err != nil {
				return nil, urlPolicyError("invalid Piglet release hostname")
			}
		}
		host = strings.TrimRight(strings.ToLower(host), ".")
		// pig additive (D18): Piglet distribution never delegates to Pi's Earendil-operated service namespace.
		if host == "pi.dev" || strings.HasSuffix(host, ".pi.dev") {
			return nil, urlPolicyError("Piglet distribution does not use the Earendil-operated host " + host)
		}
		return parsed, nil
	}
	if parsed.Scheme == "http" && loopbackHTTPAllowed() {
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		if strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback() {
			return parsed, nil
		}
	}
	return nil, urlPolicyError("Piglet release URL must use HTTPS; loopback HTTP requires PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP=1")
}

func loopbackHTTPAllowed() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP")))
	return value == "1" || value == "true" || value == "yes"
}

func validDigest(value string) bool {
	hexValue, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(hexValue) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(hexValue)
	return err == nil && hex.EncodeToString(decoded) == hexValue
}

func readCurrent(piglet string) (currentPointer, error) {
	data, err := readManagedFile("artifacts", filepath.Join(piglet, "current"))
	if err != nil {
		return currentPointer{}, err
	}
	var current currentPointer
	if err := strictJSON(data, &current); err != nil {
		return currentPointer{}, err
	}
	if !validRelativePath(current.Artifact) || !validRelativePath(current.Receipt) || current.SignerKeyID == "" {
		return currentPointer{}, fmt.Errorf("current pointer has invalid paths or signer")
	}
	return current, nil
}

func validRelativePath(path string) bool {
	clean := filepath.Clean(filepath.FromSlash(path))
	return path != "" && !strings.Contains(path, `\\`) && path == filepath.ToSlash(clean) && !filepath.IsAbs(path) && clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func currentPath(piglet string) string {
	return filepath.Join(codingagent.PigletArtifactsDir(), piglet, "current")
}

func commitPull(stage, indexURL string, verified VerifiedIndex, manifest signature.Manifest, digest string, size int64, target string, now time.Time, artifacts *os.Root) (Result, error) {
	index := verified.Index
	goos, goarch, _ := strings.Cut(target, "/")
	name := "pig-" + index.Piglet
	if goos == "windows" {
		name += ".exe"
	}
	artifactRelative := filepath.Join(index.Piglet, index.Version, goos, goarch, name)
	receiptRelative := filepath.Join(index.Piglet, index.Version, goos, goarch+".pull")
	artifactPath := filepath.Join(codingagent.PigletArtifactsDir(), artifactRelative)
	receiptPath := filepath.Join(codingagent.PigletRecordsDir(), receiptRelative)
	receipt := Receipt{
		IndexURL: indexURL, IndexEnvelope: verified.Envelope, Manifest: manifest,
		Artifact:    ReceiptArtifact{Path: filepath.ToSlash(artifactRelative), Digest: digest, Size: size},
		InstalledAt: now.Format(time.RFC3339),
	}
	receiptData, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return Result{}, err
	}
	receiptData = append(receiptData, '\n')
	current := currentPointer{
		Artifact: filepath.ToSlash(artifactRelative), Receipt: filepath.ToSlash(receiptRelative),
		SignerKeyID: index.Signer.KeyID,
	}
	currentData, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return Result{}, err
	}
	currentData = append(currentData, '\n')
	return publishPull(artifacts, stage, receiptData, currentData, digest, size, Result{
		Piglet: index.Piglet, Version: index.Version, Target: target, SignerKeyID: index.Signer.KeyID,
		Artifact: artifactPath, Receipt: receiptPath,
	})
}

func publishPull(artifacts *os.Root, source string, receiptData, currentData []byte, digest string, size int64, result Result) (Result, error) {
	records, err := openStore("receipts", true)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = records.Close() }()
	artifactRelative, err := filepath.Rel(codingagent.PigletArtifactsDir(), result.Artifact)
	if err != nil {
		return Result{}, err
	}
	receiptRelative, err := filepath.Rel(codingagent.PigletRecordsDir(), result.Receipt)
	if err != nil {
		return Result{}, err
	}
	artifactDir, err := openDirectory(artifacts, filepath.Dir(artifactRelative), true)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = artifactDir.Close() }()
	receiptDir, err := openDirectory(records, filepath.Dir(receiptRelative), true)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = receiptDir.Close() }()
	currentDir, err := openDirectory(artifacts, result.Piglet, false)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = currentDir.Close() }()
	input, err := os.Open(source)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = input.Close() }()
	artifactStage, err := stageIn(artifactDir, input, 0o755)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = artifactDir.Remove(artifactStage) }()
	staged, err := artifactDir.Open(artifactStage)
	if err != nil {
		return Result{}, err
	}
	err = verifyArtifact(staged, digest, size)
	_ = staged.Close()
	if err != nil {
		return Result{}, fmt.Errorf("verify staged Piglet Binary: %w", err)
	}
	receiptStage, err := stageIn(receiptDir, bytes.NewReader(receiptData), 0o644)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = receiptDir.Remove(receiptStage) }()
	currentStage, err := stageIn(currentDir, bytes.NewReader(currentData), 0o644)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = currentDir.Remove(currentStage) }()
	artifactName, receiptName := filepath.Base(result.Artifact), filepath.Base(result.Receipt)
	if err := artifactDir.Link(artifactStage, artifactName); err != nil {
		return Result{}, fmt.Errorf("commit Piglet Binary %s: %w", result.Artifact, err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = artifactDir.Remove(artifactName)
		}
	}()
	if err := receiptDir.Link(receiptStage, receiptName); err != nil {
		return Result{}, fmt.Errorf("commit Piglet release receipt %s: %w", result.Receipt, err)
	}
	defer func() {
		if !complete {
			_ = receiptDir.Remove(receiptName)
		}
	}()
	if err := currentDir.Rename(currentStage, "current"); err != nil {
		return Result{}, fmt.Errorf("commit current Piglet release: %w", err)
	}
	complete = true
	return result, nil
}
