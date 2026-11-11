// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestVerifyHandled(t *testing.T) {
	g := newFakeGitHub(nil)
	g.statuses["h2"], g.statuses["h3"], g.statuses["h7"], g.statuses["h10"] = "success", "failure", "success", "success"
	withComment(g, 2, state{Head: "h2"})
	withComment(g, 10, state{Head: "h10"})
	withComment(g, 3, state{Head: "old"})
	g.broken[-7] = true
	server := httptest.NewServer(g)
	defer server.Close()
	a := newAPI(server.URL, "t")
	hs := []handled{
		{PR: 1, Result: resultDeferred, PassedOn: true},
		{PR: 1, Result: resultDeferred},
		{PR: 2, Head: "h2", Result: resultPass},
		{PR: 3, Head: "h3", Result: resultFail},
		{PR: 4, Head: "h4", Result: resultPass},
		{PR: 5, Result: resultError, Error: "its pre-flight comment could not be read"},
		{PR: 2, Head: "h2", Result: resultDeferred, PassedOn: true},
		{PR: 6, Head: "h6", Result: resultDeferred, PassedOn: true},
		{PR: 10, Head: "h10", Result: resultDeferred, PassedOn: true},
		{PR: 11, Result: resultDeferred, PassedOn: true},
		{PR: 7, Head: "h7", Result: resultPass},
		{PR: 8, Head: "gone", Result: resultPass},
		{PR: 9, Head: "h9", Result: resultPass},
		// The statuses cannot be read: a line, not a skip.
		{PR: 12, Head: "down", Result: resultPass},
		{Result: resultError, Error: "cannot list the PRs"},
	}
	var log strings.Builder
	a.log = &log
	var got []string
	for _, m := range verifyHandled(context.Background(), a, "o/r", defaultBot, hs) {
		got = append(got, m.where()+": "+m.What)
	}
	want := []string{
		"the run: the pre-flight could not run: cannot list the PRs",
		"#3 `h3`: the comment holds no state for this head",
		"#4 `h4`: no preflight status, no pre-flight comment",
		"#5: the pre-flight could not run: its pre-flight comment could not be read",
		"#6 `h6`: no preflight status, no pre-flight comment",
		"#7 `h7`: its comment could not be checked: GET repos/o/r/issues/7/comments?per_page=100: HTTP 502: down",
		"#9 `h9`: no preflight status, no pre-flight comment",
		"#11: passed on to a next job, which did not report it",
		"#12 `down`: could not be checked: GET repos/o/r/commits/down/statuses?per_page=100: HTTP 502: down",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("missing:\n%s", strings.Join(got, "\n"))
	}
	if !strings.Contains(log.String(), "verify: PR #8: head gone is gone, its new head is judged next run") {
		t.Fatalf("log:\n%s", log.String())
	}
	if what := verifyPR(context.Background(), newAPI(server.URL, "t"), "o/r", defaultBot, handled{PR: 8, Head: "gone404"}); what != "" {
		t.Fatalf("a 404 head: %q", what)
	}
}

func TestSameListComparesTheTablesAlone(t *testing.T) {
	ms := []missing{{PR: 1, Head: "h", What: "x"}}
	if !sameList(issueBody(ms, "https://run/1"), issueBody(ms, "")) || sameList(issueBody(ms, ""), issueBody([]missing{{PR: 2, What: "x"}}, "")) {
		t.Fatal("sameList")
	}
	if !sameList("no table", "no table") || sameList("no table", "other") {
		t.Fatal("sameList without a table")
	}
}

func TestCapWarningsCarriesEveryCount(t *testing.T) {
	ws := []warning{{Text: "and 2 more", More: 2}, {File: "a"}, {Text: "and 3 more", More: 3}, {File: "b"}}
	got := capWarnings(ws)
	if len(got) != 3 || got[0].File != "a" || got[1].File != "b" || got[2].More != 5 {
		t.Fatalf("capped %+v", got)
	}
}

func botIssue(g *fakeGitHub, n int, body, state string) {
	i := &fakeIssue{Number: n, Title: issueTitle, Body: body, State: state}
	i.User.Login = defaultBot
	g.issues[n] = i
}

