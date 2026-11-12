package coding

// pi: packages/coding-agent/src/core/agent-session-services.ts

import (
	"context"
	"errors"
	"testing"
)

type servicesSignalKey struct{}

// agent-session-services.ts:154 awaits resourceLoader.reload before anything else is built on it; Go runs that reload (and the local refresh after the drain) under
// the caller's ModelRuntimeSignal, so the extension loader sees the caller's context and a cancelled signal rejects the creation with no services handed back.
func TestServicesReloadRunsUnderTheModelRuntimeSignal(t *testing.T) {
	signal := context.WithValue(context.Background(), servicesSignalKey{}, "caller")
	var seen any
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{
		CWD: t.TempDir(), AgentDir: t.TempDir(), ModelRuntimeSignal: signal,
		ResourceLoaderOptions: &DefaultResourceLoaderOptions{LoadExtensions: func(ctx context.Context, _ ExtensionLoadRequest) (LoadExtensionsResult, error) {
			seen = ctx.Value(servicesSignalKey{})
			return LoadExtensionsResult{}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	services.Close()
	if seen != "caller" {
		t.Fatalf("the extension loader ran under %v, want the ModelRuntimeSignal", seen)
	}

	cancelled, cancel := context.WithCancel(signal)
	cancel()
	loads := 0
	services, err = CreateAgentSessionServices(CreateAgentSessionServicesOptions{
		CWD: t.TempDir(), AgentDir: t.TempDir(), ModelRuntimeSignal: cancelled,
		ResourceLoaderOptions: &DefaultResourceLoaderOptions{LoadExtensions: func(ctx context.Context, _ ExtensionLoadRequest) (LoadExtensionsResult, error) {
			loads++
			return LoadExtensionsResult{}, ctx.Err()
		}},
	})
	if services != nil {
		services.Close()
		t.Fatal("a cancelled signal still produced services")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	if loads != 1 {
		t.Fatalf("loader ran %d times, want once", loads)
	}
}
