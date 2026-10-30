// Ports packages/coding-agent/src/core/resource-loader.ts
package codingagent

import (
	"cmp"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodefs"
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
// worktree root supplies its own copy. Nonempty input paths resolve against the process working directory before discovery.
func LoadProjectContextFiles(cwd, agentDir string) []ContextFile {
	loaded, _ := discoverContextFiles(cwd, agentDir, true)
	return loaded
}

// LoadProjectContextFilesScoped is LoadProjectContextFiles for a run that may withhold the project's context files.
// With loadProject false it loads only the context file in the agent directory and the one in the user's home directory, when that directory is a strict ancestor of cwd. Every other file the walk finds is project context and is skipped without a read.
// pig additive (D211).
func LoadProjectContextFilesScoped(cwd, agentDir string, loadProject bool) []ContextFile {
	loaded, _ := discoverContextFiles(cwd, agentDir, loadProject)
	return loaded
}

// WithheldProjectContextFiles lists the paths of the project context files that LoadProjectContextFilesScoped skips with loadProject false, root-first.
// pig additive (D211).
func WithheldProjectContextFiles(cwd, agentDir string) []string {
	_, withheld := discoverContextFiles(cwd, agentDir, false)
	return withheld
}

// homeDirs is the user's home directory in the two spellings a walk can meet: as the environment gives it, and with symlinks resolved.
type homeDirs struct{ raw, canonical string }

// userHomeDir is the user's home directory as trust-manager.ts:187 resolves it: HOME first, then the user database.
func userHomeDir() homeDirs {
	//perf:ok reached only when a run withholds the project's context files, once per walk
	home := cmp.Or(os.Getenv("HOME"), osHomedir())
	if home == "" {
		return homeDirs{}
	}
	return homeDirs{raw: filepath.Clean(home), canonical: canonicalPath(home)}
}

// isAncestorHome reports whether dir is the home directory and not the working directory itself. The home directory's file is the user's own, not the project's.
func (h homeDirs) isAncestorHome(dir, cwd string) bool {
	return h.raw != "" && dir != cwd && (dir == h.raw || dir == h.canonical)
}

// HasProjectContextFile reports whether the walk from cwd to the filesystem root finds a project context file: one that LoadProjectContextFilesScoped skips with loadProject false. It reads nothing.
// pig additive (D211).
func HasProjectContextFile(cwd string) bool {
	home := userHomeDir()
	var probe nodefs.PathProbe
	dir, clean := cwd, false
	for {
		if !home.isAncestorHome(dir, cwd) && firstContextFilePathProbed(&probe, dir) != "" {
			return true
		}
		parent := nodefs.ParentDir(dir, clean)
		if parent == dir {
			return false
		}
		dir, clean = parent, true
	}
}

// discoverContextFiles is the walk behind LoadProjectContextFiles. withheld holds the paths it skipped when loadProject is false; their content is never read.
func discoverContextFiles(cwd, agentDir string, loadProject bool) (loaded []ContextFile, withheld []string) {
	if cwd != "" {
		if absolute, err := ResolvePath(cwd, ""); err == nil {
			cwd = absolute
		}
	}
	if agentDir != "" {
		if absolute, err := ResolvePath(agentDir, ""); err == nil {
			agentDir = absolute
		}
	}
	seen := make(map[string]struct{})

	// 1. Global context from agent config dir.
	if agentDir != "" {
		if cf := loadContextFileFromDir(agentDir); cf != nil {
			if _, dup := seen[cf.Path]; !dup {
				seen[cf.Path] = struct{}{}
				loaded = append(loaded, *cf)
			}
		}
	}

	// 2. Walk from cwd upward to root, collecting files in reverse
	// (root-first) order so the nearest file appears last: matching
	// upstream's ancestorContextFiles.unshift() pattern.
	if cwd != "" {
		var ancestors []ContextFile
		var skipped []string
		shadowed := findShadowedContextFile(cwd)
		var home homeDirs
		if !loadProject {
			home = userHomeDir()
		}
		dir := cwd
		var probe nodefs.PathProbe
		clean := false
		for {
			// A run that withholds the project keeps the home directory's file, unless the project is the home directory itself.
			if !loadProject && !home.isAncestorHome(dir, cwd) {
				if path := firstContextFilePathProbed(&probe, dir); path != "" {
					isShadowed := shadowed != "" && filepath.Join(canonicalPath(dir), filepath.Base(path)) == shadowed
					if _, dup := seen[path]; !dup && !isShadowed {
						seen[path] = struct{}{}
						skipped = append([]string{path}, skipped...)
					}
				}
			} else if cf := loadContextFileProbed(&probe, dir); cf != nil {
				// upstream: packages/coding-agent/src/core/resource-loader.ts:loadProjectContextFiles compares the directory's real path plus the file name, so a symlinked context file never equals the shadowed path.
				isShadowed := shadowed != "" && filepath.Join(canonicalPath(dir), filepath.Base(cf.Path)) == shadowed
				if _, dup := seen[cf.Path]; !dup && !isShadowed {
					seen[cf.Path] = struct{}{}
					ancestors = append([]ContextFile{*cf}, ancestors...)
				}
			}
			parent := nodefs.ParentDir(dir, clean)
			if parent == dir {
				break // reached root
			}
			dir, clean = parent, true
		}
		loaded = append(loaded, ancestors...)
		withheld = skipped
	}

	return loaded, withheld
}

// firstContextFilePathProbed returns the path of the first context candidate in dir that is a regular file, or "". It reads nothing.
// pig additive (D211).
func firstContextFilePathProbed(probe *nodefs.PathProbe, dir string) string {
	for _, name := range contextCandidates {
		if !probe.Set(dir, name).Exists() {
			continue
		}
		p := filepath.Join(dir, name)
		//perf:ok reached only for a directory that has a candidate, as in loadContextFileProbed
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// loadContextFileFromDir checks context candidates in priority order.
// Returns the first match, or nil if none found.
// Mirrors upstream resource-loader.ts:184-202 loadContextFileFromDir.
func loadContextFileFromDir(dir string) *ContextFile {
	var probe nodefs.PathProbe
	return loadContextFileProbed(&probe, dir)
}

// loadContextFileProbed is loadContextFileFromDir with the probe an ancestor walk reuses at every level.
func loadContextFileProbed(probe *nodefs.PathProbe, dir string) *ContextFile {
	for _, name := range contextCandidates {
		// existsSync, then statSync, as Pi does: a missing candidate, the case in nearly every ancestor, costs one access(2)
		// and neither a path string nor a FileInfo.
		if !probe.Set(dir, name).Exists() {
			continue
		}
		p := filepath.Join(dir, name)
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			reportCouldNotRead(p, err)
			continue
		}
		return &ContextFile{Path: p, Content: text.StripBom(string(data))}
	}
	return nil
}

func findShadowedContextFile(cwd string) string {
	paths, ok := findGitPaths(cwd)
	if !ok {
		return ""
	}
	commonGitDir := canonicalPath(paths.commonGitDir)
	worktreeRoot := canonicalPath(paths.repoDir)
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
	return filepath.Join(mainRepoRoot, filepath.Base(worktreeContext.Path))
}

// canonicalPath resolves symlinks for worktree comparisons.
func canonicalPath(p string) string {
	return CanonicalizePath(p)
}
