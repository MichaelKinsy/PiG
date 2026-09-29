package env

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

// Pi's NodeExecutionEnv.exec (harness/env/nodejs.ts:590-610) catches what
// spawn throws and settles with ExecutionError("spawn_error", message). Node's
// normalizeSpawnArguments throws ERR_INVALID_ARG_VALUE for a NUL in the
// command, which is the shell's last argument, and for a NUL in an env value,
// on every platform. Pi's own NodeExecutionEnv is the oracle.
func TestExecReportsSpawnArgumentErrorsAsPiDoes(t *testing.T) {
	type execCase struct {
		Command string            `json:"command"`
		Env     map[string]string `json:"env"`
	}
	cases := []execCase{
		{Command: "echo a\x00b"},
		{Command: "echo ok", Env: map[string]string{"PIG_NUL_VALUE": "v\x00"}},
	}
	cwd := t.TempDir()
	root, err := filepath.EvalSymlinks("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-agent-core")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"root": root, "cwd": cwd, "cases": cases})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const { root, cwd, cases } = JSON.parse(readFileSync(0, "utf8"));
const { NodeExecutionEnv } = await import(pathToFileURL(join(root, "dist", "harness", "env", "nodejs.js")).href);
const shell = new NodeExecutionEnv({ cwd });
const outcomes = [];
for (const { command, env } of cases) {
	const result = await shell.exec(command, { env: env ?? undefined }, {});
	outcomes.push(result.ok ? { code: "", message: "exit " + result.value.exitCode } : { code: result.error.code, message: result.error.message });
}
process.stdout.write(JSON.stringify(outcomes));
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("Pi NodeExecutionEnv: %v; output %s", err, out)
	}
	type outcome struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	var want []outcome
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(cases) {
		t.Fatalf("decode Pi outcomes %s: %v", out, err)
	}
	shell := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: cwd})
	for i, c := range cases {
		if want[i].Code != string(harness.ExecutionErrorSpawnError) {
			t.Fatalf("case %d: Pi outcome %+v is not a spawn error", i, want[i])
		}
		_, _, err := execCollect(context.Background(), shell, c.Command, &harness.ShellExecOptions{Env: c.Env})
		var got outcome
		if executionErr, ok := errors.AsType[*harness.ExecutionError](err); ok {
			got = outcome{Code: string(executionErr.Code), Message: executionErr.Message}
		} else {
			got = outcome{Message: "error " + errString(err)}
		}
		if got != want[i] {
			t.Errorf("case %d %+q\n got %+v\nwant %+v", i, c, got, want[i])
		}
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
