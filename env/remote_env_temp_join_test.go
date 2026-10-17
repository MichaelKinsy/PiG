package env

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// packages/env/src/remote-env.ts:774-777 createTempDir returns the aborted result before it asks the daemon for anything, so an aborted
// context creates no directory under the remote tmpdir.
func TestRemoteExecutionEnvCreateTempDirAbortsBeforeTouchingTheDaemon(t *testing.T) {
	t.Parallel()
	env, connection := remoteEnvironment(t)
	info, err := connection.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prefix := "pi-env-aborted-"
	dir, err := env.CreateTempDir(ctx, &prefix)
	var fileError *durableenv.FileError
	if !errors.As(err, &fileError) || fileError.Code != durableenv.FileErrorAborted || dir != "" {
		t.Fatalf("CreateTempDir with an aborted context = %q, %v; want the aborted FileError", dir, err)
	}
	after, _ := os.ReadDir(info.Tmpdir)
	for _, entry := range after {
		if strings.HasPrefix(entry.Name(), prefix) {
			_ = os.RemoveAll(filepath.Join(info.Tmpdir, entry.Name()))
			t.Fatalf("an aborted CreateTempDir created %s", entry.Name())
		}
	}
}

// remote-env.ts:787-808 createTempFile makes its directory with the "tmp-" prefix and names the file prefix + UUID + suffix inside it.
func TestRemoteExecutionEnvCreateTempFileUsesATmpDirectoryAndTheOptionsNames(t *testing.T) {
	t.Parallel()
	env, _ := remoteEnvironment(t)
	ctx := context.Background()
	file, err := env.CreateTempFile(ctx, &durableenv.CreateTempFileOptions{Prefix: "pre-", Suffix: ".txt"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(file)) })
	if !strings.HasPrefix(filepath.Base(filepath.Dir(file)), "tmp-") {
		t.Errorf("the temp file's directory %q does not start with tmp-", filepath.Dir(file))
	}
	name := filepath.Base(file)
	if !strings.HasPrefix(name, "pre-") || !strings.HasSuffix(name, ".txt") || len(name) != len("pre-")+36+len(".txt") {
		t.Errorf("temp file name %q is not prefix + UUID + suffix", name)
	}
	if stat, err := os.Stat(file); err != nil || stat.Size() != 0 {
		t.Errorf("the temp file is missing or not empty: %v", err)
	}
}

// remote-env.ts:440-446 joinPath rejects with the connection's failure instead of joining without knowing the remote's path flavour.
func TestRemoteExecutionEnvJoinPathReportsAConnectionFailure(t *testing.T) {
	t.Parallel()
	connection := NewConnection(ConnectionOptions{Command: []string{"/nonexistent/pi-env-daemon"}})
	t.Cleanup(connection.Close)
	env := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:broken", Cwd: t.TempDir()})
	if joined, err := env.JoinPath(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatalf("JoinPath over a connection that cannot start = %q, nil; want the connection's error", joined)
	}
}
