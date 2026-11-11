// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func testEvaluation() (config, *data, evaluation) {
	cfg := config{Checks: []check{{Name: "signed"}, {Name: "template"}, {Name: "reuse"}}}
	d := &data{Repo: "o/r", PR: 7, Mode: modeFull, Reason: "no previous state"}
	ev := evaluation{Head: "head1234567", Base: "base1234567", Version: "v", Checks: map[string]verdict{
		"signed":   {Result: resultPass, Reason: "1 commit(s) Verified"},
		"template": {Result: resultFail, Reason: "the PR body misses `Fix: yes` or `Fix: no` | x"},
		"reuse":    {Result: resultPass, Reason: "scan found nothing", Warnings: []warning{{File: "a.sh", Line: 2, Notice: "# Copyright X", Text: "adds a copyright notice", Blob: "b1"}}},
	}}
	ev.finish()
	return cfg, d, ev
}

func TestStatusAndMarkdown(t *testing.T) {
	cfg, d, ev := testEvaluation()
	state, description := statusFor(cfg, ev)
	if state != "failure" || description != "1 failed: template" {
		t.Fatalf("status %q %q", state, description)
	}
	md := markdown(cfg, d, ev)
	for _, want := range []string{commentMarker, "❌ fail", "| ❌ | template |", `\| x`, "🔶 | reuse | a.sh:2", "[[ack]]", `blob = "b1"`} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q:\n%s", want, md)
		}
	}
}

// recordServer is a GitHub that keeps one PR's comments and statuses for record.
type recordServer struct {
	mu          sync.Mutex
	calls       []string
	comments    map[int64]string
	alerts      []string
	statuses    []map[string]string
	patchStatus int
	next        int64
}