func TestKeepIssue(t *testing.T) {
	g := newFakeGitHub(nil)
	g.issues[1] = &fakeIssue{Number: 1, Title: issueTitle, Body: issueMarker + " by someone", State: "open"}
	g.issues[1].User.Login = "someone"
	botIssue(g, 2, "something else", "open")
	server := httptest.NewServer(g)
	defer server.Close()
	a := newAPI(server.URL, "t")
	ctx := context.Background()
	if err := keepIssue(ctx, a, "o/r", defaultBot, nil, "", io.Discard); err != nil || len(g.issues) != 2 || g.issues[1].State != "open" || g.issues[2].State != "open" {
		t.Fatalf("err %v, issues %d", err, len(g.issues))
	}
	ms := []missing{{PR: 3, Head: "h3", What: "no preflight status"}, {What: "a <b>file</b> | `x`\nline"}}
	var log strings.Builder
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms, "https://run/1", &log); err != nil {
		t.Fatal(err)
	}
	issue := g.issues[902]
	if issue == nil || issue.Title != issueTitle || !strings.HasPrefix(issue.Body, issueMarker) || !strings.Contains(issue.Body, "| #3 `h3` | <code>no preflight status</code> |") ||
		!strings.Contains(issue.Body, "| the run | <code>a &lt;b&gt;file&lt;/b&gt; &#124; `x` line</code> |") || !strings.Contains(issue.Body, "Run: https://run/1") {
		t.Fatalf("issue %+v, log %s", issue, log.String())
	}
	hits := g.hits["PATCH /repos/o/r/issues/902"]
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms, "https://run/2", io.Discard); err != nil || g.hits["PATCH /repos/o/r/issues/902"] != hits {
		t.Fatal("an unchanged list was written again")
	}
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms[:1], "https://run/3", io.Discard); err != nil || len(g.issues) != 3 || strings.Contains(issue.Body, "the run") || !strings.Contains(issue.Body, "run/3") {
		t.Fatalf("err %v, issues %d, body %q", err, len(g.issues), issue.Body)
	}
	if err := keepIssue(ctx, a, "o/r", defaultBot, nil, "https://run/4", io.Discard); err != nil || issue.State != "closed" || len(issue.comments) != 1 || !strings.Contains(issue.comments[0], "https://run/4") {
		t.Fatalf("err %v, issue %+v", err, issue)
	}
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms, "", io.Discard); err != nil || issue.State != "open" || len(g.issues) != 3 || !strings.Contains(issue.Body, "the run") {
		t.Fatalf("err %v, issues %d, issue %+v", err, len(g.issues), issue)
	}
	botIssue(g, 950, issueMarker+" later", "open")
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms, "", io.Discard); err != nil || g.issues[950].State != "closed" || issue.State != "open" || !strings.Contains(g.issues[950].comments[0], "#902") {
		t.Fatalf("err %v, %+v", err, g.issues[950])
	}
	issue.State = "closed"
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms, "", io.Discard); err != nil || issue.State != "open" || g.issues[950].State != "closed" {
		t.Fatalf("err %v", err)
	}
	issue.State, g.issues[950].State = "closed", "open"
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms, "", io.Discard); err != nil || issue.State != "closed" || g.issues[950].State != "open" {
		t.Fatalf("err %v, 902 %s, 950 %s", err, issue.State, g.issues[950].State)
	}
	issue.State, g.issues[950].State = "open", "closed"
	g.searchDown = true
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms, "", io.Discard); err == nil {
		t.Fatal("a failed search was not reported")
	}
	g.searchDown, g.issuesDown = false, true
	for _, tc := range []struct {
		name  string
		list  []missing
		setup func()
	}{
		{"update", ms[:1], func() { issue.State, issue.Body = "open", issueMarker+" old"; g.issues[950].State = "closed" }},
		{"close", nil, func() { issue.State = "open"; g.issues[950].State = "closed" }},
		{"reopen", ms[:1], func() { issue.State = "closed"; g.issues[950].State = "closed" }},
		{"duplicate", ms[:1], func() { issue.State, issue.Body = "open", issueBody(ms[:1], ""); g.issues[950].State = "open" }},
	} {
		tc.setup()
		if err := keepIssue(ctx, a, "o/r", defaultBot, tc.list, "", io.Discard); err == nil {
			t.Errorf("%s: a refused write was not reported", tc.name)
		}
	}
	g.issues = map[int]*fakeIssue{}
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms, "", io.Discard); err == nil {
		t.Error("open: a refused write was not reported")
	}
	g.issuesDown = false
	if err := closeIssue(ctx, newAPI("http://127.0.0.1:1", "t"), "o/r", 1, "x"); err == nil {
		t.Error("a comment that cannot be posted was not reported")
	}
}

