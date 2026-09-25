// Package tools: fd/rg installer.
//
// This file ports the download/extract pipeline from upstream
// packages/coding-agent/src/utils/tools-manager.ts. The pipeline ensures
// that fd and rg are available even when the user has not installed them
// via the system package manager:
//
//  1. EnsureTool first checks <agentDir>/bin/<binary> (local install),
//  2. then PATH (system install),
//  3. then, if neither is found and we're online and not on Android/Termux,
//     downloads the appropriate platform asset from GitHub Releases,
//     extracts it, and installs the binary into <agentDir>/bin.
//
// The asset table selects platform-specific release archives. On darwin/x64, fd uses a pinned release.
//
// Extraction divergence (D14: documented in DIVERGENCES.md): upstream
// spawns `tar`, `unzip`, and PowerShell. pig uses Go stdlib
// (archive/tar + compress/gzip + archive/zip) on all platforms. The
// extracted binary is byte-identical; only the error message text on
// extraction failure differs.
package tools

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/managementhttp"
)

const (
	toolsNetworkTimeout  = 10 * time.Second
	toolsDownloadTimeout = 120 * time.Second
)

// toolConfig mirrors upstream ToolConfig (tools-manager.ts:20–27).
type toolConfig struct {
	Name              string
	Repo              string   // GitHub repo (e.g. "sharkdp/fd")
	BinaryName        string   // Name of the binary inside the archive
	SystemBinaryNames []string // Alternative system command names to try
	TagPrefix         string   // Prefix for tags ("v" or "")
	// GetAssetName returns the release asset filename, or "" if the
	// platform/arch combo is unsupported. Matches upstream
	// (version, plat, architecture) → string|null.
	GetAssetName func(version, plat, arch string) string
}

// toolsTable selects fd and rg release archives for each platform.
// Linux fd and arm64 rg currently select GNU archives; Linux x64 rg selects musl.
var toolsTable = map[string]toolConfig{
	"fd": {
		Name:              "fd",
		Repo:              "sharkdp/fd",
		BinaryName:        "fd",
		SystemBinaryNames: []string{"fd", "fdfind"},
		TagPrefix:         "v",
		GetAssetName: func(version, plat, architecture string) string {
			switch plat {
			case "darwin":
				return fmt.Sprintf("fd-v%s-%s-apple-darwin.tar.gz", version, archStr(architecture))
			case "linux":
				return fmt.Sprintf("fd-v%s-%s-unknown-linux-gnu.tar.gz", version, archStr(architecture))
			case "windows", "win32":
				return fmt.Sprintf("fd-v%s-%s-pc-windows-msvc.zip", version, archStr(architecture))
			}
			return ""
		},
	},
	"rg": {
		Name:       "ripgrep",
		Repo:       "BurntSushi/ripgrep",
		BinaryName: "rg",
		TagPrefix:  "",
		GetAssetName: func(version, plat, architecture string) string {
			switch plat {
			case "darwin":
				return fmt.Sprintf("ripgrep-%s-%s-apple-darwin.tar.gz", version, archStr(architecture))
			case "linux":
				if architecture == "arm64" {
					return fmt.Sprintf("ripgrep-%s-aarch64-unknown-linux-gnu.tar.gz", version)
				}
				return fmt.Sprintf("ripgrep-%s-x86_64-unknown-linux-musl.tar.gz", version)
			case "windows", "win32":
				return fmt.Sprintf("ripgrep-%s-%s-pc-windows-msvc.zip", version, archStr(architecture))
			}
			return ""
		},
	},
}

// archStr maps Go GOARCH (or Node's arch) values to upstream's tuple form.
// upstream Node arch: "arm64" → "aarch64", "x64" → "x86_64".
// Go GOARCH: "arm64" → "aarch64", "amd64" → "x86_64".
func archStr(architecture string) string {
	switch architecture {
	case "arm64", "aarch64":
		return "aarch64"
	case "amd64", "x64", "x86_64":
		return "x86_64"
	}
	return architecture
}

// termuxPackages mirrors upstream TERMUX_PACKAGES (tools-manager.ts:339).
var termuxPackages = map[string]string{
	"fd": "fd",
	"rg": "ripgrep",
}

