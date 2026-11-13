package extensionconformance

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The Node SDK is Pi's own runtime, and what its handler's JSON.stringify of a deep value does is V8's (D268), which moves with the Node version: V8 13.6 (Node 24) overflows its stack
// on a nested array or object past 4458 levels; V8 14.6 (Node 26) stringifies one 2,000,000 levels deep on its iterative fast path, and takes the recursive path again (and the old limit)
// when a string in the value is a concatenation it has not flattened, which is what an `append` operation of an event stream leaves, as Pi's own `text += delta` does. The rows
// therefore ask the `node` the extensions run on, the first on PATH, instead of assuming a depth.

// nodeStringifyOverflowProbe reads one JSON document a line and answers 1 when JSON.stringify of the parsed value ends in V8's RangeError (`Maximum call stack size exceeded`) and 0 when it returns.
// Any other failure ends the probe with a message the test reports.
const nodeStringifyOverflowProbe = `
const lines = require("node:fs").readFileSync(0, "utf8").split("\n").filter((line) => line !== "");
const out = [];
for (const line of lines) {
  const value = JSON.parse(line);
  try {
    JSON.stringify(value);
    out.push("0");
  } catch (err) {
    if (!(err instanceof RangeError) || !String(err.message).includes("Maximum call stack size exceeded")) throw err;
    out.push("1");
  }
}
process.stdout.write(out.join("\n") + "\n");
`

// nodeEventStreamProbe replays the request frames the host wrote to the Node SDK for one event through the runtime's own EventReplicas (runtime-node/event-stream.mjs: the replica of the stream,
// its `append` as a concatenation, and the iterative copy handed to the handler), and for each event answers what its handlers' JSON.stringify does on what they were handed: the first handler of an
// event's frames is the fixture's, which stringifies a tool update's `args`; the last is the probe's, which stringifies the whole payload. Each answer is two characters: the whole payload, then the
// arguments; 1 for V8's RangeError and 0 for a result.
const nodeEventStreamProbe = `
(async () => {
  const { pathToFileURL } = require("node:url");
  const { EventReplicas } = await import(pathToFileURL(process.env.PIG_EVENT_STREAM).href);
  const per = Number(process.env.PIG_FRAMES_PER_EVENT);
  const lines = require("node:fs").readFileSync(0, "utf8").split("\n").filter((line) => line !== "");
  const overflows = (value) => {
    try {
      JSON.stringify(value);
      return "0";
    } catch (err) {
      if (!(err instanceof RangeError) || !String(err.message).includes("Maximum call stack size exceeded")) throw err;
      return "1";
    }
  };
  const replicas = new EventReplicas();
  const out = [];
  let args = "0";
  lines.forEach((line, index) => {
    const request = JSON.parse(line).request;
    replicas.apply(request);
    if (request.streamError) throw request.streamError;
    if (index % per === 0) args = overflows(request.args?.args);
    if (index % per === per - 1) out.push(overflows(request.args) + args);
  });
  process.stdout.write(out.join("\n") + "\n");
})();
`

func runNode(t *testing.T, what, script, stdin string, env ...string) []string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is not available to probe %s: %v", what, err)
	}
	cmd := exec.CommandContext(t.Context(), node, "-e", script)
	cmd.Env = append(cmd.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the %s probe on %s failed: %v: %.600s", what, node, err, stderr.String())
	}
	return strings.Fields(stdout.String())
}

// nodeStringifyOverflows reports, for each JSON text, whether the running Node runs out of stack in JSON.stringify of its parsed value.
func nodeStringifyOverflows(t *testing.T, texts []string) []bool {
	t.Helper()
	answers := runNode(t, "JSON.stringify", nodeStringifyOverflowProbe, strings.Join(texts, "\n")+"\n")
	if len(answers) != len(texts) {
		t.Fatalf("the JSON.stringify probe answered %d of %d texts: %q", len(answers), len(texts), answers)
	}
	overflows := make([]bool, len(texts))
	for index, answer := range answers {
		overflows[index] = answer == "1"
	}
	return overflows
}

// nodeEventStreamOutcomes reports, for each of events events, where the Node SDK's handlers fail with V8's RangeError on the frames the host wrote (see nodeEventStreamProbe): probe for the
// probe's stringify of the whole payload, fixture for the fixture's own handler's stringify of a tool update's arguments. The host writes one frame to each handler of an event, so frames
// holds the same number for every event.
func nodeEventStreamOutcomes(t *testing.T, frames [][]byte, events int) (probe, fixture []bool) {
	t.Helper()
	if events == 0 || len(frames) == 0 || len(frames)%events != 0 {
		t.Fatalf("the host wrote %d request frames for %d events, want the same number of frames for each", len(frames), events)
	}
	var stdin strings.Builder
	for _, frame := range frames {
		stdin.Write(frame)
		stdin.WriteByte('\n')
	}
	runtimeSource := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node", "event-stream.mjs")
	answers := runNode(t, "event stream", nodeEventStreamProbe, stdin.String(), "PIG_EVENT_STREAM="+runtimeSource, "PIG_FRAMES_PER_EVENT="+strconv.Itoa(len(frames)/events))
	if len(answers) != events {
		t.Fatalf("the event stream probe answered %d of %d events: %q", len(answers), events, answers)
	}
	probe, fixture = make([]bool, events), make([]bool, events)
	for index, answer := range answers {
		probe[index], fixture[index] = answer[0] == '1', answer[1] == '1'
	}
	return probe, fixture
}
