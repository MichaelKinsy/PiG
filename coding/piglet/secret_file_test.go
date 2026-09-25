package piglet

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// writeOwnerOnlySecret writes data to the new file path, readable by its owner
// only: mode 0600, or on Windows a protected DACL for the current user.
func writeOwnerOnlySecret(t *testing.T, path string, data []byte) {
	t.Helper()
	file, err := ownerfile.CreateNew(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func requireSecretFileOwnerOnly(t *testing.T, path string, want bool) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	ownerOnly, err := ownerfile.OwnerOnly(path, info)
	if err != nil {
		t.Fatal(err)
	}
	if ownerOnly != want {
		t.Fatalf("ownerfile.OwnerOnly(%s) = %t, want %t", path, ownerOnly, want)
	}
}

// A secret file must be readable by its owner only: a file others can read
// is refused on every platform (mode bits, or on Windows the DACL).
func TestSecretFileMustBeOwnerOnly(t *testing.T) {
	file := filepath.Join(t.TempDir(), "secret")
	writeOwnerOnlySecret(t, file, []byte("FILE-SECRET\n"))
	requireSecretFileOwnerOnly(t, file, true)

	testenv.GrantOthersRead(t, file)
	requireSecretFileOwnerOnly(t, file, false)
	p, err := ParseBytes([]byte("name: secrets\nsecrets:\n  - name: file-secret\n    from: {file: " + file + "}\nagentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: file-secret\n      target: {env: FILE_TOKEN}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRequiredSecrets(context.Background(), p); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("resolve secret others can read: error = %v, want the owner-only refusal", err)
	}
}
