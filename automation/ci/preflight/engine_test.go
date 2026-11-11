// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub serves the parts of the REST API a run reads and writes, for open PRs of o/r. Comments it is sent are by
// the bot; others can be put in its list by hand.
type fakeGitHub struct {
	mu       sync.Mutex
	bodies   map[int]string
	broken   map[int]bool
	hits     map[string]int
	statuses map[string]string
	// comments holds each PR's comments in order.
	comments map[int][]fakeComment
	nextID   int64
	// tip is where main is; empty is "base". tipDown makes reading it fail.
	tip     string
	tipDown bool
	// unknown answers mergeable null, as GitHub does before it computed it.
	unknown bool
}

type fakeComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

func newFakeGitHub(bodies map[int]string) *fakeGitHub {
	return &fakeGitHub{bodies: bodies, broken: map[int]bool{}, hits: map[string]int{}, statuses: map[string]string{}, comments: map[int][]fakeComment{}}
}

// preflightComment is the bot's pre-flight comment on a PR, "" when there is none.
func (g *fakeGitHub) preflightComment(pr int) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.comments[pr] {
		if c.User.Login == defaultBot && strings.HasPrefix(c.Body, commentMarker) {
			return c.Body
		}
	}
	return ""
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hits[r.Method+" "+r.URL.Path]++
	var pr int
	var id int64
	var rest string
	if _, err := fmt.Sscanf(r.URL.Path, "/repos/o/r/pulls/%d", &pr); err == nil {
		rest = strings.TrimPrefix(r.URL.Path, fmt.Sprintf("/repos/o/r/pulls/%d", pr))
	}
	switch {
	case r.URL.Path == "/repos/o/r/pulls":
		var open []map[string]int
		for n := range g.bodies {
			open = append(open, map[string]int{"number": n})
		}
		_ = json.NewEncoder(w).Encode(open)
	case r.URL.Path == "/repos/o/r/git/ref/heads/main" && g.tipDown:
		http.Error(w, "gone", http.StatusNotFound)
	case r.URL.Path == "/repos/o/r/git/ref/heads/main":
		tip := g.tip
		if tip == "" {
			tip = "base"
		}
		_, _ = fmt.Fprintf(w, `{"object":{"sha":%q}}`, tip)
	case pr > 0 && g.broken[pr]:
		http.Error(w, "down", http.StatusBadGateway)
	case pr > 0 && rest == "":
		var mergeable any = true
		if g.unknown {
			mergeable = nil
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": pr, "state": "open", "body": g.bodies[pr], "mergeable": mergeable, "commits": 1, "changed_files": 1,
			"head": map[string]string{"sha": fmt.Sprintf("head%d", pr)}, "base": map[string]string{"sha": "old-base", "ref": "main"},
		})
	case rest == "/files":
		_, _ = fmt.Fprint(w, `[{"filename":"a.go","status":"modified","sha":"blob","patch":"@@ -1 +1,2 @@\n x\n+y"}]`)
	case rest == "/commits":
		_, _ = fmt.Fprint(w, `[{"sha":"c1c1c1c1c1","commit":{"message":"a: b","verification":{"verified":true,"reason":"valid"}}}]`)
	case strings.HasPrefix(r.URL.Path, "/repos/o/r/compare/"):
		_, _ = fmt.Fprint(w, `{"status":"ahead","ahead_by":1,"behind_by":0}`)
	case strings.HasPrefix(r.URL.Path, "/repos/o/r/statuses/"):
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.statuses[strings.TrimPrefix(r.URL.Path, "/repos/o/r/statuses/")] = body["state"]
	case strings.HasSuffix(r.URL.Path, "/comments"):
		_, _ = fmt.Sscanf(r.URL.Path, "/repos/o/r/issues/%d/comments", &pr)
		if g.broken[-pr] {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(append([]fakeComment{}, g.comments[pr]...))
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.nextID++
		c := fakeComment{ID: g.nextID, Body: body["body"]}
		c.User.Login = defaultBot
		g.comments[pr] = append(g.comments[pr], c)
		_, _ = fmt.Fprintf(w, `{"id":%d}`, c.ID)
	case r.Method == http.MethodPatch:
		_, _ = fmt.Sscanf(r.URL.Path, "/repos/o/r/issues/comments/%d", &id)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		for n, cs := range g.comments {
			for i := range cs {
				if cs[i].ID == id {
					g.comments[n][i].Body = body["body"]
					_, _ = fmt.Fprint(w, `{}`)
					return
				}
			}
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

const engineConfig = `
engine-revision: 1
checks:
  - name: template
    when: {always: true}
    stage: cheap
    reads: [pull]
    rule: body-fields
    with: {fields: [{name: Fix, pattern: '^(?:yes|no)$', help: '` + "`Fix: yes` or `Fix: no`" + `'}]}
  - name: marker
    when: {always: true}
    stage: scan
    scan:
      input: added
      run: [marker, '{input}']
      exit: {clean: [0]}
      report: {in: stdout, format: lines, pattern: '(?P<message>.+)'}
`

func TestRunBatch(t *testing.T) {
	cfg, err := loadConfig([]byte(engineConfig))
	if err != nil {
		t.Fatal(err)
	}
	g := newFakeGitHub(map[int]string{1: "Fix: no", 2: "no template", 3: "Fix: yes"})
	g.broken[3] = true
	server := httptest.NewServer(g)
	defer server.Close()

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	// The scan tool leaves a marker named after the PR it ran for, so the test sees which PRs reached the scans.
	fakeTool(t, bin, "marker", `touch "`+dir+`/scanned-$(basename "$(dirname "$1")")"`)
	o := runOptions{apiBase: server.URL, token: "tok", repo: "o/r", root: dir, bin: bin, work: filepath.Join(dir, "work"), jobs: 2, bot: defaultBot}

	var out strings.Builder
	// A PR whose API is down is reported on its own; the run stays green.
	_, err = runBatch(context.Background(), cfg, o, &out)
	if err != nil || !strings.Contains(out.String(), "the pre-flight could not run for 1 of 3 PR(s)") {
		t.Fatalf("err = %v, log:\n%s", err, out.String())
	}
	if g.statuses["head1"] != "success" || g.statuses["head2"] != "failure" || g.preflightComment(1) == "" || g.preflightComment(2) == "" {
		t.Fatalf("the PRs beside the broken one were not recorded: statuses %v", g.statuses)
	}
	if st := parseState(g.preflightComment(1)); st == nil || st.Head != "head1" || st.Base != "base" || st.Checks["template"].Result != resultPass {
		t.Fatalf("the state in PR #1's comment: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, "scanned-1")); err != nil {
		t.Error("PR #1 passed its cheap checks but its scan did not run")
	}
	if _, err := os.Stat(filepath.Join(dir, "scanned-2")); err == nil {
		t.Error("PR #2 failed a cheap check but its scan ran")
	}
	for _, want := range []string{"PR #1: pass", "PR #2: fail", "PR #3: error", "::group::PR #3", "could not run"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the log lacks %q:\n%s", want, out.String())
		}
	}

	// A second run with nothing changed reuses PR #1's verdicts from its comment: no data read, no scan, nothing sent.
	if err := os.Remove(filepath.Join(dir, "scanned-1")); err != nil {
		t.Fatal(err)
	}
	g.broken = map[int]bool{}
	before := g.hits["GET /repos/o/r/pulls/1/files"]
	sent := g.hits["PATCH /repos/o/r/issues/comments/1"] + g.hits["POST /repos/o/r/statuses/head1"]
	o.prs = []int{1}
	out.Reset()
	if _, err := runBatch(context.Background(), cfg, o, &out); err != nil {
		t.Fatal(err)
	}
	if g.hits["GET /repos/o/r/pulls/1/files"] != before || !strings.Contains(out.String(), "mode reuse") {
		t.Fatalf("PR #1 was fetched again; log:\n%s", out.String())
	}
	if g.hits["PATCH /repos/o/r/issues/comments/1"]+g.hits["POST /repos/o/r/statuses/head1"] != sent || !strings.Contains(out.String(), "nothing changed, nothing sent") || strings.Contains(out.String(), "could not run") {
		t.Fatalf("an unchanged PR was written; log:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "scanned-1")); err == nil {
		t.Error("the reused PR ran its scan again")
	}

	// A reused PR whose scans are still open does not run them, even when scans are forced.
	o.prs, o.forceScans = []int{2}, true
	out.Reset()
	if _, err := runBatch(context.Background(), cfg, o, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "scanned-2")); err == nil || !strings.Contains(out.String(), "mode reuse") {
		t.Errorf("a reused PR ran its open scans; log:\n%s", out.String())
	}

	// Main moving, or the description changing, decides the PR again; the pull's base.sha does not move.
	o.prs, o.forceScans = []int{1}, false
	for _, change := range []func(){func() { g.tip = "new-base" }, func() { g.bodies[1] = "Fix: yes" }} {
		change()
		out.Reset()
		if _, err := runBatch(context.Background(), cfg, o, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "mode full") {
			t.Fatalf("not decided again; log:\n%s", out.String())
		}
	}
	if st := parseState(g.preflightComment(1)); st == nil || st.Base != "new-base" || st.Body != bodyHash("Fix: yes") {
		t.Fatalf("state %+v", st)
	}

	// A comment that cannot be read is an error for that PR alone, and no new comment is posted beside the old one.
	g.broken[-1] = true
	o.prs = []int{1, 2}
	out.Reset()
	if _, err := runBatch(context.Background(), cfg, o, &out); err != nil || !strings.Contains(out.String(), "its pre-flight comment could not be read") || !strings.Contains(out.String(), "PR #2: fail") {
		t.Fatalf("err %v, log:\n%s", err, out.String())
	}
	// When it could run for none of its PRs, the run itself is broken.
	g.broken[-2] = true
	out.Reset()
	if _, err := runBatch(context.Background(), cfg, o, &out); err == nil || !strings.Contains(err.Error(), "could not run for any of the 2 PR(s)") {
		t.Fatalf("err %v, log:\n%s", err, out.String())
	}
	if len(g.comments[1]) != 1 {
		t.Fatalf("PR #1 has %d comments", len(g.comments[1]))
	}
}

func TestRunPRMarksScansWhenTheirInputsFail(t *testing.T) {
	cfg, err := loadConfig([]byte(strings.Replace(engineConfig, "input: added", "input: changed", 1)))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newFakeGitHub(map[int]string{1: "Fix: no"}))
	defer server.Close()
	dir := t.TempDir()
	o := runOptions{apiBase: server.URL, gitBase: "file://" + filepath.Join(dir, "no-such-remote"), token: "t", repo: "o/r", root: dir, bin: dir, work: filepath.Join(dir, "work"), dryRun: true}
	var log strings.Builder
	result, err := runPR(context.Background(), cfg, &ruleEnv{root: dir}, o, "v", pending{pr: 1}, nil, nil, &log)
	if err != nil || result != resultFail || !strings.Contains(log.String(), "the scan inputs could not be prepared") {
		t.Fatalf("result %s, err %v, log:\n%s", result, err, log.String())
	}
}

// withComment puts a bot's pre-flight comment holding st on a PR of g.
func withComment(g *fakeGitHub, pr int, st state) {
	g.nextID++
	c := fakeComment{ID: g.nextID, Body: withState(commentMarker+"\n", st)}
	c.User.Login = defaultBot
	g.comments[pr] = append(g.comments[pr], c)
}

func TestRunBatchBudgets(t *testing.T) {
	cfg, err := loadConfig([]byte(strings.SplitN(engineConfig, "  - name: marker", 2)[0]))
	if err != nil {
		t.Fatal(err)
	}
	g := newFakeGitHub(map[int]string{1: "Fix: no", 2: "Fix: no", 3: "Fix: no", 4: "Fix: no"})
	server := httptest.NewServer(g)
	defer server.Close()
	dir := t.TempDir()
	o := runOptions{apiBase: server.URL, token: "t", repo: "o/r", root: dir, bin: dir, work: filepath.Join(dir, "work"), jobs: 1, budget: 2, bot: defaultBot}
	// PRs go in number order, and the budget of two leaves 3 and 4 to a next job.
	var out strings.Builder
	deferred, err := runBatch(context.Background(), cfg, o, &out)
	if err != nil || !slices.Equal(deferred, []int{3, 4}) {
		t.Fatalf("deferred %v, err %v", deferred, err)
	}
	for _, want := range []string{"PR #1: pass", "PR #2: pass", "PR #3: deferred", "mode deferred", "2 PR(s) deferred by the budget, for a next job: [3 4]"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the log lacks %q:\n%s", want, out.String())
		}
	}
	if g.hits["GET /repos/o/r/pulls/3/files"] != 0 || g.statuses["head3"] != "" {
		t.Fatalf("a deferred PR was fetched or recorded: %v", g.hits)
	}
	// The next job runs the deferred PRs by name: named PRs are all run, whatever the budget.
	o.prs, o.budget = []int{3, 4}, 1
	out.Reset()
	if deferred, err := runBatch(context.Background(), cfg, o, &out); err != nil || len(deferred) != 0 || !strings.Contains(out.String(), "PR #4: pass") {
		t.Fatalf("deferred %v, err %v, log:\n%s", deferred, err, out.String())
	}
	// The next run reuses all four, which do not count.
	o.prs, o.budget = nil, 2
	out.Reset()
	if deferred, err := runBatch(context.Background(), cfg, o, &out); err != nil || len(deferred) != 0 || strings.Count(out.String(), "mode reuse") != 4 || strings.Contains(out.String(), "deferred") {
		t.Fatalf("deferred %v, err %v, log:\n%s", deferred, err, out.String())
	}
	// Where main is cannot be read: nothing is reused.
	g.tipDown = true
	out.Reset()
	if _, err := runBatch(context.Background(), cfg, o, &out); err != nil || strings.Contains(out.String(), "mode reuse") || !strings.Contains(out.String(), "the tip of main could not be read, so nothing is reused") {
		t.Fatalf("err %v, log:\n%s", err, out.String())
	}
	g.tipDown = false
	// A spent time budget starts no PR.
	o.timeBudget = time.Nanosecond
	out.Reset()
	if deferred, err := runBatch(context.Background(), cfg, o, &out); err != nil || len(deferred) != 4 || !strings.Contains(out.String(), "time budget of 1ns is spent") {
		t.Fatalf("deferred %v, log:\n%s", deferred, out.String())
	}
}