// ToolsManager provides the download/install pipeline for fd and rg.
// It is parameterised on the agent dir so tests can use a tempdir.
//
// Concurrent EnsureTool calls for the same tool deduplicate downloads
// via the singleflight in-flight map.
type ToolsManager struct {
	agentDir string

	mu       sync.Mutex
	inflight map[string]chan struct{}

	// httpClient is the client used for GitHub API and asset downloads.
	// Tests may override this to point at a fake server.
	httpClient *http.Client

	// releaseBaseURL is the GitHub web origin that serves both the
	// releases/latest redirect and the asset downloads. Tests may override it
	// to point at a local server. Default: "https://github.com".
	releaseBaseURL string

	// platform/arch overrides for testing. Empty means use runtime values.
	platformOverride string
	archOverride     string
}

// NewToolsManager constructs a manager rooted at agentDir.
func NewToolsManager(agentDir string) *ToolsManager {
	return &ToolsManager{
		agentDir:       agentDir,
		inflight:       map[string]chan struct{}{},
		httpClient:     &http.Client{Timeout: toolsDownloadTimeout},
		releaseBaseURL: "https://github.com",
	}
}

// BinDir returns <agentDir>/bin (mirrors upstream getBinDir).
func (m *ToolsManager) BinDir() string {
	return filepath.Join(m.agentDir, "bin")
}

func (m *ToolsManager) platform() string {
	if m.platformOverride != "" {
		return m.platformOverride
	}
	// Map Go's runtime.GOOS to Node's process.platform vocabulary so the
	// asset-name table can be shared.
	switch runtime.GOOS {
	case "windows":
		return "win32"
	case "android":
		return "android"
	}
	return runtime.GOOS // "darwin", "linux"
}

func (m *ToolsManager) arch() string {
	if m.archOverride != "" {
		return m.archOverride
	}
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH // "arm64"
}

// GetToolPath mirrors upstream getToolPath (tools-manager.ts:80).
// Returns the path to the tool, or "" if not found.
//
// Lookup order:
//  1. <agentDir>/bin/<binary>[.exe] (local install)
//  2. systemBinaryNames in PATH
func (m *ToolsManager) GetToolPath(tool string) string {
	config, ok := toolsTable[tool]
	if !ok {
		return ""
	}
	binExt := ""
	if m.platform() == "win32" {
		binExt = ".exe"
	}
	localPath := filepath.Join(m.BinDir(), config.BinaryName+binExt)
	if _, err := os.Stat(localPath); err == nil {
		return localPath
	}
	for _, name := range systemBinaryNames(systemToolConfig{
		BinaryName:        config.BinaryName,
		SystemBinaryNames: config.SystemBinaryNames,
	}) {
		if path, err := exec.LookPath(name); err == nil {
			if runtime.GOOS == "windows" {
				return name
			}
			return path
		}
	}
	return ""
}

// ToolStatus mirrors upstream ToolStatus: progress and failures EnsureTool
// reports instead of printing. Type is "info" or "warning".
type ToolStatus struct {
	Type    string
	Message string
}

// EnsureTool mirrors upstream ensureTool: it returns the tool's path, or "" when unavailable. It reports progress and failures through onStatus (nil discards them), never writes to the console, and includes up to five distinct cause messages in download warnings.
func (m *ToolsManager) EnsureTool(ctx context.Context, tool string, onStatus func(ToolStatus)) string {
	report := func(kind, message string) {
		if onStatus != nil {
			onStatus(ToolStatus{Type: kind, Message: message})
		}
	}
	if existing := m.GetToolPath(tool); existing != "" {
		return existing
	}
	config, ok := toolsTable[tool]
	if !ok {
		return ""
	}

	if IsOfflineModeEnabled() {
		report("warning", config.Name+" not found. Offline mode enabled, skipping download.")
		return ""
	}
	if m.platform() == "android" {
		pkg := termuxPackages[tool]
		if pkg == "" {
			pkg = tool
		}
		report("warning", config.Name+" not found. Install with: pkg install "+pkg)
		return ""
	}

	// Deduplicate concurrent EnsureTool calls for the same tool.
	m.mu.Lock()
	if ch, busy := m.inflight[tool]; busy {
		m.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ""
		}
		return m.GetToolPath(tool)
	}
	ch := make(chan struct{})
	m.inflight[tool] = ch
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.inflight, tool)
		close(ch)
		m.mu.Unlock()
	}()

	report("info", config.Name+" not found. Downloading...")
	path, err := m.downloadTool(ctx, tool)
	if err != nil {
		report("warning", "Failed to download "+config.Name+": "+toolFailureMessage(err))
		return ""
	}
	report("info", config.Name+" installed to "+path)
	return path
}

