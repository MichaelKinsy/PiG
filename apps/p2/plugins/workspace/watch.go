package workspace

import (
	"context"
	"slices"
	"strings"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// watcher is a running watch that counts itself in the workspace until stopped or failed.
type watcher struct {
	durableenv.FileWatcher
	stop func()
}

// Stop stops the watch.
func (w watcher) Stop(ctx context.Context) error {
	w.stop()
	return w.FileWatcher.Stop(ctx)
}

// Watch watches the working tree (without .git internals and node_modules) plus the repository's HEAD and index, which
// commits and staging change. A linked worktree (or a directory below the repository root) has them in a git directory
// elsewhere, and its config in the main repository's. It returns nil when the environment cannot watch.
func (w *Workspace) Watch(ctx context.Context, onChange func()) (durableenv.FileWatcher, error) {
	root, err := w.canonicalRoot(ctx)
	if err != nil {
		return nil, err
	}
	var gitDir, commonDir string
	if located, err := w.capture(ctx, []string{"git", "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir"}, 8192, ""); err == nil && located.exitCode == 0 {
		lines := strings.Split(located.stdout, "\n")
		gitDir = strings.TrimSpace(lines[0])
		if len(lines) > 1 {
			commonDir = strings.TrimSpace(lines[1])
		}
	}
	base := root
	if base == "/" {
		base = ""
	}
	git := gitDir
	if git == "" {
		git = base + "/.git"
	}
	config := commonDir
	if config == "" {
		config = git
	}
	config += "/config"
	// Counted in `watchers` from start until closed or failed: a failed watcher reports nothing more.
	var open bool
	stop := func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if !open {
			return
		}
		open = false
		w.watchers--
		w.filters = nil
	}
	tree, err := w.watchTargets(ctx, root)
	if err != nil {
		return nil, err
	}
	result, err := w.Env.Watch(ctx, append(tree, durableenv.WatchTarget{Path: git + "/HEAD"}, durableenv.WatchTarget{Path: git + "/index"}, durableenv.WatchTarget{Path: config}),
		func(change durableenv.FileChange) {
			// Any change may be configuration: .git/config, or a file it includes from the working tree.
			w.mu.Lock()
			w.filters = nil
			w.mu.Unlock()
			if _, failed := change.(durableenv.FileChangeFailed); failed {
				stop()
			} else {
				onChange()
			}
		})
	if err != nil {
		return nil, nil
	}
	w.mu.Lock()
	open = true
	w.watchers++
	w.mu.Unlock()
	return watcher{FileWatcher: result, stop: stop}, nil
}

// watchTargets are the working tree's watch targets, without private directories inside it (p2's data directory below a
// home folder changes all the time): the root recursively; around a private directory, each directory on the way to it by
// its own entries and its other folders recursively. A tree over `watchDirectories` folders gets its root's entries only.
// Counted with a walk first (stopping at the bound, and when the watch is abandoned), as the watcher would scan it.
func (w *Workspace) watchTargets(ctx context.Context, root string) ([]durableenv.WatchTarget, error) {
	join := func(directory, name string) string {
		if directory == "/" {
			directory = ""
		}
		return directory + "/" + name
	}
	within := func(directory, path string) bool { return inside(directory, path) }
	var hidden []string
	for _, denied := range w.denied {
		if denied != root && within(root, denied) {
			hidden = append(hidden, denied)
		}
	}
	holdsPrivate := func(path string) bool {
		return slices.ContainsFunc(hidden, func(denied string) bool { return within(path, denied) })
	}
	// folders are the folders of the directories on the way to a private one.
	folders := map[string][]string{}
	queue := []string{root}
	counted := 0
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if err := ctx.Err(); err != nil {
			return nil, context.Cause(ctx)
		}
		listed, err := w.Env.ListDir(ctx, next)
		if err != nil {
			continue
		}
		var names []string
		for _, entry := range listed {
			if entry.Kind != durableenv.FileKindDirectory || slices.Contains(watchExcluded, entry.Name) {
				continue
			}
			path := join(next, entry.Name)
			if slices.ContainsFunc(hidden, func(denied string) bool { return within(denied, path) }) {
				continue
			}
			if counted++; counted > watchDirectories {
				return []durableenv.WatchTarget{{Path: root}}, nil
			}
			names = append(names, entry.Name)
			queue = append(queue, path)
		}
		if holdsPrivate(next) {
			folders[next] = names
		}
	}
	var targets []durableenv.WatchTarget
	var cover func(directory string)
	cover = func(directory string) {
		if !holdsPrivate(directory) {
			targets = append(targets, durableenv.WatchTarget{Path: directory, Recursive: true, Exclude: &durableenv.WatchExclude{Names: watchExcluded}})
			return
		}
		targets = append(targets, durableenv.WatchTarget{Path: directory})
		for _, name := range folders[directory] {
			cover(join(directory, name))
		}
	}
	cover(root)
	return targets, nil
}
