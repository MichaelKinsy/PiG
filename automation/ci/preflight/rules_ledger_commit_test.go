// SPDX-License-Identifier: MIT

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// commitAnswer is what the commit endpoint says for one commit: a failure status, or the files the commit changed.
type commitAnswer struct {
	status int
	files  []string
}

// ledgerAPI serves a PR whose changed test files, commits and ledger are given, and the commit endpoint from answers.
// calls collects the commit endpoint's requests as "<sha10> page <n>".
type ledgerAPI struct {
	*httptest.Server
	mu    sync.Mutex
	calls []string
}

func newLedgerAPI(t *testing.T, ledger string, tests, commits []string, answers map[string]commitAnswer) *ledgerAPI {
	t.Helper()
	files, err := json.Marshal(ledgerFiles(tests...))
	if err != nil {
		t.Fatal(err)
	}
	var shas []string
	for _, c := range commits {
		shas = append(shas, `{"sha":"`+c+`"}`)
	}
	api := &ledgerAPI{}
	api.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == "/repos/o/r/pulls/7":
			_, _ = fmt.Fprintf(w, `{"number":7,"body":"","mergeable":true,"commits":%d,"changed_files":%d,"head":{"sha":"headsha"},"base":{"sha":"b","ref":"main"}}`, len(commits), 2*len(tests))
		case p == "/repos/o/r/pulls/7/files":
			_, _ = w.Write(files)
		case p == "/repos/o/r/pulls/7/commits":
			_, _ = io.WriteString(w, "["+strings.Join(shas, ",")+"]")
		case strings.HasPrefix(p, "/repos/o/r/compare/"):
			_, _ = io.WriteString(w, `{"status":"ahead","ahead_by":1,"behind_by":0}`)
		case p == "/repos/o/r/contents/test/parity/testprune-reviewed.toml":
			_, _ = io.WriteString(w, `{"encoding":"base64","content":"`+base64.StdEncoding.EncodeToString([]byte(ledger))+`"}`)
		case strings.HasPrefix(p, "/repos/o/r/commits/"):
			sha := strings.TrimPrefix(p, "/repos/o/r/commits/")
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
			api.mu.Lock()
			api.calls = append(api.calls, fmt.Sprintf("%s page %d", sha[:10], page))
			api.mu.Unlock()
			a, ok := answers[sha]
			if !ok || a.status != 0 {
				http.Error(w, "no", max(a.status, http.StatusNotFound))
				return
			}
			var listed []string
			for i := (page - 1) * per; i < min(page*per, len(a.files)) && i >= 0; i++ {
				listed = append(listed, `{"filename":`+strconv.Quote(a.files[i])+`}`)
			}
			_, _ = io.WriteString(w, `{"sha":"`+sha+`","files":[`+strings.Join(listed, ",")+`]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	return api
}

func (a *ledgerAPI) evaluate(t *testing.T) (*data, evaluation) {
	t.Helper()
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	d, err := fetchWith(t, cfg, fetchOptions{apiBase: a.URL, repo: "o/r", pr: 7, version: "v"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return d, evaluate(cfg, d, ledgerEnv(t))
}

// manyFiles are n file names, the one at index at being file.
func manyFiles(n, at int, file string) []string {
	var out []string
	for i := range n {
		out = append(out, fmt.Sprintf("other/f%03d.go", i))
	}
	out[at] = file
	return out
}

// A row admits a test file only when the commit it names changed that file, as the line lint requires. The commit's
// files are fetched for the rows that apply, one commit at a time, and a failure of that fetch leaves the check undecided.
func TestAdmissionRequiresTheNamedCommitToChangeTheFile(t *testing.T) {
	rowA := ledgerRow(ledgerCommitA, ledgerTestA, "NOBEHAVIOR", "", "PiG-only.")
	rowB := ledgerRow(ledgerCommitB, ledgerTestB, "NOBEHAVIOR", "", "Also PiG-only.")
	for _, tc := range []struct {
		name    string
		ledger  string
		tests   []string
		commits []string
		answers map[string]commitAnswer
		result  string
		reason  string
		failed  bool
		calls   []string
	}{
		{"the commit changed the file", rowA, []string{ledgerTestA}, []string{ledgerCommitA},
			map[string]commitAnswer{ledgerCommitA: {files: []string{"a/code.go", ledgerTestA}}},
			resultPass, "admitted", false, []string{"aaaaaaaaaa page 1"}},
		{"the commit changed other files", rowA, []string{ledgerTestA}, []string{ledgerCommitA},
			map[string]commitAnswer{ledgerCommitA: {files: []string{"a/code.go", "b/b_test.go"}}},
			resultFail, "ledger row aaaaaaaaaa a/a_test.go: that commit does not change the file", false, []string{"aaaaaaaaaa page 1"}},
		{"the commit changed nothing", rowA, []string{ledgerTestA}, []string{ledgerCommitA},
			map[string]commitAnswer{ledgerCommitA: {}},
			resultFail, "that commit does not change the file", false, []string{"aaaaaaaaaa page 1"}},
		{"the commit endpoint fails", rowA, []string{ledgerTestA}, []string{ledgerCommitA},
			map[string]commitAnswer{ledgerCommitA: {status: http.StatusBadGateway}},
			resultError, "source reviewed-ledger failed", true, []string{"aaaaaaaaaa page 1", "aaaaaaaaaa page 1", "aaaaaaaaaa page 1", "aaaaaaaaaa page 1"}},
		{"the commit endpoint answers 404 for a commit the PR lists", rowA, []string{ledgerTestA}, []string{ledgerCommitA},
			map[string]commitAnswer{},
			resultError, "source reviewed-ledger failed", true, []string{"aaaaaaaaaa page 1"}},
		{"the file is on the third page of a commit", rowA, []string{ledgerTestA}, []string{ledgerCommitA},
			map[string]commitAnswer{ledgerCommitA: {files: manyFiles(250, 230, ledgerTestA)}},
			resultPass, "admitted", false, []string{"aaaaaaaaaa page 1", "aaaaaaaaaa page 2", "aaaaaaaaaa page 3"}},
		{"the commit has exactly two full pages of files", rowA, []string{ledgerTestA}, []string{ledgerCommitA},
			map[string]commitAnswer{ledgerCommitA: {files: manyFiles(200, 150, ledgerTestA)}},
			resultPass, "admitted", false, []string{"aaaaaaaaaa page 1", "aaaaaaaaaa page 2", "aaaaaaaaaa page 3"}},
		{"the file is on no page of a long commit", rowA, []string{ledgerTestA}, []string{ledgerCommitA},
			map[string]commitAnswer{ledgerCommitA: {files: manyFiles(250, 230, "elsewhere_test.go")}},
			resultFail, "that commit does not change the file", false, []string{"aaaaaaaaaa page 1", "aaaaaaaaaa page 2", "aaaaaaaaaa page 3"}},
		{"two rows, one call each", rowA + "\n" + rowB, []string{ledgerTestA, ledgerTestB}, []string{ledgerCommitA, ledgerCommitB},
			map[string]commitAnswer{ledgerCommitA: {files: []string{ledgerTestA}}, ledgerCommitB: {files: []string{ledgerTestB}}},
			resultPass, "2 changed test file(s)", false, []string{"aaaaaaaaaa page 1", "bbbbbbbbbb page 1"}},
		{"two rows, the second commit did not change its file", rowA + "\n" + rowB, []string{ledgerTestA, ledgerTestB}, []string{ledgerCommitA, ledgerCommitB},
			map[string]commitAnswer{ledgerCommitA: {files: []string{ledgerTestA}}, ledgerCommitB: {files: []string{ledgerTestA}}},
			resultFail, "ledger row bbbbbbbbbb b/b_test.go: that commit does not change the file", false, []string{"aaaaaaaaaa page 1", "bbbbbbbbbb page 1"}},
		{"a row of a commit the PR does not have asks for nothing", rowB, []string{ledgerTestB}, []string{ledgerCommitA},
			map[string]commitAnswer{}, resultFail, "in the PR body and a commit trailer", false, nil},
		{"a row of another file asks for nothing", rowB, []string{ledgerTestA}, []string{ledgerCommitB},
			map[string]commitAnswer{}, resultFail, "in the PR body and a commit trailer", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newLedgerAPI(t, tc.ledger, tc.tests, tc.commits, tc.answers)
			d, ev := api.evaluate(t)
			v := ev.Checks["test-expectation"]
			if _, failed := d.SourceErrors["reviewed-ledger"]; failed != tc.failed || v.Result != tc.result || !strings.Contains(v.Reason, tc.reason) {
				t.Fatalf("source errors %v, verdict %s %q, want %s %q", d.SourceErrors, v.Result, v.Reason, tc.result, tc.reason)
			}
			if strings.Join(api.calls, ", ") != strings.Join(tc.calls, ", ") {
				t.Fatalf("the commit endpoint was asked %q, want %q", api.calls, tc.calls)
			}
		})
	}
}

// What the commit changed is an input of the check: the same ledger and files with another answer is another input.
func TestCommitFilesAreAnInputOfTheCheck(t *testing.T) {
	row := ledgerRow(ledgerCommitA, ledgerTestA, "NOBEHAVIOR", "", "PiG-only.")
	changed := newLedgerAPI(t, row, []string{ledgerTestA}, []string{ledgerCommitA}, map[string]commitAnswer{ledgerCommitA: {files: []string{ledgerTestA}}})
	unchanged := newLedgerAPI(t, row, []string{ledgerTestA}, []string{ledgerCommitA}, map[string]commitAnswer{ledgerCommitA: {files: []string{"x.go"}}})
	_, evA := changed.evaluate(t)
	_, evB := unchanged.evaluate(t)
	if evA.Checks["test-expectation"].Input == evB.Checks["test-expectation"].Input {
		t.Fatal("two commits that change different files give the same input hash: a reused verdict could outlive the commit's files")
	}
}

// The commit endpoint is asked once for each commit a row names for a changed test file, and for no more than
// maxLedgerCommits of them: a PR with rows of more commits is a finding that asks to split it, with no call made.
func TestLedgerRowsOfTooManyCommitsAskToSplit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		n      int
		result string
		reason string
		calls  int
	}{
		{"at the cap", maxLedgerCommits, resultPass, "admitted", maxLedgerCommits},
		{"one over the cap", maxLedgerCommits + 1, resultFail, fmt.Sprintf("rows of %d commits of the PR", maxLedgerCommits+1), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tests, commits []string
			var rows []string
			answers := map[string]commitAnswer{}
			for i := range tc.n {
				test := fmt.Sprintf("d%02d/t_test.go", i)
				sha := fmt.Sprintf("%010x", i+1) + strings.Repeat("e", 30)
				tests, commits = append(tests, test), append(commits, sha)
				rows = append(rows, ledgerRow(sha, test, "NOBEHAVIOR", "", "PiG-only."))
				answers[sha] = commitAnswer{files: []string{test}}
			}
			api := newLedgerAPI(t, strings.Join(rows, "\n"), tests, commits, answers)
			_, ev := api.evaluate(t)
			v := ev.Checks["test-expectation"]
			if v.Result != tc.result || !strings.Contains(v.Reason, tc.reason) || len(api.calls) != tc.calls {
				t.Fatalf("verdict %s %q after %d commit call(s), want %s %q after %d", v.Result, v.Reason, len(api.calls), tc.result, tc.reason, tc.calls)
			}
		})
	}
}

// The finding of a row whose commit does not change the file names the path of the row, which the pull request wrote.
func TestCommitCheckFindingTextCannotStartAWorkflowCommand(t *testing.T) {
	for _, h := range hostileTexts {
		t.Run(h.name, func(t *testing.T) {
			name := "a/" + h.text + "_test.go"
			d := &data{
				Files:          ledgerFiles(name),
				Commits:        []prCommit{commit(ledgerCommitA, "a: b", true)},
				ReviewedLedger: ledgerRow(ledgerCommitA, name, "NOBEHAVIOR", "", "A reason."),
				CommitFiles:    changedBy([]string{"a/other.go"}, ledgerCommitA),
			}
			log, _ := printedBy(t, d)
			const commandOfError = "::error title=pre-flight test-expectation #7::"
			if cmds := workflowCommands(log); len(cmds) != 1 || !strings.HasPrefix(cmds[0], commandOfError) {
				t.Fatalf("the log runs %q, want the one error of the finding:\n%q", cmds, log)
			}
			if legacyLeft(log) {
				t.Errorf("the log holds the sequence of a legacy workflow command:\n%q", log)
			}
			if !strings.Contains(log, "that commit does not change the file") {
				t.Errorf("the finding is missing:\n%q", log)
			}
		})
	}
}
