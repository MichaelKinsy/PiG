package ai

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// piCodingAgentPackage is Pi 0.87.1's pinned pi-coding-agent package.
func piCodingAgentPackage(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// runPiLockedStore runs one operation of Pi's own store on file and returns the message the operation throws, or "ok". auth-sync and auth-async are FileAuthStorageBackend.withLock and withLockAsync, logout is pi-ai's Models.logout over AuthStorage.create(file), and models-write is FileModelsStore.write.
func runPiLockedStore(t *testing.T, op, file string) string {
	t.Helper()
	const script = `(async () => {
  const [root, op, file] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  try {
    if (op === 'auth-sync') {
      const { FileAuthStorageBackend } = await load('dist/core/auth-storage.js');
      new FileAuthStorageBackend(file).withLock(() => ({ result: undefined, next: '{}' }));
    } else if (op === 'auth-async') {
      const { FileAuthStorageBackend } = await load('dist/core/auth-storage.js');
      await new FileAuthStorageBackend(file).withLockAsync(async () => ({ result: undefined, next: '{}' }));
    } else if (op === 'logout') {
      const { AuthStorage } = await load('dist/core/auth-storage.js');
      const { createModels } = await load('node_modules/@earendil-works/pi-ai/dist/models.js');
      await createModels({ credentials: AuthStorage.create(file) }).logout('openai');
    } else if (op === 'models-write') {
      const { FileModelsStore } = await load('dist/core/models-store.js');
      await new FileModelsStore(file).write('probe', { models: [] });
    } else {
      throw new Error('unknown op ' + op);
    }
    console.log('ok');
  } catch (err) {
    console.log(err.message);
  }
})()`
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), op, file).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// staleNonEmptyLock leaves a stale lock directory that holds a file, so proper-lockfile's removal of it fails with ENOTEMPTY (lib/lockfile.js:71-79,88-96).
func staleNonEmptyLock(t *testing.T, lock string) {
	t.Helper()
	if err := os.Mkdir(lock, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lock, "entry"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
}

// Pi's FileAuthStorageBackend rethrows proper-lockfile's error unchanged from both lock paths (auth-storage.ts:82,141), and pi-ai's Models.logout wraps it only in "Credential store delete failed for <provider>: <message>" (models.ts:619-628, resolve.ts:36-42). PiG's auth storage returns the same error, not one prefixed with "auth: acquire lock: ". Each row runs Pi's own store on the same lock state first and compares the exact message.
func TestAuthStorageLockErrorIsProperLockfileErrorUpstream(t *testing.T) {
	const stored = `{"anthropic":{"type":"api_key","key":"stored"}}`
	t.Run("sync-held", func(t *testing.T) {
		storage, path := authPortFile(t, stored)
		if err := os.Mkdir(path+".lock", 0o777); err != nil {
			t.Fatal(err)
		}
		want := runPiLockedStore(t, "auth-sync", path)
		err := storage.Set("openai", Credential{Type: CredentialAPIKey, Key: "new"})
		if !errors.Is(err, pilock.ErrLocked) {
			t.Fatalf("Set = %v, want ErrLocked", err)
		}
		if err.Error() != want {
			t.Fatalf("Set error = %q, want Pi's %q", err.Error(), want)
		}
		authPortDisk(t, path, stored)
	})
	t.Run("async-stale-nonempty", func(t *testing.T) {
		storage, path := authPortFile(t, stored)
		staleNonEmptyLock(t, path+".lock")
		want := runPiLockedStore(t, "auth-async", path)
		_, err := storage.Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
			return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
		})
		if err == nil || nodeerrno.ErrorCode(err) != "ENOTEMPTY" {
			t.Fatalf("Modify = %v, want rmdir's ENOTEMPTY", err)
		}
		if err.Error() != want {
			t.Fatalf("Modify error = %q, want Pi's %q", err.Error(), want)
		}
		authPortDisk(t, path, stored)
	})
	t.Run("logout", func(t *testing.T) {
		storage, path := authPortFile(t, stored)
		staleNonEmptyLock(t, path+".lock")
		want := runPiLockedStore(t, "logout", path)
		models := CreateModels(CreateModelsOptions{Credentials: storage})
		t.Cleanup(models.Close)
		err := models.Logout(context.Background(), "openai")
		var modelsErr *ModelsError
		if !errors.As(err, &modelsErr) || modelsErr.Code != ModelsErrorAuth || nodeerrno.ErrorCode(err) != "ENOTEMPTY" {
			t.Fatalf("Logout = %v, want an auth ModelsError caused by rmdir's ENOTEMPTY", err)
		}
		if err.Error() != want {
			t.Fatalf("Logout error = %q, want Pi's %q", err.Error(), want)
		}
		authPortDisk(t, path, stored)
	})
}

