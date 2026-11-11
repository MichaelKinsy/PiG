// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyHandled(t *testing.T) {
	g := newFakeGitHub(nil)
	// PR 2 has everything; PR 3 a status but a comment for an older head; PR 4 neither.
	g.statuses["h2"], g.statuses["h3"] = "success", "failure"
	withComment(g, 2, state{Head: "h2"})
	withComment(g, 3, state{Head: "old"})
	server := httptest.NewServer(g)
	defer server.Close()
	a := newAPI(server.URL, "t")
	hs := []handled{
		// Deferred by a next job's time budget: the next run takes it.
		{PR: 1, Result: resultDeferred, PassedOn: true},
		{PR: 1, Result: resultDeferred},
		{PR: 2, Head: "h2", Result: resultPass},
		{PR: 3, Head: "h3", Result: resultFail},
		{PR: 4, Head: "h4", Result: resultPass},
		{PR: 5, Result: resultError, Error: "its pre-flight comment could not be read"},
		// Passed on by the first job and run by a next one.
		{PR: 2, Head: "h2", Result: resultDeferred, PassedOn: true},
		// Passed on, and no next job reported it.
		{PR: 6, Head: "h6", Result: resultDeferred, PassedOn: true},
	}
	ms, err := verifyHandled(context.Background(), a, "o/r", defaultBot, hs)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range ms {
		got = append(got, m.where()+": "+m.What)
	}
	want := []string{
		"#3 `h3`: the comment holds no state for this head",
		"#4 `h4`: no preflight status, no pre-flight comment",
		"#5: the pre-flight could not run: its pre-flight comment could not be read",
		"#6 `h6`: passed on to a next job, which did not report it",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("missing:\n%s", strings.Join(got, "\n"))
	}
	g.broken[-2] = true
	if _, err := verifyHandled(context.Background(), a, "o/r", defaultBot, hs[2:3]); err == nil {
		t.Fatal("a comment that cannot be read was not reported")
	}
	if _, err := verifyHandled(context.Background(), newAPI("http://127.0.0.1:1", "t"), "o/r", defaultBot, hs[2:3]); err == nil {
		t.Fatal("statuses that cannot be read were not reported")
	}
}

func TestCapWarningsCarriesEveryCount(t *testing.T) {
	ws := []warning{{Text: "and 2 more", More: 2}, {File: "a"}, {Text: "and 3 more", More: 3}, {File: "b"}}
	got := capWarnings(ws)
	if len(got) != 3 || got[0].File != "a" || got[1].File != "b" || got[2].More != 5 {
		t.Fatalf("capped %+v", got)
	}
}

