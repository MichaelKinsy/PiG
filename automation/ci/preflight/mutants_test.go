// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Tests for the edges mutation testing found unguarded, grouped by the file they guard.

func TestParseAcknowledgedNamesTheBadEntry(t *testing.T) {
	good := "[[ack]]\ncheck='a'\nfile='b'\nblob='d'\n"
	for _, missing := range []string{"check", "file", "blob"} {
		bad := strings.Replace(good, missing+"=", "x"+missing+"=", 1)
		bad = strings.Replace(bad, "x"+missing+"=", "# "+missing+"=", 1)
		_, err := parseAcknowledged(good + "\n" + bad)
		if err == nil || !strings.Contains(err.Error(), "entry 2 needs") {
			t.Errorf("an entry without %s: %v", missing, err)
		}
	}
}

func TestApplyAcknowledgedMatchesEveryField(t *testing.T) {
	w := warning{File: "a.sh", Line: 2, Blob: "b1", Notice: "n", Text: "t"}
	d := &data{Files: []prFile{{Filename: "a.sh", SHA: "b1"}}}
	for name, a := range map[string]acknowledgement{
		"another check": {Check: "other", File: "a.sh", Blob: "b1"},
		"another file":  {Check: "licenses", File: "z.sh", Blob: "b1"},
	} {
		ev := evaluation{Checks: map[string]verdict{"licenses": {Result: resultPass, Warnings: []warning{w}}, "other": {Result: resultPass}}}
		applyAcknowledged(&ev, d, []acknowledgement{a})
		if ev.Checks["licenses"].Warnings[0].Acknowledged {
			t.Errorf("%s acknowledged the warning", name)
		}
		if n := len(ev.Checks["licenses"].Warnings) + len(ev.Checks["other"].Warnings); n != 1 {
			t.Errorf("%s: an entry for an unchanged blob was reported stale (%d warnings)", name, n)
		}
	}
	// An entry for a file the PR does not change is not judged.
	ev := evaluation{Checks: map[string]verdict{"licenses": {Result: resultPass}}}
	applyAcknowledged(&ev, d, []acknowledgement{{Check: "licenses", File: "untouched.sh", Blob: "old"}})
	if len(ev.Checks["licenses"].Warnings) != 0 {
		t.Error("an entry for a file outside the PR was reported stale")
	}
}

