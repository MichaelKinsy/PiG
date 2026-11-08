package plugins

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Ports packages/chord/test/next-node/plugins/support.ts (FIXTURES, KINDS, tempRoot, writeFiles, writePlugin, gitRepository). The host
// packages (`HOST_PACKAGES`, `kindsOf`) and `writePackage` serve the esbuild and `vm` tests, which this package replaces
// by the Builder and Evaluator seams (plugins_build.go, plugins_evaluate.go).

// fixtures is the fixture plugins' directory.
const fixtures = "testdata/plugins"

// kinds are the facet kinds of the fixtures' app.
var kinds = []string{"server", "worker", "web"}

// copied are the directories tempRoot filled, by test, that the test has written no entry to since: Windows reports each one's
// last-write time as a change of it at a moment no test can bound (see withoutDirectoryStamps), so a plugin of that name may be
// marked dirty though the test edited nothing in it.
var copied = struct {
	sync.Mutex
	dirs map[*testing.T]map[string]struct{}
}{dirs: map[*testing.T]map[string]struct{}{}}

func copiedDirs(t *testing.T) map[string]struct{} {
	if _, ok := copied.dirs[t]; !ok {
		copied.dirs[t] = map[string]struct{}{}
		t.Cleanup(func() {
			copied.Lock()
			defer copied.Unlock()
			delete(copied.dirs, t)
		})
	}
	return copied.dirs[t]
}

// withoutCopyStamps is marks without what Windows adds for the directories tempRoot filled and the test did not write to: the
// set and the dirty plugins of those directories (a plugin is named as its directory is, after the last "/").
func withoutCopyStamps(t *testing.T, marks PluginsState) PluginsState {
	copied.Lock()
	defer copied.Unlock()
	dirs := copied.dirs[t]
	if len(dirs) == 0 {
		return marks
	}
	marks.Set = false
	marks.Dirty = slices.DeleteFunc(slices.Clone(marks.Dirty), func(name string) bool {
		_, dir, ok := strings.Cut(name, "/")
		if !ok {
			dir = name
		}
		_, stamped := dirs[dir]
		return stamped
	})
	return marks
}

// tempRoot is a temporary directory removed after the test, with fixture plugins (names of directories of testdata) copied
// into it.
func tempRoot(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		if err := os.CopyFS(filepath.Join(root, name), os.DirFS(filepath.Join(fixtures, name))); err != nil {
			t.Fatal(err)
		}
		copied.Lock()
		copiedDirs(t)[name] = struct{}{}
		copied.Unlock()
	}
	return root
}

// writeFiles writes files (relative path to text) into dir, creating directories.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	copied.Lock()
	delete(copied.dirs[t], filepath.Base(dir))
	copied.Unlock()
	for path, text := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// writePlugin writes a plugin named name into root/dir: its package.json (with manifest's fields) and files.
func writePlugin(t *testing.T, root, dir, name string, files map[string]string, manifest map[string]any) string {
	t.Helper()
	path := filepath.Join(root, dir)
	fields := map[string]any{"name": name, "type": "module"}
	maps.Copy(fields, manifest)
	text, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{"package.json": string(text)}
	maps.Copy(all, files)
	writeFiles(t, path, all)
	return path
}

// realOf is the real path of path.
func realOf(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// eventually polls until check holds, failing after timeout: vitest's `vi.waitFor(..., { timeout })`.
func eventually(t *testing.T, timeout time.Duration, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settler is the barrier of a component's watcher: a directory outside its roots that the watcher watches too, where a new
// directory is made to learn that the events of everything written before it were handled.
type settler struct {
	dir   string
	count int
}

var settlers sync.Map

// settle returns once the component's watcher handled the events of every file operation made before the call: a watch event
// of an edit arrives on its own time, and one that arrives after a build that followed the first event of the edit would leave
// the plugin dirty. A write in place is two events, a removed directory many. The events of one file system arrive in order,
// and a directory made in a watched one is watched when its event was handled: the watcher's own state (`watched`, read in a
// section of its loop) says the barrier's directory was reached, so everything before it was. Nothing is waited for when the
// component does not watch (no first use yet, or a watcher that failed).
func settle(t *testing.T, plugins *Plugins) {
	t.Helper()
	found, _ := settlers.LoadOrStore(plugins, &settler{dir: t.TempDir()})
	barrier := found.(*settler)
	var watching bool
	var directories []string
	plugins.section(func() {
		fs := plugins.watcher.fs
		if fs == nil {
			return
		}
		watching = true
		// A discovery drops the trees that are no root's, base's or linked plugin's: the barrier's is put back each time.
		plugins.watcher.watchTree(barrier.dir)
		directories = slices.Collect(maps.Keys(plugins.watcher.watched))
	})
	if !watching {
		return
	}
	if runtime.GOOS == "windows" {
		settleDirectoryStamps(directories)
	}
	barrier.count++
	sentinel := filepath.Join(barrier.dir, "sentinel-"+strconv.Itoa(barrier.count))
	if err := os.Mkdir(sentinel, 0o755); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "the watcher to handle a directory made after the edits", func() bool {
		var reached bool
		plugins.section(func() { _, reached = plugins.watcher.watched[sentinel] })
		return reached
	})
}

// settleDirectoryStamps has Windows report, before the barrier's directory is made, the last-write time of the watched
// directories whose entries changed. Windows reports it as a write of the directory to the watch of its parent (see
// withoutDirectoryStamps), lazily, at a moment after the entry's own event that no barrier orders; opening and closing each
// watched directory makes the report come then, so a case reads marks that already hold it.
func settleDirectoryStamps(directories []string) {
	for _, directory := range directories {
		if file, err := os.Open(directory); err == nil {
			_ = file.Close()
		}
	}
}

// gitRepo is a local bare git repository and the work tree it was made from.
type gitRepo struct {
	url, commit, work, bare string
}

// gitRepository is a local bare git repository `<name>.git` in a temporary directory with files committed (one commit),
// tagged tag: its `file://` URL and the commit. A call with previous adds another commit on top of that repository. No
// network.
func gitRepository(t *testing.T, name string, files map[string]string, tag string, previous *gitRepo) gitRepo {
	t.Helper()
	git := func(cwd string, args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-c", "user.email=test@example.com", "-c", "user.name=test"}, args...)...)
		command.Dir = cwd
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	var work string
	if previous != nil {
		work = previous.work
	} else {
		work = tempRoot(t)
		git(work, "init", "--quiet")
	}
	writeFiles(t, work, files)
	git(work, "add", "--all")
	git(work, "commit", "--quiet", "--message", tag)
	git(work, "tag", tag)
	var bare string
	if previous != nil {
		bare = previous.bare
		git(work, "push", "--quiet", "--tags", bare, "HEAD:refs/heads/master")
	} else {
		bare = filepath.Join(tempRoot(t), name+".git")
		git(work, "clone", "--quiet", "--bare", work, bare)
	}
	return gitRepo{url: "file://" + bare, commit: git(work, "rev-parse", "HEAD"), work: work, bare: bare}
}