func TestChunks(t *testing.T) {
	if c := chunks([]int{1, 2, 3, 4, 5}, 2); !slices.Equal(c, []string{"1 2", "3 4", "5"}) {
		t.Fatalf("chunks %q", c)
	}
	if c := chunks([]int{1, 2}, 0); !slices.Equal(c, []string{"1", "2"}) || chunks(nil, 3) != nil {
		t.Fatalf("chunks %q", c)
	}
}

func TestRunPRAsksGitWhenGitHubCannotTellMergeable(t *testing.T) {
	cfg, err := loadConfig([]byte(strings.SplitN(engineConfig, "  - name: marker", 2)[0] + `
  - name: up-to-date
    when: {always: true}
    stage: cheap
    reads: [pull, compare-main]
    rule: up-to-date
`))
	if err != nil {
		t.Fatal(err)
	}
	g := newFakeGitHub(map[int]string{1: "Fix: no"})
	g.unknown = true
	server := httptest.NewServer(g)
	defer server.Close()
	dir := t.TempDir()
	o := runOptions{apiBase: server.URL, gitBase: "file://" + filepath.Join(dir, "no-such-remote"), token: "t", repo: "o/r", root: dir, bin: dir, work: filepath.Join(dir, "work"), dryRun: true}
	var log strings.Builder
	result, err := runPR(context.Background(), cfg, &ruleEnv{root: dir}, o, "v", pending{pr: 1}, nil, nil, &log)
	if err != nil || result != resultPending || !strings.Contains(log.String(), "git merge-tree could not tell") {
		t.Fatalf("result %s, err %v, log:\n%s", result, err, log.String())
	}
	// A reused PR asks nothing.
	prev := &state{Version: "v", Head: "head1", Base: "old-base", Body: bodyHash("Fix: no"), Checks: map[string]verdict{"template": {Result: resultPass}, "up-to-date": {Result: resultPass}}}
	log.Reset()
	if result, err := runPR(context.Background(), cfg, &ruleEnv{root: dir}, o, "v", pending{pr: 1, comment: prComment{State: prev}}, nil, nil, &log); err != nil || result != resultPass || strings.Contains(log.String(), "merge-tree") {
		t.Fatalf("result %s, err %v, log:\n%s", result, err, log.String())
	}
}

