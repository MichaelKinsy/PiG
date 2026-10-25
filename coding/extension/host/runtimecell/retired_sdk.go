package runtimecell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// RetiredStagedSDK reports whether dir, an extension's local SDK replace or path dependency, names an SDK an earlier build of the host
// staged or installed rather than the one this host reads now. stagedDir is the current SDK directory for the language
// ([pigsdklock.StagedSDKDir]). A link an author made to such a copy keeps resolving after an update (staging never deletes an older
// directory, and a package store keeps an older installation until it is collected) and would build the extension against an SDK the running
// host no longer speaks. After resolving symlinks, dir is retired when it lies outside stagedDir and either lies inside the config root's
// staging area (<config root>/state/pigsdk, the parent of a staged stagedDir) or holds an SDK version marker, which only staging and
// installation write. An empty stagedDir disables the check.
func RetiredStagedSDK(dir, stagedDir string) bool {
	if dir == "" || stagedDir == "" {
		return false
	}
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	current := filepath.Clean(stagedDir)
	if resolved, err := filepath.EvalSymlinks(stagedDir); err == nil {
		current = resolved
	}
	if pathWithin(current, canonicalDir) {
		return false
	}
	// pig additive (D195): a marked copy outside the current SDK directory is another build's staged or installed SDK, wherever it lives.
	//perf:ok once per extension SDK resolution, which also reads the extension's go.mod or Cargo.toml
	if _, err := os.Stat(filepath.Join(canonicalDir, pigsdklock.MarkerFile)); err == nil {
		return true
	}
	area := filepath.Dir(filepath.Clean(stagedDir))
	if filepath.Base(area) != "pigsdk" || filepath.Base(filepath.Dir(area)) != "state" {
		return false
	}
	canonicalArea, err := filepath.EvalSymlinks(area)
	if err != nil {
		return false
	}
	return pathWithin(canonicalArea, canonicalDir) && canonicalDir != canonicalArea
}

// installedSDK returns lang's installed SDK directory when this binary's SDKs are installed ([pigsdklock.InstalledSDKRoot]), with the
// installation's fault when that SDK is missing or mismatched. installed is false when the SDKs stage under the config root; a resolver
// then looks further. An installed binary never falls back to another copy of an SDK it cannot verify.
// pig additive (D195): see pigsdklock.InstalledSDKRoot.
func installedSDK(lang string) (dir string, installed bool, err error) {
	if pigsdklock.InstalledSDKRoot() == "" {
		return "", false, nil
	}
	if err := pigsdklock.CheckInstalledSDK(lang); err != nil {
		return "", true, err
	}
	roots := stagedSDKRoots(lang)
	if len(roots) == 0 {
		return "", true, fmt.Errorf("this program links no %s extension SDK", lang)
	}
	return roots[0], true, nil
}

// ambientStagedSDKDir is the current staging directory for lang under the
// config root resolved from the environment, or "" when none is registered.
func ambientStagedSDKDir(lang string) string {
	if roots := stagedSDKRoots(lang); len(roots) > 0 {
		return roots[0]
	}
	return ""
}

// pathWithin reports whether path is root or lies beneath it.
func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
