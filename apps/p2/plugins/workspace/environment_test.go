// Ports test/p2/workspace-environment.test.ts (Pi main c23b9ec1).
package workspace_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace"
	"github.com/MichaelKinsy/PiG/chord/delta"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	nodeenv "github.com/MichaelKinsy/PiG/durable/env/node"
)

func TestMain(m *testing.M) { p2test.Main(m) }

var ctx = p2test.Background

func realTemp(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	command.Dir = cwd
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func nodeEnv(cwd string) durableenv.ExecutionEnv {
	return nodeenv.NewNodeExecutionEnv(nodeenv.NodeExecutionEnvOptions{Cwd: cwd})
}

// execEnv is an environment whose Exec a test intercepts.
type execEnv struct {
	durableenv.ExecutionEnv
	exec func(ctx context.Context, command any, options *durableenv.ShellExecOptions) (durableenv.ShellExecResult, error)
}

func (e *execEnv) Exec(ctx context.Context, command any, options *durableenv.ShellExecOptions) (durableenv.ShellExecResult, error) {
	return e.exec(ctx, command, options)
}

func query(t *testing.T, w *workspace.Workspace, pairs ...any) *delta.JsonObject {
	t.Helper()
	result, err := w.Query(ctx, delta.JsonObjectOf(pairs...))
	if err != nil {
		t.Fatalf("query %v: %v", pairs, err)
	}
	return result
}

func list(value any) []any { items, _ := value.([]any); return items }

func TestSearchWithoutGitGrepMKeepsPerFileLimitsAndPropagatesOtherErrors(t *testing.T) {
	p2test.CheckLeaks(t, 0)
	directory := realTemp(t)
	write(t, filepath.Join(directory, "many.txt"), strings.Repeat("needle\n", 25))
	write(t, filepath.Join(directory, "other.txt"), "needle\n")
	for _, repo := range []bool{false, true} {
		if repo {
			git(t, directory, "init", "-q")
		}
		inner := nodeEnv(directory)
		var rejected atomic.Int32
		var fail atomic.Bool
		env := &execEnv{ExecutionEnv: inner}
		env.exec = func(ctx context.Context, command any, options *durableenv.ShellExecOptions) (durableenv.ShellExecResult, error) {
			if argv, ok := command.([]string); ok && slices.Contains(argv, "grep") && (fail.Load() || slices.Contains(argv, "-m")) {
				rejected.Add(1)
				text := "error: unknown switch `m'\n"
				if fail.Load() {
					text = "fatal: fixture failure\n"
				}
				options.OnOutput(ctx, text, durableenv.ShellOutputInfo{Stream: durableenv.ShellStderr})
				return durableenv.ShellExecResult{ExitCode: 129}, nil
			}
			return inner.Exec(ctx, command, options)
		}
		w := workspace.NewWorkspace(env, nil)
		result := query(t, w, "kind", "search", "path", "", "text", "needle")
		if rejected.Load() != 1 {
			t.Fatalf("repo=%v: rejected = %d, want 1", repo, rejected.Load())
		}
		count := map[string]int{}
		for _, match := range list(result.Value("matches")) {
			count[match.(*delta.JsonObject).Value("path").(string)]++
		}
		if count["many.txt"] != 20 || count["other.txt"] != 1 || result.Value("truncated") != false {
			t.Fatalf("repo=%v: matches %v truncated %v", repo, count, result.Value("truncated"))
		}
		fail.Store(true)
		if _, err := w.Query(ctx, delta.JsonObjectOf("kind", "search", "path", "", "text", "needle")); err == nil || !strings.Contains(err.Error(), "fixture failure") {
			t.Fatalf("repo=%v: error = %v", repo, err)
		}
		if rejected.Load() != 2 {
			t.Fatalf("repo=%v: unrelated failures are retried (%d)", repo, rejected.Load())
		}
	}
}

// waitFor polls check for up to ten seconds.
func waitFor(check func() bool) bool {
	for i := 0; i < 100 && !check(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	return check()
}

// Pi Mobile 24e3fccdd: a subagent's own cwd may be a linked worktree, whose index and HEAD live in the main repository.
func TestLinkedGitWorktreeIndexAndHEADAreWatched(t *testing.T) {
	p2test.CheckLeaks(t, 0)
	directory := realTemp(t)
	root, linked := filepath.Join(directory, "repo"), filepath.Join(directory, "linked")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q")
	write(t, filepath.Join(root, "a.txt"), "a\n")
	git(t, root, "add", "a.txt")
	git(t, root, "commit", "-q", "-m", "init")
	git(t, root, "worktree", "add", "-q", "-b", "side", linked)
	w := workspace.NewWorkspace(nodeEnv(linked), nil)
	write(t, filepath.Join(linked, "new.txt"), "x\n")
	var changes atomic.Int32
	watcher, err := w.Watch(ctx, func() { changes.Add(1) })
	if err != nil || watcher == nil {
		t.Fatalf("watch = %v, %v", watcher, err)
	}
	defer func() { _ = watcher.Stop(ctx) }()
	// Staging changes only the worktree's index, in the main repository's git directory.
	git(t, linked, "add", "new.txt")
	if !waitFor(func() bool { return changes.Load() > 0 }) {
		t.Fatal("index change not reported")
	}
}

func TestWatchLeavesOutAPrivateDirectoryAndALargeTreeWatchesItsRootOnly(t *testing.T) {
	p2test.CheckLeaks(t, 0)
	directory := realTemp(t)
	root := filepath.Join(directory, "small")
	for _, sub := range []string{"a/b", "home/private", "home/other"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w := workspace.NewWorkspace(nodeEnv(root), []string{filepath.Join(root, "home/private")})
	var changes atomic.Int32
	watcher, err := w.Watch(ctx, func() { changes.Add(1) })
	if err != nil || watcher == nil {
		t.Fatalf("watch = %v, %v", watcher, err)
	}
	write(t, filepath.Join(root, "home/private/log"), "x\n")
	time.Sleep(time.Second)
	if changes.Load() != 0 {
		t.Fatal("a change in the private directory is reported")
	}
	write(t, filepath.Join(root, "home/other/file"), "x\n")
	if !waitFor(func() bool { return changes.Load() > 0 }) {
		t.Fatal("a sibling of the private directory is not watched")
	}
	changes.Store(0)
	write(t, filepath.Join(root, "a/b/file"), "x\n")
	if !waitFor(func() bool { return changes.Load() > 0 }) {
		t.Fatal("other folders are not watched recursively")
	}
	_ = watcher.Stop(ctx)

	large := filepath.Join(directory, "large")
	for index := 0; index <= 2000; index++ {
		if err := os.MkdirAll(filepath.Join(large, "d"+itoa(index)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	big := workspace.NewWorkspace(nodeEnv(large), nil)
	var bigChanges atomic.Int32
	bigWatcher, err := big.Watch(ctx, func() { bigChanges.Add(1) })
	if err != nil || bigWatcher == nil {
		t.Fatalf("watch = %v, %v", bigWatcher, err)
	}
	defer func() { _ = bigWatcher.Stop(ctx) }()
	write(t, filepath.Join(large, "d5/file"), "x\n")
	time.Sleep(time.Second)
	if bigChanges.Load() != 0 {
		t.Fatal("below the root of a large tree something is watched")
	}
	write(t, filepath.Join(large, "top.txt"), "x\n")
	if !waitFor(func() bool { return bigChanges.Load() > 0 }) {
		t.Fatal("the root's entries are not watched")
	}
}

func itoa(value int) string { return strconv.Itoa(value) }

func TestARootInsideARepositoryListsChangesAndCommitsRelativeToItself(t *testing.T) {
	p2test.CheckLeaks(t, 0)
	directory := realTemp(t)
	write(t, filepath.Join(directory, "apps/web/a.txt"), "before\n")
	write(t, filepath.Join(directory, "outside.txt"), "before\n")
	git(t, directory, "init", "-q")
	git(t, directory, "add", ".")
	git(t, directory, "commit", "-q", "-m", "init")
	write(t, filepath.Join(directory, "apps/web/a.txt"), "after\n")
	write(t, filepath.Join(directory, "apps/web/new.txt"), "new\n")
	write(t, filepath.Join(directory, "outside.txt"), "after\n")
	w := workspace.NewWorkspace(nodeEnv(filepath.Join(directory, "apps/web")), nil)
	status := query(t, w, "kind", "status")
	var paths []string
	for _, change := range list(status.Value("changes")) {
		paths = append(paths, change.(*delta.JsonObject).Value("path").(string))
	}
	slices.Sort(paths)
	if status.Value("repo") != true || !slices.Equal(paths, []string{"a.txt", "new.txt"}) {
		t.Fatalf("status = %v", paths)
	}
	if diff := query(t, w, "kind", "diff", "path", "a.txt"); !strings.Contains(diff.Value("patch").(string), "+after") {
		t.Fatalf("diff = %v", diff.Value("patch"))
	}
	if file := query(t, w, "kind", "file", "path", "a.txt"); file.Value("text") != "after\n" {
		t.Fatalf("file = %v", file.Value("text"))
	}
	hash := strings.TrimSpace(git(t, directory, "rev-parse", "HEAD"))
	commit := query(t, w, "kind", "commit", "commit", hash)
	changes := list(commit.Value("changes"))
	if len(changes) != 1 || changes[0].(*delta.JsonObject).Value("path") != "a.txt" {
		t.Fatalf("commit changes = %v", changes)
	}
	if old := query(t, w, "kind", "file", "path", "a.txt", "commit", hash); old.Value("text") != "before\n" {
		t.Fatalf("file at commit = %v", old.Value("text"))
	}
	if diff := query(t, w, "kind", "diff", "path", "a.txt", "commit", hash); !strings.Contains(diff.Value("patch").(string), "+before") {
		t.Fatalf("diff at commit = %v", diff.Value("patch"))
	}
}
