package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

var wallClockMillis = regexp.MustCompile(`"timestamp":[0-9]{13}`)

// A JSON value a subprocess extension supplies keeps the member order it was written in, as a JavaScript object does, on every surface Pi shows it on: a tool's details and partial result (agent-loop.ts:778-786, 912-919), a tool_result handler's replacement details (runner.ts:707-755), an appendEntry data object and a sendMessage details object (agent-session.ts sendCustomMessage, session-manager.ts appendCustomEntry). Each expectation below is the output of the real Pi 0.99.1 CLI (dist/cli.js with test-faux-provider.ts) run on the same extension and prompt.
func TestExtensionSuppliedJSONKeepsMemberOrderOnEverySurface(t *testing.T) {
	t.Parallel()
	const (
		toolDetails = `{"zeta":1,"alpha":{"yy":2,"bb":3},"mid":[{"qq":1,"aa":2}]}`
		hookDetails = `{"omega":1,"beta":{"zz":2,"cc":3}}`
		partial     = `{"content":[{"type":"text","text":"partial"}],"details":` + toolDetails + `}`
		resultText  = `[{"type":"text","text":"echo-bridge: hello"}]`
	)
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name    string
		hook    bool
		details string
	}{
		{"tool details", false, toolDetails},
		{"tool_result handler details", true, hookDetails},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			cwd, agentDir, sessions := filepath.Join(home, "project"), filepath.Join(home, "agent"), filepath.Join(home, "sessions")
			artifact, entry := filepath.Join(home, "events.txt"), filepath.Join(home, "observe.mjs")
			hook := ""
			if tc.hook {
				hook = `pi.on("tool_result", () => ({details: {omega: 1, beta: {zz: 2, cc: 3}}}));`
			}
			writeStartupFixtureFile(t, entry, `import {appendFileSync} from "node:fs";
const log = (kind, value) => appendFileSync(`+strconv.Quote(artifact)+`, kind + " " + JSON.stringify(value) + "\n");
const details = () => ({zeta: 1, alpha: {yy: 2, bb: 3}, mid: [{qq: 1, aa: 2}]});
export default function(pi) {
 pi.registerTool({
  name: "echo_bridge", label: "Probe", description: "probe", parameters: {type: "object", properties: {text: {type: "string"}}},
  async execute(id, params, signal, onUpdate) {
   onUpdate?.({content: [{type: "text", text: "partial"}], details: details()});
   return {content: [{type: "text", text: "echo-bridge: hello"}], details: details()};
  },
 });
 pi.on("session_start", () => {
  pi.appendEntry("probe-entry", details());
  pi.sendMessage({customType: "probe-msg", content: "note", display: true, details: details()});
 });
 `+hook+`
 for (const type of ["tool_execution_update", "tool_execution_end", "tool_result"]) pi.on(type, (event) => log(type, event));
 pi.on("context", (event) => log("context", event.messages.filter((m) => m.role === "custom" || m.role === "toolResult")));
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
			lines := strings.Split(strings.TrimSpace(wallClockMillis.ReplaceAllString(withoutDurationMs(t, string(observed)), `"timestamp":TS`)), "\n")
			customMessage := `{"role":"custom","customType":"probe-msg","content":"note","display":true,"details":` + toolDetails + `,"timestamp":TS}`
			toolResult := `{"role":"toolResult","toolCallId":"call_test_faux_1","toolName":"echo_bridge","content":` + resultText + `,"details":` + tc.details + `,"isError":false,"timestamp":TS}`
			for _, want := range []string{
				`context [` + customMessage + `]`,
				`tool_execution_update {"type":"tool_execution_update","toolCallId":"call_test_faux_1","toolName":"echo_bridge","args":{"text":"hello"},"partialResult":` + partial + `}`,
				`tool_execution_end {"type":"tool_execution_end","toolCallId":"call_test_faux_1","toolName":"echo_bridge","result":{"content":` + resultText + `,"details":` + tc.details + `},"isError":false}`,
				`context [` + customMessage + `,` + toolResult + `]`,
			} {
				if !slices.Contains(lines, want) {
					t.Errorf("extension observed\n%s\nwant a line\n%s", strings.Join(lines, "\n"), want)
				}
			}
			toolResultEvent := ""
			for _, line := range lines {
				if strings.HasPrefix(line, "tool_result ") {
					toolResultEvent = line
				}
			}
			for _, want := range []string{`"content":` + resultText, `"details":` + tc.details} {
				if !strings.Contains(toolResultEvent, want) {
					t.Errorf("tool_result event %s lacks %s", toolResultEvent, want)
				}
			}
			stdout := wallClockMillis.ReplaceAllString(withoutDurationMs(t, run.stdout), `"timestamp":TS`)
			for _, want := range []string{
				`"partialResult":` + partial + `}`,
				`"result":{"content":` + resultText + `,"details":` + tc.details + `},"isError":false}`,
				`"role":"toolResult","toolCallId":"call_test_faux_1","toolName":"echo_bridge","content":` + resultText + `,"details":` + tc.details + `,"isError":false,"timestamp":TS}`,
			} {
				if !strings.Contains(stdout, want) {
					t.Errorf("--mode json output lacks %s\n%s", want, relevantJSONLines(stdout))
				}
			}
			sessionFiles, err := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
			if err != nil || len(sessionFiles) != 1 {
				t.Fatalf("session files = %v, %v", sessionFiles, err)
			}
			persisted, err := os.ReadFile(sessionFiles[0])
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				`"customType":"probe-entry","data":` + toolDetails,
				`"customType":"probe-msg","content":"note","display":true,"details":` + toolDetails,
				`"toolName":"echo_bridge","content":` + resultText + `,"details":` + tc.details + `,"isError":false`,
			} {
				if !strings.Contains(string(persisted), want) {
					t.Errorf("session file lacks %s\n%s", want, relevantJSONLines(string(persisted)))
				}
			}
		})
	}
}

// relevantJSONLines keeps the lines of a JSONL stream that carry the extension's values.
func relevantJSONLines(text string) string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, "probe") || strings.Contains(line, "echo_bridge") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// A custom message an extension sent is written on the RPC and JSON wire as createCustomMessage builds it (messages.ts:123-137): {role, customType, content, display, details, timestamp}, with `details` as the extension wrote it.
func TestRPCCustomMessageIsWrittenInPiMemberOrder(t *testing.T) {
	message := agent.AgentMessage{Custom: map[string]any{
		"timestamp": int64(1), "details": json.RawMessage(`{"zeta":1,"alpha":2}`), "display": true, "content": "note", "customType": "probe", "role": "custom",
	}}
	events, err := rpcAgentEvent(agent.MessageEndEvent{Message: message})
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(events[0]); err != nil {
		t.Fatal(err)
	}
	const want = `{"type":"message_end","message":{"role":"custom","customType":"probe","content":"note","display":true,"details":{"zeta":1,"alpha":2},"timestamp":1}}` + "\n"
	if encoded.String() != want {
		t.Fatalf("message_end =\n%swant\n%s", encoded.String(), want)
	}
}

// toolDurationMs matches the execution or response time Pi 1.1.0 writes after isError on tool_execution_end and results (agent-loop.ts:933-942) and on assistant messages (event-stream.ts:127-128). Its value is wall time, so only its presence and position are stable.
var toolDurationMs = regexp.MustCompile(`,"durationMs":[0-9]+`)

// withoutDurationMs drops every durationMs in text and fails when it finds none: a tool that ran reports one.
func withoutDurationMs(t *testing.T, text string) string {
	t.Helper()
	if !toolDurationMs.MatchString(text) {
		t.Errorf("no message carries durationMs:\n%s", toolExecutionLines(text))
	}
	return toolDurationMs.ReplaceAllString(text, "")
}
