package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi 1.0.4 types.ts ProjectTrustContext ({cwd, mode, hasUI, ui: select/confirm/input/notify}) is the second argument of a
// project_trust handler; cli/project-trust.ts createProjectTrustContext builds it. In a non-interactive mode hasUI is false and
// select and input resolve undefined and confirm resolves false. Go's *extension.Context is the ProjectTrustContext (a superset).
func TestProjectTrustHandlerReceivesProjectTrustContext(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	home := t.TempDir()
	project := trustProjectFixture(t)
	marker := filepath.Join(home, "ctx.json")
	fixture := filepath.Join(home, "trustctx.mjs")
	source := fmt.Sprintf(`import {writeFileSync} from "node:fs";
export default function(pi) {
 pi.on("project_trust", async (event, ctx) => {
  writeFileSync(%q, JSON.stringify({
   cwd: ctx.cwd, mode: ctx.mode, hasUI: ctx.hasUI, eventCwd: event.cwd,
   select: await ctx.ui.select("t", ["a","b"]) ?? null,
   confirm: await ctx.ui.confirm("t", "m"),
   input: await ctx.ui.input("t", "p") ?? null,
  }));
  return {trusted: "yes"};
 });
}`, marker)
	if err := os.WriteFile(fixture, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, codingagent.CONFIG_DIR_NAME, "extensions", "project.ts"), []byte(`export default function(){}`), 0o600); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	models := `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux","api":"test-faux","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(testbudget.Context(t), binary, "--model", "test-faux/faux-1", "--no-session", "-e", fixture, "--print", "What is 20+22?")
	command.Dir = project
	command.Env = append(os.Environ(), "PIG_HOME="+home, "PI_HOME=", "PIG_CODING_AGENT_DIR=", "PI_CODING_AGENT_DIR=", "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pig: %v\n%s", err, output)
	}
	observed, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("handler did not run: %v\n%s", err, output)
	}
	resolved, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	want := func(cwd string) string {
		return fmt.Sprintf(`{"cwd":%q,"mode":"print","hasUI":false,"eventCwd":%q,"select":null,"confirm":false,"input":null}`, cwd, cwd)
	}
	if got := string(observed); got != want(project) && got != want(resolved) {
		t.Fatalf("ProjectTrustContext = %s, want %s", got, want(project))
	}
}