func TestConfigEdges(t *testing.T) {
	cheap := `{name: a, when: {always: true}, stage: cheap, reads: [pull], rule: up-to-date}`
	for _, tc := range []struct{ name, yaml, want string }{
		{"the bad tool is named by its place", "engine-revision: 1\ntools: [{name: x, version: '1', pip: r}, {name: y, version: '1'}]\nchecks: [" + cheap + "]", "tool 2"},
		{"the nameless check is named by its place", "engine-revision: 1\nchecks: [" + cheap + ", {when: {always: true}, stage: cheap, reads: [pull], rule: up-to-date}]", "check 2 has no name"},
		{"an empty checkout path", "engine-revision: 1\nchecks: [{name: a, when: {always: true}, stage: scan, scan: {input: added, run: [x], checkout: [''], exit: {clean: [0]}, report: {in: stdout, format: lines, pattern: '(?P<message>.+)'}}}]", "must be relative"},
	} {
		if _, err := loadConfig([]byte(tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	re, err := globRegexp("docs/**/x.md")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{"docs/x.md": true, "docs/a/b/x.md": true, "docs/a/y.md": false, "x.md": false} {
		if re.MatchString(path) != want {
			t.Errorf("docs/**/x.md matches %s = %v, want %v", path, !want, want)
		}
	}
}

func TestBindMainIsPerCheck(t *testing.T) {
	root := t.TempDir()
	write := func(p, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const yaml = `
engine-revision: 1
checks:
  - {name: licenses, when: {always: true}, stage: cheap, reads: [files], rule: up-to-date, main: [LICENSES]}
  - {name: rules, when: {always: true}, stage: cheap, reads: [files], rule: up-to-date, main: ['r/*.toml', r/a.toml]}
  - name: lint
    when: {always: true}
    stage: scan
    scan: {input: changed, overlay: [lint.yaml], run: [x], exit: {clean: [0]}, report: {in: stdout, format: lines, pattern: '(?P<message>.+)'}}
`
	bind := func(text string) (string, map[string]string) {
		t.Helper()
		cfg, err := loadConfig([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		version, err := bindMain(&cfg, root)
		if err != nil {
			t.Fatal(err)
		}
		defs := map[string]string{}
		for _, c := range cfg.Checks {
			defs[c.Name] = c.definition
		}
		return version, defs
	}
	changed := func(a, b map[string]string) []string {
		var names []string
		for n := range a {
			if a[n] != b[n] {
				names = append(names, n)
			}
		}
		slices.Sort(names)
		return names
	}
	v0, d0 := bind(yaml)
	write("LICENSES/sub/MIT.txt", "mit")
	v1, d1 := bind(yaml)
	write("r/a.toml", "a")
	_, d2 := bind(yaml)
	write("lint.yaml", "l")
	_, d3 := bind(yaml)
	write("unrelated.txt", "u")
	v4, d4 := bind(yaml)
	cfg, _ := loadConfig([]byte(yaml))
	first, _ := bindMain(&cfg, root)
	if again, _ := bindMain(&cfg, root); again != first || again != v4 {
		t.Error("binding twice changed the version")
	}
	switch {
	case !slices.Equal(changed(d0, d1), []string{"licenses"}):
		t.Errorf("a nested licence file changed %v", changed(d0, d1))
	case !slices.Equal(changed(d1, d2), []string{"rules"}):
		t.Errorf("a globbed file changed %v", changed(d1, d2))
	case !slices.Equal(changed(d2, d3), []string{"lint"}):
		t.Errorf("an overlay changed %v", changed(d2, d3))
	case len(changed(d3, d4)) != 0:
		t.Errorf("an unrelated file changed %v", changed(d3, d4))
	case v0 == v1:
		t.Error("a changed definition kept the run's version")
	}
	if v, _ := bind(strings.Replace(yaml, "engine-revision: 1", "engine-revision: 2", 1)); v == v4 {
		t.Error("another engine revision kept the run's version")
	}
	if _, d := bind(strings.Replace(yaml, "main: [LICENSES]}", "main: [LICENSES], revision: 2}", 1)); !slices.Equal(changed(d4, d), []string{"licenses"}) {
		t.Errorf("a revision changed %v", changed(d4, d))
	}
	cfg, _ = loadConfig([]byte(strings.Replace(yaml, "'r/*.toml'", "'r/[.toml'", 1)))
	if _, err := bindMain(&cfg, root); err == nil || !strings.Contains(err.Error(), "check rules") {
		t.Fatalf("a bad glob: %v", err)
	}
	write("locked/f", "x")
	if err := os.Chmod(filepath.Join(root, "locked", "f"), 0); err != nil {
		t.Fatal(err)
	}
	cfg, _ = loadConfig([]byte(strings.Replace(yaml, "main: [LICENSES]", "main: [locked]", 1)))
	if _, err := bindMain(&cfg, root); err == nil || !strings.Contains(err.Error(), "check licenses") {
		t.Fatalf("an unreadable file: %v", err)
	}
	if err := os.Chmod(filepath.Join(root, "locked"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "locked"), 0o755) })
	if _, err := bindMain(&cfg, root); err == nil {
		t.Fatal("an unreadable directory gave no error")
	}
}

func TestEvaluateIgnoresAStateOfAnotherEngine(t *testing.T) {
	cfg, err := loadConfig([]byte(engineConfig))
	if err != nil {
		t.Fatal(err)
	}
	d := &data{Version: "v2", Mode: modeFull, Relevant: []string{"template"}, Previous: &state{Version: "v1", Engine: 1}}
	d.Pull.Body = "no template"
	d.Previous.Checks = map[string]verdict{"template": {Result: resultPass, Input: cheapInputHash(d, cfg.Checks[0])}}
	if ev := evaluate(cfg, d, &ruleEnv{}); ev.Checks["template"].Result != resultPass || ev.Engine != 1 {
		t.Fatalf("an equal input hash under another run version did not reuse the check's verdict: %+v", ev)
	}
	d.Previous.Engine = 0
	if ev := evaluate(cfg, d, &ruleEnv{}); ev.Checks["template"].Result != resultFail {
		t.Fatalf("a verdict of another engine revision was reused: %+v", ev.Checks["template"])
	}
	d.Mode = modeReuse
	if ev := evaluate(cfg, d, &ruleEnv{}); ev.Checks["template"].Result != resultFail {
		t.Fatalf("a reuse of another engine revision's state: %+v", ev.Checks["template"])
	}
	// A full run decides a check whose input changed.
	d.Mode, d.Previous.Engine = modeFull, 1
	d.Previous.Checks["template"] = verdict{Result: resultPass, Input: "other"}
	if ev := evaluate(cfg, d, &ruleEnv{}); ev.Checks["template"].Result != resultFail {
		t.Fatalf("a full run copied a stale verdict: %+v", ev.Checks["template"])
	}
}

func TestEvaluateReadsAcknowledged(t *testing.T) {
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	f := prFile{Filename: "x.sh", Status: "modified", SHA: "blob", Patch: "@@ -1 +1,2 @@\n x\n+# Copyright 2020 X"}
	d := &data{Version: "v", Files: []prFile{f}, Relevant: []string{"licenses"}}
	w := warning{File: "x.sh", Line: 2, Blob: "blob", Notice: "# Copyright 2020 X"}
	d.Acknowledged = ackEntry("licenses", w)
	ev := evaluate(cfg, d, &ruleEnv{root: t.TempDir()})
	if ws := ev.Checks["licenses"].Warnings; len(ws) != 1 || !ws[0].Acknowledged {
		t.Fatalf("the acknowledgement was not applied: %+v", ws)
	}
	d.Acknowledged = "[[ack]]\nbroken"
	if ev := evaluate(cfg, d, &ruleEnv{root: t.TempDir()}); ev.Checks["licenses"].Result != resultFail {
		t.Fatalf("a broken acknowledged.toml passed: %+v", ev.Checks["licenses"])
	}
}

func TestReportLines(t *testing.T) {
	cfg := config{Checks: []check{{Name: "a"}, {Name: "b"}, {Name: "c"}}}
	ev := evaluation{PR: 7, Checks: map[string]verdict{"a": {Result: resultPass, Reason: "ok"}, "b": {Result: resultFail, Reason: "bad"}, "c": {Result: resultError, Reason: "broke"}}}
	var out strings.Builder
	report(&out, cfg, ev)
	got := out.String()
	if strings.Contains(got, "to acknowledge") || strings.Contains(got, "::error title=pre-flight a") || !strings.Contains(got, "::error title=pre-flight b #7::bad") || !strings.Contains(got, "::error title=pre-flight c #7::broke") {
		t.Fatalf("report:\n%s", got)
	}
	ev.Checks["a"] = verdict{Result: resultPass, Warnings: []warning{{File: "f", Notice: "n", Text: "t"}}}
	out.Reset()
	report(&out, cfg, ev)
	if !strings.Contains(out.String(), "to acknowledge") || !strings.Contains(out.String(), `f t: "n"`) {
		t.Fatalf("report with a warning:\n%s", out.String())
	}
	if got := (warning{File: "f", Line: 3, Notice: "n", Text: "t"}).where(); got != `f:3 t: "n"` {
		t.Fatalf("where = %q", got)
	}
}

func TestFetchLimitsAndRenames(t *testing.T) {
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		commits, files int
		limit          string
	}{{250, 3000, ""}, {251, 1, "251 commits"}, {1, 3001, "3001 changed files"}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/repos/o/r/pulls/1":
				_ = json.NewEncoder(w).Encode(map[string]any{"number": 1, "mergeable": true, "commits": tc.commits, "changed_files": tc.files, "head": map[string]string{"sha": "h"}, "base": map[string]string{"sha": "b", "ref": "main"}})
			case "/repos/o/r/pulls/1/files":
				_, _ = io.WriteString(w, `[{"filename":"docs/w.yml","previous_filename":".github/workflows/w.yml","status":"renamed"}]`)
			case "/repos/o/r/compare/main...h":
				_, _ = io.WriteString(w, `{"status":"diverged","files":[{"filename":"z"}]}`)
			default:
				_, _ = io.WriteString(w, `[]`)
			}
		}))
		d, err := fetchWith(t, cfg, fetchOptions{apiBase: server.URL, repo: "o/r", pr: 1, version: "v", previous: &state{Version: "v", Head: "old"}}, io.Discard)
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		if (tc.limit == "" && d.Limit != "") || !strings.Contains(d.Limit, tc.limit) {
			t.Errorf("%d commits, %d files: limit %q, want %q", tc.commits, tc.files, d.Limit, tc.limit)
		}
		if !strings.Contains(strings.Join(d.Relevant, " "), "workflows") {
			t.Errorf("a workflow moved out of .github did not make the workflow checks relevant: %v", d.Relevant)
		}
	}
	if formatBool(nil) != "unknown" || formatBool(new(true)) != "true" {
		t.Error("formatBool")
	}
}

