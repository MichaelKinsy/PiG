package nodefs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Access is the attribute check of Node's fs.accessSync(F_OK), which a runnable Microsoft Store alias passes. An alias cannot be
// made here, so a junction whose target is gone stands in: like an alias it is a reparse point whose target a follow-up stat
// cannot reach, so os.Stat and Exists reject what Access accepts.
func TestAccessAcceptsReparsePointWithMissingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(link); err == nil {
		t.Fatal("os.Stat reached a junction whose target is gone")
	}
	if !Access(link) {
		t.Error("Access rejected a reparse point whose target is gone")
	}
	if Exists(link) {
		t.Error("Exists accepted a reparse point whose target is gone")
	}
	if Access(filepath.Join(dir, "absent")) {
		t.Error("Access accepted a missing path")
	}
}
