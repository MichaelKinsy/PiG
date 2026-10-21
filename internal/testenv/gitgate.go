package testenv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// GitOp is one git process the code under test starts.
type GitOp struct {
	// Dir is the checkout the process works in: its working directory, or the target directory of a clone. It is a physical path (PhysicalPath).
	Dir  string
	Args []string
}

func (op GitOp) String() string { return op.Dir + ": git " + strings.Join(op.Args, " ") }

// GitGate observes every git process the code under test starts and lets the test hold chosen ones before they run. The processes are real git: the gate puts a git on PATH that reports to the gate and then runs the git it replaced.
//
// The gate counts processes per checkout and records each one that starts while another one is still inside the same checkout. Git takes .git/index.lock for the same span, so such an overlap is the race a concurrent update loses.
type GitGate struct {
	hold   func(GitOp) <-chan struct{}
	exited func(GitOp)

	mu       sync.Mutex
	active   map[string]int
	ops      map[string][]GitOp
	overlaps []GitOp
}

// NewGitGate installs the gate for the test. hold runs when a git process starts and returns a channel the process waits on before it runs, or nil to let it run at once. exited runs after a process has finished. Either may be nil. They run on the gate's server goroutines, concurrently.
//
// It needs node, as the repository's other package-manager fixtures do. Call IsolateGit first so the real git ignores the host's configuration.
func NewGitGate(t testing.TB, hold func(GitOp) <-chan struct{}, exited func(GitOp)) *GitGate {
	t.Helper()
	nodePath := node(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	gate := &GitGate{hold: hold, exited: exited, active: map[string]int{}, ops: map[string][]GitOp{}}
	server := httptest.NewServer(gate)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	bin := t.TempDir()
	script := filepath.Join(bin, "gitgate.cjs")
	if err := os.WriteFile(script, []byte(gitGateScript), 0o600); err != nil {
		t.Fatal(err)
	}
	stub, content := "git", "#!/bin/sh\nexec \"$PIG_TEST_GIT_GATE_NODE\" \"$PIG_TEST_GIT_GATE_SCRIPT\" \"$@\"\n"
	if runtime.GOOS == "windows" {
		stub, content = "git.cmd", "@echo off\r\n\"%PIG_TEST_GIT_GATE_NODE%\" \"%PIG_TEST_GIT_GATE_SCRIPT%\" %*\r\n"
	}
	if err := os.WriteFile(filepath.Join(bin, stub), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_TEST_GIT_GATE_NODE", nodePath)
	t.Setenv("PIG_TEST_GIT_GATE_SCRIPT", script)
	t.Setenv("PIG_TEST_GIT_GATE_REAL", realGit)
	t.Setenv("PIG_TEST_GIT_GATE_URL", server.URL)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return gate
}

// Ops returns the git processes that started in checkout, in start order.
func (g *GitGate) Ops(checkout string) []GitOp {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]GitOp(nil), g.ops[PhysicalPath(checkout)]...)
}

// Overlaps returns every git process that started while another one was still inside its checkout.
func (g *GitGate) Overlaps() []GitOp {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]GitOp(nil), g.overlaps...)
}

func (g *GitGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := GitOp{Dir: PhysicalPath(r.FormValue("cwd"))}
	if err := json.Unmarshal([]byte(r.FormValue("args")), &op.Args); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(op.Args) > 0 && op.Args[0] == "clone" {
		op.Dir = PhysicalPath(op.Args[len(op.Args)-1])
	}
	switch strings.TrimPrefix(r.URL.Path, "/") {
	case "enter":
		g.mu.Lock()
		g.active[op.Dir]++
		if g.active[op.Dir] > 1 {
			g.overlaps = append(g.overlaps, op)
		}
		g.ops[op.Dir] = append(g.ops[op.Dir], op)
		g.mu.Unlock()
		if g.hold != nil {
			if wait := g.hold(op); wait != nil {
				select {
				case <-wait:
				case <-r.Context().Done():
				}
			}
		}
	case "exit":
		g.mu.Lock()
		g.active[op.Dir]--
		g.mu.Unlock()
		if g.exited != nil {
			g.exited(op)
		}
	default:
		http.NotFound(w, r)
	}
}

