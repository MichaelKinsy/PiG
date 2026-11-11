// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseDependsOn(t *testing.T) {
	for _, tc := range []struct {
		body string
		n    int
		ok   bool
	}{
		{"Tracking: x\nDepends on: None\n", 0, true},
		{"Depends on: #421", 421, true},
		{"  depends on:   #7  ", 7, true},
		{"Depends on:", 0, false},
		{"Depends on: 421", 0, false},
		{"Depends on: #0", 0, false},
		{"Depends on: #421, #422", 0, false},
		{"Depends on: TBD", 0, false},
		{"no line at all", 0, false},
	} {
		n, ok := parseDependsOn(tc.body)
		if n != tc.n || ok != tc.ok {
			t.Errorf("parseDependsOn(%q) = %d, %v, want %d, %v", tc.body, n, ok, tc.n, tc.ok)
		}
	}
}

func TestDecideMode(t *testing.T) {
	prev := &state{Version: "v", Head: "h1", Base: "b1", Body: "x"}
	for _, tc := range []struct {
		name             string
		prev             *state
		version          string
		head, base, body string
		mode             string
	}{
		{"no state", nil, "v", "h1", "b1", "x", modeFull},
		{"engine changed", prev, "w", "h1", "b1", "x", modeFull},
		{"same head, base and description", prev, "v", "h1", "b1", "x", modeReuse},
		{"main moved", prev, "v", "h1", "b2", "x", modeFull},
		{"main unknown", &state{Version: "v", Head: "h1", Body: "x"}, "v", "h1", "", "x", modeFull},
		{"new head", prev, "v", "h2", "b1", "x", modeFull},
		{"description edited", prev, "v", "h1", "b1", "y", modeFull},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if mode, reason := decideMode(tc.prev, tc.version, tc.head, tc.base, tc.body); mode != tc.mode || reason == "" {
				t.Fatalf("mode %q reason %q, want %q", mode, reason, tc.mode)
			}
		})
	}
}

func TestGetFollowsPages(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		switch r.URL.Query().Get("page") {
		case "":
			w.Header().Set("Link", fmt.Sprintf(`<%s/items?page=2>; rel="next", <%s/items?page=2>; rel="last"`, server.URL, server.URL))
			_, _ = io.WriteString(w, `[{"filename":"a"},{"filename":"b"}]`)
		case "2":
			_, _ = io.WriteString(w, `[{"filename":"c"}]`)
		}
	}))
	defer server.Close()
	a := newAPI(server.URL, "tok")
	var files []prFile
	if err := a.get(context.Background(), "items", &files, true); err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[2].Filename != "c" || a.callCount() != 2 {
		t.Fatalf("files %v calls %d", files, a.calls)
	}
}

func TestGetReportsHTTPErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer server.Close()
	var p pull
	a := newAPI(server.URL, "")
	err := a.get(context.Background(), "x", &p, false)
	if err == nil || !hasStatus(err, http.StatusNotFound) || !strings.Contains(err.Error(), "404") || a.callCount() != 1 {
		t.Fatalf("err = %v, calls %d (a 404 is not retried)", err, a.callCount())
	}
}

func TestGetRetriesTransientFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failures int
		status   int
		ok       bool
		calls    int
		retries  int
	}{
		{"recovers after two 502s", 2, http.StatusBadGateway, true, 3, 2},
		{"recovers after a 429", 1, http.StatusTooManyRequests, true, 2, 1},
		{"gives up after three retries", 10, http.StatusServiceUnavailable, false, 4, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			served := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				served++
				if served <= tc.failures {
					http.Error(w, "busy", tc.status)
					return
				}
				_, _ = io.WriteString(w, `{"number":7}`)
			}))
			defer server.Close()
			var log strings.Builder
			a := newAPI(server.URL, "")
			a.retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
			a.log = &log
			var p pull
			err := a.get(context.Background(), "pulls/7", &p, false)
			if (err == nil) != tc.ok || a.callCount() != tc.calls || strings.Count(log.String(), "retry ") != tc.retries {
				t.Fatalf("err %v, calls %d, log %q", err, a.callCount(), log.String())
			}
			if tc.ok && p.Number != 7 {
				t.Fatalf("pull %+v", p)
			}
		})
	}
}

