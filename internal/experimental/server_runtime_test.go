package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// upstream: packages/coding-agent/src/experimental/server.ts:521-524
func TestStartServerProviderValidationPrecedesProfileAcquisition(t *testing.T) {
	isolateExperimentalTest(t)
	for _, provider := range []string{"anthropic", "", "custom-provider"} {
		t.Run(provider, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "uncreated-server")
			runtime, err := StartServer(t.Context(), StartServerOptions{
				Directory: &directory,
				ServerId:  new("invalid"),
				Provider:  &provider,
			})
			if runtime != nil {
				t.Cleanup(func() {
					if closeErr := runtime.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				})
				t.Error("invalid model selection returned a running server")
			}
			if err == nil || err.Error() != "Server model provider requires a model" {
				t.Fatalf("startup error = %v, want provider-without-model rejection", err)
			}
			if _, statErr := os.Lstat(directory); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid model selection created the profile directory: %v", statErr)
			}
		})
	}
}

// upstream: packages/coding-agent/src/experimental/server.ts:538-564,719-738,743-762
func TestServerStartupFailureReleasesLauncherAndActivation(t *testing.T) {
	isolateExperimentalTest(t)
	for _, test := range []struct {
		name  string
		start func(context.Context, StartServerOptions) (*RunningServer, error)
	}{
		{name: "direct", start: StartServer},
		{name: "foreground", start: StartForegroundServer},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := socketDir(t)
			serverID := "00000000-0000-4000-8000-000000000001"
			sessionDirectory := filepath.Join(directory, "sessions")
			profilePath := filepath.Join(directory, "plugin-packages-"+serverID+".json")
			if err := os.WriteFile(profilePath, []byte("null\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// upstream: packages/coding-agent/src/experimental/plugins/package.ts:99-133
			// Invalid persisted plugin configuration fails after launcher acquisition.
			runtime, err := test.start(t.Context(), StartServerOptions{
				Directory:  &directory,
				ServerId:   &serverID,
				SessionDir: &sessionDirectory,
			})
			if runtime != nil {
				t.Cleanup(func() {
					if closeErr := runtime.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				})
				t.Fatal("failed startup returned a running server")
			}
			wantError := "Invalid experimental plugin package profile " + profilePath
			if goruntime.GOOS == "windows" {
				wantError = "Unix socket directory requires a POSIX user ID"
			}
			if err == nil || err.Error() != wantError {
				t.Fatalf("startup error = %v, want %q", err, wantError)
			}
			data, readErr := os.ReadFile(profilePath)
			if readErr != nil || string(data) != "null\n" {
				t.Fatalf("failed startup changed the persisted profile: data=%q, error=%v", data, readErr)
			}
			for _, name := range []string{"launcher-" + serverID + ".lock", "activation-" + serverID + ".lock"} {
				if _, statErr := os.Lstat(filepath.Join(directory, name)); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("failed startup retained %s: %v", name, statErr)
				}
			}
			profile, acquireErr := AcquireServerProfile(t.Context(), directory, &serverID)
			if acquireErr != nil {
				t.Fatal(acquireErr)
			}
			if profile.ServerID != serverID {
				t.Errorf("reacquired identity = %q, want %q", profile.ServerID, serverID)
			}
			if releaseErr := profile.Release(); releaseErr != nil {
				t.Fatal(releaseErr)
			}
		})
	}
}

