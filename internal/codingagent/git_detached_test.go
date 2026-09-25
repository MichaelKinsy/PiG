package codingagent

import (
	"os/exec"
	"testing"
)

func TestResolveGitBranchDetached(t *testing.T) {
	dir := t.TempDir()
	run := func(a ...string) {
		c := exec.Command("git", a...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	run("init", "-q")
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "one")
	if b := resolveGitBranch(dir); b == "" || b == "detached" {
		t.Fatalf("on-branch want a name, got %q", b)
	}
	out, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	run("checkout", "-q", string(out[:len(out)-1]))
	if b := resolveGitBranch(dir); b != "detached" {
		t.Fatalf("detached HEAD: want %q, got %q", "detached", b)
	}
}
