package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

const pyDetachedCallFixture = `import os
import threading
import time

import pig_sdk

def new_extension():
    e = pig_sdk.Extension("pyapi-detached")

    def detached(ctx, args):
        directory = args["dir"]

        def nested():
            try:
                outcome = ctx.execute_tool("wait_release", {})
                record = "ok:" + outcome["result"]["content"][0]["text"]
            except Exception as error:
                record = "err:" + str(error)
            with open(os.path.join(directory, "out"), "w") as out:
                out.write(record)

        threading.Thread(target=nested, daemon=True).start()
        deadline = time.time() + 10
        while time.time() < deadline:
            if os.path.exists(os.path.join(directory, "started")):
                return "returned"
            time.sleep(0.005)
        raise RuntimeError("the nested call never started")

    e.tool("detached_call", "Starts a nested call on a thread and returns once the host reports it pending", {"type": "object"}, detached)
    return e
`

// A Python handler that starts a host call on a thread and returns leaves the call running, as Pi's un-awaited call does (loader.ts createExtensionRuntime has no request scope; only the call's own AbortSignal ends it). TestExtensionAPIHostCallStartedByAHandlerOutlivesItsResponseGo is the Go SDK's row.
func TestPythonSDKHostCallStartedByAHandlerOutlivesItsResponse(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		started, release, ended := make(chan struct{}, 1), make(chan struct{}), make(chan string, 1)
		actions := &subprocess.HostCallbacks{
			ExecuteTool: func(ctx context.Context, callerID, name string, _ json.RawMessage, _ extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
				outcome := extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: ai.JsonObject{}}}
				started <- struct{}{}
				select {
				case <-ctx.Done():
					ended <- "Aborted"
					outcome.IsError = true
				case <-release:
					ended <- "released"
					outcome.Result = agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "released"}}}
				}
				return outcome, nil
			},
		}
		rig := newPyAPIRig(t, isolation, actions, pyAPIFixture{"pyapi-detached", pyDetachedCallFixture})
		dir := t.TempDir()
		args, err := json.Marshal(map[string]string{"dir": dir})
		if err != nil {
			t.Fatal(err)
		}
		def := rig.exts["pyapi-detached"].Tools["detached_call"]
		type done struct {
			text string
			err  error
		}
		returned := make(chan done, 1)
		go func() {
			result, err := def.Definition.Execute(t.Context(), "call-d", args, nil)
			text := ""
			if typed, ok := result, true; ok {
				text = typed.Text()
			}
			returned <- done{text, err}
		}()
		select {
		case <-started:
		case <-time.After(20 * time.Second):
			t.Fatal("the thread's nested call never reached the host")
		}
		if err := os.WriteFile(filepath.Join(dir, "started"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-returned:
			if got.err != nil || got.text != "returned" {
				t.Fatalf("detached_call = %q, %v", got.text, got.err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the handler never returned")
		}
		close(release)
		select {
		case got := <-ended:
			if got != "released" {
				t.Fatalf("the nested call ended %q, want released", got)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the nested call never ended")
		}
		var record []byte
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if data, err := os.ReadFile(filepath.Join(dir, "out")); err == nil && len(data) > 0 {
				record = data
				break
			}
		}
		if string(record) != "ok:released" {
			t.Fatalf("the thread's call recorded %q, want ok:released", record)
		}
	})
}