// upstream: packages/coding-agent/src/experimental/server.ts:539-565,719-738
func TestServerBuildFailureRetainsSelectionAndReleasesProfile(t *testing.T) {
	isolateExperimentalTest(t)
	directory := socketDir(t)
	serverId := "00000000-0000-4000-8000-000000000001"
	missingPackage := filepath.Join(directory, "missing-package")
	runtime, err := StartServer(t.Context(), StartServerOptions{
		Directory: &directory, ServerId: &serverId,
		PluginPackages: []string{missingPackage},
	})
	if runtime != nil {
		t.Cleanup(func() {
			if closeErr := runtime.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		})
		t.Fatal("failed package build returned a running server")
	}
	if goruntime.GOOS == "windows" {
		if err == nil || err.Error() != "Unix socket directory requires a POSIX user ID" {
			t.Fatalf("startup error = %v, want POSIX prerequisite error", err)
		}
	} else {
		wantError := "Could not access facet package " + missingPackage
		if err == nil || err.Error() != wantError || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("startup error = %v, want %q with missing-file cause", err, wantError)
		}
		paths, readErr := RestoreServerPluginPackageProfile(directory, serverId, nil)
		if readErr != nil || !slices.Equal(paths, []string{missingPackage}) {
			t.Fatalf("retained plugin selection = %v, %v", paths, readErr)
		}
	}
	if _, statErr := os.Lstat(filepath.Join(directory, "launcher-"+serverId+".lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed build retained launcher ownership: %v", statErr)
	}
}

