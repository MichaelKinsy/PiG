package env

// Ports packages/env/test/remote.test.ts

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

var background = context.Background()

func remoteEnvironment(t *testing.T) (*RemoteExecutionEnv, *Connection) {
	t.Helper()
	connection := NewConnection(ConnectionOptions{Command: []string{daemonBinary(t)}})
	t.Cleanup(connection.Close)
	return NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:test", Cwd: t.TempDir()}), connection
}

// must returns value, or fails the test through a panic, which Go reports with the failing line.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func mustDo(err error) {
	if err != nil {
		panic(err)
	}
}

func errorCodeOf(err error) string {
	if err == nil {
		return "ok"
	}
	if fileError, ok := errors.AsType[*durableenv.FileError](err); ok {
		return string(fileError.Code)
	}
	if executionError, ok := errors.AsType[*durableenv.ExecutionError](err); ok {
		return string(executionError.Code)
	}
	return "other"
}

// Pi source: packages/env/src/remote-env.ts
// mutation-checked: zeroing the results of RemoteExecutionEnv.Exec, RemoteExecutionEnv.OpenBinaryReader, RemoteExecutionEnv.ReadBinaryFile fails it
// mutation-checked: dropping the reads and writes of ShellExecOptions.Window, ShellOutputInfo.Skipped fails it
func TestRemoteExecutionEnv(t *testing.T) {
	t.Run("transfers only about a window of output a caller keeps, with exact counts", func(t *testing.T) {
		// upstream: packages/env/test/remote.test.ts:28
		env, _ := remoteEnvironment(t)
		const total = 5_000_000
		var delivered, skipped int
		var tail string
		var mu sync.Mutex
		result := must(env.Exec(background, []string{"sh", "-c", "yes 0123456789abcdef | head -c 5000000"}, &durableenv.ShellExecOptions{
			Window: &durableenv.ShellOutputWindow{MaxBytes: 1000, MaxLines: 20, MinIntervalMs: 20, BytesPerSecond: 1_000_000},
			OnOutput: func(_ context.Context, text string, info durableenv.ShellOutputInfo) {
				mu.Lock()
				defer mu.Unlock()
				delivered += len(text)
				if info.Skipped != nil {
					skipped += info.Skipped.Bytes
					tail = ""
				}
				tail += text
			},
		}))
		if result.ExitCode != 0 {
			t.Fatalf("exit code %d", result.ExitCode)
		}
		mu.Lock()
		defer mu.Unlock()
		if delivered+skipped != total {
			t.Fatalf("delivered %d + skipped %d != %d", delivered, skipped, total)
		}
		if skipped <= 0 || delivered >= total/10 {
			t.Fatalf("skipped %d, delivered %d of %d", skipped, delivered, total)
		}
		// After the last skip comes everything to the end, more than the window.
		full := strings.Repeat("0123456789abcdef\n", (total+16)/17+1)[:total]
		if !strings.HasSuffix(full, tail) {
			t.Fatalf("the text after the last skip is not the end of the output: %q", tail[:min(len(tail), 80)])
		}
		if len(tail) <= 1000 && strings.Count(tail, "\n") <= 20 {
			t.Fatalf("the text after the last skip is not more than the window: %d bytes, %d lines", len(tail), strings.Count(tail, "\n"))
		}
	})

	t.Run("aborts commands cancelled right after they are sent", func(t *testing.T) {
		// upstream: packages/env/test/remote.test.ts:59
		env, _ := remoteEnvironment(t)
		started := time.Now()
		for attempt := range 20 {
			ctx, cancel := context.WithCancel(background)
			done := make(chan error, 1)
			go func() {
				_, err := env.Exec(ctx, []string{"sleep", "5"}, nil)
				done <- err
			}()
			if attempt%2 == 0 {
				cancel()
			} else {
				time.AfterFunc(time.Duration(attempt%5)*time.Millisecond, cancel)
			}
			if code := errorCodeOf(<-done); code != "aborted" {
				t.Fatalf("attempt %d: code %s, want aborted", attempt, code)
			}
			cancel()
		}
		if elapsed := time.Since(started); elapsed >= 10*time.Second {
			t.Fatalf("twenty cancelled commands took %s", elapsed)
		}
	})

	t.Run("times out a command that produces no output", func(t *testing.T) {
		// upstream: packages/env/test/remote.test.ts:73
		env, _ := remoteEnvironment(t)
		started := time.Now()
		timeout := 0.3
		_, err := env.Exec(background, []string{"sleep", "5"}, &durableenv.ShellExecOptions{Timeout: &timeout})
		if code := errorCodeOf(err); code != "timeout" {
			t.Fatalf("code %s, want timeout", code)
		}
		if elapsed := time.Since(started); elapsed >= 3*time.Second {
			t.Fatalf("the timeout took %s", elapsed)
		}
	})

	t.Run("fails requests on handles of a lost connection instead of reaching the new daemon", func(t *testing.T) {
		// upstream: packages/env/test/remote.test.ts:81
		env, connection := remoteEnvironment(t)
		mustDo(env.WriteFile(background, "a.txt", "AAA"))
		mustDo(env.WriteFile(background, "b.txt", "BBB"))
		a := must(env.OpenBinaryReader(background, "a.txt", nil))
		info := must(connection.Info(background))
		process, err := os.FindProcess(info.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if err := process.Signal(syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		// The new daemon numbers its handles from 1 again.
		b := must(env.OpenBinaryReader(background, "b.txt", nil))
		if again := must(connection.Info(background)); again.Pid == info.Pid {
			t.Fatalf("the daemon was not started again: pid %d", again.Pid)
		}
		stale, err := a.Read(background, 0, 3)
		if code := errorCodeOf(err); code != "unknown" {
			t.Fatalf("stale read: code %s (%q), want unknown", code, stale)
		}
		if got := must(b.Read(background, 0, 3)); string(got) != "BBB" {
			t.Fatalf("read %q, want BBB", got)
		}
		mustDo(a.Close(background))
		mustDo(b.Close(background))
	})

	t.Run("computes the command at each start and retries a start that failed", func(t *testing.T) {
		// upstream: packages/env/test/remote.test.ts:99
		starts := 0
		binary := daemonBinary(t)
		connection := NewConnection(ConnectionOptions{CommandFunc: func(context.Context) ([]string, error) {
			starts++
			if starts == 1 {
				return nil, errors.New("ssh gpu-box failed: Network is unreachable")
			}
			return []string{binary}, nil
		}})
		t.Cleanup(connection.Close)
		env := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:test", Cwd: t.TempDir()})
		_, err := env.ReadTextFile(background, "missing.txt")
		fileError, ok := errors.AsType[*durableenv.FileError](err)
		if !ok || fileError.Code != durableenv.FileErrorUnknown || fileError.Message != "ssh gpu-box failed: Network is unreachable" {
			t.Fatalf("offline read: %v", err)
		}
		result := must(env.Exec(background, []string{"sh", "-c", "exit 0"}, nil))
		if result.ExitCode != 0 || starts != 2 {
			t.Fatalf("exit %d after %d starts, want 0 after 2", result.ExitCode, starts)
		}
	})

	t.Run("writes and reads files larger than one transfer chunk in order", func(t *testing.T) {
		// upstream: packages/env/test/remote.test.ts:122
		env, _ := remoteEnvironment(t)
		content := make([]byte, 3_500_017)
		for index := range content {
			content[index] = byte(index * 7919 % 251)
		}
		mustDo(env.WriteFile(background, "big.bin", content))
		mustDo(env.AppendFile(background, "big.bin", content))
		read := must(env.ReadBinaryFile(background, "big.bin"))
		if len(read) != len(content)*2 || !bytes.Equal(read[:len(content)], content) || !bytes.Equal(read[len(content):], content) {
			t.Fatalf("read %d bytes, want the content twice (%d)", len(read), len(content)*2)
		}
		reader := must(env.OpenBinaryReader(background, "big.bin", nil))
		rangeBytes := must(reader.Read(background, 1_000_003, 2_000_000))
		mustDo(reader.Close(background))
		if !bytes.Equal(rangeBytes, read[1_000_003:3_000_003]) {
			t.Fatal("the byte range differs from the whole file")
		}
	})
}

// packages/env/src/remote-env.ts:774-785 createTempDir(prefix): a fresh directory named prefix plus random characters under the remote tmpdir ("tmp-" by default), and
// :760-772 remove(path, { recursive, force }): a directory needs recursive, a missing path needs force, and both default to false. The env serves its daemon connection
// (remote-env.ts:49 RemoteExecutionEnvOptions.connection) for the requests.
func TestRemoteExecutionEnvCreatesTempDirsAndRemovesPathsLikePi(t *testing.T) {
	env, connection := remoteEnvironment(t)
	ctx := context.Background()
	if env.Connection != connection {
		t.Fatal("the environment does not use the connection it was given")
	}
	info := must(connection.Info(ctx))
	prefix := "pi-env-test-"
	dir := must(env.CreateTempDir(ctx, &prefix))
	if !strings.HasPrefix(dir, filepath.Join(info.Tmpdir, prefix)) || len(dir) != len(filepath.Join(info.Tmpdir, prefix))+6 {
		t.Fatalf("temp dir %q, want %q plus six random characters", dir, filepath.Join(info.Tmpdir, prefix))
	}
	if exists := must(env.Exists(ctx, dir)); !exists {
		t.Fatalf("temp dir %q was not created", dir)
	}
	defaulted := must(env.CreateTempDir(ctx, nil))
	if !strings.HasPrefix(defaulted, filepath.Join(info.Tmpdir, "tmp-")) {
		t.Fatalf("default temp dir %q does not start with tmp-", defaulted)
	}
	mustDo(env.CreateDir(ctx, dir+"/inner", nil))
	if code := errorCodeOf(env.Remove(ctx, dir, nil)); code == "" {
		t.Fatal("removing a directory without recursive succeeded")
	}
	if code := errorCodeOf(env.Remove(ctx, dir+"/missing", nil)); code != "not_found" {
		t.Fatalf("removing a missing path without force: code %q, want not_found", code)
	}
	mustDo(env.Remove(ctx, dir+"/missing", &durableenv.RemoveOptions{Force: true}))
	mustDo(env.Remove(ctx, dir, &durableenv.RemoveOptions{Recursive: true}))
	if exists := must(env.Exists(ctx, dir)); exists {
		t.Fatal("recursive remove left the directory")
	}
	mustDo(env.Remove(ctx, defaulted, &durableenv.RemoveOptions{Recursive: true, Force: true}))
}
