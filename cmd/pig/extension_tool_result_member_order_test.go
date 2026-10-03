package main

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// A tool's result is the object the tool built: tool_execution_update carries it as partialResult and tool_execution_end as result, unchanged, so its members are in the order the tool wrote them (agent-loop.ts:778-786, 912-919). A Node tool that returns {details, isError, content} is observed, and written on the JSON stream, in that order. Each expectation is what the real Pi 0.99.1 CLI (dist/cli.js with test-faux-provider.ts) printed for the same extension and prompt.
func TestExtensionToolResultKeepsTheToolsMemberOrder(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home := t.TempDir()
	cwd, agentDir, sessions := filepath.Join(home, "project"), filepath.Join(home, "agent"), filepath.Join(home, "sessions")
	artifact, entry := filepath.Join(home, "events.txt"), filepath.Join(home, "observe.mjs")
	writeStartupFixtureFile(t, entry, `import {appendFileSync} from "node:fs";
const log = (kind, value) => appendFileSync(`+strconv.Quote(artifact)+`, kind + " " + JSON.stringify(value) + "\n");
const details = () => ({zeta: 1, alpha: {yy: 2, bb: 3}});
export default function(pi) {
 pi.registerTool({
  name: "echo_bridge", label: "Probe", description: "probe", parameters: {type: "object", properties: {text: {type: "string"}}},
  async execute(id, params, signal, onUpdate) {
   onUpdate?.({details: details(), content: [{type: "text", text: "partial"}]});
   return {details: details(), isError: true, content: [{type: "text", text: "done"}]};
  },
 });
 for (const type of ["tool_execution_update", "tool_execution_end"]) pi.on(type, (event) => log(type, event));
}`)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	run := runPigStartup(t, binary, home, agentDir, cwd, "", "--mode", "json", "--session-dir", sessions, "--model", "test-faux/faux-1", "-e", entry, "Run: extension echo hello")
	if run.err != nil {
		t.Fatalf("%v\nstdout: %s\nstderr: %s", run.err, run.stdout, run.stderr)
	}
	observed, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("%v\nstdout: %s\nstderr: %s", err, run.stdout, run.stderr)
	}
	const (
		partial = `{"details":{"zeta":1,"alpha":{"yy":2,"bb":3}},"content":[{"type":"text","text":"partial"}]}`
		result  = `{"details":{"zeta":1,"alpha":{"yy":2,"bb":3}},"isError":true,"content":[{"type":"text","text":"done"}]}`
		prefix  = `{"type":"tool_execution_`
		ids     = `"toolCallId":"call_test_faux_1","toolName":"echo_bridge"`
	)
	lines := strings.Split(strings.TrimSpace(string(observed)), "\n")
	for _, want := range []string{
		`tool_execution_update ` + prefix + `update",` + ids + `,"args":{"text":"hello"},"partialResult":` + partial + `}`,
		`tool_execution_end ` + prefix + `end",` + ids + `,"result":` + result + `,"isError":true}`,
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("extension observed\n%s\nwant a line\n%s", strings.Join(lines, "\n"), want)
		}
	}
	for _, want := range []string{
		`"partialResult":` + partial + `}`,
		`"result":` + result + `,"isError":true}`,
	} {
		if !strings.Contains(run.stdout, want) {
			t.Errorf("--mode json output lacks %s\n%s", want, toolExecutionLines(run.stdout))
		}
	}
}

// toolExecutionLines keeps the tool_execution_* lines of a JSONL stream.
func toolExecutionLines(text string) string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, `{"type":"tool_execution_`) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// The members a tool wrote stay members when their values are null or false: Pi hands the tool's object on, so JSON.stringify writes "isError":false, "details":null and "terminate":false (agent-loop.ts:778-786, 912-919). Each expectation is what the real Pi 0.99.1 CLI (dist/cli.js with test-faux-provider.ts) printed for the same extension and prompt.
func TestExtensionToolResultKeepsMembersWrittenAsNullOrFalse(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home := t.TempDir()
	cwd, agentDir, sessions := filepath.Join(home, "project"), filepath.Join(home, "agent"), filepath.Join(home, "sessions")
	artifact, entry := filepath.Join(home, "events.txt"), filepath.Join(home, "observe.mjs")
	writeStartupFixtureFile(t, entry, `import {appendFileSync} from "node:fs";
const log = (kind, value) => appendFileSync(`+strconv.Quote(artifact)+`, kind + " " + JSON.stringify(value) + "\n");
export default function(pi) {
 pi.registerTool({
  name: "echo_bridge", label: "Probe", description: "probe", parameters: {type: "object", properties: {text: {type: "string"}}},
  async execute(id, params, signal, onUpdate) {
   onUpdate?.({content: [{type: "text", text: "partial"}], isError: false, details: null});
   return {content: [{type: "text", text: "done"}], isError: false, details: null, terminate: false};
  },
 });
 for (const type of ["tool_execution_update", "tool_execution_end"]) pi.on(type, (event) => log(type, event));
}`)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	run := runPigStartup(t, binary, home, agentDir, cwd, "", "--mode", "json", "--session-dir", sessions, "--model", "test-faux/faux-1", "-e", entry, "Run: extension echo hello")
	if run.err != nil {
		t.Fatalf("%v\nstdout: %s\nstderr: %s", run.err, run.stdout, run.stderr)
	}
	observed, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("%v\nstdout: %s\nstderr: %s", err, run.stdout, run.stderr)
	}
	const (
		update = `{"type":"tool_execution_update","toolCallId":"call_test_faux_1","toolName":"echo_bridge","args":{"text":"hello"},"partialResult":{"content":[{"type":"text","text":"partial"}],"isError":false,"details":null}}`
		end    = `{"type":"tool_execution_end","toolCallId":"call_test_faux_1","toolName":"echo_bridge","result":{"content":[{"type":"text","text":"done"}],"isError":false,"details":null,"terminate":false},"isError":false}`
	)
	lines := strings.Split(strings.TrimSpace(string(observed)), "\n")
	for _, want := range []string{"tool_execution_update " + update, "tool_execution_end " + end} {
		if !slices.Contains(lines, want) {
			t.Errorf("extension observed\n%s\nwant a line\n%s", strings.Join(lines, "\n"), want)
		}
	}
	stdout := strings.Split(run.stdout, "\n")
	for _, want := range []string{update, end} {
		if !slices.Contains(stdout, want) {
			t.Errorf("--mode json output lacks the line %s\n%s", want, toolExecutionLines(run.stdout))
		}
	}
}
