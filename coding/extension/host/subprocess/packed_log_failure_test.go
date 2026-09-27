package subprocess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPackedStderrLogOwnership(t *testing.T) {
	for _, retainFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup-before-late-report", true: "reported-before-cleanup"}[retainFirst], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "pig-packed-owned.log")
			foreign := filepath.Join(dir, "pig-packed-other-session.log")
			for _, name := range []string{path, foreign} {
				if err := os.WriteFile(name, []byte("stderr evidence"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			log := &packedStderrLog{path: path}
			if retainFirst && log.retain() != path {
				t.Fatal("retention lost path")
			}
			log.remove()
			log.remove()
			if retainFirst {
				assertPackedLogs(t, dir, path, foreign)
			} else {
				assertPackedLogs(t, dir, foreign)
				if got := log.retain(); got != "" {
					t.Fatalf("late diagnostic references deleted file: %s", got)
				}
			}
		})
	}
}

func TestPackedStderrLogLoadFailure(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "whole-cell", true: "one-member"}[partial], func(t *testing.T) {
			configs := packedLogTestConfigs(t, "node")
			if !partial {
				configs = configs[:1]
			}
			if err := os.WriteFile(configs[0].Source, []byte(`export default function() { throw new Error("packed-log-load-failure"); }`), 0o600); err != nil {
				t.Fatal(err)
			}
			tmp := privatePackedLogTemp(t)
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			_, loadErrors := h.LoadAll(t.Context(), configs)
			if len(loadErrors) != 1 {
				t.Fatalf("load errors = %v, want one failing member", loadErrors)
			}
			var loadErr *LoadError
			if !errors.As(loadErrors[0], &loadErr) || loadErr.StderrLog == "" {
				t.Fatalf("load error lacks diagnostic path: %v", loadErrors[0])
			}
			h.Shutdown("after load failure")
			assertPackedLogs(t, tmp, loadErr.StderrLog)
			data, err := os.ReadFile(loadErr.StderrLog)
			if err != nil || !strings.Contains(string(data), "packed-log-load-failure") {
				t.Fatalf("retained log = %q, error = %v", data, err)
			}
		})
	}
}

func TestPackedStderrLogUnreportedExitAndCancellation(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(map[bool]string{false: "unreported-exit", true: "parent-cancelled"}[cancelParent], func(t *testing.T) {
			configs := packedLogTestConfigs(t, "node")
			tmp := privatePackedLogTemp(t)
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			crashes := make(chan string, 2*len(configs))
			if cancelParent {
				h.SetCrashHandler(func(name string, _ time.Duration, _ bool, _ string) { crashes <- name })
			}
			_, loadErrors := h.LoadAll(ctx, configs)
			if len(loadErrors) != 0 {
				t.Fatal(loadErrors)
			}
			h.mu.Lock()
			me := h.exts[configs[0].Name]
			process := me.packedProcess
			h.mu.Unlock()
			if cancelParent {
				cancel()
			} else if err := process.cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			<-process.watchDone
			if cancelParent {
				// Drive the closed-connection path before Shutdown can suppress a false crash.
				h.handleIncoming(me)
			}
			h.Shutdown("after exit")
			assertPackedLogs(t, tmp)
			select {
			case name := <-crashes:
				t.Fatalf("parent cancellation reported %s as a crash", name)
			default:
			}
		})
	}
}
