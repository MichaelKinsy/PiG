package env

import (
	"context"
	"errors"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// remote-env.ts watch: the call checks its signal before it resolves the targets or touches the daemon, so a watch opened
// with an aborted context fails with the aborted FileError and opens no watcher, whatever state the connection is in.
func TestRemoteExecutionEnvWatchWithAnAbortedContextFailsAbortedBeforeTouchingTheConnection(t *testing.T) {
	t.Parallel()
	connection := NewConnection(ConnectionOptions{Command: []string{"/nonexistent/pi-env-daemon"}})
	t.Cleanup(connection.Close)
	env := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:broken", Cwd: t.TempDir()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	watcher, err := env.Watch(ctx, []durableenv.WatchTarget{{Path: "."}}, func(durableenv.FileChange) { calls++ })
	var fileError *durableenv.FileError
	if !errors.As(err, &fileError) || fileError.Code != durableenv.FileErrorAborted || watcher != nil {
		t.Fatalf("Watch with an aborted context = %v, %v; want the aborted FileError and no watcher", watcher, err)
	}
	if calls != 0 {
		t.Fatalf("onChange ran %d times for a watch that never opened", calls)
	}
}
