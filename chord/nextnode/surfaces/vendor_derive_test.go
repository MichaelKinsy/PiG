package surfaces

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The derived Pi tree a vendored chord copy is taken from (automation/gen/vendor-chord-derive.sh; both vendoring scripts
// and `make vendor-chord-carried-check` use it): a carried commit is a whole Pi commit. A conflict under a root the copies
// read fails; a conflict outside them leaves that path as the tree had it, takes the rest of the commit and prints the path.

// deriveRepo is a small Pi-shaped repository: `pin`, a later commit the port does not carry (it changes files of apps/ and
// the lock), and commits to carry.
type deriveRepo struct {
	t    *testing.T
	dir  string
	tool string
}

func newDeriveRepo(t *testing.T) *deriveRepo {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("automation/gen/vendor-chord-derive.sh is a bash script of the vendoring tools, which run on Linux and macOS")
	}
	tool, err := filepath.Abs(filepath.Join("..", "..", "..", "automation", "gen", "vendor-chord-derive.sh"))
	if err != nil {
		t.Fatal(err)
	}
	r := &deriveRepo{t: t, dir: filepath.Join(t.TempDir(), "pi"), tool: tool}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.git("init", "--quiet", "--initial-branch=main")
	return r
}

func (r *deriveRepo) command(dir, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_AUTHOR_NAME=derive", "GIT_AUTHOR_EMAIL=derive@localhost", "GIT_COMMITTER_NAME=derive", "GIT_COMMITTER_EMAIL=derive@localhost")
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err = cmd.Run()
	return out.String(), errs.String(), err
}

func (r *deriveRepo) git(args ...string) string {
	r.t.Helper()
	out, errs, err := r.command(r.dir, "git", args...)
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, errs)
	}
	return strings.TrimSpace(out)
}

// commit writes files and commits them; it returns the commit.
func (r *deriveRepo) commit(subject string, files map[string]string) string {
	r.t.Helper()
	for path, text := range files {
		full := filepath.Join(r.dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	r.git("add", "--all")
	r.git("commit", "--quiet", "-m", subject)
	return r.git("rev-parse", "HEAD")
}

// derive runs the tool: a new worktree at commit with carried picked onto it.
func (r *deriveRepo) derive(commit string, carried ...string) (tree, stdout, stderr string, err error) {
	tree = filepath.Join(r.t.TempDir(), "tree")
	stdout, stderr, err = r.command(r.dir, "bash", r.tool, r.dir, tree, commit, strings.Join(carried, ","))
	return tree, stdout, stderr, err
}

func (r *deriveRepo) file(tree, path string) string {
	text, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(path)))
	if os.IsNotExist(err) {
		return "(absent)"
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return string(text)
}

func TestTheDerivedTreeLeavesAConflictOutsideTheCopiedRootsAsItWasAndPrintsThePath(t *testing.T) {
	r := newDeriveRepo(t)
	pin := r.commit("pin", map[string]string{
		"packages/chord/src/a.ts": "one\n",
		"apps/p2/notes.md":        "one\n",
		"package-lock.json":       "{}\n",
	})
	r.commit("not carried: apps only", map[string]string{"apps/p2/notes.md": "two\n", "apps/p2/later.md": "later\n"})
	carried := r.commit("carried: chord and apps", map[string]string{
		"packages/chord/src/a.ts": "two\n",
		"apps/p2/notes.md":        "three\n",
		"apps/p2/later.md":        "later, changed\n",
		"apps/p2/added.ts":        "added\n",
	})
	tree, stdout, stderr, err := r.derive(pin, carried)
	if err != nil {
		t.Fatalf("derive: %v\n%s", err, stderr)
	}
	if want := "left " + carried + " apps/p2/later.md\nleft " + carried + " apps/p2/notes.md\n"; stdout != want {
		t.Fatalf("printed %q, want %q", stdout, want)
	}
	for path, want := range map[string]string{
		"packages/chord/src/a.ts": "two\n",    // the carried commit's change under a root
		"apps/p2/notes.md":        "one\n",    // conflicted outside: as the tree had it
		"apps/p2/later.md":        "(absent)", // changed by the commit, absent in the tree: stays absent
		"apps/p2/added.ts":        "added\n",  // no conflict: the commit's
	} {
		if got := r.file(tree, path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	status, _, err := r.command(tree, "git", "status", "--porcelain")
	subject, _, _ := r.command(tree, "git", "log", "-1", "--format=%s")
	if err != nil || status != "" || strings.TrimSpace(subject) != "carried: chord and apps" {
		t.Fatalf("the pick is not committed: status %q, head %q, %v", status, subject, err)
	}
}

func TestTheDerivedTreeFailsOnAConflictInsideACopiedRoot(t *testing.T) {
	for _, root := range []string{"packages/chord/src/a.ts", "packages/schema/src/index.ts", "packages/telemetry/src/index.ts", "package-lock.json"} {
		t.Run(root, func(t *testing.T) {
			r := newDeriveRepo(t)
			pin := r.commit("pin", map[string]string{root: "one\n", "apps/p2/notes.md": "one\n"})
			r.commit("not carried", map[string]string{root: "two\n", "apps/p2/notes.md": "two\n"})
			carried := r.commit("carried", map[string]string{root: "three\n", "apps/p2/notes.md": "three\n"})
			_, stdout, stderr, err := r.derive(pin, carried)
			if err == nil {
				t.Fatalf("derive passed with a conflict in %s; printed %q", root, stdout)
			}
			if !strings.Contains(stderr, "does not cherry-pick") || !strings.Contains(stderr, root) || strings.Contains(stdout, "left ") {
				t.Fatalf("stderr = %q, stdout = %q, want the refusal naming %s and no path left", stderr, stdout, root)
			}
		})
	}
}
