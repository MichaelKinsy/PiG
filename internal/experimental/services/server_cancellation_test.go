package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
)

// The server Promise tail does not check cancellation itself. Queued callbacks run in admission order and receive the original, possibly cancelled context.
func TestServerQueuedCancellationPreservesAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services, err := CreateExperimentalServerServices(ExperimentalServerServicesOptions{List: func(context.Context) ([]SessionSummary, error) { return []SessionSummary{}, nil }})
		requireModelsOK(t, err)
		entered, release := make(chan struct{}), make(chan struct{})
		releaseFirst := sync.OnceFunc(func() { close(release) })
		defer releaseFirst()
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		var calls []string
		presentation := testServerPresentation{attach: func(callCtx context.Context, id string) error {
			calls = append(calls, id)
			if id == "first" {
				close(entered)
				<-release
				return nil
			}
			if callCtx != ctx {
				t.Error("queued cancellation lost its context")
			}
			return context.Cause(callCtx)
		}}
		a, err := services.Host.AttachClient(t.Context(), presentation)
		requireModelsOK(t, err)
		first, second := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := serverCall(t.Context(), a, SessionManagementDefinition.Id(), "attach", "first")
			first <- err
		}()
		<-entered
		go func() {
			_, err := serverCall(ctx, a, SessionManagementDefinition.Id(), "attach", "second")
			second <- err
		}()
		synctest.Wait()
		failure := errors.New("cancel queued")
		cancel(failure)
		synctest.Wait()
		select {
		case err := <-second:
			t.Fatalf("cancelled call bypassed its predecessor: %v", err)
		default:
		}
		checkModelsEqual(t, calls, []string{"first"})
		releaseFirst()
		requireModelsOK(t, <-first)
		if err := <-second; !errors.Is(err, failure) {
			t.Fatal(err)
		}
		checkModelsEqual(t, calls, []string{"first", "second"})
		requireModelsOK(t, services.Dispose())
	})
}
