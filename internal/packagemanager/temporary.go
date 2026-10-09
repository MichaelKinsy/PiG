package packagemanager

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
	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// ResolveManagedPackagePath is package-manager.ts resolveManagedPath: path.resolve(resolve(root), ...parts), so an absolute component replaces the preceding root, then a check that the result is the root or starts with the root and a separator.
// Ports packages/coding-agent/src/core/package-manager.ts.
func ResolveManagedPackagePath(root string, parts ...string) (string, error) {
	resolvedRoot, err := nodepath.Resolve(root)
	if err != nil {
		return "", err
	}
	resolved, err := nodepath.Resolve(append([]string{resolvedRoot}, parts...)...)
	if err != nil {
		return "", err
	}
	if resolved != resolvedRoot && !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("Refusing to use path outside package install root: %s", resolved)
	}
	return resolved, nil
}

// Ports packages/coding-agent/src/core/package-manager.ts (resolvePackageSources with the temporary scope).
// ResolveTemporarySource resolves temporary Packages before loading their extensions. Offline sources that need installation contribute no resources. Cached unpinned Git sources refresh before discovery; a failed refresh retains the cache, while an initial installation failure is surfaced.
func ResolveTemporarySource(cwd, agentDir string, sm *codingagent.SettingsManager, source string, progress ProgressCallback) (string, error) {
	kind := DetectSourceKind(source)
	if kind == "local" {
		return ResolveLocalPackageRoot(cwd, source)
	}
	if kind == "git" {
		return resolveTemporaryGitSource(sm, source, progress)
	}
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil {
		return "", err
	}
	root, err := TemporaryPackagePath(agentDir, "npm", "")
	if err != nil {
		return "", err
	}
	packageRoot := NpmPackagePath(root, ref)
	pkg := ConfiguredPackage{Source: codingagent.PackageSource{Source: source}, InstalledPath: packageRoot}
	if !InstalledPackageMatchesConfiguredVersion(pkg) {
		if IsOfflineModeEnabled() {
			return "", nil
		}
		if err := EnsureManagedPackageRoot(root); err != nil {
			return "", err
		}
		command := DefaultNpmCommand(sm)
		args := append([]string{}, command[1:]...)
		manager, err := PackageManagerName(command)
		if err != nil {
			return "", err
		}
		args = append(args, NpmInstallArgs(manager, ref.Locator, root, ref.NPMRegistry)...)
		if err := RunPackageProcess("", command[0], args...); err != nil {
			return "", err
		}
	}
	return packageRoot, nil
}

func NpmPackagePath(root string, source sourceref.Ref) string {
	return filepath.Join(root, "node_modules", filepath.FromSlash(source.NPMName))
}

func TemporaryPackagePath(agentDir, prefix, suffix string) (string, error) {
	tempRoot := filepath.Join(agentDir, "tmp", "extensions")
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(tempRoot, 0o700); err != nil {
		return "", err
	}
	root, err := ResolveManagedPackagePath(tempRoot, prefix)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(prefix + "-" + suffix))
	return ResolveManagedPackagePath(root, fmt.Sprintf("%x", digest[:4]), filepath.FromSlash(suffix))
}

// resolveTemporaryGitSource waits for an unpinned cache refresh and its progress callbacks before discovering resources. Offline cache misses contribute no resources; refresh failures report an error event and retain the existing checkout.
// Ports packages/coding-agent/src/core/package-manager.ts (resolvePackageSources, refreshTemporaryGitSource, getTemporaryDir).
func resolveTemporaryGitSource(sm *codingagent.SettingsManager, source string, progress ProgressCallback) (string, error) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || ref.Kind != sourceref.KindGit {
		return "", fmt.Errorf("invalid Git package source: %s", source)
	}
	checkout, err := TemporaryGitCheckoutPath(sm.AgentDir(), ref)
	if err != nil {
		return "", err
	}
	packageRoot := filepath.Join(checkout, filepath.FromSlash(ref.GitSubdir))
	_, statErr := os.Stat(checkout)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		if IsOfflineModeEnabled() {
			return "", nil
		}
		if err := InstallGitCheckout(sm, ref, checkout, packageRoot, ""); err != nil {
			return "", err
		}
	case statErr != nil:
		return "", statErr
	case ref.GitRef == "" && !IsOfflineModeEnabled():
		// upstream: packages/coding-agent/src/core/package-manager.ts:refreshTemporaryGitSource
		_ = WithProgress(progress, "pull", source, fmt.Sprintf("Refreshing %s...", source), func() error {
			return InstallGitCheckout(sm, ref, checkout, packageRoot, "")
		})
	}
	if err := RequireGitSubdirectoryWithinCheckout(checkout, packageRoot); err != nil {
		return "", err
	}
	return packageRoot, nil
}

// TemporaryGitCheckoutPath is a temporary Git source's checkout below the agent temporary extension root; paths outside that root are refused.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/package-manager.ts:2153-2183 (getGitInstallPath, getTemporaryDir).
func TemporaryGitCheckoutPath(agentDir string, ref sourceref.Ref) (string, error) {
	root := filepath.Join(agentDir, "tmp", "extensions")
	relative, err := GitCheckoutRelative(runtime.GOOS, root, ref)
	if err != nil {
		return "", err
	}
	host, path, _ := strings.Cut(relative, string(filepath.Separator))
	// Each pinned ref gets its own checkout: the ref is part of the hash (.upstream/v0.99.1/packages/coding-agent/src/core/package-manager.ts:2153-2183).
	key := "git-" + ref.GitHost + "-" + ref.GitPath
	if ref.GitRef != "" {
		key += "@" + ref.GitRef
	}
	digest := sha256.Sum256([]byte(key))
	return filepath.Join(root, "git-"+host, fmt.Sprintf("%x", digest)[:8], path), nil
}