func TestVerifyCommand(t *testing.T) {
	g := newFakeGitHub(nil)
	g.statuses["h1"] = "success"
	withComment(g, 1, state{Head: "h1"})
	server := httptest.NewServer(g)
	defer server.Close()
	t.Setenv("GITHUB_TOKEN", "t")
	t.Setenv("GITHUB_RUN_ID", "77")
	t.Setenv("GITHUB_SERVER_URL", "https://github.example")
	dir := t.TempDir()
	write := func(name string, hs ...handled) {
		t.Helper()
		if err := writeJSON(filepath.Join(dir, name), append([]handled{}, hs...)); err != nil {
			t.Fatal(err)
		}
	}
	write(firstReport, handled{PR: 1, Head: "h1", Result: resultPass}, handled{PR: 2, Result: resultDeferred, PassedOn: true})
	write("preflight-handled-0-more.json", handled{PR: 2, Head: "h2", Result: resultPass})
	args := []string{"verify", "--repo", "o/r", "--api", server.URL, "--handled", dir}
	var out, errOut strings.Builder
	if code := run(args, &out, &errOut); code != 1 || !strings.Contains(out.String(), "::error title=pre-flight verify::#2 h2: no preflight status, no pre-flight comment") {
		t.Fatalf("exit %d:\n%s%s", code, out.String(), errOut.String())
	}
	if issue := g.issues[900]; issue == nil || !strings.Contains(issue.Body, "Run: https://github.example/o/r/actions/runs/77") {
		t.Fatalf("issue %+v", issue)
	}
	g.statuses["h2"] = "success"
	withComment(g, 2, state{Head: "h2"})
	out.Reset()
	if code := run(args, &out, &errOut); code != 0 || !strings.Contains(out.String(), "every PR of the run has its verdict (3 report line(s))") || g.issues[900].State != "closed" {
		t.Fatalf("exit %d:\n%s%s", code, out.String(), errOut.String())
	}
	if err := os.Rename(filepath.Join(dir, firstReport), filepath.Join(dir, "x")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := run(args, &out, &errOut); code != 1 || !strings.Contains(out.String(), "the run: the first job's report is missing") || !strings.Contains(out.String(), "the run: the report bad.json could not be read") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(g.issues[900].Body, "bad.json") || g.issues[900].State != "open" {
		t.Fatalf("issue %+v", g.issues[900])
	}
	if _, problems := readHandled(filepath.Join(dir, "[")); len(problems) != 1 {
		t.Fatalf("a bad directory: %v", problems)
	}
	issues := len(g.issues)
	for _, bad := range [][]string{
		{"verify", "--repo", "o/r", "--api", server.URL},
		{"verify", "--repo", "", "--api", server.URL, "--handled", dir},
		{"verify", "--nope"},
	} {
		if code := run(bad, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d", bad, code)
		}
	}
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	if code := run(args, &out, &errOut); code != 2 {
		t.Errorf("no token: exit %d", code)
	}
	if len(g.issues) != issues {
		t.Fatalf("a refused call wrote issues: %d, was %d", len(g.issues), issues)
	}
	t.Setenv("GITHUB_TOKEN", "t")
	g.issuesGone = true
	g.statuses["h2"] = ""
	out.Reset()
	if code := run(args, &out, &errOut); code != 1 || !strings.Contains(out.String(), "issues are disabled in o/r") {
		t.Fatalf("issues off: exit %d\n%s", code, out.String())
	}
	g.issuesGone = false
	g.searchDown = true
	if code := run(args, &out, &errOut); code != 2 {
		t.Errorf("an issue that cannot be kept: exit %d", code)
	}
}

func TestAnnotation(t *testing.T) {
	if got := annotation("50% done\r\n::error::x"); got != "50%25 done%0D%0A::error::x" {
		t.Fatalf("annotation %q", got)
	}
}

func TestReadHandledReadsPastABadReport(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a-bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, firstReport), []handled{{PR: 1, Head: "h", Result: resultPass}}); err != nil {
		t.Fatal(err)
	}
	hs, problems := readHandled(dir)
	if len(hs) != 1 || len(problems) != 1 || !strings.Contains(problems[0].What, "a-bad.json") {
		t.Fatalf("handled %+v, problems %+v", hs, problems)
	}
}

