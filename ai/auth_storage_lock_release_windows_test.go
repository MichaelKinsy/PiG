package ai

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// runPiStoreWithLockHandshake runs op on Pi's own store for file. While the store holds its lock, the operation calls handshake(lockPath): whileHeld then runs in this process on that lock directory before the operation continues. It returns the message the operation throws, or "" when it succeeds. auth-async and auth-sync write through FileAuthStorageBackend.withLockAsync and withLock, auth-sync-throw throws from withLock's callback, and models-write is FileModelsStore.write with the handshake inside its backend's callback.
func runPiStoreWithLockHandshake(t *testing.T, op, file string, whileHeld func(lock string)) string {
	t.Helper()
	const script = `(async () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const [root, op, file, markers] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(path.join(root, rel)).href);
  const sleeper = new Int32Array(new SharedArrayBuffer(4));
  let step = 0;
  const handshake = (lock) => {
    step++;
    fs.writeFileSync(path.join(markers, 'held-' + step + '.tmp'), lock); fs.renameSync(path.join(markers, 'held-' + step + '.tmp'), path.join(markers, 'held-' + step));
    while (!fs.existsSync(path.join(markers, 'go-' + step))) Atomics.wait(sleeper, 0, 0, 1);
  };
  const next = JSON.stringify({ written: true });
  let error = null;
  try {
    const { FileAuthStorageBackend } = await load('dist/core/auth-storage.js');
    if (op === 'auth-async') {
      await new FileAuthStorageBackend(file).withLockAsync(async () => { handshake(file + '.lock'); return { result: undefined, next }; });
    } else if (op === 'auth-sync') {
      new FileAuthStorageBackend(file).withLock(() => { handshake(file + '.lock'); return { result: undefined, next }; });
    } else if (op === 'auth-sync-throw') {
      new FileAuthStorageBackend(file).withLock(() => { handshake(file + '.lock'); throw new Error('mutate failed'); });
    } else if (op === 'models-write') {
      const { FileModelsStore } = await load('dist/core/models-store.js');
      const store = new FileModelsStore(file);
      const backend = store.storage;
      const withLockAsync = backend.withLockAsync.bind(backend);
      backend.withLockAsync = (fn, options) => withLockAsync(async (content) => { handshake(file + '.lock'); return fn(content); }, options);
      await store.write('probe', { models: [] });
    } else {
      throw new Error('unknown op ' + op);
    }
  } catch (err) {
    error = err.message;
  }
  console.log(JSON.stringify({ error }));
})()`
	markers := filepath.Join(t.TempDir(), "markers")
	if err := os.Mkdir(markers, 0o777); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), op, file, markers)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(60 * time.Second)
	for step := 1; ; {
		held := filepath.Join(markers, "held-"+strconv.Itoa(step))
		if lock, err := os.ReadFile(held); err == nil {
			whileHeld(string(lock))
			if err := os.WriteFile(filepath.Join(markers, "go-"+strconv.Itoa(step)), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			step++
			continue
		}
		select {
		case err := <-exited:
			if err != nil {
				t.Fatalf("node: %v\n%s", err, out.String())
			}
			var result struct {
				Error *string `json:"error"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &result); err != nil {
				t.Fatalf("Pi output %q: %v", out.String(), err)
			}
			if result.Error == nil {
				return ""
			}
			return *result.Error
		case <-deadline:
			_ = cmd.Process.Kill()
			t.Fatalf("Pi store did not finish: %s", out.String())
		case <-time.After(time.Millisecond):
		}
	}
}

// runPiThenReset runs op on Pi's store for a fresh file holding content, marking the lock directory for deletion while Pi holds it. Once Pi has finished, the removal completes and the file is reset to content, so PiG then runs on the same path and lock state. It returns the path, Pi's thrown message ("" for success) and the file Pi left.
func runPiThenReset(t *testing.T, op, name, content string) (path, piErr, piData string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), name)
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var removals []func()
	piErr = runPiStoreWithLockHandshake(t, op, path, func(lock string) { removals = append(removals, markAuthLockBeingRemoved(t, lock)) })
	if len(removals) != 1 {
		t.Fatalf("Pi's %s held its lock %d times, want 1", op, len(removals))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	removals[0]()
	if _, err := os.Lstat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Pi's lock directory after its removal completed: %v", err)
	}
	if content == "" {
		err = os.Remove(path)
	} else {
		err = os.WriteFile(path, []byte(content), 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
	return path, piErr, string(data)
}

// A lock directory that another process is removing makes the release's rmdir fail with EPERM (lib/lockfile.js:88-96, 268-293; see TestCheckAndReleaseReadLockBeingRemovedAsNodeStatDoesUpstream). Pi's withLockAsync ignores that unlock error (auth-storage.ts:191-198), so the write stands and the operation succeeds. withLock releases in a finally block (auth-storage.ts:109-113), so the unlock error is thrown after the write, and it replaces an error the callback threw. Each row runs Pi's store first, then PiG's on the same path, with the lock directory marked for deletion while the store holds it.
func TestAuthStorageLockReleaseFailureFollowsPiUpstream(t *testing.T) {
	const stored = `{"anthropic":{"type":"api_key","key":"stored"}}`
	written := `{"anthropic":{"type":"api_key","key":"stored"},"openai":{"type":"api_key","key":"new"}}`
	storageAt := func(t *testing.T, path string) *AuthStorage {
		storage, err := NewAuthStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		return storage
	}

	t.Run("async", func(t *testing.T) {
		path, want, piData := runPiThenReset(t, "auth-async", "auth.json", stored)
		if want != "" || piData != `{"written":true}` {
			t.Fatalf("Pi withLockAsync = %q with %s, want success and the write", want, piData)
		}
		_, err := storageAt(t, path).Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
			markAuthLockBeingRemoved(t, path+".lock")
			return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
		})
		if err != nil {
			t.Fatalf("Modify = %v, want success with the unlock error ignored", err)
		}
		authPortDisk(t, path, written)
	})
	t.Run("sync", func(t *testing.T) {
		path, want, piData := runPiThenReset(t, "auth-sync", "auth.json", stored)
		if want == "" || piData != `{"written":true}` {
			t.Fatalf("Pi withLock = %q with %s, want the unlock error after the write", want, piData)
		}
		err := storageAt(t, path).Update("openai", func(Credential, bool) (Credential, error) {
			markAuthLockBeingRemoved(t, path+".lock")
			return Credential{Type: CredentialAPIKey, Key: "new"}, nil
		})
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || err.Error() != want {
			t.Fatalf("Update = %v, want Pi's unlock error %q", err, want)
		}
		authPortDisk(t, path, written)
	})
	t.Run("sync-callback-error", func(t *testing.T) {
		path, want, piData := runPiThenReset(t, "auth-sync-throw", "auth.json", stored)
		if want == "" || want == "mutate failed" || piData != stored {
			t.Fatalf("Pi withLock = %q with %s, want the unlock error in place of the callback's", want, piData)
		}
		mutateErr := errors.New("mutate failed")
		err := storageAt(t, path).Update("openai", func(Credential, bool) (Credential, error) {
			markAuthLockBeingRemoved(t, path+".lock")
			return Credential{}, mutateErr
		})
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, mutateErr) || err.Error() != want {
			t.Fatalf("Update = %v, want Pi's unlock error %q in place of the callback's", err, want)
		}
		authPortDisk(t, path, stored)
	})
}

// FileModelsStore writes through FileAuthStorageBackend.withLockAsync (models-store.ts:128-137), which ignores an unlock error (auth-storage.ts:191-198). PiG's models store keeps the write and reports success when its release fails.
func TestFileModelsStoreLockReleaseFailureIsIgnoredUpstream(t *testing.T) {
	path, want, piData := runPiThenReset(t, "models-write", "models-store.json", "")
	if want != "" {
		t.Fatalf("Pi FileModelsStore.write = %q, want success", want)
	}
	store := NewFileModelsStore(path)
	store.lock = func(ctx context.Context, lockPath string, fn func(func() error) error) error {
		return withSidecarLock(ctx, lockPath, func(check func() error) error {
			markAuthLockBeingRemoved(t, lockPath+".lock")
			return fn(check)
		})
	}
	if err := store.Write(t.Context(), "probe", ModelsStoreEntry{Models: []AnyModel{}}); err != nil {
		t.Fatalf("Write = %v, want success with the unlock error ignored", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != piData {
		t.Fatalf("models store = %q, %v; want Pi's %q", data, err, piData)
	}
	if _, err := pilock.AcquireSync(path); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("lock directory after the failed release: %v, want it still being removed", err)
	}
}
