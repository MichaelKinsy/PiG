package ai

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// markAuthLockBeingRemoved leaves dir delete-pending, as another process's RemoveDirectoryW does between marking it and closing its handle. The returned func, which also runs when the test ends, closes the handle and so completes the removal.
func markAuthLockBeingRemoved(t *testing.T, dir string) func() {
	t.Helper()
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.DELETE|windows.SYNCHRONIZE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	finish := func() {
		if !closed {
			closed = true
			_ = windows.CloseHandle(handle)
		}
	}
	t.Cleanup(finish)
	var flags [4]byte // FILE_DISPOSITION_INFO_EX.Flags
	binary.LittleEndian.PutUint32(flags[:], windows.FILE_DISPOSITION_DELETE|windows.FILE_DISPOSITION_POSIX_SEMANTICS)
	if err := windows.SetFileInformationByHandle(handle, windows.FileDispositionInfoEx, &flags[0], uint32(len(flags))); err != nil {
		t.Fatal(err)
	}
	return finish
}

// Pi's FileAuthStorageBackend rethrows the lock's fs error unchanged (auth-storage.ts:134-141), so a user sees Node's message for an auth lock directory that another process is removing: "EPERM: operation not permitted, mkdir '<auth.json.lock>'". PiG's auth storage error carries the same message.
func TestAuthStorageLockErrorCarriesNodeMessageUpstream(t *testing.T) {
	storage, path := authPortFile(t, `{"anthropic":{"type":"api_key","key":"stored"}}`)
	if err := os.Mkdir(path+".lock", 0o777); err != nil {
		t.Fatal(err)
	}
	markAuthLockBeingRemoved(t, path+".lock")
	message := "EPERM: operation not permitted, mkdir '" + path + ".lock'"

	backend, err := filepath.Abs(filepath.Join("..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "core", "auth-storage.js"))
	if err != nil {
		t.Fatal(err)
	}
	const script = `(async () => {
  const [backend, file] = process.argv.slice(1);
  const { FileAuthStorageBackend } = await import(require('node:url').pathToFileURL(backend));
  try {
    await new FileAuthStorageBackend(file).withLockAsync(async () => ({ result: undefined, next: '{}' }));
    console.log('wrote');
  } catch (err) {
    console.log(err.message);
  }
})()`
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, backend, path).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != message {
		t.Fatalf("Pi auth storage error = %q, want %q", got, message)
	}

	_, err = storage.Modify(context.Background(), "openai", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
	})
	if err == nil || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("modify error = %v, want the lock's ERROR_ACCESS_DENIED", err)
	}
	if !strings.HasSuffix(err.Error(), message) {
		t.Fatalf("modify error = %q, want it to end with Pi's %q", err.Error(), message)
	}
	authPortDisk(t, path, `{"anthropic":{"type":"api_key","key":"stored"}}`)
}
