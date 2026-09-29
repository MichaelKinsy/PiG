package codingagent

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func countingNativeProvider(checks *atomic.Int32) *extension.NativeProvider {
	return &extension.NativeProvider{ID: "held-native", Name: "Held", Models: []extension.ProviderModelConfig{{ID: "model", API: ai.APIOpenAICompletions, BaseURL: "https://native.invalid"}},
		CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) {
			checks.Add(1)
			return &ai.AuthCheck{Type: ai.CredentialAPIKey}, nil
		},
		ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
			return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "key"}}, nil, nil
		},
		ResolveRefreshCredential: func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) { return nil, nil, nil },
		Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions, bool) (*ai.AssistantMessageEventStream, error) {
			return ai.NewAssistantMessageEventStream(), nil
		},
	}
}

// Pi flushes the provider registrations of the extensions it loads only after every factory has finished (agent-session-services.ts:158-182), so a held registry queues the refresh: StartRegistrationRefresh runs no provider callback until the last hold is released, and an awaited yield still runs the queue.
func TestHeldRegistrationRefreshStartsOnlyAtRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := NewModelRegistry(t.TempDir())
		t.Cleanup(registry.CloseModelTasks)
		var checks atomic.Int32
		if err := registry.RegisterNativeProvider(t.Context(), countingNativeProvider(&checks)); err != nil {
			t.Fatal(err)
		}
		releaseOuter, releaseInner := registry.HoldRegistrationRefresh(), registry.HoldRegistrationRefresh()
		registry.StartRegistrationRefresh(t.Context())
		synctest.Wait()
		if checks.Load() != 0 {
			t.Fatal("a held registry started the queued refresh")
		}
		releaseInner()
		releaseInner()
		synctest.Wait()
		if checks.Load() != 0 {
			t.Fatal("releasing one of two holds (twice) started the queued refresh")
		}
		releaseOuter()
		synctest.Wait()
		if checks.Load() == 0 {
			t.Fatal("releasing the last hold did not start the queued refresh")
		}

		var yielded atomic.Int32
		if err := registry.RegisterNativeProvider(t.Context(), countingNativeProvider(&yielded)); err != nil {
			t.Fatal(err)
		}
		release := registry.HoldRegistrationRefresh()
		registry.YieldToRegistrationRefresh(t.Context())
		if yielded.Load() == 0 {
			t.Fatal("an awaited yield ignored by a hold did not run the queued refresh")
		}
		release()
	})
}

// A /reload that loads several extension factories queues their provider registrations and starts the refresh after the last factory has loaded (Pi's bindCore flush). The stand-in host registers a Provider and then ends its callback the way cmd/pig's host callbacks do: it starts the queued refresh.
func TestReloadRunsNoProviderCallbackWhileExtensionsLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := NewModelRegistry(t.TempDir())
		t.Cleanup(registry.CloseModelTasks)
		var checks atomic.Int32
		var duringReload int32 = -1
		host := &orderRecordingHost{}
		host.onReload = func() {
			if err := registry.RegisterNativeProvider(t.Context(), countingNativeProvider(&checks)); err != nil {
				t.Error(err)
			}
			registry.StartRegistrationRefresh(t.Context())
			synctest.Wait()
			duringReload = checks.Load()
		}
		m := reloadTestMode(InteractiveOptions{SubprocessHost: host, ModelRegistry: registry})
		if err := m.buildSlashContext(t.Context()).Reload(); err != nil {
			t.Fatal(err)
		}
		if duringReload != 0 {
			t.Fatalf("a Provider callback ran %d times while reload was still loading extensions", duringReload)
		}
		synctest.Wait()
		if checks.Load() == 0 {
			t.Fatal("the queued registration refresh never started after reload finished loading")
		}
	})
}
