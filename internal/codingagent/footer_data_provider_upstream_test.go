package codingagent

// pi: packages/coding-agent/src/utils/fs-watch.ts
// FS_WATCH_RETRY_DELAY_MS (5000) and watchWithErrorHandler: TestFooterRetriesGitWatchersFiveSecondsAfterAsyncWatchError.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestFooterGitCommandArguments(t *testing.T) {
	cmd := gitBranchCommand(t.Context(), "repo")
	if !slices.Equal(cmd.Args, []string{"git", "--no-optional-locks", "symbolic-ref", "--quiet", "--short", "HEAD"}) || cmd.Dir != "repo" || cmd.Stdin != nil || cmd.Stderr != nil {
		t.Fatalf("command=%+v", cmd)
	}
}

type footerWatchFixture struct {
	watcher        *gitBranchWatcher
	events         chan fsnotify.Event
	errors         chan error
	calls, notices atomic.Int64
	active         atomic.Pointer[gitWatchSource]
	cancel         context.CancelFunc
	done           chan struct{}
}

func footerFakeWatcher(t *testing.T) *footerWatchFixture {
	t.Helper()
	dir := t.TempDir()
	head := filepath.Join(dir, ".git", "HEAD")
	writeGitFixtureFile(t, head, "ref: refs/heads/.invalid\n")
	f := &footerWatchFixture{events: make(chan fsnotify.Event, 3), errors: make(chan error, 1), done: make(chan struct{})}
	f.watcher = &gitBranchWatcher{paths: gitPaths{repoDir: dir, commonGitDir: filepath.Dir(head), headPath: head}, cached: "main", open: func(gitPaths) (*gitWatchSource, error) {
		source := &gitWatchSource{events: f.events, errors: f.errors, close: func() error { f.active.Store(nil); return nil }}
		f.active.Store(source)
		return source, nil
	}, resolve: func(context.Context, gitPaths) string { f.calls.Add(1); return "main" }, publish: func(string) { f.notices.Add(1) }}
	ctx, cancel := context.WithCancel(t.Context())
	f.cancel = cancel
	go func() { defer close(f.done); f.watcher.run(ctx) }()
	synctest.Wait()
	return f
}
func (f *footerWatchFixture) close() { f.cancel(); <-f.done }
func (f *footerWatchFixture) change() {
	f.events <- fsnotify.Event{Name: filepath.Join(f.watcher.paths.commonGitDir, "reftable", "tables.list")}
}

// .upstream/v0.87.1/packages/coding-agent/test/footer-data-provider.test.ts:177
func TestFooterDoesNotNotifyWhenReftableUpdatesKeepSameBranch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := footerFakeWatcher(t)
		defer f.close()
		f.change()
		synctest.Wait()
		time.Sleep(501 * time.Millisecond)
		synctest.Wait()
		if f.calls.Load() != 1 || f.notices.Load() != 0 || f.watcher.cached != "main" {
			t.Fatalf("calls=%d notices=%d branch=%s", f.calls.Load(), f.notices.Load(), f.watcher.cached)
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/footer-data-provider.test.ts:202
func TestFooterDebouncesRapidReftableUpdatesIntoSingleAsyncRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := footerFakeWatcher(t)
		defer f.close()
		for range 3 {
			f.change()
		}
		synctest.Wait()
		time.Sleep(499 * time.Millisecond)
		synctest.Wait()
		if f.calls.Load() != 0 {
			t.Fatal("refresh before debounce")
		}
		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		if f.calls.Load() != 1 {
			t.Fatalf("calls=%d", f.calls.Load())
		}
		time.Sleep(650 * time.Millisecond)
		synctest.Wait()
		if f.calls.Load() != 1 {
			t.Fatalf("extra refreshes=%d", f.calls.Load())
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/footer-data-provider.test.ts:250
func TestFooterRetriesGitWatchersFiveSecondsAfterAsyncWatchError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := footerFakeWatcher(t)
		defer f.close()
		original := f.active.Load()
		if original == nil {
			t.Fatal("no head watcher")
		}
		f.errors <- errors.New("simulated EMFILE")
		synctest.Wait()
		if f.active.Load() != nil {
			t.Fatal("watch not closed on error")
		}
		time.Sleep(4999 * time.Millisecond)
		synctest.Wait()
		if f.active.Load() != nil {
			t.Fatal("retry too early")
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if active := f.active.Load(); active == nil || active == original {
			t.Fatal("watcher not replaced")
		}
	})
}