func TestInstallReplacesACorruptCache(t *testing.T) {
	archive := tarGz(t, map[string]string{"tool": "#!/bin/sh\n"})
	served := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, sha256Hex(archive)), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config{Tools: []tool{{Name: "tool", Version: "1", URL: server.URL, SHA256: sha256Hex(archive)}}}
	if err := install(context.Background(), cfg, dir, filepath.Join(dir, "bin"), cache, io.Discard); err != nil || served != 1 {
		t.Fatalf("a corrupt cache entry was used (%d downloads): %v", served, err)
	}
}

func TestCommandLineEdges(t *testing.T) {
	var out, errOut strings.Builder
	if code := installCommand(context.Background(), config{}, []string{"--bin", t.TempDir()}, &out, &errOut); code != 0 {
		t.Fatalf("install of no tools: %d %s", code, errOut.String())
	}
	if code := installCommand(context.Background(), config{}, []string{"--nope"}, &out, &errOut); code != 2 {
		t.Fatalf("an unknown install flag: %d", code)
	}
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	errOut.Reset()
	if code := run([]string{"run", "--prs", "1"}, &out, &errOut); code != 2 || strings.Contains(errOut.String(), "not a PR number") {
		t.Fatalf("PR 1 was rejected: %s", errOut.String())
	}
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	if got := defaultCache(); got != filepath.Join(os.TempDir(), "pig-ci-downloads") {
		t.Fatalf("defaultCache without a home = %q", got)
	}
}

