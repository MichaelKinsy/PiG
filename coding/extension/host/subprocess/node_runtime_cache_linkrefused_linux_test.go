//go:build linux

package subprocess

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Android refuses hard links in Termux's data directory, so each launcher's Node runtime tree is a copy of the cached tree there.
func TestNodeRuntimeMaterializesWhereLinksAreRefused(t *testing.T) {
	if !testenv.RunWithHardLinksRefused(t) {
		return
	}
	syscall.Umask(0o077)
	destination := filepath.Join(t.TempDir(), "launcher")
	if err := materializeNodeRuntime(t.Context(), t.TempDir(), destination); err != nil {
		t.Fatalf("materializeNodeRuntime: %v", err)
	}
	want, err := fs.ReadFile(nodeRuntimeFS, "runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "runtime.mjs"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("launcher runtime.mjs = %d bytes (err %v), want the embedded %d bytes", len(got), err, len(want))
	}
}