// FileModelsStore locks through FileAuthStorageBackend.withLockAsync (models-store.ts:55,128), which rethrows proper-lockfile's error unchanged. PiG's models store returns the same error, not one prefixed with "models store: acquire lock: ".
func TestFileModelsStoreLockErrorIsProperLockfileErrorUpstream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-store.json")
	staleNonEmptyLock(t, path+".lock")
	want := runPiLockedStore(t, "models-write", path)
	err := NewFileModelsStore(path).Write(t.Context(), "probe", ModelsStoreEntry{Models: []json.RawMessage{}})
	if err == nil || nodeerrno.ErrorCode(err) != "ENOTEMPTY" {
		t.Fatalf("Write = %v, want rmdir's ENOTEMPTY", err)
	}
	if err.Error() != want {
		t.Fatalf("Write error = %q, want Pi's %q", err.Error(), want)
	}
}

// withLockAsync's throwIfCompromised throws the error its lock's heartbeat reported (auth-storage.ts:168-189), not an abort, and its release, which then fails with ERELEASED, is ignored (auth-storage.ts:191-198). Here the lock directory's mtime changes while the callback runs, so the heartbeat reports "Unable to update lock within the stale threshold" (lib/lockfile.js:129-138). PiG's Modify returns that compromise alone and writes nothing. The heartbeat interval is shortened on both sides: PiG's through the acquire seam, Pi's by running proper-lockfile's timers of a second or more after 20 ms.
func TestAuthStorageLockCompromisedDuringModifyUpstream(t *testing.T) {
	const stored = `{"anthropic":{"type":"api_key","key":"stored"}}`
	piFile := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(piFile, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	const script = `(async () => {
  const fs = require('node:fs');
  const [root, file] = process.argv.slice(1);
  const realSetTimeout = setTimeout;
  globalThis.setTimeout = (fn, ms, ...args) => realSetTimeout(fn, ms >= 1000 ? 20 : ms, ...args);
  const { FileAuthStorageBackend } = await import(require('node:url').pathToFileURL(require('node:path').join(root, 'dist/core/auth-storage.js')).href);
  try {
    await new FileAuthStorageBackend(file).withLockAsync(async () => {
      const later = new Date(Date.now() + 3600e3);
      fs.utimesSync(file + '.lock', later, later);
      await new Promise((resolve) => realSetTimeout(resolve, 500));
      return { result: undefined, next: '{}' };
    });
    console.log('ok');
  } catch (err) {
    console.log(err.message);
  }
})()`
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), piFile).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	want := strings.TrimSpace(string(out))
	if data, err := os.ReadFile(piFile); err != nil || string(data) != stored {
		t.Fatalf("Pi wrote under a compromised lock: %q, %v", data, err)
	}

	storage, path := authPortFile(t, stored)
	var held *pilock.Lock
	stubAuthFileLockAcquire(t, func(ctx context.Context, name string) (*pilock.Lock, error) {
		lock, err := pilock.AcquireWithOptions(ctx, name, pilock.AcquireOptions{Stale: 30 * time.Second, Update: 20 * time.Millisecond, Retry: time.Millisecond})
		held = lock
		return lock, err
	})
	_, err = storage.Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
		later := time.Now().Add(time.Hour)
		if err := os.Chtimes(path+".lock", later, later); err != nil {
			return nil, err
		}
		select {
		case <-held.Context().Done():
		case <-time.After(10 * time.Second):
			t.Error("heartbeat did not report the compromise")
		}
		return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
	})
	var compromise *pilock.CompromisedError
	if !errors.As(err, &compromise) || errors.Is(err, context.Canceled) {
		t.Fatalf("Modify = %v, want the heartbeat's compromise", err)
	}
	if err.Error() != want {
		t.Fatalf("Modify error = %q, want Pi's %q", err.Error(), want)
	}
	authPortDisk(t, path, stored)
}