func TestStatusAndMarkdownEdges(t *testing.T) {
	cfg := config{Checks: []check{{Name: "a"}, {Name: "b"}}}
	ev := evaluation{Result: resultPass, Checks: map[string]verdict{"a": {Result: resultPass}, "b": {Result: resultPass}}}
	if state, description := statusFor(cfg, ev); state != "success" || description != "2 checks passed" {
		t.Fatalf("%s %q", state, description)
	}
	if md := markdown(cfg, &data{}, ev); strings.Contains(md, "<details>") {
		t.Fatal("a report without warnings has the acknowledge section")
	}
	long := evaluation{Result: resultScan, Scans: []string{strings.Repeat("x", 200)}, Checks: map[string]verdict{}}
	if _, description := statusFor(cfg, long); len(description) != 140 || !strings.HasSuffix(description, "...") {
		t.Fatalf("a long description is %d characters: %q", len(description), description)
	}
	exact := evaluation{Result: resultScan, Scans: []string{strings.Repeat("y", 140-len("scans still to run: "))}, Checks: map[string]verdict{}}
	if _, description := statusFor(cfg, exact); len(description) != 140 || strings.HasSuffix(description, "...") {
		t.Fatalf("a description of exactly 140 characters was cut: %q", description)
	}
}

func TestRecordReportsAFailedCommentWrite(t *testing.T) {
	cfg, d, ev := testEvaluation()
	for _, existing := range []string{`[]`, `[{"id":2,"body":"` + commentMarker + `old"}]`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet:
				_, _ = io.WriteString(w, existing)
			case strings.Contains(r.URL.Path, "/statuses/"):
				w.WriteHeader(http.StatusCreated)
			default:
				http.Error(w, "forbidden", http.StatusForbidden)
			}
		}))
		o := recordOptions{apiBase: server.URL, token: "t"}
		if existing != `[]` {
			o.comment = prComment{ID: 2, Body: commentMarker + "old"}
		}
		err := record(context.Background(), cfg, d, ev, o, io.Discard)
		server.Close()
		if err == nil || !strings.Contains(err.Error(), "403") {
			t.Errorf("comments %s: a refused comment write was not reported: %v", existing, err)
		}
	}
}

