package coding

// pi: packages/coding-agent/src/core/model-runtime.ts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// causeRecordingStore blocks every catalog read until its context ends and records the cause that ended it.
type causeRecordingStore struct {
	*ai.InMemoryModelsStore
	mu     sync.Mutex
	causes []error
}

func (store *causeRecordingStore) Read(ctx context.Context, _ string) (*ai.ModelsStoreEntry, error) {
	<-ctx.Done()
	store.mu.Lock()
	store.causes = append(store.causes, context.Cause(ctx))
	store.mu.Unlock()
	return nil, context.Cause(ctx)
}

func (store *causeRecordingStore) recorded() []error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]error(nil), store.causes...)
}

// model-runtime.ts:218-270 create races options.signal against the modelRefreshTimeoutMs timer with AbortSignal.any and awaits the refresh, which ends with whichever came first; create returns the runtime either way and clears the timer.
func TestCreateModelRuntimeRefreshEndsWithTheCallerSignalOrTheTimeout(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	// The model network is enabled only while neither offline variable is set.
	for _, name := range []string{"PI_OFFLINE", "PIG_OFFLINE"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	modelsPath := filepath.Join(t.TempDir(), "models.json")
	callerCause := errors.New("caller aborted create")
	create := func(ctx context.Context, store ai.ModelsStore, timeoutMs int) (*ModelRuntime, error) {
		return CreateModelRuntime(ctx, CreateModelRuntimeOptions{Credentials: ai.NewInMemoryCredentialStore(), ModelsPath: new(new(modelsPath)), ModelsStore: store, AllowModelNetwork: true, ModelRefreshTimeoutMs: &timeoutMs})
	}
	check := func(t *testing.T, store *causeRecordingStore, want error, notWant error) {
		t.Helper()
		causes := store.recorded()
		if len(causes) == 0 {
			t.Fatal("no catalog read observed its context end")
		}
		for _, cause := range causes {
			if !errors.Is(cause, want) || errors.Is(cause, notWant) {
				t.Fatalf("a catalog read ended with %v, want %v", cause, want)
			}
		}
	}
	t.Run("the caller signal ends the refresh before the timer", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			store := &causeRecordingStore{InMemoryModelsStore: ai.NewInMemoryModelsStore()}
			ctx, cancel := context.WithCancelCause(t.Context())
			type created struct {
				runtime *ModelRuntime
				err     error
			}
			result := make(chan created, 1)
			go func() {
				runtime, err := create(ctx, store, 3_600_000)
				result <- created{runtime, err}
			}()
			synctest.Wait()
			if len(result) != 0 {
				t.Fatal("create returned while the catalog reads were still waiting")
			}
			cancel(callerCause)
			outcome := <-result
			if outcome.err != nil || outcome.runtime == nil {
				t.Fatalf("create = %v, %v, want the runtime", outcome.runtime, outcome.err)
			}
			t.Cleanup(outcome.runtime.Close)
			synctest.Wait()
			check(t, store, callerCause, context.DeadlineExceeded)
		})
	})
	t.Run("the timer ends the refresh while the caller signal is live", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			store := &causeRecordingStore{InMemoryModelsStore: ai.NewInMemoryModelsStore()}
			start := time.Now()
			runtime, err := create(t.Context(), store, 50)
			// The fake clock reads exactly the timeout: the refresh ended at modelRefreshTimeoutMs, not later.
			if elapsed := time.Since(start); elapsed != 50*time.Millisecond {
				t.Fatalf("create took %v, want the 50ms refresh timeout", elapsed)
			}
			if err != nil || runtime == nil {
				t.Fatalf("create = %v, %v, want the runtime", runtime, err)
			}
			t.Cleanup(runtime.Close)
			synctest.Wait()
			check(t, store, context.DeadlineExceeded, callerCause)
		})
	})
}
