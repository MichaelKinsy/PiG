//go:build parity

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestScenarioEnvPreservesRuntimeTokensAndURLs(t *testing.T) {
	root := t.TempDir()
	values := []string{"ARTIFACT={{TEMP}}/result.json", "ENDPOINT=https://example.invalid/v1", "FIXTURE=testdata/home"}
	got := resolveScenarioEnvVars(filepath.Join(root, "scenario.toml"), values)
	want := []string{values[0], values[1], "FIXTURE=" + filepath.Join(root, "testdata", "home")}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("environment value %q = %q, want %q", values[i], got[i], want[i])
		}
	}
}

func TestRPCArtifactIsCapturedAfterCompletionBarrier(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	script := filepath.Join(root, "artifact.mjs")
	if err := os.WriteFile(script, []byte(`
import { writeFileSync } from "node:fs";
import { createInterface } from "node:readline";
for await (const line of createInterface({ input: process.stdin })) {
  JSON.parse(line);
  writeFileSync(process.env.ARTIFACT_OUTPUT, '{"complete":true}\n');
  process.stdout.write('{"id":"artifact-written"}\n');
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	scenario := &Scenario{
		Name: "rpc-artifact-barrier", SourcePath: filepath.Join(root, "scenario.toml"),
		Env: EnvOverrides{Pig: []string{"ARTIFACT_OUTPUT={{TEMP}}/registry.json"}},
		RPC: RPCDriverConfig{
			ArtifactPath: "{{TEMP}}/registry.json",
			Steps: []RPCInputStep{{
				Line:         `{}`,
				WaitContains: []string{"artifact-written"},
			}},
		},
	}
	result := (rpcModeDriver{}).Run(t.Context(), t, BinaryRef{Label: "pig", Path: node, Args: []string{script}}, scenario)
	if result.Err != nil || result.ArtifactErr != nil || result.ExitCode != 0 {
		t.Fatalf("RPC result: %+v", result)
	}
	if result.Artifact != "{\"complete\":true}\n" {
		t.Fatalf("artifact after completion = %q", result.Artifact)
	}
}
