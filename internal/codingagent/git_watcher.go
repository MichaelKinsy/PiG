package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// watchGitBranch polls the git HEAD every 5s and updates the StatusLine
// when the branch changes. Mirrors upstream footer-data-provider.ts
// setupGitWatcher (fs.watch on HEAD + refs). We use polling because
// Go's fsnotify adds a dependency and fs.watch is flaky on macOS for
// nested paths.
//
// Runs until ctx is cancelled. Intended to be launched as a goroutine.
func watchGitBranch(ctx context.Context, cwd string, sl *StatusLine) {
	paths, ok := findGitPaths(cwd)
	if !ok {
		return // not in a git repo
	}
	headPath := paths.headPath

	var lastMod time.Time
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(headPath)
			if err != nil {
				continue
			}
			if info.ModTime() != lastMod {
				lastMod = info.ModTime()
				branch := resolveGitBranch(cwd)
				sl.mu.Lock()
				old := sl.gitBranch
				sl.gitBranch = branch
				sl.mu.Unlock()
				if old != branch {
					sl.Invalidate()
					sl.notifyBranchChange()
				}
			}
		}
	}
}

// gitPaths are the repository metadata locations for a working directory.
type gitPaths struct {
	repoDir  string
	headPath string
}

// findGitPaths walks up from cwd to the nearest .git entry. It handles a
// regular repository (.git directory) and a worktree (.git file naming its
// gitdir). Mirrors upstream footer-data-provider.ts findGitPaths without the
// common git directory, which only upstream's reftable watcher consumes.
func findGitPaths(cwd string) (gitPaths, bool) {
	dir := cwd
	for {
		gitPath := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitPath); err == nil {
			if info.Mode().IsRegular() {
				data, err := os.ReadFile(gitPath)
				if err != nil {
					return gitPaths{}, false
				}
				// A .git file that names no gitdir is not a worktree link;
				// upstream keeps walking to the parent.
				if gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: "); ok {
					gitDir = resolveGitPath(dir, strings.TrimSpace(gitDir))
					headPath := filepath.Join(gitDir, "HEAD")
					if _, err := os.Stat(headPath); err != nil {
						return gitPaths{}, false
					}
					return gitPaths{repoDir: dir, headPath: headPath}, true
				}
			} else if info.IsDir() {
				headPath := filepath.Join(gitPath, "HEAD")
				if _, err := os.Stat(headPath); err != nil {
					return gitPaths{}, false
				}
				return gitPaths{repoDir: dir, headPath: headPath}, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return gitPaths{}, false
		}
		dir = parent
	}
}

// resolveGitPath mirrors Node path.resolve(base, target) for git metadata paths.
func resolveGitPath(base, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Join(base, target)
}

// startGitBranchWatcher keeps the footer branch current when the user switches
// branches while pig runs. Mirrors upstream footer-data-provider.ts
// setupGitWatcher. Like upstream's onBranchChange subscription in interactive
// mode, a branch change requests a render so an idle footer repaints.
func (m *InteractiveMode) startGitBranchWatcher(ctx context.Context) {
	unsubscribe := m.statusLine.OnBranchChange(m.requestRender)
	go func() {
		defer unsubscribe()
		watchGitBranch(ctx, m.opts.CWD, m.statusLine)
	}()
}
