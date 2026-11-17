// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readEnvDump(t *testing.T, file string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the program did not run: %v", err)
	}
	env := map[string]string{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if name, value, ok := strings.Cut(line, "="); ok {
			env[name] = value
		}
	}
	return env
}

// The scanners and git read what a PR wrote. None of them gets the token the run holds (GITHUB_TOKEN or GH_TOKEN), nor
// any other *_TOKEN variable of the job.
func TestProgramsThatReadAPRNeverGetATokenOfTheRun(t *testing.T) {
	secrets := []string{"GITHUB_TOKEN", "GH_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_TOKEN", "ACTIONS_RUNTIME_TOKEN"}
	for _, name := range secrets {
		t.Setenv(name, "secret-of-"+name)
	}
	t.Setenv("PIG_PRIVATE_TERMS_FILE", "/terms")
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	dump := filepath.Join(dir, "env")
	t.Setenv("ENV_DUMP", dump)
	clean := func(t *testing.T, file string) map[string]string {
		t.Helper()
		env := readEnvDump(t, file)
		for name, value := range env {
			if strings.HasSuffix(name, "_TOKEN") || strings.Contains(value, "secret-of-") {
				t.Errorf("the program got %s=%s", name, value)
			}
		}
		if env["PIG_PRIVATE_TERMS_FILE"] != "/terms" || env["PATH"] == "" {
			t.Errorf("the program lost the rest of the environment: %v", env)
		}
		return env
	}

	t.Run("scanner", func(t *testing.T) {
		fakeTool(t, bin, "fake", `env > "$1"`)
		ws := workspace{dir: dir, main: "MAIN", bin: bin, added: filepath.Join(dir, "added")}
		s := scanSpec{Input: inputAdded, Run: []string{"fake", "{out}"}, Env: map[string]string{"SCAN_CONFIG": "{main}/cfg"}, Exit: exitCodes{Clean: []int{0}}, Report: reportSpec{In: "stdout", Format: "lines", Pattern: `(?P<message>.+)`}}
		if o := runScan(context.Background(), scanCheck(t, s), &data{}, ws, io.Discard); o.result != resultPass {
			t.Fatalf("outcome %+v", o)
		}
		env := clean(t, filepath.Join(dir, "fake.report"))
		if env["SCAN_CONFIG"] != "MAIN/cfg" || !strings.HasPrefix(env["PATH"], bin+string(os.PathListSeparator)) {
			t.Errorf("the scan's own environment is missing: SCAN_CONFIG=%q PATH=%q", env["SCAN_CONFIG"], env["PATH"])
		}
	})

	fakes := filepath.Join(dir, "fakes")
	fakeTool(t, fakes, "git", `env > "$ENV_DUMP"`)
	fakeTool(t, fakes, "python3", `env > "$ENV_DUMP.python"; mkdir -p "$3/bin"; printf '#!/bin/sh\nenv > "$ENV_DUMP.pip"\n' > "$3/bin/pip"; chmod +x "$3/bin/pip"`)
	t.Setenv("PATH", fakes+string(os.PathListSeparator)+os.Getenv("PATH"))

	t.Run("git", func(t *testing.T) {
		if out, err := git(context.Background(), "status"); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		clean(t, dump)
	})
	t.Run("git with the remote's credentials", func(t *testing.T) {
		if out, err := gitAuth(context.Background(), "https://github.example", "fetch-credential", "fetch"); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if env := clean(t, dump); env["GIT_CONFIG_KEY_0"] != "http.https://github.example/.extraHeader" || !strings.HasPrefix(env["GIT_CONFIG_VALUE_0"], "Authorization: Basic ") {
			t.Errorf("git lost the header for its remote: %v", env)
		}
	})
	t.Run("pip", func(t *testing.T) {
		if err := installPip(context.Background(), tool{Name: "reuse", Pip: "requirements.txt"}, dir, filepath.Join(dir, "tools")); err != nil {
			t.Fatal(err)
		}
		clean(t, dump+".python")
		clean(t, dump+".pip")
	})
}
