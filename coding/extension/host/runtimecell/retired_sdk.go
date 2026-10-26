package runtimecell

import (
	"path/filepath"
	"strings"
)

// RetiredStagedSDK reports whether dir, an extension's local SDK replace or path
// dependency, names an SDK an earlier build of the host staged rather than the
// one staged now. stagedDir is the current staging directory for the language
// ([pigsdklock.StagedSDKDir]); its parent, <config root>/state/pigsdk, is the
// staging area. dir is retired when, after resolving symlinks, it lies inside
// that area but outside stagedDir. Staging never deletes an older directory, so
// a link an author made to it keeps resolving after an update and would build
// the extension against an SDK the running host no longer speaks. A stagedDir
// outside that layout disables the check.
func RetiredStagedSDK(dir, stagedDir string) bool {
	if dir == "" || stagedDir == "" {
		return false
	}
	area := filepath.Dir(filepath.Clean(stagedDir))
	if filepath.Base(area) != "pigsdk" || filepath.Base(filepath.Dir(area)) != "state" {
		return false
	}
	canonicalArea, err := filepath.EvalSymlinks(area)
	if err != nil {
		return false
	}
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	if !pathWithin(canonicalArea, canonicalDir) || canonicalDir == canonicalArea {
		return false
	}
	current := filepath.Join(canonicalArea, filepath.Base(stagedDir))
	if resolved, err := filepath.EvalSymlinks(stagedDir); err == nil {
		current = resolved
	}
	return !pathWithin(current, canonicalDir)
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