// toolFailureMessage joins distinct cause messages in order. Go wrappers often already append their cause text; remove that suffix before joining so the same cause is not printed twice.
func toolFailureMessage(err error) string {
	var messages []string
	// upstream: packages/coding-agent/src/utils/tools-manager.ts:ensureTool
	for depth := 0; err != nil && depth < 5; depth++ {
		message := err.Error()
		cause := errors.Unwrap(err)
		if cause != nil {
			message = strings.TrimSuffix(message, ": "+cause.Error())
		}
		if !slices.Contains(messages, message) {
			messages = append(messages, message)
		}
		err = cause
	}
	return strings.Join(messages, ": ")
}

// IsOfflineModeEnabled mirrors the env-var check used elsewhere in pig.
// Duplicated here (instead of imported from cmd/pig) because tools is a
// lower-level package that cannot depend on the CLI.
func IsOfflineModeEnabled() bool {
	for _, key := range []string{"PIG_OFFLINE", "PI_OFFLINE"} {
		v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
		if v == "1" || v == "true" || v == "yes" {
			return true
		}
	}
	return false
}

// getLatestVersion mirrors upstream getLatestVersion (tools-manager.ts). It
// resolves the version from the releases/latest redirect on the web origin,
// which, unlike the api.github.com endpoint, costs no anonymous API quota.
// Returns the tag with one leading "v" stripped.
func (m *ToolsManager) getLatestVersion(ctx context.Context, repo string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.releaseBaseURL+"/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "pig-coding-agent")
	// redirect: "manual": only the status and Location matter.
	client := *m.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := managementhttp.FetchWithRetry(&client, req, managementhttp.FetchRetryOptions{Timeout: toolsNetworkTimeout})
	if err != nil {
		return "", err
	}
	// Only the headers matter. Like Response.body.cancel upstream, close
	// without reading a potentially unbounded redirect body.
	_ = resp.Body.Close()

	location := ""
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		location = resp.Header.Get("Location")
	}
	if location == "" {
		return "", fmt.Errorf("Failed to resolve latest %s release: HTTP %d without redirect", repo, resp.StatusCode)
	}
	return latestReleaseTagVersion(repo, location)
}

// latestReleaseTagVersion mirrors the upstream redirect parsing: resolve the
// Location against https://github.com, take the last path segment, and require
// a /releases/tag/ target.
func latestReleaseTagVersion(repo, location string) (string, error) {
	ref, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	resolved := (&url.URL{Scheme: "https", Host: "github.com", Path: "/"}).ResolveReference(ref)
	segments := strings.Split(resolved.EscapedPath(), "/")
	tag, err := url.PathUnescape(segments[len(segments)-1])
	if err != nil {
		return "", err
	}
	if tag == "" || !strings.Contains(location, "/releases/tag/") {
		return "", fmt.Errorf("Failed to resolve latest %s release: unexpected redirect to %s", repo, location)
	}
	return strings.TrimPrefix(tag, "v"), nil
}

// downloadFile mirrors upstream downloadFile (tools-manager.ts).
func (m *ToolsManager) downloadFile(ctx context.Context, rawURL, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := managementhttp.FetchWithRetry(m.httpClient, req, managementhttp.FetchRetryOptions{Timeout: toolsDownloadTimeout})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Download failed with HTTP %d: %s", resp.StatusCode, rawURL)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	return nil
}

