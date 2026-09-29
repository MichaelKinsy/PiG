package subprocess

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/buildprogress"
)

func writeConcurrentBuildSource(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module "+name+"\ngo 1.26\n")
	mustWrite(t, filepath.Join(dir, "main.go"), fmt.Sprintf("package main\nimport \"fmt\"\nfunc main(){fmt.Println(%q)}\n", name))
	return dir
}

func TestBuilderIndependentBuildsReachCompilerConcurrently(t *testing.T) {
	builder := NewBuilderWithConfigRoot(t.TempDir(), t.TempDir())
	sources := []string{writeConcurrentBuildSource(t, "alpha"), writeConcurrentBuildSource(t, "beta")}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{}, len(sources))
	ctx = buildprogress.Observe(ctx, func(event buildprogress.Event) {
		if event.Phase == "Compiling Go member" {
			started <- struct{}{}
			<-ctx.Done()
		}
	}, false)
	var workers sync.WaitGroup
	for i, source := range sources {
		workers.Go(func() {
			_, err := builder.BuildContext(ctx, fmt.Sprint(i), source)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("build error = %v, want cancellation", err)
			}
		})
	}
	// This watchdog diagnoses a serialized build deadlock; elapsed time is not a performance assertion.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for range sources {
		select {
		case <-started:
		case <-ctx.Done():
		case <-timer.C:
			t.Error("independent build did not reach compiler while its peer was blocked")
			cancel()
		}
	}
	cancel()
	workers.Wait()
}

func TestLoadAllBoundsBuildConcurrencyAndCancelsQueuedBuilds(t *testing.T) {
	old := runtime.GOMAXPROCS(2)
	t.Cleanup(func() { runtime.GOMAXPROCS(old) })
	limit := maxConcurrentCellPreparations()
	configs := make([]ExtConfig, limit+1)
	for i := range configs {
		name := fmt.Sprintf("concurrent-%d", i)
		configs[i] = ExtConfig{Name: name, Source: writeConcurrentBuildSource(t, name), Enabled: true, Isolation: "isolated", RuntimeLanguage: "go", RuntimeKind: "subprocess", EntrypointKind: "standalone"}
	}
	synctest.Test(t, func(t *testing.T) {
		host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		t.Cleanup(func() { host.Shutdown("test done") })
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started := make(chan struct{}, len(configs))
		ctx = buildprogress.Observe(ctx, func(event buildprogress.Event) {
			if event.Phase == "Compiling Go member" {
				started <- struct{}{}
				<-ctx.Done()
			}
		}, false)
		done := make(chan []error, 1)
		go func() {
			loaded, errs := host.LoadAll(ctx, configs)
			if len(loaded) != 0 {
				t.Errorf("cancelled build loaded %d extensions", len(loaded))
			}
			done <- errs
		}()
		synctest.Wait()
		if got := len(started); got != limit {
			t.Errorf("compiler admissions = %d, want GOMAXPROCS bound %d", got, limit)
		}
		cancel()
		errs := <-done
		if len(errs) != len(configs) {
			t.Errorf("cancelled builds returned %d errors, want %d", len(errs), len(configs))
		}
		for _, err := range errs {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("load error = %v, want cancellation", err)
			}
		}
		if got := len(started); got > limit {
			t.Fatalf("queued compiler started after cancellation: admissions=%d", got)
		}
	})
}

func TestBuilderConcurrentSameArtifactPublishesOnce(t *testing.T) {
	builder := NewBuilderWithConfigRoot(t.TempDir(), t.TempDir())
	source := writeConcurrentBuildSource(t, "same-artifact")
	var builds atomic.Int64
	ctx := buildprogress.Observe(t.Context(), func(event buildprogress.Event) {
		if event.Phase == "Compiling Go member" {
			builds.Add(1)
		}
	}, false)
	results := make([]*BuildResult, 4)
	var workers sync.WaitGroup
	for i := range results {
		workers.Go(func() {
			var err error
			results[i], err = builder.BuildContext(ctx, "same-artifact", source)
			if err != nil {
				t.Errorf("build: %v", err)
			}
		})
	}
	workers.Wait()
	if t.Failed() {
		t.FailNow()
	}
	if builds.Load() != 1 {
		t.Fatalf("one content identity compiled %d times", builds.Load())
	}
	for _, result := range results {
		if result.BinaryPath != results[0].BinaryPath || result.Hash != results[0].Hash {
			t.Fatalf("concurrent publication disagreed: %+v vs %+v", result, results[0])
		}
		output, err := exec.CommandContext(t.Context(), result.BinaryPath).Output()
		if err != nil || string(output) != "same-artifact\n" {
			t.Fatalf("published output = %q, err=%v", output, err)
		}
	}
	cached, err := builder.BuildContext(ctx, "same-artifact", source)
	if err != nil || !cached.Cached || builds.Load() != 1 {
		t.Fatalf("warm build = %+v, err=%v, compiles=%d", cached, err, builds.Load())
	}
}
