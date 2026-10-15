package node

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Pi packages/durable/src/env/node-watch.ts:164 takes `options.maxDirectories ?? DEFAULT_MAX_DIRECTORIES`, so only an unset
// limit is the default: a set 0 is the limit, and node-watch.ts:329-330 refuses the first directory with BudgetExceeded.
func TestNodeWatchMaxDirectoriesZeroRefusesEveryDirectory(t *testing.T) {
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir(), Watch: NodeWatchOptions{MaxDirectories: new(0)}})
	mustDo(t, env.CreateDir(background, "tree", nil))
	_, err := env.Watch(background, []durableenv.WatchTarget{{Path: "tree", Recursive: true}}, func(durableenv.WatchChange) {})
	fileErr, ok := errors.AsType[*durableenv.FileError](err)
	if !ok || fileErr.Code != durableenv.FileErrorInvalid || !strings.Contains(fileErr.Message, "Watched paths exceed 0 directories") {
		t.Fatalf("Watch error = %v, want the invalid budget error for 0 directories", err)
	}
	unset := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir()})
	mustDo(t, unset.CreateDir(background, "tree", nil))
	watcher, err := unset.Watch(background, []durableenv.WatchTarget{{Path: "tree", Recursive: true}}, func(durableenv.WatchChange) {})
	if err != nil {
		t.Fatalf("an unset limit is the default 10,000 directories: %v", err)
	}
	_ = watcher.Close(background)
}

// Pi node-watch.ts:163 takes `options.pollIntervalMs ?? DEFAULT_POLL_MS` and passes it to setTimeout (:230-233). Node runs a
// timer whose delay is below 1 or above 2^31-1 after 1 ms (lib/internal/timers.js Timeout), so a set 0 polls every 1 ms.
func TestNodeTimerDelayFollowsSetTimeout(t *testing.T) {
	for _, c := range []struct {
		ms   int
		want time.Duration
	}{
		{0, time.Millisecond}, {-5, time.Millisecond}, {1, time.Millisecond}, {2000, 2 * time.Second},
		{math.MaxInt32, math.MaxInt32 * time.Millisecond}, {math.MaxInt32 + 1, time.Millisecond},
	} {
		if got := nodeTimerDelay(c.ms); got != c.want {
			t.Errorf("nodeTimerDelay(%d) = %v, want %v", c.ms, got, c.want)
		}
	}
	for _, c := range []struct {
		options NodeWatchOptions
		want    time.Duration
	}{
		{NodeWatchOptions{Mode: durableenv.WatchPolling}, 2 * time.Second},
		{NodeWatchOptions{Mode: durableenv.WatchPolling, PollIntervalMs: new(0)}, time.Millisecond},
		{NodeWatchOptions{Mode: durableenv.WatchPolling, PollIntervalMs: new(250)}, 250 * time.Millisecond},
	} {
		watcher, err := openFileWatcher([]durableenv.WatchTarget{{Path: t.TempDir()}}, func(path string) string { return path }, func(durableenv.WatchChange) {}, c.options)
		if err != nil {
			t.Fatal(err)
		}
		if watcher.pollInterval != c.want {
			t.Errorf("poll interval for %+v = %v, want %v", c.options, watcher.pollInterval, c.want)
		}
		_ = watcher.Close(background)
	}
}
