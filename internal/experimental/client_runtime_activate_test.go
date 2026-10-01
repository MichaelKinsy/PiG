package experimental

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// readyOverride keeps a real remote binding and replaces only its readiness join.
type readyOverride struct {
	chord.RemoteServices
	ready func(context.Context) error
}

func (binding readyOverride) Ready(ctx context.Context) error { return binding.ready(ctx) }

type readyOverrideServerSource struct {
	ServerServiceSource
	ready func(context.Context) error
}

func (source readyOverrideServerSource) Open(options chord.RemoteServiceSourceOpenOptions) (chord.RemoteServices, error) {
	opened, err := source.ServerServiceSource.Open(options)
	return readyOverride{RemoteServices: opened, ready: source.ready}, err
}

type readyOverrideSessionSource struct {
	SessionServiceSource
	ready func(context.Context) error
}

func (source readyOverrideSessionSource) Open(options chord.RemoteServiceSourceOpenOptions) (chord.RemoteServices, error) {
	opened, err := source.SessionServiceSource.Open(options)
	return readyOverride{RemoteServices: opened, ready: source.ready}, err
}

// upstream: packages/coding-agent/src/experimental/client-runtime.ts:221
// `await Promise.all([serverServices.ready(...), sessionServices.ready(...)])` rejects with the first failure instead of waiting for the other namespace, which may never hydrate.
func TestActivateBuiltinClientServicesSurfacesTheFirstReadinessFailureAndReleasesTheOtherWait(t *testing.T) {
	requirePOSIXServerDirectory(t)
	for _, failing := range []string{"server", "session"} {
		t.Run(failing+" namespace fails first", func(t *testing.T) {
			setupExperimentalRemoteTest(t)
			_, running := makeExperimentalServer(t)
			opened, err := OpenClientRuntime(t.Context(), ClientCommand{
				Command: "client",
				Connect: &TransportAddress{Transport: "unix", Path: running.SocketPath},
			}, OpenClientRuntimeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := opened.Dispose(); err != nil {
					t.Error(err)
				}
			})
			cause := errors.New("namespace discovery failed")
			stuckObservedCancel := make(chan struct{})
			fail := func(context.Context) error { return cause }
			stuck := func(ctx context.Context) error {
				<-ctx.Done()
				close(stuckObservedCancel)
				return ctx.Err()
			}
			serverReady, sessionReady := fail, stuck
			if failing == "session" {
				serverReady, sessionReady = stuck, fail
			}
			server := *opened.Servers[0]
			server.Server = readyOverrideServerSource{ServerServiceSource: server.Server, ready: serverReady}
			server.Session = readyOverrideSessionSource{SessionServiceSource: server.Session, ready: sessionReady}
			result := make(chan error, 1)
			go func() {
				_, err := ActivateBuiltinClientServices(context.Background(), &server)
				result <- err
			}()
			select {
			case err := <-result:
				if !errors.Is(err, cause) {
					t.Fatalf("activation error = %v, want the first readiness failure %v", err, cause)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("activation is blocked joining the namespace that never hydrates")
			}
			select {
			case <-stuckObservedCancel:
			case <-time.After(10 * time.Second):
				t.Fatal("the pending readiness wait was not released")
			}
		})
	}
}
