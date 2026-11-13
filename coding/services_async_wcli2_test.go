package coding

// pi: packages/coding-agent/src/core/agent-session-services.ts

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// agent-session-services.ts:140-146 hands options.modelRuntimeSignal to ModelRuntime.create and to nothing else: `await resourceLoader.reload(options.resourceLoaderReloadOptions)`
// (:154) takes no signal, so a reload that is still running when the signal fires runs to its end and the services are created.
func TestServicesReloadOutlivesTheModelRuntimeSignal(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	bound := errors.New("the model runtime bound fired")
	signal, expire := context.WithCancelCause(t.Context())
	loads := 0
	var afterBound error
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{
		CWD: t.TempDir(), AgentDir: t.TempDir(), ModelRuntimeSignal: signal,
		ResourceLoaderOptions: &DefaultResourceLoaderOptions{LoadExtensions: func(ctx context.Context, _ ExtensionLoadRequest) (LoadExtensionsResult, error) {
			loads++
			// The bound fires while the reload is running.
			expire(bound)
			afterBound = ctx.Err()
			return LoadExtensionsResult{}, ctx.Err()
		}},
	})
	if err != nil {
		t.Fatalf("creation failed with %v: the reload ended at the model runtime bound", err)
	}
	t.Cleanup(services.Close)
	if loads != 1 {
		t.Fatalf("the extension loader ran %d times, want once", loads)
	}
	if !errors.Is(context.Cause(signal), bound) {
		t.Fatalf("the signal ended with %v, want the bound", context.Cause(signal))
	}
	if afterBound != nil {
		t.Fatalf("the reload saw %v after the bound fired, want a live context", afterBound)
	}
}

// agent-session-services.ts:194 `await modelRuntime.refresh({ allowNetwork: false })` takes no signal either: after a create whose availability pass the signal
// ended (model-runtime.ts:263 refresh under options.signal publishes nothing), the refresh after the reload publishes the availability of the environment key.
func TestServicesLocalRefreshAfterTheReloadOutlivesTheModelRuntimeSignal(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-wcli2")
	fired, cancel := context.WithCancel(t.Context())
	cancel()
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{
		CWD: t.TempDir(), AgentDir: t.TempDir(), ModelRuntimeSignal: fired,
		ResourceLoaderOptions: &DefaultResourceLoaderOptions{LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) {
			return LoadExtensionsResult{Runtime: extension.CreateExtensionRuntime()}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	if !services.ModelRuntime().HasConfiguredAuth("openai") {
		t.Fatal("the refresh after the reload published no availability: it ran under the fired model runtime signal")
	}
}

// model-runtime.ts:218-268 create awaits runtime.refresh under options.signal, and an availability pass the signal ends publishes no snapshot (:357-370 is
// never reached) and records no error (:382). Without a resource loader nothing refreshes after create, so the snapshot shows what create published.
func TestServicesModelRuntimeCreateEndsAtTheModelRuntimeSignal(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-wcli2")
	create := func(signal context.Context) *AgentSessionServices {
		t.Helper()
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), ModelRuntimeSignal: signal})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services.Close)
		return services
	}
	if live := create(t.Context()); !live.ModelRuntime().HasConfiguredAuth("openai") {
		t.Fatal("create under a live signal published no availability for the environment key")
	}
	fired, cancel := context.WithCancel(t.Context())
	cancel()
	runtime := create(fired).ModelRuntime()
	if runtime.HasConfiguredAuth("openai") {
		t.Fatal("create published availability although its signal had fired")
	}
	if text := runtime.GetError(); text != "" {
		t.Fatalf("GetError() = %q, want no error for a pass its signal ended", text)
	}
}
