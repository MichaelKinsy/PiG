package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi model-runtime.ts:286-299,323-329 rejects Promise.all and publishes the first error while unrelated branches remain pending.
func TestAvailabilityFailureSettlesBeforeCredentialList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, services := newAvailabilitySession(t)
		runtime := session.ModelRuntime()
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		releaseAll := func() { once.Do(func() { close(release) }) }
		t.Cleanup(func() { releaseAll(); services.Close() })
		runtime.availability.listCredentials = func(context.Context) ([]ai.CredentialInfo, error) {
			close(entered)
			<-release
			return nil, nil
		}
		failure := errors.New("availability failed")
		runtime.availability.getAvailable = func(context.Context, string) ([]*ai.Model, error) { return nil, failure }
		done := make(chan error, 1)
		go func() { _, err := runtime.GetAvailable(t.Context()); done <- err }()
		<-entered
		synctest.Wait()
		returned := false
		select {
		case err := <-done:
			returned = true
			requireAvailabilityErrorIdentity(t, err, failure)
		default:
			t.Error("GetAvailable still waits for credential list after availability failed")
		}
		if !strings.Contains(runtime.GetError(), failure.Error()) {
			t.Error("availability failure is not visible while credential list remains pending")
		}
		trace, err := json.Marshal(struct {
			Returned     bool   `json:"returned"`
			ListPending  bool   `json:"listPending"`
			Error        string `json:"error"`
			VisibleError string `json:"visibleError"`
		}{returned, true, failure.Error(), runtime.GetError()})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("AVAILABILITY_FAILFAST %s\n", trace)
		releaseAll()
		if !returned {
			<-done
		}
		synctest.Wait()
	})
}

func TestAvailabilityFirstRejectionDoesNotWaitForEarlierBranch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, services := newAvailabilitySession(t)
		runtime := session.ModelRuntime()
		originalAvailable, originalList := runtime.availability.getAvailable, runtime.availability.listCredentials
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		releaseAll := func() { once.Do(func() { close(release) }) }
		t.Cleanup(func() { releaseAll(); services.Close() })
		first, late := errors.New("credential list failed first"), errors.New("availability failed later")
		var calls atomic.Int32
		runtime.availability.getAvailable = func(ctx context.Context, provider string) ([]*ai.Model, error) {
			if calls.Add(1) == 1 {
				close(entered)
				<-release
				return nil, late
			}
			return originalAvailable(ctx, provider)
		}
		runtime.availability.listCredentials = func(context.Context) ([]ai.CredentialInfo, error) { <-entered; return nil, first }
		done := make(chan error, 1)
		go func() { _, err := runtime.GetAvailable(t.Context()); done <- err }()
		<-entered
		synctest.Wait()
		select {
		case err := <-done:
			requireAvailabilityErrorIdentity(t, err, first)
		default:
			t.Fatal("later-index rejection waited for the earlier availability branch")
		}
		if runtime.GetError() != "Availability refresh: "+first.Error() {
			t.Fatalf("first error was not published: %q", runtime.GetError())
		}
		runtime.availability.listCredentials = originalList
		if _, err := runtime.GetAvailable(t.Context()); err != nil {
			t.Fatal(err)
		}
		releaseAll()
		synctest.Wait()
		if runtime.GetError() != "" {
			t.Fatalf("late sibling overwrote recovered error state: %q", runtime.GetError())
		}
	})
}

// The provider refresh catch handlers insert errors as their operations settle, not in provider input order (model-runtime.ts:722-731).
func TestProviderAvailabilityErrorsUseSettlementOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, services := newAvailabilitySession(t)
		runtime := session.ModelRuntime()
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		releaseAll := func() { once.Do(func() { close(release) }) }
		t.Cleanup(func() { releaseAll(); services.Close() })
		first := errors.New("a failed later")
		second := errors.New("b failed first")
		runtime.availability.getAvailable = func(_ context.Context, provider string) ([]*ai.Model, error) {
			if provider == "a" {
				close(entered)
				<-release
				return nil, first
			}
			<-entered
			return nil, second
		}
		done := make(chan ai.ModelsRefreshResult, 1)
		go func() {
			done <- runtime.refreshAvailability(t.Context(), ai.ModelsRefreshOptions{Providers: []string{"a", "b"}}, ai.ModelsRefreshResult{})
		}()
		<-entered
		synctest.Wait()
		if !strings.Contains(runtime.GetError(), second.Error()) {
			t.Fatal("first settled provider failure was not published")
		}
		releaseAll()
		result := <-done
		if !slices.Equal(result.ErrorOrder, []string{"b", "a"}) {
			t.Fatalf("error order=%v, want completion order [b a]", result.ErrorOrder)
		}
		requireAvailabilityErrorIdentity(t, result.Errors["a"], first)
		requireAvailabilityErrorIdentity(t, result.Errors["b"], second)
	})
}

// Pi model-runtime.ts:339-343 uses the same fail-fast Promise.all inside provider-scoped refresh.
func TestProviderAvailabilityFailureSettlesBeforeCredentialRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session, services := newAvailabilitySession(t)
		runtime := session.ModelRuntime()
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		releaseAll := func() { once.Do(func() { close(release) }) }
		t.Cleanup(func() { releaseAll(); services.Close() })
		failure := errors.New("provider availability failed")
		runtime.availability.getAvailable = func(context.Context, string) ([]*ai.Model, error) { <-entered; return nil, failure }
		runtime.availability.readCredential = func(context.Context, string) (*ai.Credential, error) { close(entered); <-release; return nil, nil }
		done := make(chan ai.ModelsRefreshResult, 1)
		go func() {
			done <- runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{Providers: []string{"selected"}, AllowNetwork: new(false)})
		}()
		<-entered
		synctest.Wait()
		select {
		case result := <-done:
			requireAvailabilityErrorIdentity(t, result.Errors["selected"], failure)
		default:
			t.Fatal("provider refresh waited for credential read after availability failed")
		}
		if !strings.Contains(runtime.GetError(), failure.Error()) {
			t.Fatal("provider error remains hidden behind credential read")
		}
		releaseAll()
		synctest.Wait()
	})
}