func TestRunCommandWritesTheDeferredPRsForNextJobs(t *testing.T) {
	g := newFakeGitHub(map[int]string{1: "x", 2: "x", 3: "x"})
	server := httptest.NewServer(g)
	defer server.Close()
	t.Setenv("GITHUB_TOKEN", "t")
	dir := t.TempDir()
	next := filepath.Join(dir, "next.json")
	var out, errOut strings.Builder
	run([]string{"run", "--repo", "o/r", "--api", server.URL, "--root", dir, "--bin", dir, "--work", dir, "--dry-run", "--jobs", "1", "--budget", "1", "--next", next}, &out, &errOut)
	raw, err := os.ReadFile(next)
	if err != nil || string(raw) != `["2","3"]`+"\n" {
		t.Fatalf("next %q, err %v, log:\n%s%s", raw, err, out.String(), errOut.String())
	}
	if err := os.WriteFile(next, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	run([]string{"run", "--repo", "o/r", "--api", server.URL, "--root", dir, "--bin", dir, "--work", dir, "--dry-run", "--prs", "1", "--next", next}, &out, &errOut)
	if raw, _ := os.ReadFile(next); strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("nothing deferred: %q", raw)
	}
	if code := run([]string{"run", "--repo", "o/r", "--api", server.URL, "--root", dir, "--bin", dir, "--work", dir, "--dry-run", "--prs", "1", "--next", filepath.Join(dir, "no", "next.json")}, &out, &errOut); code != 2 {
		t.Fatalf("an unwritable --next: exit %d", code)
	}
}

