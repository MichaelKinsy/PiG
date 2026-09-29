package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// resolveTemporaryGitSource waits for an unpinned cache refresh and its progress callbacks before discovering resources. Offline cache misses contribute no resources; refresh failures report an error event and retain the existing checkout.
// Ports packages/coding-agent/src/core/package-manager.ts (resolvePackageSources, refreshTemporaryGitSource, getTemporaryDir).
func resolveTemporaryGitSource(sm *codingagent.SettingsManager, source string, progress packagemanager.ProgressCallback) (string, error) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || ref.Kind != sourceref.KindGit {
		return "", fmt.Errorf("invalid Git package source: %s", source)
	}
	checkout, err := temporaryGitCheckoutPath(sm.AgentDir(), ref)
	if err != nil {
		return "", err
	}
	packageRoot := filepath.Join(checkout, filepath.FromSlash(ref.GitSubdir))
	_, statErr := os.Stat(checkout)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		if packagemanager.IsOfflineModeEnabled() {
			return "", nil
		}
		if err := packagemanager.InstallGitCheckout(sm, ref, checkout, packageRoot, ""); err != nil {
			return "", err
		}
	case statErr != nil:
		return "", statErr
	case ref.GitRef == "" && !packagemanager.IsOfflineModeEnabled():
		// upstream: packages/coding-agent/src/core/package-manager.ts:refreshTemporaryGitSource
		_ = packagemanager.WithProgress(progress, "pull", source, fmt.Sprintf("Refreshing %s...", source), func() error {
			return packagemanager.InstallGitCheckout(sm, ref, checkout, packageRoot, "")
		})
	}
	if err := packagemanager.RequireGitSubdirectoryWithinCheckout(checkout, packageRoot); err != nil {
		return "", err
	}
	return packageRoot, nil
}

// temporaryGitCheckoutPath is a temporary Git source's checkout below the agent temporary extension root; paths outside that root are refused.
// Ports packages/coding-agent/src/core/package-manager.ts (getGitInstallPath, getTemporaryDir).
func temporaryGitCheckoutPath(agentDir string, ref sourceref.Ref) (string, error) {
	root := filepath.Join(agentDir, "tmp", "extensions")
	relative, err := packagemanager.GitCheckoutRelative(runtime.GOOS, root, ref)
	if err != nil {
		return "", err
	}
	host, path, _ := strings.Cut(relative, string(filepath.Separator))
	digest := sha256.Sum256([]byte("git-" + ref.GitHost + "-" + ref.GitPath))
	return filepath.Join(root, "git-"+host, fmt.Sprintf("%x", digest)[:8], path), nil
}