func TestNewAPIUsesTheDefaultRetryDelays(t *testing.T) {
	want := defaultRetryDelays
	if got := newAPI("x", "").retryDelays; !slices.Equal(got, want) {
		t.Fatalf("retry delays %v, want %v", got, want)
	}
}

func TestFetchKeepsGoingWhenOneSourceFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == "/repos/o/r/pulls/7":
			_, _ = io.WriteString(w, `{"number":7,"body":"Depends on: None","mergeable":true,"commits":1,"changed_files":1,"head":{"sha":"h"},"base":{"sha":"b","ref":"main"}}`)
		case p == "/repos/o/r/pulls/7/files":
			_, _ = io.WriteString(w, `[{"filename":"a.go","status":"modified","sha":"s"}]`)
		case p == "/repos/o/r/pulls/7/commits":
			http.Error(w, "down", http.StatusBadGateway)
		case strings.HasPrefix(p, "/repos/o/r/compare/"):
			_, _ = io.WriteString(w, `{"status":"ahead","ahead_by":1,"behind_by":0}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	d, err := fetchWith(t, cfg, fetchOptions{apiBase: server.URL, repo: "o/r", pr: 7, version: "v"}, &log)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.SourceErrors) != 1 || d.SourceErrors["commits"] == "" {
		t.Fatalf("source errors %v, want only commits", d.SourceErrors)
	}
	if len(d.Files) != 1 || d.CompareMain.Status != "ahead" {
		t.Fatalf("the other sources were not fetched: files %v compare %+v", d.Files, d.CompareMain)
	}
	if strings.Count(log.String(), "retry ") != 3 {
		t.Fatalf("want three retries of the failing source only, log:\n%s", log.String())
	}
}

func fetchWith(t *testing.T, cfg config, o fetchOptions, log io.Writer) (*data, error) {
	t.Helper()
	saved := defaultRetryDelays
	defaultRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { defaultRetryDelays = saved })
	return fetch(context.Background(), cfg, o, log)
}

func TestGetRefusesANextPageOutsideTheBase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `<https://elsewhere.example/items?page=2>; rel="next"`)
		_, _ = io.WriteString(w, `[]`)
	}))
	defer server.Close()
	var files []prFile
	err := newAPI(server.URL, "").get(context.Background(), "items", &files, true)
	if err == nil || !strings.Contains(err.Error(), "is not under") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchWaveTwo(t *testing.T) {
	saved := mergeableRetryDelay
	mergeableRetryDelay = time.Millisecond
	t.Cleanup(func() { mergeableRetryDelay = saved })
	pulls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; p {
		case "/repos/o/r/pulls/7":
			pulls++
			mergeable := "null"
			if pulls > 1 {
				mergeable = "true"
			}
			_, _ = io.WriteString(w, `{"number":7,"body":"Depends on: #5","mergeable":`+mergeable+`,"commits":2,"changed_files":2,"head":{"sha":"new"},"base":{"sha":"b","ref":"main"}}`)
		case "/repos/o/r/pulls/7/files":
			_, _ = io.WriteString(w, `[{"filename":"a.go","status":"modified","sha":"s1"},{"filename":"go.sum","status":"modified","sha":"s2"}]`)
		case "/repos/o/r/pulls/7/commits":
			_, _ = io.WriteString(w, `[{"sha":"old"},{"sha":"new"}]`)
		case "/repos/o/r/compare/main...new":
			_, _ = io.WriteString(w, `{"status":"diverged","ahead_by":2,"behind_by":1}`)
		case "/repos/o/r/pulls/5":
			_, _ = io.WriteString(w, `{"number":5,"state":"closed","merged":true}`)
		case "/repos/o/r/dependency-graph/compare/b...new":
			_, _ = io.WriteString(w, `[{"change_type":"added","name":"x","version":"1"}]`)
		case "/repos/o/r/contents/" + acknowledgedPath:
			_, _ = io.WriteString(w, `{"encoding":"base64","content":"`+base64.StdEncoding.EncodeToString([]byte("[[ack]]\n"))+`"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	prev := &state{Version: "v", Head: "old", Base: "b"}
	d, err := fetchWith(t, cfg, fetchOptions{apiBase: server.URL, repo: "o/r", pr: 7, version: "v", previous: prev}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case d.Mode != modeFull:
		t.Fatalf("mode %s", d.Mode)
	case d.DependsOn == nil || !d.DependsOn.Merged:
		t.Fatalf("depends on %+v", d.DependsOn)
	case len(d.Dependencies) != 1:
		t.Fatalf("dependencies %+v", d.Dependencies)
	case d.Acknowledged != "[[ack]]\n":
		t.Fatalf("acknowledged %q", d.Acknowledged)
	case d.Pull.Mergeable == nil || !*d.Pull.Mergeable:
		t.Fatalf("mergeable after the retry: %v", formatBool(d.Pull.Mergeable))
	case len(d.SourceErrors) != 0:
		t.Fatalf("source errors %v", d.SourceErrors)
	case !slices.Contains(d.Relevant, "dependency-review") || slices.Contains(d.Relevant, "workflows"):
		t.Fatalf("relevant %v", d.Relevant)
	}

	prev.Head, prev.Body = "new", bodyHash("")
	calls := 0
	counted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"number":7,"head":{"sha":"new"},"base":{"sha":"b","ref":"main"}}`)
	}))
	defer counted.Close()
	d, err = fetchWith(t, cfg, fetchOptions{apiBase: counted.URL, repo: "o/r", pr: 7, version: "v", previous: prev}, io.Discard)
	if err != nil || d.Mode != modeReuse || calls != 1 {
		t.Fatalf("mode %s, %d call(s), err %v", d.Mode, calls, err)
	}
}

