package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/text"
)

// ContextFile holds a loaded project context file (AGENTS.override.md,
// AGENTS.md, or CLAUDE.md).
// Mirrors upstream resource-loader.ts::loadProjectContextFiles return shape.
type ContextFile struct {
	Path    string
	Content string
}

// contextCandidates are the filenames checked in each directory, in priority
// order. The first one found wins per directory.
// Mirrors upstream resource-loader.ts:59 candidates array.
var contextCandidates = []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"}

// LoadProjectContextFiles discovers and loads project context files from the
// agent config directory and every ancestor directory from cwd up to the
// filesystem root.
//
// Load order (matches upstream resource-loader.ts:76-115):
//  1. Agent config dir (e.g. ~/.pig/): global context
//  2. Ancestor directories from root down to cwd: project context
//
// Within each directory, AGENTS.override.md replaces that directory's regular
// AGENTS/CLAUDE candidate while ancestor layering remains intact.
// Duplicate paths are skipped by their loaded path. In a nested linked
// worktree, the main checkout's same-named context file is skipped when the
// worktree root supplies its own copy.
func LoadProjectContextFiles(cwd, agentDir string) []ContextFile {
	var result []ContextFile
	seen := make(map[string]struct{})

	// 1. Global context from agent config dir.
	if agentDir != "" {
		if cf := loadContextFileFromDir(agentDir); cf != nil {
			if _, dup := seen[cf.Path]; !dup {
				seen[cf.Path] = struct{}{}
				result = append(result, *cf)
			}
		}
	}

	// 2. Walk from cwd upward to root, collecting files in reverse
	// (root-first) order so the nearest file appears last: matching
	// upstream's ancestorContextFiles.unshift() pattern.
	if cwd != "" {
		var ancestors []ContextFile
		shadowed := findShadowedContextFile(cwd)
		dir := cwd
		for {
			if cf := loadContextFileFromDir(dir); cf != nil {
				isShadowed := shadowed != "" && canonicalPath(cf.Path) == shadowed
				if _, dup := seen[cf.Path]; !dup && !isShadowed {
					seen[cf.Path] = struct{}{}
					ancestors = append([]ContextFile{*cf}, ancestors...)
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break // reached root
			}
			dir = parent
		}
		result = append(result, ancestors...)
	}

	return result
}

// loadContextFileFromDir checks context candidates in priority order.
// Returns the first match, or nil if none found.
// Mirrors upstream resource-loader.ts:58-72 loadContextFileFromDir.
func loadContextFileFromDir(dir string) *ContextFile {
	for _, name := range contextCandidates {
		p := filepath.Join(dir, name)
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not read %s: %v\n", p, err)
			continue
		}
		return &ContextFile{Path: p, Content: text.StripBom(string(data))}
	}
	return nil
}

func findShadowedContextFile(cwd string) string {
	repoDir, commonGitDir, ok := findContextGitPaths(cwd)
	if !ok {
		return ""
	}
	commonGitDir = canonicalPath(commonGitDir)
	worktreeRoot := canonicalPath(repoDir)
	mainRepoRoot := filepath.Dir(commonGitDir)
	if !strings.HasPrefix(worktreeRoot, mainRepoRoot+string(filepath.Separator)) {
		return ""
	}
	if canonicalPath(filepath.Join(mainRepoRoot, ".git")) != commonGitDir {
		return ""
	}
	worktreeContext := loadContextFileFromDir(worktreeRoot)
	if worktreeContext == nil {
		return ""
	}
	return canonicalPath(filepath.Join(mainRepoRoot, filepath.Base(worktreeContext.Path)))
}

func findContextGitPaths(cwd string) (repoDir, commonGitDir string, ok bool) {
	for dir := cwd; ; dir = filepath.Dir(dir) {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			switch {
			case info.IsDir():
				if _, err := os.Stat(filepath.Join(gitPath, "HEAD")); err == nil {
					return dir, gitPath, true
				}
			case info.Mode().IsRegular():
				data, err := os.ReadFile(gitPath)
				gitDirText, found := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
				if err != nil || !found {
					return "", "", false
				}
				gitDir := gitDirText
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Join(dir, gitDir)
				}
				if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); err != nil {
					return "", "", false
				}
				commonDir := gitDir
				if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
					commonDir = filepath.Join(gitDir, strings.TrimSpace(string(data)))
				}
				return dir, commonDir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
	}
}

// canonicalPath resolves symlinks for worktree comparisons.
func canonicalPath(p string) string {
	if abs, err := filepath.EvalSymlinks(p); err == nil {
		return abs
	}
	return p
}
