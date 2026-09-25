package codingagent

import (
	"os"
	"path/filepath"
	"slices"
)

// ResolvedPath pairs a candidate input path with the canonical path it
// resolves to via filepath.EvalSymlinks. Returned by DedupBySymlink.
type ResolvedPath struct {
	Original  string
	Canonical string
}

// firstNonEmpty returns the first non-empty argument, or "" if none.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// DedupBySymlink walks the input paths, resolves each via
// filepath.EvalSymlinks, and returns one entry per distinct canonical
// path (in input order). Duplicates produced by symlinks pointing at
// the same target are silently dropped.
//
// On EvalSymlinks error (broken symlink, permission denied, loop), the
// raw path is treated as its own canonical and kept. This mirrors
// upstream package-manager.ts:2280-2320 which falls back to the raw
// path on realpathSync failure (try/catch).
//
// Loop guard: filepath.EvalSymlinks already returns an error after Go's
// internal max-link traversal limit, so we just trust the stdlib.
//
// Reference: .upstream/current/packages/coding-agent/src/core/package-manager.ts
func DedupBySymlink(paths []string) []ResolvedPath {
	seen := make(map[string]struct{}, len(paths))
	out := make([]ResolvedPath, 0, len(paths))
	for _, p := range paths {
		canonical, err := filepath.EvalSymlinks(p)
		if err != nil {
			// Fallback to raw path. Two distinct broken symlinks
			// pointing nowhere are kept as two entries (matches
			// upstream's per-entry try/catch fallback).
			canonical = p
		}
		if _, dup := seen[canonical]; dup {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, ResolvedPath{Original: p, Canonical: canonical})
	}
	return out
}

// ListSkills enumerates skill directories under skillsDir, applying
// symlink dedup. Returns skill names (the directory basenames)
// sorted alphabetically. A skill directory is anything containing a
// `SKILL.md` file.
//
// A skill can be linked from multiple configured roots. Resolve symlinks so
// the user sees one entry in the /skills selector.
func ListSkills(skillsDir string) ([]string, error) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, nil
	}
	var candidates []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(skillsDir, e.Name())
		if _, statErr := os.Stat(filepath.Join(path, "SKILL.md")); statErr != nil {
			continue
		}
		candidates = append(candidates, path)
	}
	resolved := DedupBySymlink(candidates)
	names := make([]string, 0, len(resolved))
	for _, r := range resolved {
		names = append(names, filepath.Base(r.Original))
	}
	slices.Sort(names)
	return names, nil
}
