package main

// pi: packages/evals/src/cli.ts

// Tests for the pig-evals command, the Go entry of packages/evals/src/cli.ts. They drive run with the recorded upstream
// behavior of parseEvalCli (internal/evals/testdata/cli_oracle.json): an argument error is reported with Pi's message
// before any Docker work, and an accepted command line selects a model or stops at the missing-model message, and one that selects a model reaches
// the credential check, the first step after the parse and the file selection.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/evals"
)

type cliOracleCase struct {
	Args   []string          `json:"args"`
	Env    map[string]string `json:"env"`
	Error  string            `json:"error"`
	Result *struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	} `json:"result"`
}

func TestRunDocsFollowsTheUpstreamCommandLine(t *testing.T) {
	data, err := os.ReadFile("../../internal/evals/testdata/cli_oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []cliOracleCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 40 {
		t.Fatalf("oracle holds %d cases", len(cases))
	}
	for _, name := range []string{"PI_PROVIDER", "PI_MODEL", "PI_EVAL_RUNS_PER_VARIANT"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	// An agent directory without auth.json stops every accepted command line at the credential check.
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	for _, c := range cases {
		args := make([]string, len(c.Args))
		for i, arg := range c.Args {
			args[i] = strings.Replace(arg, ".docs.eval.ts", evals.DocsEvalSuffix, 1)
		}
		for name, value := range c.Env {
			t.Setenv(name, value)
		}
		code, err := run(context.Background(), append([]string{"docs"}, args...))
		for name := range c.Env {
			_ = os.Unsetenv(name)
		}
		if err == nil {
			t.Errorf("run docs %q env %v succeeded without credentials", args, c.Env)
			continue
		}
		if code != 0 {
			t.Errorf("run docs %q env %v code = %d, want 0 (main turns the error into 1)", args, c.Env, code)
		}
		if c.Error != "" {
			if err.Error() != c.Error {
				t.Errorf("run docs %q env %v error = %q, want %q", args, c.Env, err, c.Error)
			}
			continue
		}
		// cli.ts:115 stops an accepted command line that selected no model before any other step.
		want := "Eval authentication file does not exist: "
		if c.Result.Provider == "" || c.Result.Model == "" {
			want = "Set PI_PROVIDER and PI_MODEL, or pass --provider and --model."
		}
		if !strings.HasPrefix(err.Error(), want) {
			t.Errorf("run docs %q env %v error = %q, want one starting %q", args, c.Env, err, want)
		}
	}
}

func TestRunRejectsMissingAndUnknownCommands(t *testing.T) {
	if code, err := run(context.Background(), nil); code != 2 || err == nil || !strings.HasPrefix(err.Error(), "usage: pig-evals") {
		t.Errorf("run(nil) = %d, %v", code, err)
	}
	if code, err := run(context.Background(), []string{"nope"}); code != 2 || err == nil || err.Error() != `unknown command "nope"` {
		t.Errorf(`run("nope") = %d, %v`, code, err)
	}
}
