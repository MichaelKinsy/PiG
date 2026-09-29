package codingagent

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// NormalizeExtensionPaths resolves extension-discovered file URLs and local paths against the session cwd, without changing the extension attribution. Invalid URLs fail before callers update resource state, with Node's own error as Pi's bindExtensions throws it.
// Ports packages/coding-agent/src/core/resource-loader.ts
func NormalizeExtensionPaths(cwd string, paths *extension.ResourcesDiscoverAggregateResult) (*extension.ResourcesDiscoverAggregateResult, error) {
	if paths == nil {
		return nil, nil
	}
	result := &extension.ResourcesDiscoverAggregateResult{}
	for _, group := range []struct {
		input  []extension.AttributedResourcePath
		output *[]extension.AttributedResourcePath
	}{
		{paths.SkillPaths, &result.SkillPaths},
		{paths.PromptPaths, &result.PromptPaths},
		{paths.ThemePaths, &result.ThemePaths},
	} {
		for _, entry := range group.input {
			path, err := ResolvePath(jsTrim(entry.Path), cwd)
			if err != nil {
				// Pi surfaces fileURLToPath's own error, with no path context.
				return nil, err
			}
			*group.output = append(*group.output, extension.AttributedResourcePath{Path: path, ExtensionPath: entry.ExtensionPath})
		}
	}
	return result, nil
}

// ResolvedPath pairs a candidate input path with its canonical path. Returned by DedupBySymlink.
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

// DedupBySymlink returns the first input for each distinct canonical path, in input order. It follows symlinks and drive junctions but preserves Windows volume mount points as directories, as Node's realpathSync does. On resolution failure it keeps the raw path as its own canonical identity.
// Ports packages/coding-agent/src/core/package-manager.ts.
func DedupBySymlink(paths []string) []ResolvedPath {
	seen := make(map[string]struct{}, len(paths))
	out := make([]ResolvedPath, 0, len(paths))
	for _, p := range paths {
		canonical := CanonicalizePath(p)
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
