package env

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// packages/env/src/remote-env.ts:760 remove: a directory needs recursive, a missing path needs force, and the removal is
// requested as "rm" with recursive and force defaulting to false (remote-env.ts:766-768).
func TestRemoteExecutionEnvRemoveHonoursRecursiveAndForce(t *testing.T) {
	env, _ := remoteEnvironment(t)
	ctx := context.Background()
	root := env.Cwd()
	mustDo(os.MkdirAll(filepath.Join(root, "dir", "inner"), 0o700))
	mustDo(os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o600))

	if err := env.Remove(ctx, "file.txt", nil); err != nil {
		t.Fatalf("Remove file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
	if err := env.Remove(ctx, "dir", nil); err == nil {
		t.Fatal("Remove of a directory without Recursive succeeded")
	}
	if err := env.Remove(ctx, "missing", nil); err == nil {
		t.Fatal("Remove of a missing path without Force succeeded")
	}
	if err := env.Remove(ctx, "dir", &durableenv.RemoveOptions{Force: true}); err == nil {
		t.Fatal("Force alone removed a directory")
	}
	if err := env.Remove(ctx, "missing", &durableenv.RemoveOptions{Recursive: true}); err == nil {
		t.Fatal("Recursive alone removed a missing path")
	}
	if err := env.Remove(ctx, "missing", &durableenv.RemoveOptions{Force: true}); err != nil {
		t.Fatalf("Remove missing with Force: %v", err)
	}
	if err := env.Remove(ctx, "dir", &durableenv.RemoveOptions{Recursive: true}); err != nil {
		t.Fatalf("Remove directory with Recursive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "dir")); !os.IsNotExist(err) {
		t.Fatalf("directory still exists: %v", err)
	}
}

// packages/env/src/remote-env.ts:774 createTempDir: mkdtemp under the remote tmpdir named prefix plus random characters,
// with prefix defaulting to "tmp-" (remote-env.ts:781).
func TestRemoteExecutionEnvCreateTempDirUsesThePrefixUnderTheRemoteTmpdir(t *testing.T) {
	env, connection := remoteEnvironment(t)
	ctx := context.Background()
	info, err := connection.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "pi-env-"
	for _, tc := range []struct {
		prefix *string
		want   string
	}{{&prefix, "pi-env-"}, {nil, "tmp-"}} {
		dir, err := env.CreateTempDir(ctx, tc.prefix)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		if filepath.Dir(dir) != filepath.Clean(info.Tmpdir) || !strings.HasPrefix(filepath.Base(dir), tc.want) || len(filepath.Base(dir)) != len(tc.want)+6 {
			t.Fatalf("CreateTempDir(%v) = %q under %q", tc.prefix, dir, info.Tmpdir)
		}
		if stat, err := os.Stat(dir); err != nil || !stat.IsDir() {
			t.Fatalf("temp dir missing: %v", err)
		}
	}
}