func (s *recordServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, r.Method+" "+r.URL.Path)
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/statuses/"):
		s.statuses = append(s.statuses, body)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments") && !strings.HasPrefix(body["body"], commentMarker):
		s.alerts = append(s.alerts, body["body"])
		_, _ = io.WriteString(w, `{"id":99}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
		s.next++
		s.comments[s.next] = body["body"]
		_, _ = fmt.Fprintf(w, `{"id":%d}`, s.next)
	case r.Method == http.MethodPatch:
		if s.patchStatus != 0 {
			http.Error(w, "no", s.patchStatus)
			return
		}
		var id int64
		_, _ = fmt.Sscanf(r.URL.Path, "/repos/o/r/issues/comments/%d", &id)
		s.comments[id] = body["body"]
		_, _ = io.WriteString(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}

func TestRecordSendsOnlyChangesAndAlertsOnce(t *testing.T) {
	cfg := config{Checks: []check{{Name: "template"}, {Name: "secrets", Alert: "revoke it"}}}
	d := &data{Repo: "o/r", PR: 7, Mode: modeFull}
	d.Pull.User.Login = "dev"
	ev := evaluation{Head: "h1", Checks: map[string]verdict{"secrets": {Result: resultFail, Reason: "1 finding(s): a:1 key"}, "template": {Result: resultPass}}}
	ev.finish()
	g := &recordServer{comments: map[int64]string{}}
	server := httptest.NewServer(g)
	defer server.Close()
	o := recordOptions{apiBase: server.URL, token: "t"}
	// step records ev and then reads the comment back as the next run would.
	step := func() *state {
		t.Helper()
		g.calls = nil
		if err := record(context.Background(), cfg, d, ev, o, io.Discard); err != nil {
			t.Fatal(err)
		}
		o.comment = prComment{ID: g.next, Body: g.comments[g.next], State: parseState(g.comments[g.next])}
		d.Previous = o.comment.State
		return d.Previous
	}
	st := step()
	if len(g.alerts) != 1 || g.alerts[0] != "@dev revoke it\n\nsecrets on `h1`: 1 finding(s): a:1 key" || st == nil || !slices.Equal(st.Alerted, []string{"secrets"}) || len(g.statuses) != 1 {
		t.Fatalf("first run: alerts %q, state %+v, statuses %v", g.alerts, st, g.statuses)
	}
	if !strings.HasPrefix(g.comments[1], commentMarker) || !strings.Contains(g.comments[1], "❌ fail") {
		t.Fatalf("comment %q", g.comments[1])
	}
	// A reused run with nothing changed sends nothing and does not alert again.
	d.Mode = modeReuse
	if step(); len(g.calls) != 0 || len(g.alerts) != 1 {
		t.Fatalf("an unchanged run sent %v", g.calls)
	}
	// A changed verdict updates the comment by its id and sets the status.
	d.Mode = modeFull
	ev.Checks["template"] = verdict{Result: resultFail, Reason: "x"}
	ev.finish()
	if step(); !slices.Equal(g.calls, []string{"POST /repos/o/r/statuses/h1", "PATCH /repos/o/r/issues/comments/1"}) {
		t.Fatalf("calls %v", g.calls)
	}
	// A full run that only moved main writes the new state but sets no status: the status did not change.
	ev.Base = "b2"
	if step(); !slices.Equal(g.calls, []string{"PATCH /repos/o/r/issues/comments/1"}) {
		t.Fatalf("calls %v", g.calls)
	}
	// A deleted comment is posted anew.
	g.patchStatus = http.StatusNotFound
	ev.Checks["template"] = verdict{Result: resultPass}
	ev.finish()
	if step(); !slices.Equal(g.calls, []string{"POST /repos/o/r/statuses/h1", "PATCH /repos/o/r/issues/comments/1", "POST /repos/o/r/issues/7/comments"}) || o.comment.ID != 2 {
		t.Fatalf("calls %v", g.calls)
	}
	g.patchStatus = 0
	// Passing clears the alert; failing again alerts again.
	ev.Checks["secrets"] = verdict{Result: resultPass}
	ev.finish()
	if st = step(); len(st.Alerted) != 0 {
		t.Fatalf("alerted %v after a pass", st.Alerted)
	}
	ev.Checks["secrets"] = verdict{Result: resultFail, Reason: "again"}
	ev.finish()
	if step(); len(g.alerts) != 2 {
		t.Fatalf("alerts %q", g.alerts)
	}
	// A new head sets the status even when it reads the same.
	ev.Head = "h2"
	g.calls = nil
	if err := record(context.Background(), cfg, d, ev, o, io.Discard); err != nil || !slices.Contains(g.calls, "POST /repos/o/r/statuses/h2") {
		t.Fatalf("calls %v, err %v", g.calls, err)
	}
	ev.Head = "h1"
	// A refused PATCH is an error, not a new comment.
	g.patchStatus = http.StatusForbidden
	ev.Checks["template"] = verdict{Result: resultFail, Reason: "y"}
	ev.finish()
	if err := record(context.Background(), cfg, d, ev, o, io.Discard); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordDryRunSaysWhatItWouldSend(t *testing.T) {
	cfg := config{Checks: []check{{Name: "secrets", Alert: "revoke it"}}}
	d := &data{Repo: "o/r", PR: 7, Mode: modeFull}
	ev := evaluation{Head: "h1", Checks: map[string]verdict{"secrets": {Result: resultFail, Reason: "r"}}}
	ev.finish()
	var log strings.Builder
	if err := record(context.Background(), cfg, d, ev, recordOptions{dryRun: true}, &log); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"record (dry run): status failure", "record (dry run): comment\n" + commentMarker, "record (dry run): alert\n@ revoke it"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("log lacks %q:\n%s", want, log.String())
		}
	}
	// What the same run would leave is unchanged for the next one.
	st := state{Head: "h1", Checks: ev.Checks, Body: bodyHash(""), Alerted: []string{"secrets"}}
	st.Status = "failure 1 failed: secrets"
	d.Previous, d.Mode = &st, modeReuse
	body := withState(markdown(cfg, d, ev), st)
	log.Reset()
	if err := record(context.Background(), cfg, d, ev, recordOptions{dryRun: true, comment: prComment{ID: 1, Body: body}}, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "nothing changed, nothing to send") || strings.Contains(log.String(), "alert") {
		t.Fatalf("log %s", log.String())
	}
	// Only the comment differs: it is sent, and the run does not say nothing changed.
	log.Reset()
	if err := record(context.Background(), cfg, d, ev, recordOptions{dryRun: true, comment: prComment{ID: 1, Body: body + "x"}}, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "record (dry run): comment") || strings.Contains(log.String(), "status") || strings.Contains(log.String(), "nothing changed") {
		t.Fatalf("log %s", log.String())
	}
}

func TestStateInTheComment(t *testing.T) {
	st := state{Version: "v", Head: "h", Checks: map[string]verdict{"a": {Result: resultFail, Reason: "a --> b -- c --- d"}}, Alerted: []string{"a"}}
	body := withState(commentMarker+"\nreport", st)
	if _, state, _ := strings.Cut(body, stateOpen); strings.Count(state, "-->") != 1 || !strings.HasSuffix(state, "\n-->\n") {
		t.Fatalf("the state ends the HTML comment early:\n%s", body)
	}
	got := parseState(body)
	if got == nil || got.Checks["a"].Reason != "a --> b -- c --- d" || got.Head != "h" {
		t.Fatalf("state %+v", got)
	}
	for _, broken := range []string{"no state", commentMarker + stateOpen + "{", commentMarker + stateOpen + "{not json" + stateClose} {
		if parseState(broken) != nil {
			t.Errorf("%q gave a state", broken)
		}
	}
}

func TestLoadCommentTrustsOnlyTheBot(t *testing.T) {
	g := newFakeGitHub(nil)
	forged := fakeComment{ID: 1, Body: withState(commentMarker+"\n", state{Head: "forged"})}
	forged.User.Login = "author"
	plain := fakeComment{ID: 2, Body: "thanks"}
	plain.User.Login = defaultBot
	g.comments[5] = []fakeComment{forged, plain}
	withComment(g, 5, state{Head: "real"})
	server := httptest.NewServer(g)
	defer server.Close()
	c, err := loadComment(context.Background(), newAPI(server.URL, "t"), "o/r", 5, defaultBot)
	if err != nil || c.State == nil || c.State.Head != "real" {
		t.Fatalf("comment %+v, err %v", c, err)
	}
	if c, err := loadComment(context.Background(), newAPI(server.URL, "t"), "o/r", 6, defaultBot); err != nil || c.ID != 0 || c.State != nil {
		t.Fatalf("a PR without a comment: %+v %v", c, err)
	}
	g.broken[-5] = true
	if _, err := loadComment(context.Background(), newAPI(server.URL, "t"), "o/r", 5, defaultBot); err == nil {
		t.Fatal("a failed read was not reported")
	}
}

func TestAppendFileKeepsEveryReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary")
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			if err := appendFile(path, strings.Repeat(string(rune('a'+i%26)), 100)+"\n"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if raw, _ := os.ReadFile(path); strings.Count(string(raw), "\n") != 50 || len(raw) != 50*101 {
		t.Fatalf("%d bytes, %d reports", len(raw), strings.Count(string(raw), "\n"))
	}
	if err := appendFile(filepath.Join(path, "x"), "y"); err == nil {
		t.Fatal("an unwritable summary was not reported")
	}
}

func TestWarningsArePerFileAndBounded(t *testing.T) {
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	// Three notices in one file are one warning that counts the other two; one acknowledgement covers the file.
	long := "# Copyright 2020 " + strings.Repeat("x", 300)
	f := prFile{Filename: "x.sh", Status: "modified", SHA: "blob", Patch: "@@ -1 +1,4 @@\n x\n+" + long + "\n+# Copyright 2021 B\n+# Copyright 2022 C"}
	d := &data{Version: "v", Files: []prFile{f}, Relevant: []string{"licenses"}}
	ev := evaluate(cfg, d, &ruleEnv{root: t.TempDir()})
	ws := ev.Checks["licenses"].Warnings
	if len(ws) != 1 || ws[0].Line != 2 || ws[0].More != 2 || len([]rune(ws[0].Notice)) != maxNotice || !strings.HasSuffix(ws[0].Notice, "…") {
		t.Fatalf("warnings %+v", ws)
	}
	if where := ws[0].where(); !strings.HasPrefix(where, "x.sh:2 adds a copyright notice: ") || !strings.HasSuffix(where, " (and 2 more in this file)") {
		t.Fatalf("where %q", where)
	}
	d.Acknowledged = ackEntry("licenses", ws[0])
	if strings.Contains(d.Acknowledged, "notice") {
		t.Fatalf("an entry names a notice:\n%s", d.Acknowledged)
	}
	if ws := evaluate(cfg, d, &ruleEnv{root: t.TempDir()}).Checks["licenses"].Warnings; len(ws) != 1 || !ws[0].Acknowledged {
		t.Fatalf("the file's entry did not cover it: %+v", ws)
	}

	// Warnings in more files than maxWarnedFiles: the first are listed, the rest counted in one line without an entry.
	d = &data{Version: "v", Relevant: []string{"licenses"}}
	for i := range maxWarnedFiles + 3 {
		d.Files = append(d.Files, prFile{Filename: fmt.Sprintf("f%02d.sh", i), Status: "modified", SHA: "b", Patch: "@@ -1 +1,2 @@\n x\n+# Copyright 2020 X"})
	}
	ev = evaluate(cfg, d, &ruleEnv{root: t.TempDir()})
	ws = ev.Checks["licenses"].Warnings
	if len(ws) != maxWarnedFiles+1 || ws[maxWarnedFiles].where() != "and 3 more file(s) with warnings" {
		t.Fatalf("%d warnings, last %+v", len(ws), ws[len(ws)-1])
	}
	md := markdown(cfg, d, ev)
	if strings.Count(md, "[[ack]]") != maxWarnedFiles || !strings.Contains(md, "| 🔶 | licenses | and 3 more file(s) with warnings |") {
		t.Fatalf("markdown:\n%s", md)
	}
	var log strings.Builder
	report(&log, cfg, ev)
	if strings.Count(log.String(), "[[ack]]") != maxWarnedFiles {
		t.Fatalf("the log offers %d entries", strings.Count(log.String(), "[[ack]]"))
	}
	exact := make([]warning, maxWarnedFiles)
	for i := range exact {
		exact[i].File = fmt.Sprint(i)
	}
	if got := capWarnings(exact); len(got) != maxWarnedFiles {
		t.Fatalf("exactly maxWarnedFiles were capped: %d", len(got))
	}
	// Capping a capped list again, as a reused verdict is, keeps the count.
	if again := capWarnings(ws); len(again) != maxWarnedFiles+1 || again[maxWarnedFiles].where() != "and 3 more file(s) with warnings" {
		t.Fatalf("capped again: %d, last %q", len(again), again[len(again)-1].where())
	}
	// A reused verdict with a stale acknowledgement: it stays listed, first, and the count grows by the file it pushes out.
	d.Acknowledged = ackEntry("licenses", warning{File: "f00.sh", Blob: "old"})
	d.Mode = modeFull
	d.Previous = &state{Engine: cfg.EngineRevision, Checks: ev.Checks}
	ev = evaluate(cfg, d, &ruleEnv{root: t.TempDir()})
	ws = ev.Checks["licenses"].Warnings
	if len(ws) != maxWarnedFiles+1 || !strings.Contains(ws[0].Text, "the file changed") || ws[maxWarnedFiles].where() != "and 4 more file(s) with warnings" {
		t.Fatalf("%d warnings: first %q, last %q", len(ws), ws[0].where(), ws[len(ws)-1].where())
	}
	if clip("abc", 3) != "abc" || clip("abcd", 3) != "ab…" || clip("äöüß", 3) != "äö…" {
		t.Fatal("clip")
	}
	if (warning{File: "f", Text: "t"}).where() != "f t" {
		t.Fatal("a warning without a notice")
	}
}

func TestRecordRefusesATooLongCommentAndA422(t *testing.T) {
	cfg := config{Checks: []check{{Name: "a"}}}
	d := &data{Repo: "o/r", PR: 7, Mode: modeFull}
	ev := evaluation{Head: "h", Checks: map[string]verdict{"a": {Result: resultFail, Reason: strings.Repeat("x", maxCommentLength)}}}
	ev.finish()
	if err := record(context.Background(), cfg, d, ev, recordOptions{dryRun: true}, io.Discard); err == nil || !strings.Contains(err.Error(), "even without its warnings, more than the 65536 GitHub takes") {
		t.Fatalf("err = %v", err)
	}
	g := &recordServer{comments: map[int64]string{}, patchStatus: http.StatusUnprocessableEntity}
	server := httptest.NewServer(g)
	defer server.Close()
	ev.Checks["a"] = verdict{Result: resultFail, Reason: "short"}
	ev.finish()
	err := record(context.Background(), cfg, d, ev, recordOptions{apiBase: server.URL, token: "t", comment: prComment{ID: 1, Body: "old"}}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "422") || slices.Contains(g.calls, "POST /repos/o/r/issues/7/comments") {
		t.Fatalf("a refused body was posted anew: %v, calls %v", err, g.calls)
	}
}

func TestCommentBodyShortensOnlyWhatCanGrow(t *testing.T) {
	cfg := config{Checks: []check{{Name: "licenses"}}}
	d := &data{}
	ev := evaluation{Head: "h", Checks: map[string]verdict{"licenses": {Result: resultPass, Reason: "ok", Warnings: []warning{{File: "f", Text: "t"}}}}}
	st := state{Head: "h", Checks: ev.Checks}
	// Exactly as long as GitHub takes: kept whole.
	overhead := len(withState("", st))
	exact := strings.Repeat("x", maxCommentLength-overhead)
	if body, err := commentBody(cfg, d, ev, exact, st); err != nil || len(body) != maxCommentLength || !strings.HasPrefix(body, exact) {
		t.Fatalf("a body of exactly the limit: %d %v", len(body), err)
	}
	// One more: the warnings go, the verdicts stay.
	body, err := commentBody(cfg, d, ev, exact+"x", st)
	if err != nil || !strings.Contains(body, strings.TrimSpace(shortened)) || strings.Contains(body, "| 🔶 |") || !strings.Contains(body, "| ✅ | licenses | ok |") {
		t.Fatalf("shortened body: %v\n%s", err, body)
	}
	if got := parseState(body); got == nil || got.Checks["licenses"].Result != resultPass || len(got.Checks["licenses"].Warnings) != 0 {
		t.Fatalf("shortened state %+v", got)
	}
	if len(ev.Checks["licenses"].Warnings) != 1 {
		t.Fatal("shortening changed the evaluation")
	}
}

func TestRecordSetsTheStatusFirst(t *testing.T) {
	cfg := config{Checks: []check{{Name: "secrets", Alert: "revoke it"}}}
	d := &data{Repo: "o/r", PR: 7, Mode: modeFull}
	ev := evaluation{Head: "h1", Checks: map[string]verdict{"secrets": {Result: resultFail, Reason: "r"}}}
	ev.finish()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/statuses/") {
			w.WriteHeader(http.StatusCreated)
			return
		}
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer server.Close()
	var log strings.Builder
	if err := record(context.Background(), cfg, d, ev, recordOptions{apiBase: server.URL, token: "t"}, &log); err == nil || !strings.HasPrefix(log.String(), "record: status failure on h1") {
		t.Fatalf("err %v, log:\n%s", err, log.String())
	}
}
