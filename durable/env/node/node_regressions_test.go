package node

// Not upstream cases: each pins node.ts behavior that env-node.test.ts does not
// reach. The expected messages were probed on Node 24 with the same fs calls
// node.ts makes.

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// writeFile and appendFile run mkdir(resolve(resolved, ".."), {recursive:
// true}) first, and toFileError takes the path of Node's error: the parent. A
// parent that exists but is not a directory is EEXIST, which maps to unknown;
// a parent below a file is ENOTDIR.
func TestFilesystemReportsTheParentDirectoryWhenWritingBelowAFile(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "file.txt", "hello"))
	parent := filepath.Join(root, "file.txt")
	for name, write := range map[string]func(string) error{
		"writeFile":  func(path string) error { return env.WriteFile(background, path, "x") },
		"appendFile": func(path string) error { return env.AppendFile(background, path, "x") },
	} {
		failure := fileError(t, write("file.txt/child.txt"))
		if failure.Code != durableenv.FileErrorUnknown || failure.Path != parent || failure.Message != "EEXIST: file already exists, mkdir '"+parent+"'" {
			t.Fatalf("%s below a file = %+v", name, failure)
		}
		nested := filepath.Join(parent, "sub")
		failure = fileError(t, write("file.txt/sub/child.txt"))
		if failure.Code != durableenv.FileErrorNotDirectory || failure.Path != nested || failure.Message != "ENOTDIR: not a directory, mkdir '"+nested+"'" {
			t.Fatalf("%s two levels below a file = %+v", name, failure)
		}
	}
}

// createDir's recursive mkdir reports a path that exists as a file as EEXIST.
func TestFilesystemReportsEEXISTForARecursiveCreateDirOverAFile(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "file.txt", "hello"))
	path := filepath.Join(root, "file.txt")
	failure := fileError(t, env.CreateDir(background, "file.txt", nil))
	if failure.Code != durableenv.FileErrorUnknown || failure.Path != path || failure.Message != "EEXIST: file already exists, mkdir '"+path+"'" {
		t.Fatalf("createDir over a file = %+v", failure)
	}
}

// createDir runs mkdir(resolved, { recursive: options?.recursive ?? true }) (node.ts:904), so options without
// recursive, like options undefined, create missing parents.
func TestFilesystemCreateDirWithoutRecursiveOptionIsRecursive(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.CreateDir(background, "unset/child", &durableenv.CreateDirOptions{}))
	mustDo(t, env.CreateDir(background, "nil/child", nil))
	for _, path := range []string{"unset/child", "nil/child"} {
		if info := must(env.FileInfo(background, path)); info.Kind != durableenv.FileKindDirectory {
			t.Fatalf("%s kind = %s", filepath.Join(root, path), info.Kind)
		}
	}
}

type tempDirPrefixEnv struct {
	*NodeExecutionEnv
	mu       sync.Mutex
	prefixes []*string
}

func (env *tempDirPrefixEnv) CreateTempDir(ctx context.Context, prefix *string) (string, error) {
	env.mu.Lock()
	env.prefixes = append(env.prefixes, prefix)
	env.mu.Unlock()
	return env.NodeExecutionEnv.CreateTempDir(ctx, prefix)
}

// createTempFile calls this.createTempDir("tmp-", context): an override sees
// the prefix, not an omitted one.
func TestFilesystemCreateTempFilePassesItsDirectoryPrefixToCreateTempDir(t *testing.T) {
	env := &tempDirPrefixEnv{NodeExecutionEnv: NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir()})}
	env.Self = env
	path := must(env.CreateTempFile(background, nil))
	if len(env.prefixes) != 1 || env.prefixes[0] == nil || *env.prefixes[0] != "tmp-" {
		t.Fatalf("CreateTempDir prefixes = %v", env.prefixes)
	}
	if base := filepath.Base(filepath.Dir(path)); len(base) != len("tmp-")+6 || base[:4] != "tmp-" {
		t.Fatalf("temp file directory = %q", base)
	}
}

// A timeout that converts to less than a nanosecond is still a timeout: Node
// runs setTimeout delays below 1 ms after 1 ms.
func TestShellTimesOutATimeoutBelowOneMillisecond(t *testing.T) {
	if got := must(resolveTimeout(new(1e-13))); got != time.Millisecond {
		t.Fatalf("resolveTimeout(1e-13) = %v, want 1ms", got)
	}
	env, _ := newTestEnv(t)
	_, err := env.Exec(background, "sleep 2", &durableenv.ShellExecOptions{Timeout: new(1e-13)})
	failure := execErr(t, err, durableenv.ExecutionErrorTimeout)
	if failure.Message != "timeout:1e-13" {
		t.Fatalf("message = %q", failure.Message)
	}
}

// SetCwd may run while other goroutines resolve paths and execute commands;
// the race detector reports an unguarded cwd.
func TestFilesystemSetCwdIsSafeAlongsideConcurrentOperations(t *testing.T) {
	env, root := newTestEnv(t)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 200 {
			env.SetCwd(root)
		}
	})
	for range 200 {
		if got := must(env.AbsolutePath(background, "file.txt")); got != filepath.Join(root, "file.txt") {
			t.Errorf("absolute = %q", got)
		}
		_ = env.Cwd()
	}
	wg.Wait()
}
