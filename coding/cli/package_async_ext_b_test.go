package cli

// pi: packages/coding-agent/src/package-manager-cli.ts

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

type scriptedModelRuntime struct {
	refresh func(context.Context) ai.ModelsRefreshResult
}

func (r scriptedModelRuntime) Refresh(ctx context.Context, _ ...ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	return r.refresh(ctx)
}

// package-manager-cli.ts:611-624 refreshModelCatalogs creates the model runtime and refreshes under one 15 s abort signal
// (`setTimeout(() => controller.abort(), 15_000)`, cleared in `finally`): a refresh still running at 15 s ends as "Model catalog
// refresh timed out." and the signal given to the runtime and its refresh is the same one, released when the call returns.
func TestRefreshModelCatalogsAbortsAtFifteenSecondsAndReleasesTheTimer(t *testing.T) {
	original := createPackageModelRuntime
	t.Cleanup(func() { createPackageModelRuntime = original })
	t.Run("refresh hangs", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			var created, refreshed context.Context
			createPackageModelRuntime = func(ctx context.Context, _ coding.CreateModelRuntimeOptions) (packageModelRuntime, error) {
				created = ctx
				return scriptedModelRuntime{func(ctx context.Context) ai.ModelsRefreshResult {
					refreshed = ctx
					<-ctx.Done()
					return ai.ModelsRefreshResult{Aborted: true}
				}}, nil
			}
			result := make(chan error, 1)
			go func() { result <- refreshModelCatalogs(t.TempDir()) }()
			time.Sleep(15*time.Second - time.Millisecond)
			synctest.Wait()
			select {
			case err := <-result:
				t.Fatalf("refresh ended before 15 s: %v", err)
			default:
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			select {
			case err := <-result:
				if err == nil || err.Error() != "Model catalog refresh timed out." {
					t.Fatalf("error = %v, want the timeout message", err)
				}
			default:
				t.Fatal("refresh did not end at 15 s")
			}
			if deadline, ok := created.Deadline(); !ok || !deadline.Equal(start.Add(15*time.Second)) {
				t.Errorf("runtime creation deadline = %v (set %v), want start+15s", deadline, ok)
			}
			if refreshed != created {
				t.Error("the refresh did not run under the signal the runtime was created with")
			}
		})
	})
	t.Run("refresh ends in time", func(t *testing.T) {
		var mu sync.Mutex
		var created context.Context
		createPackageModelRuntime = func(ctx context.Context, _ coding.CreateModelRuntimeOptions) (packageModelRuntime, error) {
			mu.Lock()
			defer mu.Unlock()
			created = ctx
			return scriptedModelRuntime{func(context.Context) ai.ModelsRefreshResult { return ai.ModelsRefreshResult{} }}, nil
		}
		if err := refreshModelCatalogs(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if created.Err() == nil {
			t.Error("the 15 s signal was not released when the refresh returned")
		}
	})
}
