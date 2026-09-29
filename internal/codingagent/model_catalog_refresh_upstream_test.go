package codingagent

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestModelCatalogRefreshUpstream(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	for _, tc := range []struct {
		name        string
		cancelFirst bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-catalog-refresh.test.ts:23
		{"shares one runtime refresh between concurrent callers", false},
		// .upstream/v0.87.1/packages/coding-agent/test/model-catalog-refresh.test.ts:38
		{"keeps the shared refresh alive when one caller stops waiting", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, _, store := radiusTestRegistry(t, "", nil)
			synctest.Test(t, func(t *testing.T) {
				blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
				registry.SetModelsStore(blocked)
				firstCtx, cancelFirst := context.WithCancel(t.Context())
				defer cancelFirst()
				type outcome struct {
					result CatalogRefreshResult
					err    error
				}
				first, second := make(chan outcome, 1), make(chan outcome, 1)
				go func() { r, e := RefreshModelCatalogs(firstCtx, registry); first <- outcome{r, e} }()
				operationCtx := <-blocked.started
				go func() { r, e := RefreshModelCatalogs(t.Context(), registry); second <- outcome{r, e} }()
				synctest.Wait()
				if len(blocked.started) != 0 {
					t.Error("runtime refreshed more than once")
				}
				if tc.cancelFirst {
					cancelFirst()
					if got := <-first; !errors.Is(got.err, context.Canceled) {
						t.Errorf("first error=%v", got.err)
					}
					if operationCtx.Err() != nil {
						t.Error("shared operation cancelled while second caller waits")
					}
				}
				close(blocked.release)
				check := func(got outcome) {
					t.Helper()
					if got.err != nil || got.result.Aborted || len(got.result.Errors) != 0 {
						t.Errorf("result=%+v error=%v", got.result, got.err)
					}
				}
				if !tc.cancelFirst {
					check(<-first)
				}
				check(<-second)
			})
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/model-catalog-refresh.test.ts:61
	t.Run("aborts an abandoned refresh and allows a later refresh to start", func(t *testing.T) {
		registry, _, store := radiusTestRegistry(t, "", nil)
		synctest.Test(t, func(t *testing.T) {
			// Upstream's promise never settles. Keep it pending until both cancellation assertions finish, then drain the Go worker.
			blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4), ignoreCancel: true}
			registry.SetModelsStore(blocked)
			firstCtx, cancelFirst := context.WithCancel(t.Context())
			done := make(chan error, 2)
			go func() { _, err := RefreshModelCatalogs(firstCtx, registry); done <- err }()
			firstSignal := <-blocked.started
			cancelFirst()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Errorf("first error=%v", err)
			}
			synctest.Wait()
			if firstSignal.Err() == nil {
				t.Error("abandoned refresh not aborted")
			}
			secondCtx, cancelSecond := context.WithCancel(t.Context())
			go func() { _, err := RefreshModelCatalogs(secondCtx, registry); done <- err }()
			secondSignal := <-blocked.started
			if secondSignal == firstSignal {
				t.Error("later refresh reused abandoned signal")
			}
			cancelSecond()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Errorf("second error=%v", err)
			}
			close(blocked.release)
			synctest.Wait()
		})
	})
}