// After the write, Pi's withLockAsync checks only throwIfCompromised, not the signal (auth-storage.ts:184-189), so an abort that arrives while auth.json is being written does not fail the committed modify: it returns the credential, and AuthStorage.modify records the new state (auth-storage.ts:456-470). PiG's Modify and Delete do the same. The oracle aborts from inside Pi's writeFileSync.
func TestAuthStorageLockAbortDuringWriteKeepsTheCommitUpstream(t *testing.T) {
	const stored = `{"anthropic":{"type":"api_key","key":"stored"}}`
	piFile := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(piFile, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	const script = `(async () => {
  const fs = require('node:fs');
  const [root, file] = process.argv.slice(1);
  const controller = new AbortController();
  const writeFileSync = fs.writeFileSync;
  fs.writeFileSync = (...args) => { if (args[0] === file) controller.abort(); return writeFileSync(...args); };
  require('node:module').syncBuiltinESMExports();
  const { AuthStorage } = await import(require('node:url').pathToFileURL(require('node:path').join(root, 'dist/core/auth-storage.js')).href);
  try {
    const result = await AuthStorage.create(file).modify('openai', async () => ({ type: 'api_key', key: 'new' }), { signal: controller.signal });
    console.log(JSON.stringify({ aborted: controller.signal.aborted, result }));
  } catch (err) {
    console.log(JSON.stringify({ aborted: controller.signal.aborted, error: err.message }));
  }
})()`
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), piFile).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != `{"aborted":true,"result":{"type":"api_key","key":"new"}}` {
		t.Fatalf("Pi modify aborted during its write: %s, want the credential", got)
	}
	written := `{"anthropic":{"type":"api_key","key":"stored"},"openai":{"type":"api_key","key":"new"}}`
	authPortDisk(t, piFile, written)

	storage, path := authPortFile(t, stored)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	previous := writeAuthFile
	writeAuthFile = func(name string, data []byte, perm os.FileMode) error {
		cancel()
		return previous(name, data, perm)
	}
	t.Cleanup(func() { writeAuthFile = previous })
	result, err := storage.Modify(ctx, "openai", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
	})
	if err != nil || result == nil || result.Key != "new" {
		t.Fatalf("Modify aborted during its write = %+v, %v; want the credential", result, err)
	}
	authPortDisk(t, path, written)
	storage.read.mu.Lock()
	cached, ok := storage.read.creds.values["openai"]
	storage.read.mu.Unlock()
	if !ok || cached.Key != "new" {
		t.Fatalf("read state after the committed write = %+v, %v; want the new credential", cached, ok)
	}

	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	if err := storage.Delete(ctx, "openai"); err != nil {
		t.Fatalf("Delete aborted during its write = %v, want success", err)
	}
	authPortDisk(t, path, stored)
}

// FileModelsStore goes through FileAuthStorageBackend.withLockAsync, which creates the missing store as "{}" before it waits for the lock (auth-storage.ts:158-163). A write aborted while another process holds the lock therefore leaves "{}" behind. PiG's models store creates the file at the same point.
func TestFileModelsStoreLockWaitAbortedAfterCreatingTheStoreUpstream(t *testing.T) {
	const script = `(async () => {
  const [root, file] = process.argv.slice(1);
  const { FileModelsStore } = await import(require('node:url').pathToFileURL(require('node:path').join(root, 'dist/core/models-store.js')).href);
  const controller = new AbortController();
  setTimeout(() => controller.abort(), 50);
  try {
    await new FileModelsStore(file).write('probe', { models: [] }, { signal: controller.signal });
    console.log('wrote');
  } catch (err) {
    console.log(err.name);
  }
})()`
	piFile := filepath.Join(t.TempDir(), "models-store.json")
	if err := os.Mkdir(piFile+".lock", 0o777); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), piFile).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	// The abort lands in the retry sleep, whose timers/promises rejection is an AbortError, or at a throwIfAborted checkpoint, which throws the signal's reason; abort() without a reason makes that reason an AbortError too, so the outcome does not depend on the machine's timing.
	if got := strings.TrimSpace(string(out)); got != "AbortError" {
		t.Fatalf("Pi FileModelsStore.write with the lock held = %q, want the abort's AbortError", got)
	}
	piData, err := os.ReadFile(piFile)
	if err != nil || string(piData) != "{}" {
		t.Fatalf("Pi's store after the aborted write = %q, %v; want {}", piData, err)
	}

	path := filepath.Join(t.TempDir(), "models-store.json")
	if err := os.Mkdir(path+".lock", 0o777); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err = NewFileModelsStore(path).Write(ctx, "probe", ModelsStoreEntry{Models: []json.RawMessage{}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Write with the lock held = %v, want the deadline", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(piData) {
		t.Fatalf("store after the aborted write = %q, %v; want Pi's %q", data, err, piData)
	}
}
