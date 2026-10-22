package aotruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	_ "github.com/MichaelKinsy/PiG/piglets/aot/internal/tlsdistrust" // hardens http.DefaultClient, the installer's download client (pig additive (D191))
)

// pollInterval is how often the worker looks for build requests.
const pollInterval = 2 * time.Second

// toolchainRetry is how long the worker waits after a failed download before it tries again.
const toolchainRetry = 10 * time.Minute

// maxLogs bounds the build logs kept.
const maxLogs = 20

// worker builds compiled hosts for the extension sets PiG records as requests. It runs while the extension's process runs: it starts at session
// start, builds one set at a time, and cancels the build when the session ends. A build never blocks PiG: the set runs on the runtime the manifest
// names as its fallback until the image exists, and the next start uses the image.
type worker struct {
	store    *store
	platform string
	// version is what the compiled host prints for --version.
	version string
	// notify reports a phase to the user; it is called only when verbose.
	notify func(message string)
	// installer downloads the compiler; tests replace its release URL and pins.
	installer installer
	// buildSet compiles a set; tests replace it.
	buildSet func(context.Context, buildOptions) (*buildResult, error)

	mu             sync.Mutex
	toolchainFails time.Time
	cancel         context.CancelFunc
	done           chan struct{}
}

func newWorker(root, platform string, notify func(string)) *worker {
	st := newStore(root, platform)
	base := releaseURL
	if override := os.Getenv(envReleaseURL); override != "" {
		base = strings.TrimSuffix(override, "/")
	}
	return &worker{
		store: st, platform: platform, version: "pig-aot-host pi-bolt-" + toolchainVersion, notify: notify, buildSet: build,
		installer: installer{providerDir: st.dir, platform: platform, baseURL: base, client: http.DefaultClient},
	}
}

