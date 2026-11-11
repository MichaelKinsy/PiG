// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
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

func TestRecordSetsStatusAndKeepsOneComment(t *testing.T) {
	cfg, d, ev := testEvaluation()
	var mu sync.Mutex
	var statuses []map[string]string
	comments := []map[string]any{{"id": 1, "body": "someone else"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/statuses/head1234567":
			statuses = append(statuses, body)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/7/comments":
			_ = json.NewEncoder(w).Encode(comments)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues/7/comments":
			comments = append(comments, map[string]any{"id": 2, "body": body["body"]})
			_, _ = io.WriteString(w, `{"id":2}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/o/r/issues/comments/2":
			comments[1]["body"] = body["body"]
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	o := recordOptions{apiBase: server.URL, token: "tok", statePath: filepath.Join(dir, "state.json")}
	for run := range 3 {
		if run == 2 {
			ev.Checks["template"] = verdict{Result: resultPass, Reason: "the template is filled in"}
			ev.finish()
		}
		if err := record(context.Background(), cfg, d, ev, o, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if len(comments) != 2 || !strings.HasPrefix(comments[1]["body"].(string), commentMarker) || !strings.Contains(comments[1]["body"].(string), "✅ pass") {
		t.Fatalf("want one pre-flight comment beside the other, updated to pass: %v", comments)
	}
	if len(statuses) != 3 || statuses[0]["state"] != "failure" || statuses[2]["state"] != "success" || statuses[2]["context"] != statusContext {
		t.Fatalf("statuses %v", statuses)
	}
	raw, err := os.ReadFile(o.statePath)
	if err != nil {
		t.Fatal(err)
	}
	var st state
	if err := json.Unmarshal(raw, &st); err != nil || st.Head != "head1234567" || st.Checks["template"].Result != resultPass {
		t.Fatalf("state %s, err %v", raw, err)
	}
}

func TestRecordSendsOnlyChangesAndAlertsOnce(t *testing.T) {
	cfg := config{Checks: []check{{Name: "template"}, {Name: "secrets", Alert: "revoke it"}}}
	d := &data{Repo: "o/r", PR: 7}
	d.Pull.User.Login = "dev"
	ev := evaluation{Head: "h1", Checks: map[string]verdict{"secrets": {Result: resultFail, Reason: "1 finding(s): a:1 key"}, "template": {Result: resultPass}}}
	ev.finish()
	var mu sync.Mutex
	var calls []string
	var alerts []string
	patchStatus := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments") && !strings.HasPrefix(body["body"], commentMarker):
			alerts = append(alerts, body["body"])
			_, _ = io.WriteString(w, `{"id":99}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			_, _ = io.WriteString(w, `{"id":5}`)
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `[]`)
		case r.Method == http.MethodPatch:
			w.WriteHeader(patchStatus)
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer server.Close()
	o := recordOptions{apiBase: server.URL, token: "t", statePath: filepath.Join(t.TempDir(), "s.json")}
	step := func() *state {
		t.Helper()
		calls = nil
		if err := record(context.Background(), cfg, d, ev, o, io.Discard); err != nil {
			t.Fatal(err)
		}
		st := readState(o.statePath)
		d.Previous = st
		return st
	}
	st := step()
	if len(alerts) != 1 || alerts[0] != "@dev revoke it\n\nsecrets on `h1`: 1 finding(s): a:1 key" || st.Comment != 5 || !slices.Equal(st.Alerted, []string{"secrets"}) {
		t.Fatalf("first run: alerts %q, state %+v", alerts, st)
	}
	// Nothing changed: nothing is sent, and the alert is not repeated.
	if st = step(); len(calls) != 0 || len(alerts) != 1 || st.Comment != 5 {
		t.Fatalf("an unchanged run sent %v", calls)
	}
	// A changed verdict updates the comment by its stored id, without listing the comments.
	ev.Checks["template"] = verdict{Result: resultFail, Reason: "x"}
	ev.finish()
	if step(); !slices.Equal(calls, []string{"POST /repos/o/r/statuses/h1", "PATCH /repos/o/r/issues/comments/5"}) {
		t.Fatalf("calls %v", calls)
	}
	// A deleted comment is found again or posted anew.
	patchStatus = http.StatusNotFound
	ev.Checks["template"] = verdict{Result: resultPass}
	ev.finish()
	if st = step(); st.Comment != 5 || !slices.Contains(calls, "GET /repos/o/r/issues/7/comments") {
		t.Fatalf("calls %v, state %+v", calls, st)
	}
	// Passing clears the alert; failing again alerts again.
	ev.Checks["secrets"] = verdict{Result: resultPass}
	ev.finish()
	if st = step(); len(st.Alerted) != 0 {
		t.Fatalf("alerted %v after a pass", st.Alerted)
	}
	ev.Checks["secrets"] = verdict{Result: resultFail, Reason: "again"}
	ev.finish()
	if step(); len(alerts) != 2 {
		t.Fatalf("alerts %q", alerts)
	}
	// A refused PATCH is an error, not a new comment.
	patchStatus = http.StatusForbidden
	ev.Checks["template"] = verdict{Result: resultFail, Reason: "y"}
	ev.finish()
	if err := record(context.Background(), cfg, d, ev, o, io.Discard); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordDryRunSaysWhatItWouldSend(t *testing.T) {
	cfg := config{Checks: []check{{Name: "secrets", Alert: "revoke it"}}}
	d := &data{Repo: "o/r", PR: 7}
	ev := evaluation{Head: "h1", Checks: map[string]verdict{"secrets": {Result: resultFail, Reason: "r"}}}
	ev.finish()
	o := recordOptions{statePath: filepath.Join(t.TempDir(), "s.json"), dryRun: true}
	var log strings.Builder
	if err := record(context.Background(), cfg, d, ev, o, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "record (dry run): status failure") || !strings.Contains(log.String(), "record (dry run): alert\n@ revoke it") {
		t.Fatalf("log %s", log.String())
	}
	d.Previous = readState(o.statePath)
	log.Reset()
	if err := record(context.Background(), cfg, d, ev, o, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "unchanged, nothing to send") || strings.Contains(log.String(), "alert") {
		t.Fatalf("log %s", log.String())
	}
	o.statePath = filepath.Join(t.TempDir(), "missing", "s.json")
	if err := record(context.Background(), cfg, d, ev, o, io.Discard); err == nil {
		t.Fatal("an unwritable state was not reported")
	}
}