// upstream: packages/coding-agent/src/experimental/server.ts:673-718
func TestRunningServerRoutesServicesAndJoinsClose(t *testing.T) {
	isolateExperimentalTest(t)
	directory := socketDir(t)
	sessionDirectory := filepath.Join(directory, "sessions")
	serverID := "00000000-0000-4000-8000-000000000001"
	// D64: this real Unix caller remains required after the owner removes experimental relay startup.
	var lease *CoordinatorStartupLease
	if goruntime.GOOS != "windows" {
		publicPath := filepath.Join(directory, serverID+".sock")
		controlPath := filepath.Join(directory, "control-"+serverID+".sock")
		coordinator, err := startCoordinator(publicPath, controlPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			coordinator.shutdown()
			<-coordinator.done
			if coordinator.err != nil {
				t.Error(coordinator.err)
			}
		})
		lease, err = EnsureCoordinator(t.Context(), publicPath, controlPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(lease.Close)
	}
	runtime, err := StartServer(t.Context(), StartServerOptions{
		Directory:      &directory,
		ServerId:       &serverID,
		SessionDir:     &sessionDirectory,
		PluginPackages: []string{},
	})
	if lease != nil {
		lease.Close()
	}
	if goruntime.GOOS == "windows" {
		if err == nil || err.Error() != "Unix socket directory requires a POSIX user ID" || runtime != nil {
			t.Fatalf("startup = (%v, %v), want unsupported POSIX directory error", runtime, err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := runtime.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if runtime.ServerId != serverID || runtime.SocketPath != filepath.Join(directory, serverID+".sock") || runtime.SessionDir != sessionDirectory {
		t.Fatalf("runtime identity = (%q, %q, %q), want (%q, %q, %q)", runtime.ServerId, runtime.SocketPath, runtime.SessionDir, serverID, filepath.Join(directory, serverID+".sock"), sessionDirectory)
	}
	if _, statErr := os.Lstat(filepath.Join(directory, "launcher-"+serverID+".lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("successful startup retained the launcher lock: %v", statErr)
	}
	closed := runtime.Closed()
	select {
	case <-closed:
		t.Fatal("foreground server is closed before explicit shutdown")
	default:
	}
	factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: runtime.SocketPath})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := client.Connect(t.Context(), client.ClientOptions{ServerId: runtime.ServerId, TransportFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := disposeServerClient(t.Context(), peer); closeErr != nil {
			t.Error(closeErr)
		}
	})
	target := protocol.ServerTarget{ServerId: runtime.ServerId}
	catalogue, err := peer.ServiceCatalogue(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	wantCatalogue := []chord.ServiceCatalogueEntry{
		{ServiceId: "pi.session-directory", Mode: chord.ServiceSingleton},
		{ServiceId: "pi.session-management", Mode: chord.ServiceSingleton},
		{ServiceId: "pi.presentation-plugins", Mode: chord.ServiceSingleton},
	}
	if !slices.Equal(catalogue, wantCatalogue) {
		t.Fatalf("server catalogue = %#v, want %#v", catalogue, wantCatalogue)
	}
	// upstream: server.ts:395-396 passes createOptions to createCatalogSession, which uses `options.id ?? randomUUID()` and rejects an explicit "" (session-catalog.ts:53-56).
	for _, selection := range []struct {
		input   string
		id      *string
		invalid bool
	}{
		{input: `{}`},
		{input: `{"id":"named-session"}`, id: new("named-session")},
		{input: `{"id":""}`, id: new(""), invalid: true},
	} {
		result, requestErr := peer.Request(t.Context(), target, chord.ServiceCall{
			ServiceId: "pi.session-management", Member: "create", Args: []json.RawMessage{json.RawMessage(selection.input)},
		})
		if selection.invalid {
			if requestErr == nil {
				t.Fatalf("create %s succeeded; want the catalog's Invalid session ID error", selection.input)
			}
			continue
		}
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		var created services.SessionSummary
		if decodeErr := json.Unmarshal(result, &created); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if created.ServerId != runtime.ServerId {
			t.Fatalf("created server ID = %q, want %q", created.ServerId, runtime.ServerId)
		}
		if selection.id == nil {
			id, parseErr := uuid.Parse(created.SessionId)
			if parseErr != nil || id.Version() != 4 {
				t.Fatalf("omitted ID generated %q, want UUIDv4 (session-catalog.ts randomUUID): %v", created.SessionId, parseErr)
			}
		} else if created.SessionId != *selection.id {
			t.Fatalf("explicit ID %q became %q", *selection.id, created.SessionId)
		}
	}
	if closeErr := disposeServerClient(t.Context(), peer); closeErr != nil {
		t.Fatal(closeErr)
	}
	// Independent close callers and passive watchers all retain the same result.
	var work sync.WaitGroup
	for range 2 {
		work.Go(func() {
			if closeErr := runtime.Close(); closeErr != nil {
				t.Error(closeErr)
			}
			select {
			case <-closed:
			default:
				t.Error("Close returned before backend and catalog closure completed")
			}
		})
		work.Go(func() {
			<-closed
			if closeErr := runtime.ClosedError(); closeErr != nil {
				t.Error(closeErr)
			}
		})
	}
	work.Wait()
	select {
	case <-runtime.Closed():
	default:
		t.Fatal("completed closure cannot be observed by a late watcher")
	}
	if closeErr := runtime.ClosedError(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if pids := runtime.WorkerPids(); len(pids) != 0 {
		t.Fatalf("closed server retains workers: %v", pids)
	}
}

// upstream: packages/coding-agent/src/experimental/server.ts:144-148
func TestActivateServerProviderValidationPrecedesDirectoryCreation(t *testing.T) {
	isolateExperimentalTest(t)
	for _, provider := range []string{"anthropic", "", "custom-provider"} {
		t.Run(provider, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "uncreated-server")
			activated, err := ActivateServer(t.Context(), ActivateServerOptions{
				Directory: directory, RequestedServerId: new("invalid"),
				SessionDir: filepath.Join(directory, "sessions"), Provider: &provider,
			})
			if activated != nil {
				t.Cleanup(func() {
					if closeErr := disposeServerClient(t.Context(), activated.Client); closeErr != nil {
						t.Error(closeErr)
					}
				})
				t.Error("invalid startup selection returned a client")
			}
			if err == nil || err.Error() != "Server model provider requires a model" {
				t.Fatalf("activation error = %v, want provider-without-model rejection", err)
			}
			if _, statErr := os.Lstat(directory); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid startup selection created a server directory: %v", statErr)
			}
		})
	}
}

// upstream: packages/coding-agent/src/experimental/server.ts:221-239
func TestServerActivationAvailabilityClassification(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "disconnected", err: &client.DisconnectedError{Message: "closed"}, want: true},
		{name: "disconnected with cause", err: &client.DisconnectedError{Message: "closed", Cause: syscall.EACCES}, want: true},
		{name: "version mismatch", err: &client.ServerError{Code: "version", Message: "stale"}, want: true},
		{name: "other server error", err: &client.ServerError{Code: "invalid_request", Message: "bad request"}},
		{name: "wrapped version mismatch", err: fmt.Errorf("outer: %w", &client.ServerError{Code: "version", Message: "stale"})},
		{name: "wrapped disconnected", err: fmt.Errorf("outer: %w", &client.DisconnectedError{Message: "closed"})},
		{name: "missing socket", err: &os.PathError{Op: "dial", Path: "server.sock", Err: syscall.ENOENT}, want: true},
		{name: "connection refused", err: fmt.Errorf("outer: %w", syscall.ECONNREFUSED), want: true},
		{name: "connection reset", err: syscall.ECONNRESET, want: true},
		{name: "broken pipe", err: syscall.EPIPE, want: true},
		{name: "socket timeout", err: syscall.ETIMEDOUT, want: true},
		{name: "permission denied", err: syscall.EACCES},
		{name: "cancelled", err: context.Canceled},
		{name: "ordinary error", err: errors.New("ENOENT in message only")},
		{name: "cyclic cause", err: &serverActivationErrorCycle{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isUnavailableServerError(test.err); got != test.want {
				t.Fatalf("unavailable(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}

// upstream: packages/coding-agent/src/experimental/server.ts:407-411,471-475
func TestServerCleanupJoinsConcurrentFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first := errors.New("repository close failed")
		second := errors.New("environment cleanup failed")
		firstStarted, secondStarted := make(chan struct{}), make(chan struct{})
		release := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			result <- settleServerCleanup("Experimental session storage cleanup failed",
				func() error { close(firstStarted); <-release; return first },
				func() error { close(secondStarted); return second },
			)
		}()
		synctest.Wait()
		for name, started := range map[string]<-chan struct{}{"repository": firstStarted, "environment": secondStarted} {
			select {
			case <-started:
			default:
				t.Errorf("%s cleanup was not admitted concurrently", name)
			}
		}
		var failure error
		returned := false
		select {
		case failure = <-result:
			returned = true
			t.Errorf("cleanup returned before every operation settled: %v", failure)
		default:
		}
		close(release)
		if !returned {
			failure = <-result
		}
		synctest.Wait()
		aggregate, ok := errors.AsType[*services.AggregateError](failure)
		if !ok || aggregate.Message != "Experimental session storage cleanup failed" {
			t.Fatalf("cleanup failure = %v, want upstream aggregate message", failure)
		}
		wantCauses := []any{first, second}
		if !slices.Equal(aggregate.Errors, wantCauses) {
			t.Fatalf("cleanup causes = %v, want operation order %v", aggregate.Errors, wantCauses)
		}
	})
}

func BenchmarkServerGeneration(b *testing.B) {
	b.Setenv("PI_OFFLINE", "")
	directory, err := os.MkdirTemp("", "psb")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			b.Error(err)
		}
	})
	serverId := "00000000-0000-4000-8000-000000000001"
	publicPath := filepath.Join(directory, serverId+".sock")
	controlPath := filepath.Join(directory, "control-"+serverId+".sock")
	coordinator, err := startCoordinator(publicPath, controlPath)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		coordinator.shutdown()
		<-coordinator.done
		if coordinator.err != nil {
			b.Error(coordinator.err)
		}
	})
	lease, err := EnsureCoordinator(b.Context(), publicPath, controlPath)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(lease.Close)
	sessionDirectory := filepath.Join(directory, "sessions")
	for range 2 {
		if _, err := CreateSession(sessionDirectory, CreateSessionOptions{Cwd: directory}); err != nil {
			b.Fatal(err)
		}
	}
	options := StartServerOptions{
		Directory: &directory, ServerId: &serverId, SessionDir: &sessionDirectory,
		PluginPackages: []string{},
	}
	b.ReportAllocs()
	for b.Loop() {
		runtime, err := StartServer(b.Context(), options)
		if err != nil {
			b.Fatal(err)
		}
		if err := runtime.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

type serverActivationErrorCycle struct{}

func (*serverActivationErrorCycle) Error() string     { return "cyclic cause" }
func (err *serverActivationErrorCycle) Unwrap() error { return err }