func TestKeepIssue(t *testing.T) {
	g := newFakeGitHub(nil)
	// Someone else's issue and a PR that start like the issue are not it.
	g.issues[1] = &fakeIssue{Number: 1, Body: issueMarker + " not by the bot", State: "closed"}
	g.issues[2] = &fakeIssue{Number: 2, Body: issueMarker, State: "open", PullRequest: &struct {
		URL string `json:"url"`
	}{"x"}}
	server := httptest.NewServer(g)
	defer server.Close()
	a := newAPI(server.URL, "t")
	ctx := context.Background()
	// An open issue of the bot that is not this one.
	g.issues[3] = &fakeIssue{Number: 3, Body: "something else", State: "open"}
	// Nothing missing and no issue: nothing is done.
	if err := keepIssue(ctx, a, "o/r", defaultBot, nil, "", io.Discard); err != nil || len(g.issues) != 3 || g.issues[2].State != "open" || g.issues[3].State != "open" {
		t.Fatalf("err %v, issues %d", err, len(g.issues))
	}
	ms := []missing{{PR: 3, Head: "h3", What: "no preflight status"}, {What: "no job reported"}}
	var log strings.Builder
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms, "https://run/1", &log); err != nil {
		t.Fatal(err)
	}
	issue := g.issues[903]
	if issue == nil || issue.Title != issueTitle || !strings.HasPrefix(issue.Body, issueMarker) || !strings.Contains(issue.Body, "| #3 `h3` | no preflight status |") || !strings.Contains(issue.Body, "| the run | no job reported |") || !strings.Contains(issue.Body, "Run: https://run/1") {
		t.Fatalf("issue %+v, log %s", issue, log.String())
	}
	// The next run with other PRs missing updates the same issue.
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms[:1], "", io.Discard); err != nil || len(g.issues) != 4 || strings.Contains(g.issues[903].Body, "no job reported") {
		t.Fatalf("err %v, issues %d, body %q", err, len(g.issues), g.issues[903].Body)
	}
	hits := g.hits["PATCH /repos/o/r/issues/903"]
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms[:1], "", io.Discard); err != nil || g.hits["PATCH /repos/o/r/issues/903"] != hits {
		t.Fatal("an unchanged issue was written again")
	}
	// Every PR has its verdict again: the issue is closed, with a word why.
	if err := keepIssue(ctx, a, "o/r", defaultBot, nil, "https://run/2", io.Discard); err != nil || g.issues[903].State != "closed" || len(g.issues[903].comments) != 1 || !strings.Contains(g.issues[903].comments[0], "https://run/2") {
		t.Fatalf("err %v, issue %+v", err, g.issues[903])
	}
	if err := keepIssue(ctx, newAPI("http://127.0.0.1:1", "t"), "o/r", defaultBot, ms, "", io.Discard); err == nil {
		t.Fatal("issues that cannot be listed were not reported")
	}
	// Two open issues of this kind: the first is kept.
	g.issues[910] = &fakeIssue{Number: 910, Body: issueMarker + " first", State: "open"}
	g.issues[911] = &fakeIssue{Number: 911, Body: issueMarker + " second", State: "open"}
	if err := keepIssue(ctx, a, "o/r", defaultBot, ms[:1], "", io.Discard); err != nil || strings.HasSuffix(g.issues[910].Body, "first") || !strings.HasSuffix(g.issues[911].Body, "second") {
		t.Fatalf("err %v, %q %q", err, g.issues[910].Body, g.issues[911].Body)
	}
	// Writes that fail are reported: opening, updating, closing.
	g.issuesDown = true
	for name, tc := range map[string]struct {
		ms   []missing
		open bool
	}{"update": {ms, true}, "close": {nil, true}, "open": {ms, false}} {
		if !tc.open {
			g.issues[910].State, g.issues[911].State = "closed", "closed"
		}
		if err := keepIssue(ctx, a, "o/r", defaultBot, tc.ms, "", io.Discard); err == nil {
			t.Errorf("%s: a refused write was not reported", name)
		}
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
	write("first.json", handled{PR: 1, Head: "h1", Result: resultPass}, handled{PR: 2, Result: resultDeferred, PassedOn: true})
	write("more-1.json", handled{PR: 2, Head: "h2", Result: resultPass})
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
	if code := run(args, &out, &errOut); code != 0 || !strings.Contains(out.String(), "every PR of the run has its verdict") || g.issues[900].State != "closed" {
		t.Fatalf("exit %d:\n%s%s", code, out.String(), errOut.String())
	}
	// No report at all is a missing line of its own.
	empty := t.TempDir()
	out.Reset()
	if code := run([]string{"verify", "--repo", "o/r", "--api", server.URL, "--handled", empty}, &out, &errOut); code != 1 || !strings.Contains(out.String(), "the run: no job reported what it did") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if err := os.WriteFile(filepath.Join(empty, "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readHandled(empty); err == nil || !strings.Contains(err.Error(), "bad.json") {
		t.Fatalf("a broken report: %v", err)
	}
	issues := len(g.issues)
	for _, bad := range [][]string{
		{"verify", "--repo", "o/r", "--api", server.URL},
		{"verify", "--repo", "", "--api", server.URL, "--handled", dir},
		{"verify", "--nope"},
		{"verify", "--repo", "o/r", "--api", "http://127.0.0.1:1", "--handled", dir},
	} {
		if code := run(bad, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d", bad, code)
		}
	}
	if len(g.issues) != issues {
		t.Fatalf("a refused call wrote issues: %d, was %d", len(g.issues), issues)
	}
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	if code := run(args, &out, &errOut); code != 2 {
		t.Errorf("no token: exit %d", code)
	}
	t.Setenv("GITHUB_TOKEN", "t")
	// The issue cannot be kept: the check could not finish.
	g.issuesDown = true
	g.statuses["h2"] = ""
	if code := run(args, &out, &errOut); code != 2 {
		t.Errorf("an issue that cannot be written: exit %d", code)
	}
}