func TestRefPathKeepsSlashes(t *testing.T) {
	for ref, want := range map[string]string{"main": "main", "team/patrick/249": "team/patrick/249", "a b/c#d": "a%20b/c%23d"} {
		if got := refPath(ref); got != want {
			t.Errorf("refPath(%q) = %q, want %q", ref, got, want)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/repos/o/r/git/ref/heads/team/x" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"object":{"sha":"tip"}}`)
	}))
	defer server.Close()
	if sha, err := newTips(newAPI(server.URL, "t")).get(context.Background(), "o/r", "team/x"); err != nil || sha != "tip" {
		t.Fatalf("tip %q, err %v", sha, err)
	}
}

func TestAcknowledgedOnlyA404IsNoFile(t *testing.T) {
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	for status, failed := range map[int]bool{http.StatusNotFound: false, http.StatusUnprocessableEntity: true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/repos/o/r/pulls/1":
				_, _ = io.WriteString(w, `{"number":1,"mergeable":true,"head":{"sha":"h"},"base":{"sha":"b","ref":"main"}}`)
			case "/repos/o/r/contents/" + acknowledgedPath:
				http.Error(w, "no", status)
			default:
				_, _ = io.WriteString(w, `[]`)
			}
		}))
		d, err := fetchWith(t, cfg, fetchOptions{apiBase: server.URL, repo: "o/r", pr: 1, version: "v"}, io.Discard)
		server.Close()
		if _, got := d.SourceErrors["acknowledged"]; err != nil || got != failed {
			t.Errorf("status %d: source errors %v, err %v", status, d.SourceErrors, err)
		}
	}
}
