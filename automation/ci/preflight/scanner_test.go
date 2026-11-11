// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAddedNumbered(t *testing.T) {
	patch := "@@ -1,3 +1,4 @@\n keep\n-gone\n+new 2\n+new 3\n keep\n@@ -10,2 +11,2 @@\n keep 11\n-x\n+new 12\n\\ No newline at end of file"
	got := addedNumbered(patch)
	want := []numberedLine{{2, "new 2"}, {3, "new 3"}, {12, "new 12"}}
	if !slices.Equal(got, want) {
		t.Fatalf("added %v, want %v", got, want)
	}
	if got := addedNumbered("+not in a hunk"); len(got) != 0 {
		t.Fatalf("a line before any hunk header counted: %v", got)
	}
}

func mustSpec(t *testing.T, r reportSpec) reportSpec {
	t.Helper()
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParseReport(t *testing.T) {
	lines := mustSpec(t, reportSpec{In: "stdout", Format: "lines", Pattern: `^(?P<file>[^:]+):(?P<line>\d+): (?P<message>.+)$`, Ignore: `^note:`})
	found, err := parseReport(lines, []byte("a.go:3: bad\nnote: x.go:1: skip me\nunrelated text\nb/c.go:10: worse\n"))
	if err != nil || len(found) != 2 || found[0] != (finding{"a.go", 3, "bad"}) || found[1] != (finding{"b/c.go", 10, "worse"}) {
		t.Fatalf("lines: %v %v", found, err)
	}

	array := mustSpec(t, reportSpec{In: "out", Format: "json", File: "File", Line: "StartLine", Message: "RuleID"})
	found, err = parseReport(array, []byte(`[{"File":"x","StartLine":4,"RuleID":"aws"},{"File":"y","StartLine":1,"RuleID":"gcp"}]`))
	if err != nil || len(found) != 2 || found[1] != (finding{"y", 1, "gcp"}) {
		t.Fatalf("json array: %v %v", found, err)
	}
	if found, err := parseReport(array, []byte("  \n")); err != nil || len(found) != 0 {
		t.Fatalf("an empty JSON report: %v %v", found, err)
	}

	nested := mustSpec(t, reportSpec{In: "stdout", Format: "json", List: "Results[].Misconfigurations[]", File: "Target", Line: "Cause.StartLine", Message: "ID"})
	found, err = parseReport(nested, []byte(`{"Results":[{"Misconfigurations":[{"ID":"A1","Target":"t","Cause":{"StartLine":7}}]},{"Misconfigurations":[{"ID":"A2"}]}]}`))
	if err != nil || len(found) != 2 || found[0] != (finding{"t", 7, "A1"}) || found[1].message != "A2" {
		t.Fatalf("json list path: %v %v", found, err)
	}

	jl := mustSpec(t, reportSpec{In: "stdout", Format: "json-lines", File: "Source.file", Line: "Source.line", Message: "Detector"})
	found, err = parseReport(jl, []byte("{\"Detector\":\"AWS\",\"Source\":{\"file\":\"/w/added/k.env\",\"line\":2}}\n\n{\"Detector\":\"Slack\",\"Source\":{\"file\":\"s\",\"line\":1}}\n"))
	if err != nil || len(found) != 2 || found[0] != (finding{"/w/added/k.env", 2, "AWS"}) {
		t.Fatalf("json lines: %v %v", found, err)
	}
	if _, err := parseReport(jl, []byte("{broken\n")); err == nil {
		t.Fatal("a broken JSON line was accepted")
	}
}

func TestRelativeTo(t *testing.T) {
	roots := []string{"/w/added", "", "/w/head"}
	for in, want := range map[string]string{
		"/w/added/a/b.go": "a/b.go",
		"/w/head/c.yml":   "c.yml",
		"./d.go":          "d.go",
		"e/f.go":          "e/f.go",
		"/elsewhere/g.go": "/elsewhere/g.go",
	} {
		if got := relativeTo(in, roots); got != want {
			t.Errorf("relativeTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOnAddedLines(t *testing.T) {
	d := &data{Files: []prFile{{Filename: "w.yml", Patch: "@@ -1,2 +1,3 @@\n a\n+b\n c"}}}
	got := onAddedLines([]finding{{"w.yml", 2, "on an added line"}, {"w.yml", 3, "on an old line"}, {"w.yml", 0, "no line"}, {"x.yml", 2, "another file"}}, d)
	if len(got) != 2 || got[0].message != "on an added line" || got[1].message != "no line" {
		t.Fatalf("kept %v", got)
	}
}

func TestOverlay(t *testing.T) {
	mainRoot, head := t.TempDir(), t.TempDir()
	write := func(root, name, text string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(mainRoot, "a/check.py", "main's")
	write(head, "a/check.py", "the PR's")
	write(head, "b/config.yaml", "the PR's own config")
	if err := overlay(mainRoot, head, "a/check.py"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(head, "a", "check.py")); string(raw) != "main's" {
		t.Fatalf("overlay left %q", raw)
	}
	if err := overlay(mainRoot, head, "b/config.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(head, "b", "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("a file main does not have stayed in the head")
	}
	if err := overlay(mainRoot, head, "../escape"); err == nil {
		t.Fatal("an overlay path leaving the head was accepted")
	}
}

// fakeTool writes an executable shell script into bin and returns its name.
func fakeTool(t *testing.T, bin, name, script string) string {
	t.Helper()
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return name
}

func scanCheck(t *testing.T, s scanSpec) check {
	t.Helper()
	c := check{Name: "fake", When: when{Always: true}, Stage: stageScan, Scan: &s}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRunScan(t *testing.T) {
	d := &data{Files: []prFile{
		{Filename: "a.go", Status: "modified", Patch: "@@ -1 +1,2 @@\n x\n+secret here"},
		{Filename: "old.go", Status: "removed", Patch: "@@ -1 +0,0 @@\n-x"},
		{Filename: ".github/workflows/ci.yml", Status: "added", Patch: "@@ -0,0 +1 @@\n+on: push"},
	}}
	lines := reportSpec{In: "stdout", Format: "lines", Pattern: `^(?P<file>[^:]+):(?P<line>\d+): (?P<message>.+)$`}
	for _, tc := range []struct {
		name   string
		script string
		spec   func(s *scanSpec)
		result string
		reason string
	}{
		{"clean", `exit 0`, nil, resultPass, "clean (2 file(s))"},
		{"a finding in the added lines is reported at its head line", `echo "a.go:1: leak"; exit 1`, nil, resultFail, "a.go:2 leak"},
		{"files and placeholders reach the tool", `echo "$@" >&2; [ "$1" = "--" ] && [ "$2" = "a.go" ] && [ "$3" = ".github/workflows/ci.yml" ] && [ "$4" = "MAIN" ] || { echo "x:1: args $*"; exit 1; }`, func(s *scanSpec) { s.Run = []string{"fake", "--", "{files}", "{main}"} }, resultPass, "clean"},
		{"findings exit without a readable finding is an error", `echo "something went wrong"; exit 1`, nil, resultError, "none could be read"},
		{"unread findings may pass when declared", `exit 1`, func(s *scanSpec) { s.Report.UnreadFindings = "pass" }, resultPass, "no finding"},
		{"an unknown exit code is an error", `echo boom >&2; exit 7`, nil, resultError, "exited 7: boom"},
		{"only added lines count", `echo "a.go:1: old problem"; exit 1`, func(s *scanSpec) { s.Input, s.AddedLinesOnly = inputChanged, true }, resultPass, "none on a line the PR adds"},
		{"select with no match passes", `exit 1`, func(s *scanSpec) { s.Select = `\.rs$` }, resultPass, "no file it scans changed"},
		{"select narrows the files", `[ "$#" -eq 2 ] && [ "$2" = ".github/workflows/ci.yml" ] || { echo "x:1: got $*"; exit 1; }`, func(s *scanSpec) { s.Select = `^\.github/`; s.Run = []string{"fake", "--", "{files}"} }, resultPass, "clean (1 file(s))"},
		{"the report file is read when declared", `printf '[{"m":"from the file","f":"a.go","l":2}]' > "$1"; exit 1`, func(s *scanSpec) {
			s.Run = []string{"fake", "{out}"}
			s.Report = reportSpec{In: "out", Format: "json", File: "f", Line: "l", Message: "m"}
		}, resultFail, "a.go:2 from the file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ws := workspace{dir: dir, main: "MAIN", bin: filepath.Join(dir, "bin"), added: filepath.Join(dir, "added")}
			fakeTool(t, ws.bin, "fake", tc.script)
			s := scanSpec{Input: inputAdded, Run: []string{"fake", "--", "{files}"}, Exit: exitCodes{Clean: []int{0}, Findings: []int{1}}, Report: lines}
			if tc.spec != nil {
				tc.spec(&s)
			}
			o := runScan(context.Background(), scanCheck(t, s), d, ws, io.Discard)
			if o.result != tc.result || !strings.Contains(o.reason, tc.reason) {
				t.Fatalf("outcome %s %q, want %s containing %q", o.result, o.reason, tc.result, tc.reason)
			}
		})
	}
}

func TestRunScanMissingTool(t *testing.T) {
	dir := t.TempDir()
	s := scanSpec{Input: inputAdded, Run: []string{"no-such-tool-anywhere"}, Exit: exitCodes{Clean: []int{0}}, Report: reportSpec{In: "stdout", Format: "lines", Pattern: `(?P<message>.+)`}}
	o := runScan(context.Background(), scanCheck(t, s), &data{}, workspace{dir: dir, bin: dir}, io.Discard)
	if o.result != resultError || !strings.Contains(o.reason, "could not run") {
		t.Fatalf("outcome %+v", o)
	}
}

func TestPrepareWorkspaceAddedLines(t *testing.T) {
	dir := t.TempDir()
	ws := workspace{dir: dir, added: filepath.Join(dir, "added")}
	d := &data{Files: []prFile{
		{Filename: "a/b.go", Status: "modified", Patch: "@@ -1 +1,2 @@\n-old\n+new one\n+new two"},
		{Filename: "gone.go", Status: "removed", Patch: "@@ -1 +0,0 @@\n-x"},
	}}
	scans := []check{scanCheck(t, scanSpec{Input: inputAdded, Run: []string{"x"}, Exit: exitCodes{Clean: []int{0}}, Report: reportSpec{In: "stdout", Format: "lines", Pattern: `(?P<message>.+)`}})}
	if err := prepareWorkspace(context.Background(), d, scans, ws, io.Discard); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(ws.added, "a", "b.go")); err != nil || string(raw) != "new one\nnew two\n" {
		t.Fatalf("added lines %q, err %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(ws.added, "gone.go")); !os.IsNotExist(err) {
		t.Fatal("a removed file was written")
	}
	d.Files = []prFile{{Filename: "../escape", Status: "added", Patch: "@@ -0,0 +1 @@\n+x"}}
	if err := prepareWorkspace(context.Background(), d, scans, ws, io.Discard); err == nil || !strings.Contains(err.Error(), "leaves") {
		t.Fatalf("err = %v", err)
	}
}

func TestScanInputHash(t *testing.T) {
	c := scanCheck(t, scanSpec{Input: inputAdded, Run: []string{"x"}, Exit: exitCodes{Clean: []int{0}}, Report: reportSpec{In: "stdout", Format: "lines", Pattern: `(?P<message>.+)`}})
	files := []prFile{{Filename: "a", SHA: "1", Patch: "+x"}}
	base := scanInputHash(c, files)
	other := c
	other.definition += "x"
	for name, other := range map[string]string{
		"another definition": scanInputHash(other, files),
		"another blob":       scanInputHash(c, []prFile{{Filename: "a", SHA: "2", Patch: "+x"}}),
		"another patch":      scanInputHash(c, []prFile{{Filename: "a", SHA: "1", Patch: "+y"}}),
	} {
		if other == base {
			t.Errorf("%s gives the same hash", name)
		}
	}
	if scanInputHash(c, files) != base {
		t.Error("the hash is not stable")
	}
}

// gitRepo makes a repository at dir/o/r whose refs/pull/1/head holds files, so a workspace can fetch the PR's head from
// dir as its remote.
func gitRepo(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	repo := filepath.Join(dir, "o", "r")
	run := func(args ...string) string {
		t.Helper()
		out, err := git(context.Background(), append([]string{"-C", repo}, args...)...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(out)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	for name, body := range files {
		p := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "head")
	head := run("rev-parse", "HEAD")
	run("update-ref", "refs/pull/1/head", head)
	return head
}

func TestPrepareWorkspaceHead(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote := t.TempDir()
	head := gitRepo(t, remote, map[string]string{
		"a/changed.go":     "package a // the PR's",
		"LICENSES/MIT.txt": "MIT text",
		"b/untouched.go":   "package b",
		"tool/check.py":    "the PR's check",
		"cfg/lint.yaml":    "the PR's lint config",
	})
	mainRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mainRoot, "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainRoot, "tool", "check.py"), []byte("main's check"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ws := workspace{dir: dir, added: filepath.Join(dir, "added"), head: filepath.Join(dir, "head"), tree: filepath.Join(dir, "tree"), main: mainRoot, remote: "file://" + remote}
	d := &data{Repo: "o/r", PR: 1, Files: []prFile{{Filename: "a/changed.go", Status: "modified", Patch: "@@ -1 +1 @@\n+package a // the PR's"}}}
	d.Pull.Head.SHA = head
	report := reportSpec{In: "stdout", Format: "lines", Pattern: `(?P<message>.+)`}
	scans := []check{
		scanCheck(t, scanSpec{Input: inputChanged, Checkout: []string{"LICENSES/"}, Overlay: []string{"tool/check.py", "cfg/lint.yaml"}, Run: []string{"x"}, Exit: exitCodes{Clean: []int{0}}, Report: report}),
		scanCheck(t, scanSpec{Input: inputTree, Run: []string{"x"}, Exit: exitCodes{Clean: []int{0}}, Report: report}),
	}
	if err := prepareWorkspace(context.Background(), d, scans, ws, io.Discard); err != nil {
		t.Fatal(err)
	}
	read := func(p string) string {
		raw, err := os.ReadFile(p)
		if err != nil {
			return "<missing>"
		}
		return string(raw)
	}
	for p, want := range map[string]string{
		"head/a/changed.go":     "package a // the PR's",
		"head/LICENSES/MIT.txt": "MIT text",
		"head/b/untouched.go":   "<missing>",
		"head/tool/check.py":    "main's check",
		"head/cfg/lint.yaml":    "<missing>",
		"tree/a/changed.go":     "package a // the PR's",
		"tree/LICENSES/MIT.txt": "<missing>",
	} {
		if got := read(filepath.Join(dir, filepath.FromSlash(p))); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "tree", ".git")); err != nil {
		t.Error("the tree is not a repository its scanner can list")
	}

	// A PR that moved after it was evaluated is not scanned at the newer head.
	d.Pull.Head.SHA = strings.Repeat("0", 40)
	ws.dir, ws.head, ws.tree = t.TempDir(), "", ""
	ws.head, ws.tree = filepath.Join(ws.dir, "head"), filepath.Join(ws.dir, "tree")
	if err := prepareWorkspace(context.Background(), d, scans, ws, io.Discard); err == nil || !strings.Contains(err.Error(), "the PR moved") {
		t.Fatalf("err = %v", err)
	}
}

func TestPrepareWorkspaceWholeTreeAndSeveralFiles(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote := t.TempDir()
	head := gitRepo(t, remote, map[string]string{"one.go": "1", "two.go": "2", "other/untouched.go": "u"})
	report := reportSpec{In: "stdout", Format: "lines", Pattern: `(?P<message>.+)`}
	d := &data{Repo: "o/r", PR: 1, Files: []prFile{
		{Filename: "gone.go", Status: "removed"},
		{Filename: "one.go", Status: "modified", Patch: "@@ -0,0 +1 @@\n+1"},
		{Filename: "two.go", Status: "modified", Patch: "@@ -0,0 +1 @@\n+2"},
	}}
	d.Pull.Head.SHA = head
	for name, tc := range map[string]struct {
		checkout  []string
		untouched bool
	}{"changed files only": {nil, false}, "whole tree": {[]string{"/*"}, true}} {
		dir := t.TempDir()
		ws := workspace{dir: dir, head: filepath.Join(dir, "head"), main: t.TempDir(), remote: "file://" + remote}
		scans := []check{scanCheck(t, scanSpec{Input: inputChanged, Checkout: tc.checkout, Run: []string{"x"}, Exit: exitCodes{Clean: []int{0}}, Report: report})}
		if err := prepareWorkspace(context.Background(), d, scans, ws, io.Discard); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"one.go", "two.go"} {
			if _, err := os.Stat(filepath.Join(ws.head, f)); err != nil {
				t.Errorf("%s: %s was not checked out", name, f)
			}
		}
		if _, err := os.Stat(filepath.Join(ws.head, "other", "untouched.go")); (err == nil) != tc.untouched {
			t.Errorf("%s: an untouched file checked out = %v, want %v", name, err == nil, tc.untouched)
		}
	}
}

func TestAddedInputReadsUnpatchedFilesWhole(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote := t.TempDir()
	head := gitRepo(t, remote, map[string]string{"blob.bin": "key\x00AKIA", "a.go": "one\ntwo", "moved.go": "same"})
	dir := t.TempDir()
	ws := workspace{dir: dir, added: filepath.Join(dir, "added"), head: filepath.Join(dir, "head"), remote: "file://" + remote}
	d := &data{Repo: "o/r", PR: 1, Files: []prFile{
		{Filename: "blob.bin", Status: "added"},
		{Filename: "a.go", Status: "modified", Patch: "@@ -1 +1,2 @@\n one\n+two"},
		{Filename: "moved.go", Status: "renamed", PreviousFilename: "old.go"},
	}}
	d.Pull.Head.SHA = head
	scans := []check{scanCheck(t, scanSpec{Input: inputAdded, Run: []string{"x"}, Exit: exitCodes{Clean: []int{0}}, Report: reportSpec{In: "stdout", Format: "lines", Pattern: `(?P<message>.+)`}})}
	if err := prepareWorkspace(context.Background(), d, scans, ws, io.Discard); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(ws.added, "blob.bin")); err != nil || string(raw) != "key\x00AKIA" {
		t.Fatalf("a file without a patch: %q %v", raw, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(ws.added, "a.go")); string(raw) != "two\n" {
		t.Fatalf("a patched file: %q", raw)
	}
	if raw, _ := os.ReadFile(filepath.Join(ws.added, "moved.go")); string(raw) != "\n" {
		t.Fatalf("a rename without a change was read whole: %q", raw)
	}
	found := []finding{{file: "blob.bin", line: 1}, {file: "a.go", line: 1}, {file: "a.go", line: 9}, {file: "other", line: 1}, {file: "a.go"}}
	toHeadLines(found, d)
	if found[0].line != 1 || found[1].line != 2 || found[2].line != 9 || found[3].line != 1 || found[4].line != 0 {
		t.Fatalf("head lines %+v", found)
	}
	kept := onAddedLines([]finding{{file: "blob.bin", line: 7}, {file: "a.go", line: 1}, {file: "a.go", line: 2}}, d)
	if len(kept) != 2 || kept[0].file != "blob.bin" || kept[1].line != 2 {
		t.Fatalf("kept %+v", kept)
	}
	// A file that cannot be read from the head fails the workspace.
	d.Files = append(d.Files, prFile{Filename: "missing.bin", Status: "added"})
	if err := os.RemoveAll(ws.head); err != nil {
		t.Fatal(err)
	}
	if err := prepareWorkspace(context.Background(), d, scans, ws, io.Discard); err == nil {
		t.Fatal("a file without a patch missing at the head was not reported")
	}
}

func TestWhole(t *testing.T) {
	for _, tc := range []struct {
		f    prFile
		want bool
	}{
		{prFile{Status: "added"}, true},
		{prFile{Status: "modified", Patch: "@@"}, false},
		{prFile{Status: "removed"}, false},
		{prFile{Status: "renamed"}, false},
		{prFile{Status: "renamed", Changes: 2}, true},
	} {
		if tc.f.whole() != tc.want {
			t.Errorf("%+v: whole %v", tc.f, !tc.want)
		}
	}
}

func TestGitAuthPassesTheTokenOnlyToTheRemotesHost(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	header := func(remote, token, target string) string {
		t.Helper()
		out, _ := gitAuth(context.Background(), remote, token, "config", "--get-urlmatch", "http.extraHeader", target)
		return strings.TrimSpace(out)
	}
	const want = "Authorization: Basic eC1hY2Nlc3MtdG9rZW46dG9r"
	if got := header("https://github.com", "tok", "https://github.com/o/r"); got != want {
		t.Fatalf("the remote's host: %q", got)
	}
	if got := header("http://gitea.local/", "tok", "http://gitea.local/o/r"); got != want {
		t.Fatalf("an http remote: %q", got)
	}
	for _, tc := range []struct{ remote, token, target string }{
		{"https://github.com", "tok", "https://evil.example/o/r"},
		{"https://github.com", "", "https://github.com/o/r"},
		{"file:///tmp/x", "tok", "https://github.com/o/r"},
		{"::bad", "tok", "https://github.com/o/r"},
	} {
		if got := header(tc.remote, tc.token, tc.target); got != "" {
			t.Errorf("%+v: a header %q", tc, got)
		}
	}
}

func TestMergeTree(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote := t.TempDir()
	repo := filepath.Join(remote, "o", "r")
	run := func(args ...string) string {
		t.Helper()
		out, err := git(context.Background(), append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(out)
	}
	commit := func(file, text string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, file), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		run("add", "-A")
		run("commit", "-q", "-m", file)
		return run("rev-parse", "HEAD")
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "main")
	run("config", "uploadpack.allowFilter", "true")
	root := commit("f", "a")
	run("checkout", "-q", "-b", "conflict")
	conflict := commit("f", "b")
	run("checkout", "-q", "-b", "clean", root)
	clean := commit("g", "x")
	run("checkout", "-q", "main")
	commit("f", "c")
	run("update-ref", "refs/pull/1/head", conflict)
	run("update-ref", "refs/pull/2/head", clean)

	ws := workspace{dir: t.TempDir(), remote: "file://" + remote}
	d := &data{Repo: "o/r"}
	d.Pull.Base.Ref = "main"
	for _, tc := range []struct {
		pr   int
		head string
		want bool
	}{{1, conflict, false}, {2, clean, true}} {
		d.PR, d.Pull.Head.SHA = tc.pr, tc.head
		if m, err := mergeTree(context.Background(), ws, d); err != nil || m != tc.want {
			t.Errorf("PR #%d: mergeable %v, err %v", tc.pr, m, err)
		}
	}
	d.PR, d.Pull.Head.SHA = 2, conflict
	if _, err := mergeTree(context.Background(), ws, d); err == nil || !strings.Contains(err.Error(), "evaluated head") {
		t.Errorf("a moved head: %v", err)
	}
	d.PR = 9
	if _, err := mergeTree(context.Background(), ws, d); err == nil || !strings.Contains(err.Error(), "git -C") {
		t.Errorf("a PR without a head ref: %v", err)
	}
}

func TestPendingResult(t *testing.T) {
	cfg := config{Checks: []check{{Name: "up-to-date"}, {Name: "other"}}}
	ev := evaluation{Checks: map[string]verdict{"up-to-date": {Result: resultPending}, "other": {Result: resultPass}}}
	ev.finish()
	if state, description := statusFor(cfg, ev); ev.Result != resultPending || state != "pending" || description != "waits for the next run: up-to-date" {
		t.Fatalf("%s: %s %q", ev.Result, state, description)
	}
	ev.Checks["scan"] = verdict{Result: resultScan}
	if ev.finish(); ev.Result != resultScan {
		t.Fatalf("a scan to run and a pending check: %s", ev.Result)
	}
	ev.Checks["other"] = verdict{Result: resultFail}
	if ev.finish(); ev.Result != resultFail {
		t.Fatalf("a failure beside a pending check: %s", ev.Result)
	}
	for _, r := range []string{resultError, resultPending} {
		prev := &state{Version: "v", Head: "h", Base: "b", Checks: map[string]verdict{"a": {Result: resultPass}, "b": {Result: r}}}
		if mode, _ := decideMode(prev, "v", "h", "b", ""); mode != modeFull {
			t.Errorf("a previous %s was reused", r)
		}
	}
	prev := &state{Version: "v", Head: "h", Base: "b", Checks: map[string]verdict{"a": {Result: resultFail}, "b": {Result: resultScan}}}
	if mode, _ := decideMode(prev, "v", "h", "b", ""); mode != modeReuse {
		t.Error("scans held back by a failed check were decided again")
	}
}