// Events during resolution cause one subsequent debounce; cancellation joins the resolver and does not publish a late result.
func TestFooterRefreshPendingAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := footerFakeWatcher(t)
		release := make(chan struct{})
		f.watcher.resolve = func(ctx context.Context, _ gitPaths) string {
			f.calls.Add(1)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return "foo"
		}
		f.change()
		synctest.Wait()
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		f.change()
		synctest.Wait()
		close(release)
		synctest.Wait()
		if f.calls.Load() != 1 || f.notices.Load() != 1 {
			t.Fatalf("first calls=%d notices=%d", f.calls.Load(), f.notices.Load())
		}
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		if f.calls.Load() != 2 || f.notices.Load() != 1 {
			t.Fatalf("pending calls=%d notices=%d", f.calls.Load(), f.notices.Load())
		}
		f.watcher.resolve = func(ctx context.Context, _ gitPaths) string { <-ctx.Done(); return "late" }
		f.change()
		synctest.Wait()
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		f.close()
		if f.notices.Load() != 1 {
			t.Fatal("late result published")
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/footer-data-provider.test.ts:227
func TestFooterUpdatesCachedBranchWhenReftableDirectoryChanges(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, "repo", ".git", "worktrees", "src")
	worktree := filepath.Join(dir, "worktree")
	table := filepath.Join(dir, "repo", ".git", "reftable", "tables.list")
	writeGitFixtureFile(t, filepath.Join(worktree, ".git"), "gitdir: "+gitDir+"\n")
	writeGitFixtureFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/.invalid\n")
	writeGitFixtureFile(t, filepath.Join(gitDir, "commondir"), "../..\n")
	writeGitFixtureFile(t, table, "0\n")
	var changed atomic.Bool
	var calls atomic.Int64
	previous := resolveBranchWithGit
	resolveBranchWithGit = func(context.Context, string) string {
		calls.Add(1)
		if changed.Load() {
			return "foo"
		}
		return "main"
	}
	t.Cleanup(func() { resolveBranchWithGit = previous })
	sl := NewFooterComponent(nil, "", nil)
	sl.SetCwd(worktree)
	calls.Store(0)
	notifications := make(chan struct{}, 1)
	unsubscribe := sl.OnBranchChange(func() { notifications <- struct{}{} })
	defer unsubscribe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	w := newGitBranchWatcher(worktree, sl)
	ready := make(chan struct{})
	w.open = func(paths gitPaths) (*gitWatchSource, error) {
		source, err := openGitWatch(paths)
		close(ready)
		return source, err
	}
	go func() { defer close(done); w.run(ctx) }()
	defer func() { cancel(); <-done }()
	<-ready
	changed.Store(true)
	writeGitFixtureFile(t, table, "1\n")
	select {
	case <-notifications:
	// Real fs watcher events can arrive late under full-suite load; Pi 1.1.0 allows 10 s
	// (footer-data-provider.test.ts "updates cached branch when the reftable directory changes", e9163107).
	case <-time.After(10 * time.Second):
		t.Fatal("reftable change never reached cached branch")
	}
	if got := sl.GitBranch(); got != "foo" {
		t.Fatalf("branch=%q", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("async resolutions=%d, want one", calls.Load())
	}
}

func BenchmarkFooterWatchLifetime(b *testing.B) {
	dir := b.TempDir()
	writeGitFixtureFileForBenchmark := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	writeGitFixtureFileForBenchmark(filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
	sl := NewFooterComponent(nil, "", nil)
	sl.SetCwd(dir)
	b.ReportAllocs()
	for b.Loop() {
		w := newGitBranchWatcher(dir, sl)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		w.run(ctx)
	}
}