// TestWorkflowPassesTheGroupsOnUnchanged runs the workflow's own line that turns --next into the job output, and checks
// that every group keeps its PR numbers apart.
func TestWorkflowPassesTheGroupsOnUnchanged(t *testing.T) {
	// The workflow is found upward from here: in the repository, and beside a copy of this package.
	var raw []byte
	for dir, _ := os.Getwd(); ; dir = filepath.Dir(dir) {
		if b, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "preflight.yml")); err == nil {
			raw = b
			break
		}
		if dir == filepath.Dir(dir) {
			t.Fatal("no .github/workflows/preflight.yml above the package")
		}
	}
	var line string
	for l := range strings.SplitSeq(string(raw), "\n") {
		if strings.Contains(l, `echo "next=`) {
			line = strings.TrimSpace(l)
		}
	}
	if line == "" {
		t.Fatal("the workflow has no next= line")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "preflight"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := chunks([]int{421, 422, 424}, 2)
	if err := writeNext(filepath.Join(dir, "preflight", "next.json"), want); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "output")
	cmd := exec.Command("bash", "-c", line)
	cmd.Env = append(os.Environ(), "RUNNER_TEMP="+dir, "GITHUB_OUTPUT="+output)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v %s", line, err, out)
	}
	got, _ := os.ReadFile(output)
	var groups []string
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(string(got)), "next=")), &groups); err != nil || !slices.Equal(groups, []string{"421 422", "424"}) {
		t.Fatalf("output %q: %v %v", got, groups, err)
	}
	if err := writeNext(filepath.Join(dir, "no", "next.json"), nil); err == nil {
		t.Fatal("an unwritable --next was not reported")
	}
}

func TestRunBatchWithoutOpenPRs(t *testing.T) {
	cfg, err := loadConfig([]byte(engineConfig))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newFakeGitHub(map[int]string{}))
	defer server.Close()
	dir := t.TempDir()
	var out strings.Builder
	if deferred, err := runBatch(context.Background(), cfg, runOptions{apiBase: server.URL, token: "t", repo: "o/r", root: dir, bin: dir, work: dir, bot: defaultBot}, &out); err != nil || deferred != nil {
		t.Fatalf("no open PR: %v %v, log:\n%s", deferred, err, out.String())
	}
}