func TestSendDecodesTheAnswerAndSetsTheContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "no content type", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":42}`)
	}))
	defer server.Close()
	var out struct{ ID int }
	if err := newAPI(server.URL, "t").send(context.Background(), http.MethodPost, "x", map[string]string{"a": "b"}, &out); err != nil || out.ID != 42 {
		t.Fatalf("send: %v %+v", err, out)
	}
}

func TestRetryingEdges(t *testing.T) {
	delays := []time.Duration{time.Millisecond}
	for _, tc := range []struct {
		status  int
		retried bool
	}{{500, true}, {300, false}, {422, false}} {
		calls := 0
		var log strings.Builder
		_, err := retrying(context.Background(), delays, &log, "GET x", func() (response, error) {
			calls++
			return response{status: tc.status}, nil
		})
		if err == nil || (calls == 2) != tc.retried {
			t.Errorf("status %d: %d call(s), err %v", tc.status, calls, err)
		}
		if tc.retried && !strings.Contains(log.String(), "retry 1/1 of GET x") {
			t.Errorf("status %d: log %q", tc.status, log.String())
		}
		if !hasStatus(err, tc.status) || hasStatus(err, tc.status+1) {
			t.Errorf("status %d is not read back: %v", tc.status, err)
		}
	}
	if hasStatus(errors.New("plain"), 404) {
		t.Error("a plain error is a 404")
	}
}

func TestToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "workflow")
	t.Setenv("GH_TOKEN", "local")
	if token() != "workflow" {
		t.Error("GITHUB_TOKEN does not win")
	}
	t.Setenv("GITHUB_TOKEN", "")
	if token() != "local" {
		t.Error("GH_TOKEN is not the fallback")
	}
}

