//go:build linux

package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports the Linux-only case of packages/chord/test/next-node/plugins/watcher.test.ts (`it.skipIf(process.platform !==
// "linux")`): as a file named for its platform, it runs where upstream's does.

func TestTheWatcherFixRound1PutsNoWatchOnNodeModulesOrGitOpusN2200PackagesAddNoInotifyWatch(t *testing.T) {
	root := realOf(t, tempRoot(t))
	dir := writePlugin(t, root, "a", "@test/a", map[string]string{"worker.ts": ""}, nil)
	for i := range 200 {
		writeFiles(t, filepath.Join(dir, "node_modules", fmt.Sprintf("pkg%d", i), "lib"), map[string]string{"index.js": ""})
	}
	writeFiles(t, filepath.Join(dir, ".git", "objects"), map[string]string{"x": ""})
	before := inotifyWatches(t)
	watcher := newPluginWatcher(watcherOptions{Roots: []string{root}, Base: []string{}, Kinds: kinds, Failed: func(error) {}},
		state.NewBatch(func(state.BatchError) {}, nil))
	// The root and the plugin's directory.
	if got := inotifyWatches(t) - before; got != 2 {
		t.Fatalf("the watcher added %d inotify watches, want 2", got)
	}
	watcher.close()
	watcher.join()
}

// inotifyWatches is the inotify watches this process holds: one line per watch in its inotify descriptors' fdinfo.
func inotifyWatches(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fdinfo")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		text, err := os.ReadFile(filepath.Join("/proc/self/fdinfo", entry.Name()))
		if err != nil {
			// Closed meanwhile.
			continue
		}
		for line := range strings.SplitSeq(string(text), "\n") {
			if strings.HasPrefix(line, "inotify wd:") {
				count++
			}
		}
	}
	return count
}
