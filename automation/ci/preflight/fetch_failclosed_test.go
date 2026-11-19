// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func fetchAndEvaluate(t *testing.T, pullJSON string, files func(w http.ResponseWriter)) (config, *data, evaluation) {
	t.Helper()
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == "/repos/o/r/pulls/7":
			_, _ = io.WriteString(w, pullJSON)
		case p == "/repos/o/r/pulls/7/files":
			files(w)
		case p == "/repos/o/r/pulls/7/commits":
			_, _ = io.WriteString(w, `[{"sha":"c1"}]`)
		case strings.HasPrefix(p, "/repos/o/r/compare/"):
			_, _ = io.WriteString(w, `{"status":"ahead","ahead_by":1,"behind_by":0}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	d, err := fetchWith(t, cfg, fetchOptions{apiBase: server.URL, repo: "o/r", pr: 7, version: "v"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, d, evaluate(cfg, d, &ruleEnv{root: t.TempDir()})
}

// Without the PR's file list nothing says which checks apply or what a scan reads. Every check that needs the list is
// then undecided and the verdict fails, with the cause named: a check never passes as "no file it covers changed".
func TestAFileListThatCannotBeReadFailsTheVerdict(t *testing.T) {
	pullJSON := `{"number":7,"body":"Depends on: None","mergeable":true,"commits":1,"changed_files":3,"head":{"sha":"h"},"base":{"sha":"b","ref":"main"}}`
	for _, tc := range []struct {
		name  string
		files func(w http.ResponseWriter)
		cause string
	}{
		{"the request fails", func(w http.ResponseWriter) { http.Error(w, "down", http.StatusBadGateway) }, "HTTP 502"},
		{"the page is cut off", func(w http.ResponseWriter) {
			_, _ = io.WriteString(w, `[{"filename":".github/workflows/ci.yml","status":"modified"},{"filename":"go.m`)
		}, "unexpected end of JSON input"},
		{"the list is shorter than the PR", func(w http.ResponseWriter) {
			_, _ = io.WriteString(w, `[{"filename":"README.md","status":"modified"}]`)
		}, "1 of the PR's 3 changed files"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, d, ev := fetchAndEvaluate(t, pullJSON, tc.files)
			if !strings.Contains(d.SourceErrors["files"], tc.cause) {
				t.Fatalf("source errors %v, want files to name %q", d.SourceErrors, tc.cause)
			}
			if ev.Result != resultFail {
				t.Errorf("verdict %s, want %s", ev.Result, resultFail)
			}
			gated := 0
			for _, c := range cfg.Checks {
				v := ev.Checks[c.Name]
				needsFiles := !c.When.Always || c.Stage == stageScan || slices.Contains(c.Reads, "files")
				if !c.When.Always {
					gated++
				}
				if needsFiles && (v.Result != resultError || !strings.Contains(v.Reason, "source files failed") || !strings.Contains(v.Reason, tc.cause)) {
					t.Errorf("%s: %s %q, want an error that names the files source and %q", c.Name, v.Result, v.Reason, tc.cause)
				}
			}
			if gated == 0 {
				t.Fatal("the shipped configuration has no path-gated check; the test proves nothing")
			}
		})
	}
}

// The commits list is held to the same count as the file list.
func TestACommitListShorterThanThePRIsASourceError(t *testing.T) {
	pullJSON := `{"number":7,"body":"Depends on: None","mergeable":true,"commits":4,"changed_files":1,"head":{"sha":"h"},"base":{"sha":"b","ref":"main"}}`
	_, d, ev := fetchAndEvaluate(t, pullJSON, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, `[{"filename":"README.md","status":"modified"}]`)
	})
	if !strings.Contains(d.SourceErrors["commits"], "1 of the PR's 4 commits") || ev.Result != resultFail {
		t.Fatalf("source errors %v, verdict %s", d.SourceErrors, ev.Result)
	}
}

// A source that failed is not an empty source: a verdict stored for the empty input is not reused for it.
func TestAFailedSourceDoesNotReuseTheVerdictOfAnEmptyOne(t *testing.T) {
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	var reader string
	var names []string
	for _, c := range cfg.Checks {
		names = append(names, c.Name)
		if slices.Contains(c.Reads, "dependency-graph") && reader == "" {
			reader = c.Name
		}
	}
	if reader == "" {
		t.Fatal("no shipped check reads the dependency graph")
	}
	env := &ruleEnv{root: t.TempDir()}
	d := &data{Repo: "o/r", PR: 7, Mode: modeFull, Relevant: names, Files: []prFile{{Filename: "go.mod", Status: "modified"}}}
	first := evaluate(cfg, d, env)
	if first.Checks[reader].Result != resultPass {
		t.Fatalf("%s with no dependency change: %+v", reader, first.Checks[reader])
	}
	d.Previous = &state{Engine: cfg.EngineRevision, Checks: first.Checks}
	d.SourceErrors = map[string]string{"dependency-graph": "HTTP 502: down"}
	second := evaluate(cfg, d, env)
	if v := second.Checks[reader]; v.Result != resultError || !strings.Contains(v.Reason, "source dependency-graph failed") {
		t.Fatalf("%s after the dependency graph failed: %s %q, want an error naming the source", reader, v.Result, v.Reason)
	}
}
