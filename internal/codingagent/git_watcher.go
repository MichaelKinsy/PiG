package codingagent

// Ports packages/coding-agent/src/core/footer-data-provider.ts
// Ports packages/coding-agent/src/utils/fs-watch.ts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// upstream: packages/coding-agent/src/core/footer-data-provider.ts:WATCH_DEBOUNCE_MS
	gitWatchDebounce = 500 * time.Millisecond
	// upstream: packages/coding-agent/src/utils/fs-watch.ts:FS_WATCH_RETRY_DELAY_MS
	gitWatchRetry = 5000 * time.Millisecond
)

type gitWatchSource struct {
	events <-chan fsnotify.Event
	errors <-chan error
	close  func() error
}

type gitBranchWatcher struct {
	paths   gitPaths
	cached  string
	open    func(gitPaths) (*gitWatchSource, error)
	resolve func(context.Context, gitPaths) string
	publish func(string)
	source  *gitWatchSource
	started chan struct{}
}

func openGitWatch(paths gitPaths) (*gitWatchSource, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	names := []string{filepath.Dir(paths.headPath)}
	reftable := filepath.Join(paths.commonGitDir, "reftable")
	if _, err := os.Stat(reftable); err == nil {
		names = append(names, reftable)
		tables := filepath.Join(reftable, "tables.list")
		if _, err := os.Stat(tables); err == nil {
			names = append(names, tables)
		}
	}
	for _, name := range names {
		if err := watcher.Add(name); err != nil {
			// upstream: packages/coding-agent/src/utils/fs-watch.ts:closeWatcher
			_ = watcher.Close()
			return nil, err
		}
	}
	return &gitWatchSource{events: watcher.Events, errors: watcher.Errors, close: watcher.Close}, nil
}

func (w *gitBranchWatcher) closeWatch() {
	if w.source != nil {
		// upstream: packages/coding-agent/src/utils/fs-watch.ts:closeWatcher
		_ = w.source.close()
		w.source = nil
	}
}

// run owns filesystem watches, debounce/retry timers, and at most one joined branch resolution. No git process runs on the input/render loop.
func (w *gitBranchWatcher) run(ctx context.Context) {
	var refresh, retry *time.Timer
	var refreshC, retryC <-chan time.Time
	stop := func(timer **time.Timer) {
		if *timer != nil {
			(*timer).Stop()
			*timer = nil
		}
	}
	defer func() { stop(&refresh); stop(&retry); w.closeWatch() }()
	var tasks sync.WaitGroup
	defer tasks.Wait()
	result := make(chan string, 1)
	inFlight, pending := false, false
	schedule := func() {
		if refresh != nil {
			return
		}
		if inFlight {
			pending = true
			return
		}
		refresh = time.NewTimer(gitWatchDebounce)
		refreshC = refresh.C
	}
	var events <-chan fsnotify.Event
	var errors <-chan error
	tables := filepath.Join(w.paths.commonGitDir, "reftable", "tables.list")
	var tableTicker, headTicker *time.Ticker
	var tableC, headC <-chan time.Time
	var tableStamp, headStamp gitStatStamp
	clearPolls := func() {
		if tableTicker != nil {
			tableTicker.Stop()
			tableTicker = nil
		}
		if headTicker != nil {
			headTicker.Stop()
			headTicker = nil
		}
		tableC = nil
		headC = nil
	}
	defer clearPolls()
	setup := func() {
		source, err := w.open(w.paths)
		if err != nil {
			retry = time.NewTimer(gitWatchRetry)
			retryC = retry.C
			return
		}
		w.source = source
		events = source.events
		errors = source.errors
		tableStamp = gitPathStamp(tables)
		headStamp = gitPathStamp(w.paths.headPath)
		if _, err := os.Stat(tables); err == nil {
			// upstream: packages/coding-agent/src/core/footer-data-provider.ts:setupGitWatcher
			tableTicker = time.NewTicker(250 * time.Millisecond)
			tableC = tableTicker.C
		}
		if runtime.GOOS == "linux" && (os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "") && isWindowsMountedRepoPath(w.paths.repoDir) {
			// upstream: packages/coding-agent/src/core/footer-data-provider.ts:setupGitWatcher
			headTicker = time.NewTicker(1000 * time.Millisecond)
			headC = headTicker.C
		}
	}
	setup()
	if w.started != nil {
		close(w.started)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if filepath.Dir(event.Name) != filepath.Dir(w.paths.headPath) || filepath.Base(event.Name) == "HEAD" || event.Name == "" {
				schedule()
			}
		case _, ok := <-errors:
			if !ok {
				errors = nil
				continue
			}
			w.closeWatch()
			clearPolls()
			events = nil
			errors = nil
			if retry == nil {
				retry = time.NewTimer(gitWatchRetry)
				retryC = retry.C
			}
		case <-retryC:
			retry = nil
			retryC = nil
			setup()
		case <-refreshC:
			refresh = nil
			refreshC = nil
			inFlight = true
			tasks.Go(func() { result <- w.resolve(ctx, w.paths) })
		case branch := <-result:
			inFlight = false
			if ctx.Err() == nil && w.cached != branch {
				w.cached = branch
				w.publish(branch)
			}
			if pending {
				pending = false
				schedule()
			}
		case <-tableC:
			if next := gitPathStamp(tables); next != tableStamp {
				tableStamp = next
				schedule()
			}
		case <-headC:
			if next := gitPathStamp(w.paths.headPath); next != headStamp {
				headStamp = next
				schedule()
			}
		}
	}
}

