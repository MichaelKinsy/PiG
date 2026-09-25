package release

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestPublicationHonorsCrossProcessStoreLock(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	key := newKey(t)
	server, ref := releaseServer(t, key, releaseSpec{piglet: "porter", version: "1.0.0", target: testTarget, pigVersion: "pig-test", binary: signedBinary(t, key, "porter", "1.0.0", testTarget, "pig-test")})
	defer server.Close()
	lock, err := lockStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPullProcess$")
	command.Env = append(os.Environ(), "PIG_TEST_PULL_URL="+ref)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "store is busy") {
		t.Fatalf("publication ignored the lock: %v %s", err, output)
	}
	for path := range snapshotTree(t, os.Getenv("PIG_HOME")) {
		if strings.HasSuffix(path, ".pull") || filepath.Base(path) == "pig-porter" || filepath.Base(path) == "current" {
			t.Errorf("busy store published %s", path)
		}
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	command = exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPullProcess$")
	command.Env = append(os.Environ(), "PIG_TEST_PULL_URL="+ref)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lock was not released: %v %s", err, output)
	}
}

func TestPinnedDirectoryCannotFollowReplacedAncestor(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", home)
	root, err := openStore("artifacts", true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	parent, err := openDirectory(root, "porter/1.0.0", true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close() }()
	original := filepath.Join(home, "artifacts", "piglets", "porter")
	pinned := original + "-moved"
	err = os.Rename(original, pinned)
	if runtime.GOOS == "windows" {
		// Windows does not rename a directory while a handle beneath it is
		// open, so the pinned parent's ancestor cannot be replaced at all.
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("rename of the pinned directory's ancestor = %v, want access denied", err)
		}
		pinned = original
	} else {
		if err != nil {
			t.Fatal(err)
		}
		testenv.Symlink(t, outside, original)
	}
	name, err := stageIn(parent, bytes.NewBufferString("verified bytes"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Link(name, "pig-porter"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("replaced parent redirected publication: %v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(pinned, "1.0.0", "pig-porter")); err != nil {
		t.Fatalf("publication did not land in the pinned directory: %v", err)
	}
}