// PhysicalPath resolves symlinks in path, or in its parent directory when path does not exist yet, so a path the test built and the working directory a process reports compare equal (macOS /var is /private/var).
func PhysicalPath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(resolved, filepath.Base(path))
	}
	return path
}

// gitGateScript reports a git process to the gate, runs the real git with the caller's streams, then reports that it finished.
const gitGateScript = `const { spawnSync } = require('node:child_process');
const args = process.argv.slice(2);
const url = process.env.PIG_TEST_GIT_GATE_URL;
const query = new URLSearchParams({ cwd: process.cwd(), args: JSON.stringify(args) }).toString();
async function report(event) {
  const response = await fetch(url + '/' + event + '?' + query, { method: 'POST' });
  if (!response.ok) throw new Error('git gate ' + event + ': ' + response.status);
}
(async () => {
  await report('enter');
  const result = spawnSync(process.env.PIG_TEST_GIT_GATE_REAL, args, { stdio: 'inherit', env: { ...process.env, PIG_TEST_GIT_GATE_URL: '' } });
  await report('exit');
  process.exit(result.status === null ? 1 : result.status);
})().catch((error) => { console.error(error); process.exit(99); });
`

// IsolateGit points git at an empty configuration with a fixed identity and no prompts, for the test and the processes it starts.
func IsolateGit(t testing.TB) {
	t.Helper()
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"GIT_CONFIG_GLOBAL":   config,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_AUTHOR_NAME":     "pig test", "GIT_AUTHOR_EMAIL": "pig-test@example.invalid",
		"GIT_COMMITTER_NAME": "pig test", "GIT_COMMITTER_EMAIL": "pig-test@example.invalid",
	} {
		t.Setenv(key, value)
	}
}

// RunGit runs git in dir and returns its trimmed output; a failure fails the test.
func RunGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// SubdirectoryOrigin is a bare repository of three commits. Every commit holds the directories a and b; Commits[1] is tagged v1 and Commits[2] is tagged v2, so a Package that selects a subdirectory at v1 and another at v2 resolve to one repository and two commits.
type SubdirectoryOrigin struct {
	// URL is the repository as a Git Package source spells it after "git:" (file://localhost/<path>).
	URL     string
	Commits [3]string
}

// NewSubdirectoryOrigin creates the repository under a new directory named name. The directory is short: a managed checkout's path repeats the origin's path below the agent directory (git/localhost/<path>), and a test directory named for a long test twice in one path passes the 260 characters Windows allows a process's working directory.
func NewSubdirectoryOrigin(t testing.TB, name string) SubdirectoryOrigin {
	t.Helper()
	root := PhysicalPath(ShortTempDir(t, "o"))
	work, bare := filepath.Join(root, name+"-work"), filepath.Join(root, name+".git")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	RunGit(t, work, "init", "-q", "-b", "main")
	var origin SubdirectoryOrigin
	for i := range origin.Commits {
		for _, sub := range []string{"a", "b"} {
			if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(work, sub, "file.txt"), []byte(name+" "+sub+" "+string(rune('0'+i))+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		RunGit(t, work, "add", "-A")
		RunGit(t, work, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "commit "+string(rune('0'+i)))
		origin.Commits[i] = RunGit(t, work, "rev-parse", "HEAD")
		if i > 0 {
			RunGit(t, work, "-c", "tag.gpgsign=false", "tag", "v"+string(rune('0'+i)))
		}
	}
	RunGit(t, root, "clone", "-q", "--bare", work, bare)
	origin.URL = "file://localhost/" + strings.TrimPrefix(filepath.ToSlash(bare), "/")
	return origin
}

// CloneAt clones the origin to dest and detaches it at commit, as a managed checkout that an update has to move.
func (o SubdirectoryOrigin) CloneAt(t testing.TB, dest, commit string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := o.URL
	if runtime.GOOS == "windows" {
		// Git for Windows reads file://localhost/C:/... as the UNC path //localhost/C:/...; the Package manager clones the same repository as file:///C:/... (packagemanager.GitCloneRepo).
		repo = strings.Replace(repo, "file://localhost/", "file:///", 1)
	}
	RunGit(t, filepath.Dir(dest), "clone", "-q", repo, dest)
	RunGit(t, dest, "checkout", "-q", "--detach", commit)
}