// findBinaryRecursively mirrors upstream findBinaryRecursively
// (tools-manager.ts:144). Returns the full path or "".
func findBinaryRecursively(rootDir, binaryFileName string) string {
	var found string
	_ = filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == binaryFileName {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// localArchiveEntry reports whether an archive entry name stays inside the
// extraction directory: relative, and with no ".." anywhere in it (the fd and
// rg archives never use one).
func localArchiveEntry(name string) bool {
	return !strings.Contains(name, "..") && filepath.IsLocal(filepath.FromSlash(name))
}

// pig divergence (D14): use Go archive readers. Ignore links that the fd and
// rg binaries do not require.
func extractTarGz(archivePath, extractDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !localArchiveEntry(hdr.Name) {
			return fmt.Errorf("tar entry escapes extract dir: %s", hdr.Name)
		}
		target := filepath.Join(extractDir, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			_ = out.Close()
		case tar.TypeSymlink, tar.TypeLink:
			continue
		}
	}
}

// pig divergence (D14): use Go's ZIP reader instead of external tools.
func extractZip(archivePath, extractDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	for _, f := range r.File {
		if !localArchiveEntry(f.Name) {
			return fmt.Errorf("zip entry escapes extract dir: %s", f.Name)
		}
		target := filepath.Join(extractDir, f.Name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode().Perm())
		if err != nil {
			_ = rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			_ = rc.Close()
			_ = out.Close()
			return err
		}
		_ = rc.Close()
		_ = out.Close()
	}
	return nil
}

// downloadTool mirrors upstream downloadTool (tools-manager.ts:247).
func (m *ToolsManager) downloadTool(ctx context.Context, tool string) (string, error) {
	config, ok := toolsTable[tool]
	if !ok {
		return "", fmt.Errorf("Unknown tool: %s", tool)
	}
	plat := m.platform()
	architecture := m.arch()

	version, err := m.getLatestVersion(ctx, config.Repo)
	if err != nil {
		return "", err
	}
	// fd uses upstream's pinned darwin/x64 release.
	if tool == "fd" && plat == "darwin" && architecture == "x64" {
		version = "10.3.0"
	}

	assetName := config.GetAssetName(version, plat, architecture)
	if assetName == "" {
		return "", fmt.Errorf("Unsupported platform: %s/%s", plat, architecture)
	}

	if err := os.MkdirAll(m.BinDir(), 0o755); err != nil {
		return "", err
	}

	downloadURL := fmt.Sprintf("%s/%s/releases/download/%s%s/%s",
		m.releaseBaseURL, config.Repo, config.TagPrefix, version, assetName)
	archivePath := filepath.Join(m.BinDir(), assetName)
	binExt := ""
	if plat == "win32" {
		binExt = ".exe"
	}
	binaryFileName := config.BinaryName + binExt
	binaryPath := filepath.Join(m.BinDir(), binaryFileName)

	if err := m.downloadFile(ctx, downloadURL, archivePath); err != nil {
		return "", err
	}

	// Unique temp dir so concurrent EnsureTool calls don't collide,
	// mirroring upstream's `extract_tmp_<bin>_<pid>_<ms>_<rand>` pattern.
	extractDir, err := os.MkdirTemp(m.BinDir(), fmt.Sprintf("extract_tmp_%s_", config.BinaryName))
	if err != nil {
		_ = os.Remove(archivePath)
		return "", err
	}
	defer func() {
		_ = os.Remove(archivePath)
		_ = os.RemoveAll(extractDir)
	}()

	switch {
	case strings.HasSuffix(assetName, ".tar.gz"):
		if err := extractTarGz(archivePath, extractDir); err != nil {
			return "", fmt.Errorf("Failed to extract %s: %w", assetName, err)
		}
	case strings.HasSuffix(assetName, ".zip"):
		if err := extractZip(archivePath, extractDir); err != nil {
			return "", fmt.Errorf("Failed to extract %s: %w", assetName, err)
		}
	default:
		return "", fmt.Errorf("Unsupported archive format: %s", assetName)
	}

	// Probe the conventional nested dir first (fast path), then walk.
	nestedDir := filepath.Join(extractDir, strings.TrimSuffix(strings.TrimSuffix(assetName, ".tar.gz"), ".zip"))
	candidates := []string{
		filepath.Join(nestedDir, binaryFileName),
		filepath.Join(extractDir, binaryFileName),
	}
	var extractedBinary string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			extractedBinary = c
			break
		}
	}
	if extractedBinary == "" {
		extractedBinary = findBinaryRecursively(extractDir, binaryFileName)
	}
	if extractedBinary == "" {
		return "", fmt.Errorf("Binary not found in archive: expected %s under %s", binaryFileName, extractDir)
	}

	// Move into place. Use os.Rename when possible; fall back to copy when
	// crossing filesystems.
	if err := os.Rename(extractedBinary, binaryPath); err != nil {
		if !errors.Is(err, os.ErrExist) {
			if cerr := copyFile(extractedBinary, binaryPath); cerr != nil {
				return "", cerr
			}
		}
	}
	if plat != "win32" {
		_ = os.Chmod(binaryPath, 0o755)
	}
	return binaryPath, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}
