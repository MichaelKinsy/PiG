// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub serves the parts of the REST API a run reads and writes, for open PRs of o/r.
type fakeGitHub struct {
	mu       sync.Mutex
	bodies   map[int]string
	broken   map[int]bool
	hits     map[string]int
	statuses map[string]string
	comments map[int]string
	// unknown answers mergeable null, as GitHub does before it computed it.
	unknown bool
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hits[r.Method+" "+r.URL.Path]++
	var pr int
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
	case pr > 0 && g.broken[pr]:
		http.Error(w, "down", http.StatusBadGateway)
	case pr > 0 && rest == "":
		var mergeable any = true
		if g.unknown {
			mergeable = nil
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": pr, "state": "open", "body": g.bodies[pr], "mergeable": mergeable, "commits": 1, "changed_files": 1,
			"head": map[string]string{"sha": fmt.Sprintf("head%d", pr)}, "base": map[string]string{"sha": "base", "ref": "main"},
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
	case strings.HasSuffix(r.URL.Path, "/comments") && r.Method == http.MethodGet:
		_, _ = fmt.Fprint(w, `[]`)
	case strings.HasSuffix(r.URL.Path, "/comments") && r.Method == http.MethodPost:
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = fmt.Sscanf(r.URL.Path, "/repos/o/r/issues/%d/comments", &pr)
		g.comments[pr] = body["body"]
		_, _ = fmt.Fprintf(w, `{"id":%d}`, pr)
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
	saved := defaultRetryDelays
	defaultRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { defaultRetryDelays = saved })

	cfg, err := loadConfig([]byte(engineConfig))
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGitHub{bodies: map[int]string{1: "Fix: no", 2: "no template", 3: "Fix: yes"}, broken: map[int]bool{3: true},
		hits: map[string]int{}, statuses: map[string]string{}, comments: map[int]string{}}
	server := httptest.NewServer(g)
	defer server.Close()

	dir := t.TempDir()
	root := filepath.Join(dir, "main")
	if err := os.MkdirAll(filepath.Join(root, "automation", "ci", "preflight"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "automation", "ci", "preflight", "config.yaml"), []byte(engineConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	// The scan tool leaves a marker named after the PR it ran for, so the test sees which PRs reached the scans.
	fakeTool(t, bin, "marker", `touch "`+dir+`/scanned-$(basename "$(dirname "$1")")"`)
	o := runOptions{apiBase: server.URL, token: "tok", repo: "o/r", root: root, bin: bin, states: filepath.Join(dir, "states"), work: filepath.Join(dir, "work"), jobs: 2}

	var out strings.Builder
	err = runBatch(context.Background(), cfg, o, &out)
	if err == nil || !strings.Contains(err.Error(), "could not run for 1 PR(s)") {
		t.Fatalf("err = %v; a PR whose API is down must be reported, log:\n%s", err, out.String())
	}
	if g.statuses["head1"] != "success" || g.statuses["head2"] != "failure" || g.comments[1] == "" || g.comments[2] == "" {
		t.Fatalf("the PRs beside the broken one were not recorded: statuses %v", g.statuses)
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

	// A second run with nothing changed reuses PR #1's verdicts: one API read, no scan.
	if err := os.Remove(filepath.Join(dir, "scanned-1")); err != nil {
		t.Fatal(err)
	}
	g.broken = map[int]bool{}
	before := g.hits["GET /repos/o/r/pulls/1/files"]
	o.prs = []int{1}
	out.Reset()
	if err := runBatch(context.Background(), cfg, o, &out); err != nil {
		t.Fatal(err)
	}
	if g.hits["GET /repos/o/r/pulls/1/files"] != before || !strings.Contains(out.String(), "mode reuse") {
		t.Fatalf("PR #1 was fetched again; log:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "scanned-1")); err == nil {
		t.Error("the reused PR ran its scan again")
	}
	if strings.Contains(out.String(), "scans skipped") {
		t.Error("a run with no scan to run said it skipped them")
	}

	// A reused PR whose scans are still open does not run them, even when scans are forced.
	o.prs, o.forceScans = []int{2}, true
	out.Reset()
	if err := runBatch(context.Background(), cfg, o, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "scanned-2")); err == nil || !strings.Contains(out.String(), "mode reuse") {
		t.Errorf("a reused PR ran its open scans; log:\n%s", out.String())
	}
}

func TestRunPRMarksScansWhenTheirInputsFail(t *testing.T) {
	cfg, err := loadConfig([]byte(strings.Replace(engineConfig, "input: added", "input: changed", 1)))
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGitHub{bodies: map[int]string{1: "Fix: no"}, broken: map[int]bool{}, hits: map[string]int{}, statuses: map[string]string{}, comments: map[int]string{}}
	server := httptest.NewServer(g)
	defer server.Close()
	dir := t.TempDir()
	o := runOptions{apiBase: server.URL, gitBase: "file://" + filepath.Join(dir, "no-such-remote"), token: "t", repo: "o/r", root: dir, bin: dir, states: dir, work: filepath.Join(dir, "work"), dryRun: true}
	var log strings.Builder
	result, err := runPR(context.Background(), cfg, &ruleEnv{root: dir}, o, "v", 1, nil, &log)
	if err != nil || result != resultFail || !strings.Contains(log.String(), "the scan inputs could not be prepared") {
		t.Fatalf("result %s, err %v, log:\n%s", result, err, log.String())
	}
}

func TestRunBatchBudgets(t *testing.T) {
	cfg, err := loadConfig([]byte(strings.SplitN(engineConfig, "  - name: marker", 2)[0]))
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGitHub{bodies: map[int]string{1: "Fix: no", 2: "Fix: no", 3: "Fix: no"}, broken: map[int]bool{}, hits: map[string]int{}, statuses: map[string]string{}, comments: map[int]string{}}
	server := httptest.NewServer(g)
	defer server.Close()
	dir := t.TempDir()
	o := runOptions{apiBase: server.URL, token: "t", repo: "o/r", root: dir, bin: dir, states: filepath.Join(dir, "states"), work: filepath.Join(dir, "work"), jobs: 1, budget: 2}
	// PR 2 was checked long ago, PR 3 recently, PR 1 never: 1, 2, 3 is the order, and the budget of two defers 3.
	if err := os.MkdirAll(o.states, 0o755); err != nil {
		t.Fatal(err)
	}
	for pr, at := range map[int]time.Time{2: time.Unix(1000, 0), 3: time.Unix(2000, 0)} {
		if err := writeJSON(filepath.Join(o.states, strconv.Itoa(pr)+".json"), state{Checked: at}); err != nil {
			t.Fatal(err)
		}
	}
	var out strings.Builder
	if err := runBatch(context.Background(), cfg, o, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PR #1: pass", "PR #2: pass", "PR #3: deferred", "mode deferred", "1 PR(s) deferred to the next run"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the log lacks %q:\n%s", want, out.String())
		}
	}
	if g.hits["GET /repos/o/r/pulls/3/files"] != 0 || g.statuses["head3"] != "" {
		t.Fatalf("a deferred PR was fetched or recorded: %v", g.hits)
	}
	// The next run reuses 1 and 2, which do not count, and checks 3 first as it waited longest.
	out.Reset()
	if err := runBatch(context.Background(), cfg, o, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "deferred") {
		t.Errorf("a run that deferred nothing says it did:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "PR #3: pass") || strings.Index(out.String(), "PR #3: pass") > strings.Index(out.String(), "PR #1: pass") {
		t.Fatalf("PR 3 did not go first:\n%s", out.String())
	}
	// A spent time budget starts no PR.
	o.timeBudget = time.Nanosecond
	out.Reset()
	if err := runBatch(context.Background(), cfg, o, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), ": deferred\n") != 3 || !strings.Contains(out.String(), "time budget of 1ns is spent") {
		t.Fatalf("log:\n%s", out.String())
	}
}

func TestLongestWaiting(t *testing.T) {
	dir := t.TempDir()
	for pr, at := range map[int]int64{5: 10, 6: 10, 7: 5} {
		if err := writeJSON(filepath.Join(dir, strconv.Itoa(pr)+".json"), state{Checked: time.Unix(at, 0)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := longestWaiting([]int{6, 5, 9, 7, 8}, dir); !slices.Equal(got, []int{8, 9, 7, 5, 6}) {
		t.Fatalf("order %v", got)
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
	saved := mergeableRetryDelay
	mergeableRetryDelay = time.Millisecond
	t.Cleanup(func() { mergeableRetryDelay = saved })
	g := &fakeGitHub{bodies: map[int]string{1: "Fix: no"}, broken: map[int]bool{}, hits: map[string]int{}, statuses: map[string]string{}, comments: map[int]string{}, unknown: true}
	server := httptest.NewServer(g)
	defer server.Close()
	dir := t.TempDir()
	o := runOptions{apiBase: server.URL, gitBase: "file://" + filepath.Join(dir, "no-such-remote"), token: "t", repo: "o/r", root: dir, bin: dir, states: dir, work: filepath.Join(dir, "work"), dryRun: true}
	var log strings.Builder
	result, err := runPR(context.Background(), cfg, &ruleEnv{root: dir}, o, "v", 1, nil, &log)
	if err != nil || result != resultPending || !strings.Contains(log.String(), "git merge-tree could not tell") {
		t.Fatalf("result %s, err %v, log:\n%s", result, err, log.String())
	}
	// A reused PR asks nothing.
	if err := writeJSON(filepath.Join(dir, "1.json"), state{Version: "v", Head: "head1", Base: "base", Checks: map[string]verdict{"template": {Result: resultPass}, "up-to-date": {Result: resultPass}}}); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	if result, err := runPR(context.Background(), cfg, &ruleEnv{root: dir}, o, "v", 1, nil, &log); err != nil || result != resultPass || strings.Contains(log.String(), "merge-tree") {
		t.Fatalf("result %s, err %v, log:\n%s", result, err, log.String())
	}
}
