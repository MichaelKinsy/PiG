//go:build linux

package piglet

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Android refuses hard links in Termux's data directory, so `pig piglet add` publishes each file by a no-replace rename there; under Android's common umask 077 the files keep their modes and no stage remains.
func TestCommitPigletAddFilesPublishesWhereLinksAreRefused(t *testing.T) {
	if !testenv.RunWithHardLinksRefused(t) {
		return
	}
	syscall.Umask(0o077)
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	base := filepath.Join(home, "piglets", "alpha.source", "commit")
	manifest, launcher := filepath.Join(base, "piglet.toml"), filepath.Join(base, "bin", "run")
	files := map[string][]byte{manifest: []byte("name = \"alpha\"\n"), launcher: []byte("#!/bin/sh\n")}
	if err := commitPigletAddFiles(files, map[string]os.FileMode{launcher: 0o755}); err != nil {
		t.Fatalf("commitPigletAddFiles: %v", err)
	}
	for path, want := range map[string]struct {
		data string
		mode os.FileMode
	}{manifest: {"name = \"alpha\"\n", 0o644}, launcher: {"#!/bin/sh\n", 0o755}} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want.data {
			t.Fatalf("%s = %q (err %v), want %q", path, data, err, want.data)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want.mode {
			t.Fatalf("%s mode = %v (err %v), want %v", path, info.Mode().Perm(), err, want.mode)
		}
		stages, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".piglet-install-*.stage"))
		if len(stages) != 0 {
			t.Fatalf("stages remain beside %s: %v", path, stages)
		}
	}
}