// start begins the polling loop. It is idempotent.
func (w *worker) start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w.cancel, w.done = cancel, done
	go func() {
		defer close(done)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			w.tick(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// stop cancels the build in progress and waits for the loop to end.
func (w *worker) stop() {
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.cancel, w.done = nil, nil
	w.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (w *worker) say(format string, args ...any) {
	if verbose() && w.notify != nil {
		w.notify("aot runtime: " + fmt.Sprintf(format, args...))
	}
}

// tick handles every pending request once. A failure of the loop itself is not a failure of any build, so it is reported only when verbose.
func (w *worker) tick(ctx context.Context) {
	if !supportedPlatform(w.platform) {
		return
	}
	if err := w.store.ensureManifest(); err != nil {
		w.say("cannot write %s: %v", w.store.manifestPath(), err)
		return
	}
	requests, err := w.store.readRequests()
	if err != nil || len(requests) == 0 {
		return
	}
	for _, req := range requests {
		if ctx.Err() != nil {
			return
		}
		if !w.handle(ctx, req) {
			return
		}
	}
}

// handle builds one request. It reports false when the loop should stop for this tick: another process is building, or the toolchain is unavailable.
func (w *worker) handle(ctx context.Context, req Request) bool {
	st := w.store
	if st.haveCurrentImage(req.Key) {
		st.deleteRequest(req.Key)
		return true
	}
	inputs := memberInputs(req.Members)
	if memo, ok := st.knownFailure(req.Key, inputs); ok {
		w.say("set %s is not built: %s (%s)", short(req.Key), memo.Reason, memo.Detail)
		st.deleteRequest(req.Key)
		return true
	}
	lock, got, err := lockFile(filepath.Join(st.dir, "build.lock"), false)
	if err != nil || !got {
		// Another PiG is building; its set runs on the fallback runtime until an image exists.
		return false
	}
	defer lock.Unlock()
	compiler, err := w.ensureToolchain(ctx)
	if err != nil {
		w.say("the compiler is not available: %v", err)
		return false
	}
	// Only the process holding the build lock builds, so every directory under work/ is an interrupted build's.
	_ = os.RemoveAll(st.workDir())
	work := filepath.Join(st.workDir(), short(req.Key))
	if err := os.MkdirAll(work, 0o755); err != nil {
		w.say("cannot create %s: %v", work, err)
		return false
	}
	defer func() { _ = os.RemoveAll(st.workDir()) }()

	if err := os.MkdirAll(st.logsDir(), 0o755); err != nil {
		w.say("cannot create %s: %v", st.logsDir(), err)
		return false
	}
	logPath := filepath.Join(st.logsDir(), short(req.Key)+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		w.say("cannot create %s: %v", logPath, err)
		return false
	}
	defer func() { _ = logFile.Close() }()
	var log io.Writer = logFile

	w.say("compiling %d extension(s) of set %s; this runs in the background", len(req.Members), short(req.Key))
	started := time.Now()
	opts := buildOptions{
		Compiler: compiler, Platform: w.platform, CPU: cpuVariant(w.platform), Request: req, WorkDir: work, Version: w.version, Log: log,
	}
	built, err := w.buildSet(ctx, opts)
	if err != nil {
		if ctx.Err() != nil {
			// The session ended: the set builds at the next start.
			return false
		}
		w.failBuild(req, inputs, err, logPath)
		return true
	}
	previous, err := st.publish(req.Key, built)
	if err != nil {
		w.failBuild(req, inputs, err, logPath)
		return true
	}
	st.deleteRequest(req.Key)
	if previous != "" && !inUse(previous) {
		_ = os.RemoveAll(previous)
	}
	budget, err := st.cacheBudget()
	if err != nil {
		budget = defaultCacheBudget
	}
	if removed, err := st.prune(budget, req.Key); err == nil && len(removed) > 0 {
		w.say("removed %d least recently used image(s) to stay within %s", len(removed), formatSize(budget))
	}
	pruneLogs(st.logsDir(), maxLogs)
	w.say("set %s compiled in %s (%s, %s); the next start uses it", short(req.Key), time.Since(started).Round(100*time.Millisecond), built.Mode, formatSize(built.Bytes))
	return true
}

// failBuild records why a set did not compile, so the same inputs are not built again, and drops the request. The set keeps running on the fallback
// runtime.
func (w *worker) failBuild(req Request, inputs []inputFile, err error, logPath string) {
	reason := "build_failed"
	var sensitive errPathSensitive
	if errors.As(err, &sensitive) {
		reason = "path_sensitive"
	}
	if recordErr := w.store.recordFailure(req.Key, failure{Reason: reason, Detail: err.Error(), When: w.store.now(), Inputs: inputs, Log: logPath}); recordErr != nil {
		w.say("cannot record the failure of set %s: %v", short(req.Key), recordErr)
	}
	w.store.deleteRequest(req.Key)
	w.say("set %s is not compiled in (%s): %v; log %s", short(req.Key), reason, err, logPath)
}

// ensureToolchain returns the verified compiler, downloading it first when it is not installed. A download happens only on the user's machine, and
// never with PIG_OFFLINE set.
func (w *worker) ensureToolchain(ctx context.Context) (string, error) {
	if exe, err := verifyToolchain(ctx, w.store.dir, w.platform, archives); err == nil {
		return exe, nil
	}
	if offline() {
		return "", errors.New("the compiler is not installed and PIG_OFFLINE is set")
	}
	w.mu.Lock()
	failedRecently := !w.toolchainFails.IsZero() && time.Since(w.toolchainFails) < toolchainRetry
	w.mu.Unlock()
	if failedRecently {
		return "", errors.New("the last download failed; trying again later")
	}
	w.say("downloading the Pi-Bolt %s compiler; it is verified against the signature key and digests pinned in the Piglet", toolchainVersion)
	_, err := w.installer.install(ctx)
	if err != nil {
		w.mu.Lock()
		w.toolchainFails = time.Now()
		w.mu.Unlock()
		return "", err
	}
	return verifyToolchain(ctx, w.store.dir, w.platform, archives)
}

func short(key string) string {
	if len(key) > 16 {
		return key[:16]
	}
	return key
}

// pruneLogs keeps the newest n logs.
func pruneLogs(dir string, n int) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= n {
		return
	}
	type logFile struct {
		path string
		when time.Time
	}
	var logs []logFile
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && !entry.IsDir() {
			logs = append(logs, logFile{filepath.Join(dir, entry.Name()), info.ModTime()})
		}
	}
	slices.SortFunc(logs, func(a, b logFile) int { return b.when.Compare(a.when) })
	for _, old := range logs[min(n, len(logs)):] {
		_ = os.Remove(old.path)
	}
}

func formatSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/float64(1<<20))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