func TestScannerEdges(t *testing.T) {
	if got := firstLines("a\n\nb\nc\nd", 2); got != "a | b" {
		t.Fatalf("firstLines = %q", got)
	}
	r := mustSpec(t, reportSpec{In: "stdout", Format: "json", File: "f", Message: "m"})
	found, _ := parseReport(r, []byte(`[{"x":1},{"f":"only-a-file"}]`))
	if len(found) != 1 || found[0].file != "only-a-file" {
		t.Fatalf("found %v", found)
	}
	if _, err := inside("/w", ".."); err == nil {
		t.Fatal(`".." was inside`)
	}
	dir := t.TempDir()
	ws := workspace{dir: dir, bin: filepath.Join(dir, "bin"), head: filepath.Join(dir, "head"), added: filepath.Join(dir, "added")}
	if err := os.MkdirAll(ws.head, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeTool(t, ws.bin, "where", `[ "$PWD" = "`+ws.head+`" ] || { echo "x:1: ran in $PWD"; exit 1; }; for i in 1 2 3 4 5 6; do echo "f$i:1: m"; done; exit 1`)
	s := scanSpec{Input: inputAdded, Run: []string{"where"}, Dir: "{head}", Exit: exitCodes{Clean: []int{0}, Findings: []int{1}}, Report: reportSpec{In: "stdout", Format: "lines", Pattern: `^(?P<file>[^:]+):(?P<line>\d+): (?P<message>.+)$`}}
	o := runScan(context.Background(), scanCheck(t, s), &data{}, ws, io.Discard)
	if !strings.Contains(o.reason, "6 finding(s)") || !strings.Contains(o.reason, "(and 1 more)") || strings.Contains(o.reason, "ran in") {
		t.Fatalf("outcome %+v", o)
	}
	fakeTool(t, ws.bin, "five", `for i in 1 2 3 4 5; do echo "f$i:1: m"; done; exit 1`)
	s.Run, s.Dir = []string{"five"}, ""
	if o := runScan(context.Background(), scanCheck(t, s), &data{}, ws, io.Discard); strings.Contains(o.reason, "more") {
		t.Fatalf("five findings said more: %+v", o)
	}
}

func TestPrepareWorkspaceEdges(t *testing.T) {
	dir := t.TempDir()
	ws := workspace{dir: dir, added: filepath.Join(dir, "added")}
	scans := []check{scanCheck(t, scanSpec{Input: inputAdded, Run: []string{"x"}, Exit: exitCodes{Clean: []int{0}}, Report: reportSpec{In: "stdout", Format: "lines", Pattern: `(?P<message>.+)`}})}
	d := &data{Files: []prFile{
		{Filename: "gone.go", Status: "removed", Patch: "@@ -1 +0,0 @@\n-x"},
		{Filename: "kept.go", Status: "modified", Patch: "@@ -1 +1,2 @@\n x\n+y"},
	}}
	if err := prepareWorkspace(context.Background(), d, scans, ws, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws.added, "kept.go")); err != nil {
		t.Fatal("a file after a removed one was not written")
	}
	// A path that cannot be written is an error, not a silently missing input.
	if err := os.MkdirAll(filepath.Join(ws.added, "blocked.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	d.Files = []prFile{{Filename: "blocked.go", Status: "added", Patch: "@@ -0,0 +1 @@\n+x"}}
	if err := prepareWorkspace(context.Background(), d, scans, ws, io.Discard); err == nil {
		t.Fatal("an unwritable input was not reported")
	}
	// overlay reports a head path it cannot remove.
	head := t.TempDir()
	if err := os.MkdirAll(filepath.Join(head, "cfg", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := overlay(t.TempDir(), head, "cfg"); err == nil {
		t.Fatal("a non-empty directory in the overlay's place was not reported")
	}
}

func TestRunPRWithoutScansSaysNothingAboutSkipping(t *testing.T) {
	cfg, err := loadConfig([]byte(strings.SplitN(engineConfig, "  - name: marker", 2)[0]))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newFakeGitHub(map[int]string{1: "no template"}))
	defer server.Close()
	dir := t.TempDir()
	var log strings.Builder
	o := runOptions{apiBase: server.URL, token: "t", repo: "o/r", root: dir, bin: dir, work: dir, dryRun: true}
	if result, _, err := runPR(context.Background(), cfg, &ruleEnv{root: dir}, o, "v", pending{pr: 1}, nil, nil, &log); err != nil || result != resultFail || strings.Contains(log.String(), "scans skipped") {
		t.Fatalf("result %s, err %v, log:\n%s", result, err, log.String())
	}
}

func TestUncoveredEdges(t *testing.T) {
	// acknowledged.toml may name a check this config does not have.
	ev := evaluation{Checks: map[string]verdict{"licenses": {Result: resultPass}}}
	applyAcknowledged(&ev, &data{Files: []prFile{{Filename: "a", SHA: "new"}}}, []acknowledgement{
		{Check: "gone", File: "a", Blob: "old"},
		{Check: "licenses", File: "a", Blob: "old"},
	})
	if len(ev.Checks) != 1 || len(ev.Checks["licenses"].Warnings) != 1 {
		t.Fatalf("an entry of an unknown check stopped the stale check of the next: %v", ev.Checks)
	}

	// An acknowledged warning shows as ACK in the log and as acknowledged in the comment, without an entry to paste.
	cfg := config{Checks: []check{{Name: "licenses"}}}
	ev = evaluation{Checks: map[string]verdict{"licenses": {Result: resultPass, Warnings: []warning{
		{File: "f", Notice: "n", Text: "t", Acknowledged: true},
		{File: "g", Notice: "open one", Text: "t"},
	}}}}
	var out strings.Builder
	report(&out, cfg, ev)
	if !strings.Contains(out.String(), "ACK   licenses") || !strings.Contains(out.String(), "WARN  licenses") || !strings.Contains(out.String(), "file = \"g\"") || strings.Count(out.String(), "::warning") != 1 {
		t.Fatalf("report:\n%s", out.String())
	}
	if md := markdown(cfg, &data{}, ev); !strings.Contains(md, "☑️ | licenses | acknowledged") || !strings.Contains(md, "🔶 | licenses | g") {
		t.Fatalf("markdown:\n%s", md)
	}

	// A PR over the API's limits fails every check with the reason.
	full, err := loadConfig([]byte(engineConfig))
	if err != nil {
		t.Fatal(err)
	}
	d := &data{Limit: "251 commits", Relevant: []string{"template", "marker"}}
	for name, v := range evaluate(full, d, &ruleEnv{}).Checks {
		if v.Result != resultFail || !strings.Contains(v.Reason, "too large") {
			t.Errorf("%s: %+v", name, v)
		}
	}

	// A fragment that adds a ### Fixed section is a fix even when the template says it is not.
	frag := prFile{Filename: "changelog.d/lane-1234567.md", Status: "added", Patch: "@@ -0,0 +1 @@\n+### Fixed"}
	if o := ruleFixLedger(&data{Pull: pull{Body: "Fix: no"}, Files: []prFile{frag}}, params{"ledger": "l"}, nil); !strings.Contains(o.reason, "adds a ### Fixed section") {
		t.Fatalf("outcome %+v", o)
	}

	if got := (finding{file: "f", message: "m"}).String(); got != "f m" {
		t.Fatalf("a finding without a line = %q", got)
	}
	if got := jsonPath(map[string]any{"a": "text"}, "a.b"); len(got) != 0 {
		t.Fatalf("a path through a string = %v", got)
	}
	mixed := map[string]any{"items": []any{"not an object", map[string]any{"a": "found"}}}
	if got := jsonPath(mixed, "items[].a"); len(got) != 1 || got[0] != "found" {
		t.Fatalf("an object after a non-object in a list = %v", got)
	}
}

func TestScanEnvironment(t *testing.T) {
	dir := t.TempDir()
	ws := workspace{dir: dir, bin: filepath.Join(dir, "bin"), main: "/main"}
	fakeTool(t, ws.bin, "envtool", `[ "$SCAN_CONFIG" = "/main/cfg" ] || { echo "x:1: got $SCAN_CONFIG"; exit 1; }`)
	s := scanSpec{Input: inputAdded, Run: []string{"envtool"}, Env: map[string]string{"SCAN_CONFIG": "{main}/cfg"}, Exit: exitCodes{Clean: []int{0}, Findings: []int{1}}, Report: reportSpec{In: "stdout", Format: "lines", Pattern: `^(?P<file>[^:]+):(?P<line>\d+): (?P<message>.+)$`}}
	if o := runScan(context.Background(), scanCheck(t, s), &data{}, ws, io.Discard); o.result != resultPass {
		t.Fatalf("the scan's environment did not reach the tool: %+v", o)
	}
}

func TestRecordWritesTheStepSummary(t *testing.T) {
	cfg, d, ev := testEvaluation()
	summary := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(summary, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	var log strings.Builder
	if err := record(context.Background(), cfg, d, ev, recordOptions{dryRun: true}, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "record (dry run): status failure") {
		t.Fatalf("writing the summary ended the record before the status:\n%s", log.String())
	}
	raw, _ := os.ReadFile(summary)
	if !strings.HasPrefix(string(raw), "before\n"+commentMarker) {
		t.Fatalf("summary:\n%s", raw)
	}
	fresh := filepath.Join(t.TempDir(), "new-summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", fresh)
	if err := record(context.Background(), cfg, d, ev, recordOptions{dryRun: true}, io.Discard); err != nil {
		t.Fatalf("a step summary that does not exist yet: %v", err)
	}
	if raw, _ := os.ReadFile(fresh); !strings.HasPrefix(string(raw), commentMarker) {
		t.Fatalf("new summary:\n%s", raw)
	}
	t.Setenv("GITHUB_STEP_SUMMARY", t.TempDir())
	if err := record(context.Background(), cfg, d, ev, recordOptions{dryRun: true}, io.Discard); err == nil {
		t.Fatal("an unwritable step summary was not reported")
	}
}

func TestRunCommandReportsABatchThatCannotStart(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "t")
	dir := t.TempDir()
	// A main file a check depends on that cannot be read stops the run before any API call.
	if err := os.Mkdir(filepath.Join(dir, "LICENSES"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "LICENSES"), 0o755) })
	var out, errOut strings.Builder
	code := run([]string{"run", "--repo", "o/r", "--root", dir, "--bin", dir, "--work", dir}, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "check licenses") {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
}

func TestInstallPip(t *testing.T) {
	dir := t.TempDir()
	fakes := filepath.Join(dir, "fakes")
	// A fake python3 makes the virtual environment with a pip that installs the tool and records its arguments.
	fakeTool(t, fakes, "python3", `venv="$3"; mkdir -p "$venv/bin"
cat > "$venv/bin/pip" <<PIP
#!/bin/sh
echo "\$@" > "$venv/pip-args"
printf '#!/bin/sh\necho reuse\n' > "$venv/bin/reuse"; chmod +x "$venv/bin/reuse"
PIP
chmod +x "$venv/bin/pip"`)
	t.Setenv("PATH", fakes+string(os.PathListSeparator)+os.Getenv("PATH"))
	bin := filepath.Join(dir, "bin")
	cfg := config{Tools: []tool{{Name: "reuse", Version: "6", Pip: "reqs.txt"}}}
	if err := install(context.Background(), cfg, "/main", bin, filepath.Join(dir, "cache"), io.Discard); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(bin, ".venv-reuse", "pip-args"))
	if !strings.Contains(string(args), "--require-hashes -r /main/reqs.txt") {
		t.Fatalf("pip ran with %q", args)
	}
	if target, err := os.Readlink(filepath.Join(bin, "reuse")); err != nil || !strings.HasSuffix(target, ".venv-reuse/bin/reuse") {
		t.Fatalf("link %q, err %v", target, err)
	}
	fakeTool(t, fakes, "python3", `echo "no venv module" >&2; exit 1`)
	if err := install(context.Background(), cfg, "/main", filepath.Join(dir, "bin2"), "", io.Discard); err == nil || !strings.Contains(err.Error(), "no venv module") {
		t.Fatalf("a failing python3 was not reported: %v", err)
	}
}

func TestScanRunsThisProgram(t *testing.T) {
	dir := t.TempDir()
	cfg, err := loadConfig([]byte(`
engine-revision: 1
checks:
  - name: self
    when: {always: true}
    stage: scan
    scan:
      input: added
      run: ['{self}', '-test.run=^$']
      exit: {clean: [0]}
      report: {in: stdout, format: lines, pattern: '^(?P<message>FAIL.*)$'}
`))
	if err != nil {
		t.Fatal(err)
	}
	d := &data{Files: []prFile{{Filename: "a", Status: "added", Patch: "@@ -0,0 +1 @@\n+x"}}}
	ev := evaluation{Checks: map[string]verdict{"self": {Result: resultScan}}, Scans: []string{"self"}}
	if err := runScans(context.Background(), cfg, d, &ev, workspace{dir: dir, bin: dir}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if v := ev.Checks["self"]; v.Result != resultPass {
		t.Fatalf("{self} did not run this program: %+v", v)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "data.json")); err != nil || !strings.Contains(string(raw), `"filename": "a"`) {
		t.Fatalf("{data} was not written: %s %v", raw, err)
	}
}
