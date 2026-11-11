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

func TestPreflightVersionChangesWithItsFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "automation", "ci", "preflight")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("automation/ci/preflight/config.yaml", "a")
	v1, err := preflightVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	write("automation/ci/preflight/acknowledged.toml", "data, not code")
	if v, _ := preflightVersion(root); v != v1 {
		t.Fatal("acknowledged.toml changed the version")
	}
	write(".gitleaks.toml", "rules")
	if v, _ := preflightVersion(root); v == v1 {
		t.Fatal("the Gitleaks config did not change the version")
	}
}