func TestWorkflowNamesTheFirstReport(t *testing.T) {
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
	type step struct {
		Uses string            `yaml:"uses"`
		Run  string            `yaml:"run"`
		With map[string]string `yaml:"with"`
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []step `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	find := func(job, what string) step {
		t.Helper()
		for _, s := range wf.Jobs[job].Steps {
			if strings.HasPrefix(s.Uses, what) || (what != "" && strings.Contains(s.Run, what)) {
				return s
			}
		}
		t.Fatalf("job %s has no step with %s", job, what)
		return step{}
	}
	index := strings.NewReplacer("${{ strategy.job-index }}", "0", "$JOB_INDEX", "0")
	firstWrites := find("preflight", "preflight\" run").Run
	firstUpload := find("preflight", "actions/upload-artifact@").With["path"]
	moreWrites := index.Replace(find("more", "preflight\" run").Run)
	moreUpload := index.Replace(find("more", "actions/upload-artifact@").With["path"])
	if filepath.Base(firstUpload) != firstReport || !strings.Contains(firstWrites, `--handled "$RUNNER_TEMP/preflight/`+firstReport+`"`) {
		t.Fatalf("the first job writes or uploads another report than %s:\n%s\n%s", firstReport, firstUpload, firstWrites)
	}
	if filepath.Base(moreUpload) == firstReport || !strings.Contains(moreWrites, `--handled "$RUNNER_TEMP/preflight/`+filepath.Base(moreUpload)+`"`) {
		t.Fatalf("a next job's report: %s\n%s", moreUpload, moreWrites)
	}
	download := find("verify", "actions/download-artifact@")
	if download.With["merge-multiple"] != "true" || download.With["path"] != "${{ runner.temp }}/handled" {
		t.Fatalf("download %+v", download.With)
	}
	temp := t.TempDir()
	for _, name := range []string{filepath.Base(firstUpload), filepath.Base(moreUpload)} {
		if err := os.MkdirAll(filepath.Join(temp, "handled"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(filepath.Join(temp, "handled", name), []handled{{PR: 1, Head: name, Result: resultPass}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(temp, "preflight"), []byte("#!/bin/sh\nls \"$5\" > \"$RUNNER_TEMP/seen\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", find("verify", "preflight\" verify").Run)
	cmd.Env = append(os.Environ(), "RUNNER_TEMP="+temp, "GITHUB_REPOSITORY=o/r")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("verify step: %v %s", err, out)
	}
	seen, _ := os.ReadFile(filepath.Join(temp, "seen"))
	hs, problems := readHandled(strings.TrimSpace(filepath.Join(temp, "handled")))
	if len(problems) != 0 || len(hs) != 2 || !strings.Contains(string(seen), firstReport) {
		t.Fatalf("handled %+v, problems %+v, seen %q", hs, problems, seen)
	}
}

func TestFindIssuesForAUserAccount(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("q")
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer server.Close()
	if _, err := findIssues(context.Background(), newAPI(server.URL, "t"), "o/r", "a-user"); err != nil || query != `repo:o/r is:issue in:title "`+issueTitle+`" author:a-user` {
		t.Fatalf("query %q, err %v", query, err)
	}
}
