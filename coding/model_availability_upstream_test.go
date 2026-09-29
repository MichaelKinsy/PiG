package coding

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type stalledCredentialList struct {
	list    func(context.Context) ([]ai.CredentialInfo, error)
	started chan struct{}
	finish  chan error
	once    sync.Once
	calls   atomic.Int32
}

func (s *stalledCredentialList) List(ctx context.Context) ([]ai.CredentialInfo, error) {
	entries, err := s.list(ctx)
	if err != nil {
		return nil, err
	}
	if s.calls.Add(1) == 1 {
		close(s.started)
		select {
		case failure := <-s.finish:
			if failure != nil {
				return nil, failure
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return entries, nil
}
func (s *stalledCredentialList) release(err error) { s.once.Do(func() { s.finish <- err }) }

func newAvailabilitySession(t *testing.T, configured ...bool) (*Session, *Services) {
	t.Helper()
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	const providerID = "availability-faux"
	model := &ai.Model{ID: "faux", Provider: &runtimeTestProvider{id: providerID}, ProviderMeta: ai.ProviderMetadata{ProviderID: providerID, API: "openai-completions"}}
	if len(configured) > 0 && configured[0] {
		if err := services.Auth().Set(providerID, ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
			t.Fatal(err)
		}
		services.Registry().RegisterProvider(providerID, extension.ProviderConfig{API: "openai-completions", BaseURL: "https://faux.invalid/v1", APIKey: "faux-key", Models: []extension.ProviderModelConfig{{ID: "faux", Name: "Faux", Input: []string{"text"}, ContextWindow: 128000, MaxTokens: 4096}}})
	}
	session, err := NewSession(services, SessionOptions{NoSession: true, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	return session, services
}
func stallAvailabilityCredentials(t *testing.T, runtime *ModelRuntime) *stalledCredentialList {
	t.Helper()
	original := runtime.availability.listCredentials
	stalled := &stalledCredentialList{list: original, started: make(chan struct{}), finish: make(chan error, 1)}
	runtime.availability.listCredentials = stalled.List
	t.Cleanup(func() { stalled.release(nil) })
	return stalled
}
func requireAvailabilityAuth(t *testing.T, runtime *ModelRuntime, provider string, want ai.AuthStatus) {
	t.Helper()
	if got := runtime.GetProviderAuthStatus(provider); !reflect.DeepEqual(got, want) {
		t.Fatalf("auth(%s)=%+v want=%+v", provider, got, want)
	}
}
func startAvailability(t *testing.T, runtime *ModelRuntime, provider string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { defer close(done); _, err := runtime.GetAvailable(t.Context(), provider); done <- err }()
	t.Cleanup(func() { <-done })
	return done
}
func waitAvailabilityStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("availability did not start")
	}
}
func recoveryAvailability(t *testing.T, runtime *ModelRuntime, stalled *stalledCredentialList) {
	t.Helper()
	result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if result.Aborted || len(result.Errors) != 0 {
		t.Fatalf("refresh=%+v", result)
	}
	if calls := stalled.calls.Load(); calls != 2 {
		t.Fatalf("credential calls=%d want original pass + recovery", calls)
	}
}

func runAvailabilityCase(t *testing.T, name string, run func(*testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) { synctest.Test(t, run) })
}

func TestStalledAvailabilityRefreshUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7301-stalled-availability-refresh.test.ts:70
	runAvailabilityCase(t, "recovers without letting the stalled refresh overwrite the newer snapshot", func(t *testing.T) {
		session, services := newAvailabilitySession(t)
		runtime := session.ModelRuntime()
		if _, err := services.Auth().Modify(t.Context(), "stale-provider", func(*ai.Credential) (*ai.Credential, error) {
			return &ai.Credential{Type: ai.CredentialAPIKey, Key: "stale-key"}, nil
		}); err != nil {
			t.Fatal(err)
		}
		runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
		requireAvailabilityAuth(t, runtime, "stale-provider", ai.AuthStatus{Configured: true, Source: ai.AuthSourceStored})
		stalled := stallAvailabilityCredentials(t, runtime)
		old := startAvailability(t, runtime, "")
		waitAvailabilityStarted(t, stalled.started)
		defer func() { stalled.release(nil); <-old }()
		if err := ai.NewRuntimeCredentials(services.Auth()).Delete(t.Context(), "stale-provider"); err != nil {
			t.Fatal(err)
		}
		if _, err := services.Auth().Modify(t.Context(), "current-provider", func(*ai.Credential) (*ai.Credential, error) {
			return &ai.Credential{Type: ai.CredentialAPIKey, Key: "current-key"}, nil
		}); err != nil {
			t.Fatal(err)
		}
		recoveryAvailability(t, runtime, stalled)
		requireAvailabilityAuth(t, runtime, "stale-provider", ai.AuthStatus{Configured: false})
		requireAvailabilityAuth(t, runtime, "current-provider", ai.AuthStatus{Configured: true, Source: ai.AuthSourceStored})
		stalled.release(nil)
		err := <-old
		if err != nil {
			t.Fatal(err)
		}
		requireAvailabilityAuth(t, runtime, "stale-provider", ai.AuthStatus{Configured: false})
		requireAvailabilityAuth(t, runtime, "current-provider", ai.AuthStatus{Configured: true, Source: ai.AuthSourceStored})
		fmt.Println("AVAILABILITY_RECOVERY stale=false current=true/stored")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7301-stalled-availability-refresh.test.ts:93
	runAvailabilityCase(t, "does not let a stale failure overwrite newer availability error state", func(t *testing.T) {
		session, _ := newAvailabilitySession(t)
		runtime := session.ModelRuntime()
		stalled := stallAvailabilityCredentials(t, runtime)
		old := startAvailability(t, runtime, "")
		waitAvailabilityStarted(t, stalled.started)
		defer func() { stalled.release(nil); <-old }()
		recoveryAvailability(t, runtime, stalled)
		if got := runtime.GetError(); got != "" {
			t.Fatal(got)
		}
		stalled.release(errors.New("stale credential list failure"))
		err := <-old
		if err == nil || !strings.Contains(err.Error(), "stale credential list failure") {
			t.Fatalf("error=%v", err)
		}
		if got := runtime.GetError(); got != "" {
			t.Fatalf("stale failure published: %s", got)
		}
		fmt.Println("AVAILABILITY_FAILURE stale error returned; current error absent")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7301-stalled-availability-refresh.test.ts:108
	runAvailabilityCase(t, "does not let a stale provider-scoped failure overwrite a newer availability pass", func(t *testing.T) {
		session, _ := newAvailabilitySession(t, true)
		runtime := session.ModelRuntime()
		// createHarness finishes registration setup before replacing the availability callback.
		synctest.Wait()
		original := runtime.availability.getAvailable
		started, gate := make(chan struct{}), make(chan struct{})
		var once sync.Once
		release := func() { once.Do(func() { close(gate) }) }
		defer release()
		var stall atomic.Bool
		stall.Store(true)
		runtime.availability.getAvailable = func(ctx context.Context, provider string) ([]*ai.Model, error) {
			if provider == "" || !stall.CompareAndSwap(true, false) {
				return original(ctx, provider)
			}
			close(started)
			select {
			case <-gate:
				return nil, errors.New("stale provider availability failure")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		old := startAvailability(t, runtime, session.Model().ProviderMeta.ProviderID)
		waitAvailabilityStarted(t, started)
		defer func() { release(); <-old }()
		if _, err := runtime.GetAvailable(t.Context(), ""); err != nil {
			t.Fatal(err)
		}
		if got := runtime.GetError(); got != "" {
			t.Fatal(got)
		}
		release()
		err := <-old
		if err == nil || !strings.Contains(err.Error(), "stale provider availability failure") {
			t.Fatalf("error=%v", err)
		}
		if got := runtime.GetError(); got != "" {
			t.Fatalf("stale provider failure published: %s", got)
		}
		fmt.Println("AVAILABILITY_PROVIDER_FAILURE stale error returned; current error absent")
	})
}
