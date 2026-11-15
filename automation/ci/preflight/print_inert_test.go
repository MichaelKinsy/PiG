// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// logf is where an error reaches the log, and an error of the API or of git can quote text of the pull request.
func TestLogfPrintsAnErrorOnOneLine(t *testing.T) {
	for _, h := range hostileTexts {
		t.Run(h.name, func(t *testing.T) {
			var out bytes.Buffer
			logf(&out, "source files failed after retries: %v", errors.New("HTTP 502: "+h.text))
			if cmds := workflowCommands(out.String()); len(cmds) != 0 {
				t.Errorf("the log runs %q:\n%q", cmds, out.String())
			}
			if legacyLeft(out.String()) {
				t.Errorf("the log holds the sequence of a legacy workflow command:\n%q", out.String())
			}
			if n := len(logLines(out.String())); n != 1 {
				t.Errorf("%d line(s), want one:\n%q", n, out.String())
			}
		})
	}
}

// The run error of `run` goes to stderr, and an error of the OS can quote a path an argument named: it is one line.
func TestRunErrorOnStderrIsOneLine(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "t")
	t.Setenv("GH_TOKEN", "t")
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hostileTexts {
		t.Run(h.name, func(t *testing.T) {
			dir := t.TempDir()
			args := []string{"--repo", "o/r", "--root", dir, "--bin", dir, "--work", filepath.Join(dir, "work"), "--api", server.URL, "--prs", "1", "--dry-run",
				"--handled", filepath.Join(dir, "no-such-dir"+h.text, "handled.json")}
			var stdout, stderr bytes.Buffer
			if rc := runCommand(context.Background(), cfg, args, &stdout, &stderr); rc != 2 {
				t.Fatalf("exit status %d, want 2; stderr:\n%q", rc, stderr.String())
			}
			lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
			if legacyLeft(stderr.String()) {
				t.Errorf("stderr holds the sequence of a legacy workflow command:\n%q", stderr.String())
			}
			if len(lines) != 1 || !strings.HasPrefix(lines[0], "preflight run:") {
				t.Errorf("stderr is %d line(s), want the one run error:\n%q", len(lines), stderr.String())
			}
		})
	}
}

// The log line of a merge that git could not judge quotes git's error, which can quote text of the pull request.
func TestMergeableLogLineIsOneLine(t *testing.T) {
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
	for _, h := range hostileTexts {
		t.Run(h.name, func(t *testing.T) {
			g := newFakeGitHub(map[int]string{1: "Fix: no"})
			g.unknown = true
			server := httptest.NewServer(g)
			defer server.Close()
			dir := t.TempDir()
			o := runOptions{apiBase: server.URL, gitBase: "file://" + filepath.Join(dir, "no-such-remote"+h.text), token: "t", repo: "o/r", root: dir, bin: dir, work: filepath.Join(dir, "work"), dryRun: true}
			var log strings.Builder
			if _, _, err := runPR(context.Background(), cfg, &ruleEnv{root: dir}, o, "v", pending{pr: 1}, nil, nil, &log); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(log.String(), "git merge-tree could not tell") {
				t.Fatalf("the merge line is missing:\n%s", log.String())
			}
			if legacyLeft(log.String()) {
				t.Errorf("the log holds the sequence of a legacy workflow command:\n%q", log.String())
			}
			if cmds := workflowCommands(log.String()); len(cmds) != 0 {
				t.Errorf("the log runs %q:\n%q", cmds, log.String())
			}
		})
	}
}

// inert keeps text readable: a legacy opener gets a space, a leading "::" is split, nothing else changes.
func TestInertKeepsTextReadable(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":              "plain text",
		"a ##[error]x b":          "a # #[error]x b",
		"###[error]x":             "## #[error]x",
		"ok\r\n##[add-mask]s":     "ok  # #[add-mask]s",
		"  ::error::x":            "  : :error::x",
		"a ::error::x":            "a ::error::x",
		"##[set-output name=a;]b": "# #[set-output name=a;]b",
	} {
		if got := inert(in); got != want {
			t.Errorf("inert(%q) = %q, want %q", in, got, want)
		}
		if legacyLeft(inert(in)) {
			t.Errorf("inert(%q) still holds a legacy opener", in)
		}
	}
}

// The alert comment names the author on purpose and quotes the verdict's reason, which holds text of the pull request.
func TestAlertBodyQuotesTheReasonInertly(t *testing.T) {
	for _, h := range hostileTexts {
		t.Run(h.name, func(t *testing.T) {
			var d data
			d.Pull.User.Login = "author"
			reason := "a/f.go:3 @someone <img src=x> [x](http://e) | `tick` " + h.text
			ev := evaluation{Head: "0123456789abcdef", Checks: map[string]verdict{"gitleaks": {Result: resultFail, Reason: reason}}}
			body := alertBody(check{Name: "gitleaks", Alert: "Please remove the secret."}, &d, ev)
			paragraphs := strings.Split(body, "\n\n")
			if len(paragraphs) != 3 || !strings.HasPrefix(paragraphs[0], "@author Please") {
				t.Fatalf("the alert has %d paragraph(s), the first %q, want the mention, the verdict line, the marker:\n%s", len(paragraphs), paragraphs[0], body)
			}
			if rest := outsideCodeSpans(paragraphs[1]); rest != "gitleaks on : " {
				t.Errorf("outside a code span the verdict line holds %q:\n%s", rest, paragraphs[1])
			}
		})
	}
}

// A backtick in the text cannot close the span: the fence is one backtick longer than the longest run in the text.
func TestCellFenceOutgrowsTheBackticksInTheText(t *testing.T) {
	for n := 1; n <= 3; n++ {
		ticks := strings.Repeat("`", n)
		text := "a" + ticks + " @someone | ::error::x " + ticks + "b"
		got := cell(text)
		fence := strings.Repeat("`", n+1)
		want := fence + " a" + ticks + " @someone \\| ::error::x " + ticks + "b " + fence
		if got != want {
			t.Errorf("cell(%q) = %q, want %q", text, got, want)
		}
		if rest := outsideCodeSpans("| " + got + " |"); rest != "|  |" {
			t.Errorf("outside the code span of %d backtick(s) the cell holds %q", n, rest)
		}
		t.Logf("%d backtick(s): %s", n, got)
	}
}
