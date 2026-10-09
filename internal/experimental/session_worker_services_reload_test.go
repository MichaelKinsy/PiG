package experimental

// pi: packages/coding-agent/src/experimental/services/worker.ts

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/durabletest"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

var reloadScope = services.WorkerServiceScope{ServerConnectionId: "server-1", AttachmentId: "attachment-1"}

func newReloadWorker(t *testing.T, load func(context.Context) (chord.LoadedFacets, error)) *services.SessionWorkerServices {
	t.Helper()
	durable := durabletest.OpenFauxConversation()
	t.Cleanup(func() { _ = durable.Harness.Close(context.Background()) })
	worker, err := services.CreateSessionWorkerServices(services.SessionWorkerServicesOptions{
		Harness: durable.Harness, Conversation: durable.Conversation, FacetLoader: sessionPluginTestLoader(load),
		Publish: func(context.Context, services.WorkerServiceScope, string, chord.ServiceProviderUpdate) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func reloadCall(worker *services.SessionWorkerServices) error {
	_, err := worker.Invoke(context.Background(), chord.ServiceCall{ServiceId: services.SessionPluginsDefinition.Id(), Member: "reload", Args: []json.RawMessage{}}, reloadScope)
	return err
}

// packages/coding-agent/src/experimental/services/worker.ts: a plugin reload whose new generation cannot start disposes that candidate, reports the
// error and keeps the running generation; a candidate that also fails to dispose reports both failures.
func TestSessionWorkerServicesReloadFailureKeepsTheRunningGeneration(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var disposals []int
	generation := 0
	startFailure := errors.New("plugin setup failed")
	candidateDisposeFailure := errors.New("candidate dispose failed")
	failNext, failDispose := false, false
	worker := newReloadWorker(t, func(context.Context) (chord.LoadedFacets, error) {
		mu.Lock()
		defer mu.Unlock()
		generation++
		current, fail, failClose := generation, failNext, failDispose
		return chord.LoadedFacets{
			Facets: []chord.Facet{{Id: "reloadable-session-plugin", Setup: func(env *chord.FacetEnvironment) error {
				if fail {
					return startFailure
				}
				return nil
			}}},
			Dispose: func(context.Context) error {
				mu.Lock()
				defer mu.Unlock()
				disposals = append(disposals, current)
				if failClose {
					return candidateDisposeFailure
				}
				return nil
			},
		}, nil
	})
	disposed := false
	t.Cleanup(func() {
		if !disposed {
			_ = worker.Dispose()
		}
	})

	// The loader's Dispose takes mu, and so do the test's cleanup and a failed check's Dispose, so mu is never held across a check.
	set := func(fail, failClose bool) {
		mu.Lock()
		defer mu.Unlock()
		failNext, failDispose = fail, failClose
	}
	snapshot := func() []int {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(disposals)
	}

	set(true, false)
	if err := reloadCall(worker); err == nil || !strings.Contains(err.Error(), startFailure.Error()) {
		t.Fatalf("failed reload = %v, want %v", err, startFailure)
	}
	if got := snapshot(); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("disposals after a failed reload = %v, want only the candidate [2]", got)
	}
	set(true, true)
	err := reloadCall(worker)
	if err == nil || err.Error() != "Session plugin reload and cleanup failed" || !errors.Is(err, startFailure) || !errors.Is(err, candidateDisposeFailure) {
		t.Fatalf("reload with a failing cleanup = %v", err)
	}
	set(false, false)
	if err := reloadCall(worker); err != nil {
		t.Fatalf("reload after the failures = %v", err)
	}
	// The first generation is retired by the successful cutover, after the two failed candidates were released.
	if got := snapshot(); !reflect.DeepEqual(got, []int{2, 3, 1}) {
		t.Fatalf("disposals = %v, want [2 3 1]", got)
	}
	disposed = true
	if err := worker.Dispose(); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(); !reflect.DeepEqual(got, []int{2, 3, 1, 4}) {
		t.Fatalf("disposals after Dispose = %v, want the live generation [4] last", got)
	}
}

// worker.ts:140 dispose awaits reloadTail: a reload already running finishes its cutover before Dispose releases anything, so the
// generation it installs is the one Dispose releases, after the retired one.
func TestSessionWorkerServicesDisposeWaitsForTheRunningReload(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var disposals []int
	entered := make(chan struct{})
	release := make(chan struct{})
	generation := 0
	worker := newReloadWorker(t, func(context.Context) (chord.LoadedFacets, error) {
		mu.Lock()
		generation++
		current := generation
		mu.Unlock()
		if current == 2 {
			close(entered)
			<-release
		}
		return chord.LoadedFacets{Dispose: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			disposals = append(disposals, current)
			return nil
		}}, nil
	})
	reloaded := make(chan error, 1)
	go func() { reloaded <- reloadCall(worker) }()
	<-entered
	disposeDone := make(chan error, 1)
	go func() { disposeDone <- worker.Dispose() }()
	select {
	case err := <-disposeDone:
		close(release)
		t.Fatalf("Dispose returned %v while a reload was still loading its generation", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-disposeDone; err != nil {
		t.Fatalf("Dispose = %v", err)
	}
	<-reloaded
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(disposals, []int{1, 2}) {
		t.Fatalf("disposals = %v, want the retired generation [1] then the one the reload installed [2]", disposals)
	}
}

// Dispose reports a plugin cleanup failure (worker.ts dispose).
func TestSessionWorkerServicesDisposeReportsPluginCleanupFailure(t *testing.T) {
	t.Parallel()
	cause := errors.New("plugin dispose failed")
	worker := newReloadWorker(t, func(context.Context) (chord.LoadedFacets, error) {
		return chord.LoadedFacets{Dispose: func(context.Context) error { return cause }}, nil
	})
	err := worker.Dispose()
	// One failure is reported as itself; several are aggregated under the message (worker.ts throwFailures).
	if !errors.Is(err, cause) {
		t.Fatalf("Dispose = %v", err)
	}
}

// A reload before the loader can produce a generation reports the loader's error and leaves the running one in place.
func TestSessionWorkerServicesReloadReportsLoaderFailure(t *testing.T) {
	t.Parallel()
	loadFailure := errors.New("loader failed")
	calls := 0
	disposals := 0
	worker := newReloadWorker(t, func(context.Context) (chord.LoadedFacets, error) {
		calls++
		if calls > 1 {
			return chord.LoadedFacets{}, loadFailure
		}
		return chord.LoadedFacets{Dispose: func(context.Context) error { disposals++; return nil }}, nil
	})
	if err := reloadCall(worker); err == nil || !strings.Contains(err.Error(), loadFailure.Error()) {
		t.Fatalf("reload = %v, want %v", err, loadFailure)
	}
	if disposals != 0 {
		t.Fatalf("the running generation was disposed %d times by a failed load", disposals)
	}
	if err := worker.Dispose(); err != nil || disposals != 1 {
		t.Fatalf("Dispose = %v after %d disposals, want one", err, disposals)
	}
}
