package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Pi's agent-loop.ts:912-919 emits the finalized AgentToolResult and agent-loop.ts:541-547 the model's parsed arguments to every extension handler as one object; a Node handler that serializes event.result sees the tool's own key order: {type, text} for a text block, not the alphabetical {text, type} a Go map yields. Pi 0.99.1's bash tool returns {content, details, structuredContent} (tools/bash.ts:394-411); details is undefined without truncation, so JSON.stringify omits it, and structuredContent keeps BashToolOutput's order.
func TestNodeExtensionSeesToolExecutionResultInInsertionOrder(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home := t.TempDir()
	cwd, agentDir := filepath.Join(home, "project"), filepath.Join(home, "agent")
	artifact, entry := filepath.Join(home, "events.txt"), filepath.Join(home, "observe.mjs")
	writeStartupFixtureFile(t, entry, `import {appendFileSync} from "node:fs";
export default function(pi) {
 for (const type of ["tool_execution_start", "tool_execution_end"]) {
  pi.on(type, (event) => {
   const {type, toolCallId, ...rest} = event;
   appendFileSync(`+strconv.Quote(artifact)+`, type + " " + JSON.stringify(rest) + "\n");
  });
 }
}`)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	run := runPigStartup(t, binary, home, agentDir, cwd, "", "--no-session", "--model", "test-faux/faux-1", "-e", entry, "-p", "Run: expr 20 + 22")
	if run.err != nil {
		t.Fatalf("%v\nstdout: %s\nstderr: %s", run.err, run.stdout, run.stderr)
	}
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("%v\nstdout: %s\nstderr: %s", err, run.stdout, run.stderr)
	}
	want := "tool_execution_start {\"toolName\":\"bash\",\"args\":{\"command\":\"expr 20 + 22\"}}\n" +
		"tool_execution_end {\"toolName\":\"bash\",\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"42\\n\"}],\"structuredContent\":{\"output\":\"42\\n\",\"truncated\":false,\"exit_code\":0,\"wall_time_seconds\":WALL}},\"isError\":false}\n"
	// Pi rounds the measured wall time to tenths of a second (tools/bash.ts:392), so only its presence and position are stable.
	wallTimeSeconds := regexp.MustCompile(`"wall_time_seconds":[0-9.]+`)
	if got := wallTimeSeconds.ReplaceAllString(string(data), `"wall_time_seconds":WALL`); got != want {
		t.Fatalf("extension observed\n%s\nwant\n%s", strings.TrimSpace(got), strings.TrimSpace(want))
	}
}