type gitStatStamp struct {
	modified int64
	changed  string
	size     int64
}

func gitPathStamp(path string) gitStatStamp {
	info, err := os.Stat(path)
	if err != nil {
		return gitStatStamp{}
	}
	stamp := gitStatStamp{modified: info.ModTime().UnixNano(), size: info.Size()}
	// FileInfo exposes platform-specific change timestamps through Sys. Read only the change field, not access time (which branch resolution itself can update).
	stat := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if stat.IsValid() && stat.Kind() == reflect.Struct {
		for _, name := range []string{"Ctim", "Ctimespec", "Ctime"} {
			if field := stat.FieldByName(name); field.IsValid() && field.CanInterface() {
				stamp.changed = fmt.Sprint(field.Interface())
				break
			}
		}
	}
	return stamp
}

func isWindowsMountedRepoPath(path string) bool {
	return len(path) >= 6 && strings.EqualFold(path[:5], "/mnt/") && ((path[5] >= 'a' && path[5] <= 'z') || (path[5] >= 'A' && path[5] <= 'Z')) && (len(path) == 6 || path[6] == '/')
}

func newGitBranchWatcher(_ string, sl *FooterComponent) *gitBranchWatcher {
	bound := sl.boundGitPaths()
	if bound == nil {
		return nil
	}
	paths := *bound
	return &gitBranchWatcher{paths: paths, cached: sl.GetGitBranch(), open: openGitWatch, resolve: resolveGitBranchAsync, publish: func(branch string) {
		if sl.isDisposed() {
			return
		}
		sl.setGitBranch(branch)
		sl.Invalidate()
		sl.notifyBranchChange()
	}}
}

func resolveGitBranchAsync(ctx context.Context, paths gitPaths) string {
	data, err := os.ReadFile(paths.headPath)
	if err != nil {
		return ""
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "ref: refs/heads/")
	if !ok {
		return "detached"
	}
	if branch == ".invalid" {
		if resolved := resolveBranchWithGit(ctx, paths.repoDir); resolved != "" {
			return resolved
		}
		return "detached"
	}
	return branch
}

type gitPaths struct{ repoDir, commonGitDir, headPath string }

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
				if gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: "); ok {
					gitDir = resolveGitPath(dir, strings.TrimSpace(gitDir))
					head := filepath.Join(gitDir, "HEAD")
					if _, err := os.Stat(head); err != nil {
						return gitPaths{}, false
					}
					common := gitDir
					commonPath := filepath.Join(gitDir, "commondir")
					if _, err := os.Stat(commonPath); err == nil {
						data, err := os.ReadFile(commonPath)
						if err != nil {
							return gitPaths{}, false
						}
						common = resolveGitPath(gitDir, strings.TrimSpace(string(data)))
					}
					return gitPaths{repoDir: dir, commonGitDir: common, headPath: head}, true
				}
			} else if info.IsDir() {
				head := filepath.Join(gitPath, "HEAD")
				if _, err := os.Stat(head); err != nil {
					return gitPaths{}, false
				}
				return gitPaths{repoDir: dir, commonGitDir: gitPath, headPath: head}, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return gitPaths{}, false
		}
		dir = parent
	}
}

func resolveGitPath(base, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Join(base, target)
}

func (m *InteractiveMode) startGitBranchWatcher(ctx context.Context) {
	if m.backgroundCtx != nil {
		ctx = m.backgroundCtx
	}
	w := newGitBranchWatcher(m.opts.CWD, m.statusLine)
	if w == nil {
		return
	}
	ctx, stop := context.WithCancel(ctx)
	m.statusLine.setWatcherStop(stop)
	publish := w.publish
	w.publish = func(branch string) { _ = m.postToMain(ctx, func() { publish(branch); m.requestRender() }) }
	w.started = make(chan struct{})
	m.backgroundTasks.Go(func() { w.run(ctx) })
	select {
	case <-w.started:
	case <-ctx.Done():
	}
}
