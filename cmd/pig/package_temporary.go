package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// Ports packages/coding-agent/src/core/package-manager.ts
// resolveCLIExtensionSource resolves temporary Packages before loading their extensions. Offline sources that need installation contribute no resources. Cached unpinned Git sources refresh before discovery; a failed refresh retains the cache, while an initial installation failure is surfaced.
func resolveCLIExtensionSource(cwd, agentDir string, sm *codingagent.SettingsManager, source string, progress packagemanager.ProgressCallback) (string, error) {
	kind := packagemanager.DetectSourceKind(source)
	if kind == "local" {
		return packagemanager.ResolveLocalPackageRoot(cwd, source)
	}
	if kind == "git" {
		return resolveTemporaryGitSource(sm, source, progress)
	}
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil {
		return "", err
	}
	root, err := temporaryPackagePath(agentDir, "npm", "")
	if err != nil {
		return "", err
	}
	packageRoot := npmPackagePath(root, ref)
	pkg := packagemanager.ConfiguredPackage{Source: codingagent.PackageSource{Source: source}, InstalledPath: packageRoot}
	if !packagemanager.InstalledPackageMatchesConfiguredVersion(pkg) {
		if packagemanager.IsOfflineModeEnabled() {
			return "", nil
		}
		if err := packagemanager.EnsureManagedPackageRoot(root); err != nil {
			return "", err
		}
		command := packagemanager.DefaultNpmCommand(sm)
		args := append([]string{}, command[1:]...)
		args = append(args, packagemanager.NpmInstallArgs(packagemanager.NpmCommandName(command), ref.Locator, root, ref.NPMRegistry)...)
		if err := packagemanager.RunPackageProcess("", command[0], args...); err != nil {
			return "", err
		}
	}
	return packageRoot, nil
}

func npmPackagePath(root string, source sourceref.Ref) string {
	return filepath.Join(root, "node_modules", filepath.FromSlash(source.NPMName))
}

func temporaryPackagePath(agentDir, prefix, suffix string) (string, error) {
	tempRoot := filepath.Join(agentDir, "tmp", "extensions")
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(tempRoot, 0o700); err != nil {
		return "", err
	}
	root, err := packagemanager.ResolveManagedPackagePath(tempRoot, prefix)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(prefix + "-" + suffix))
	return packagemanager.ResolveManagedPackagePath(root, fmt.Sprintf("%x", digest[:4]), filepath.FromSlash(suffix))
}
