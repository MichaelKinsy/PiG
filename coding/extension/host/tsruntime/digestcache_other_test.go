//go:build !(linux || openbsd || dragonfly || solaris || darwin || freebsd || netbsd)

package tsruntime

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Where no file identity is trusted, every start hashes the executable and writes no record.
func TestEveryStartHashesWhereNoFileIdentityIsTrusted(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pig-ts-host")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, err := sumSHA256(strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: Platform(), Provider: "test"}
	for range 2 {
		hashes := 0
		v := NewVerifier(nil)
		v.probe = func(context.Context, string) (string, error) { return "", nil }
		v.sum = func(r io.Reader) (string, error) { hashes++; return sumSHA256(r) }
		v.settled = func(fileIdentity) bool { return true }
		if _, err := verifyClosed(v, t.Context(), m, exe, digest); err != nil || hashes != 1 {
			t.Fatalf("%d hashes, err %v; want 1 hash", hashes, err)
		}
		if _, err := os.Stat(digestRecordPath(exe)); err == nil {
			t.Fatal("a record was written where no identity is trusted")
		}
	}
}
