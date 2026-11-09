package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The async contract of the AgentSession members for the Python SDK: the child_abort_probe and child_order_probe tools of the Go fixture (testfixture/childsession/async_probes.go) as a Python extension. A member Pi declares `async` rides childSession.opAsync and never holds a later call; Python has no ctx, so the abort of a running executeBash is abort_bash from another thread.
const childAsyncPython = `import json
import threading
import time
import pig_sdk


def new_extension():
    ext = pig_sdk.Extension("child-session")

    def create(ctx):
        return ctx.create_agent_session(pig_sdk.CreateAgentSessionOptions(
            model={"provider": "sentinel", "id": "echo"}, in_memory=True, settings={"compaction": {"enabled": False}},
            resources=pig_sdk.ResourceOptions(no_extensions=True, no_skills=True, no_prompt_templates=True, no_themes=True, no_context_files=True),
        ))

    def abort_probe(ctx, args):
        session = create(ctx)
        try:
            box = {}

            def run():
                try:
                    box["result"] = session.execute_bash("sleep 25")
                except Exception as error:  # noqa: BLE001
                    box["err"] = str(error)

            start = time.monotonic()
            runner = threading.Thread(target=run, daemon=True)
            runner.start()
            time.sleep(0.7)
            session.abort_bash()
            runner.join(12)
            if runner.is_alive():
                return json.dumps({"returned": False})
            return json.dumps({"returned": True, "cancelled": bool((box.get("result") or {}).get("cancelled")), "err": box.get("err", ""), "seconds": time.monotonic() - start})
        finally:
            session.dispose()

    def order_probe(ctx, args):
        session = create(ctx)
        try:
            members = {
                "cycleModel": lambda: session.cycle_model("forward"),
                "executeBash": lambda: session.execute_bash("echo x"),
                "setModel": lambda: session.set_model({"provider": "sentinel", "id": "echo"}),
            }

            def run():
                try:
                    members[args["first"]]()
                except Exception:  # noqa: BLE001 - the held member's value is not observed
                    pass

            runner = threading.Thread(target=run, daemon=True)
            runner.start()
            time.sleep(0.4)
            start = time.monotonic()
            session.abort_bash()
            took = time.monotonic() - start
            runner.join(15)
            return json.dumps({"abortTook": took})
        finally:
            session.dispose()

    for name, fn in (("child_abort_probe", abort_probe), ("child_order_probe", order_probe)):
        ext.register_tool(pig_sdk.ToolDefinition(name=name, label=name, description=name, parameters={"type": "object"}, execute=fn))
    return ext
`

// pyHeldMember is a child session whose host holds one member until released, as a member awaiting a slow auth check or model_select handler.
type pyHeldMember struct {
	subprocess.ChildSession
	member  string
	release chan struct{}
}

func (h pyHeldMember) Op(ctx context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
	if op == h.member {
		select {
		case <-h.release:
		case <-time.After(10 * time.Second):
		}
		return nil, nil
	}
	return h.ChildSession.Op(ctx, op, args)
}

func loadChildAsyncPython(t *testing.T, wrap func(subprocess.ChildSession) subprocess.ChildSession) extension.Extension {
	t.Helper()
	host, _ := newChildSessionHostWith(t, true, wrap)
	cfg := packedFlagFactory(t, findModuleRoot(t), "python", "child-session", false)
	cfg.Isolation = "strict"
	cfg.ContentHash = "python-child-async-" + fmt.Sprint(len(childAsyncPython))
	if err := os.WriteFile(filepath.Join(cfg.Source, cfg.Package+".py"), []byte(childAsyncPython), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, failures := host.LoadAll(t.Context(), []subprocess.ExtConfig{cfg})
	if len(failures) != 0 || len(loaded) != 1 {
		t.Fatalf("load: %v", failures)
	}
	return loaded[0]
}

func runChildAsyncPythonProbe(t *testing.T, ext extension.Extension, tool, args string) map[string]any {
	t.Helper()
	result, err := ext.Tools[tool].Definition.Execute(t.Context(), "call", json.RawMessage(args), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Text()), &got); err != nil {
		t.Fatalf("%q: %v", result.Text(), err)
	}
	return got
}

// TestChildAgentSessionAbortEndsAwaitedMemberPython: execute_bash("sleep 25") in one thread, abort_bash from another after 0.7 s: the abort must not queue behind the awaited command, and the command comes back cancelled long before it would end.
func TestChildAgentSessionAbortEndsAwaitedMemberPython(t *testing.T) {
	got := runChildAsyncPythonProbe(t, loadChildAsyncPython(t, nil), "child_abort_probe", `{}`)
	if got["returned"] != true || got["cancelled"] != true {
		t.Fatalf("an awaited member aborted from another thread did not return cancelled: %v", got)
	}
	if seconds, _ := got["seconds"].(float64); seconds > 8 {
		t.Fatalf("the abort waited behind the awaited member: %v s", seconds)
	}
}

// TestChildAgentSessionAbortNotHeldBehindAwaitedMemberPython: with one awaited member held in the host for 3 s, an abort_bash sent 0.4 s later from another thread returns at once. Every member Pi declares `async` that reaches childSession.op and can be held without a run is listed (the rest are covered by the lane table in extensions/sdk-py/tests).
func TestChildAgentSessionAbortNotHeldBehindAwaitedMemberPython(t *testing.T) {
	for _, member := range []string{"cycleModel", "executeBash", "setModel"} {
		t.Run(member, func(t *testing.T) {
			release := make(chan struct{})
			ext := loadChildAsyncPython(t, func(s subprocess.ChildSession) subprocess.ChildSession { return pyHeldMember{s, member, release} })
			time.AfterFunc(3*time.Second, func() { close(release) })
			got := runChildAsyncPythonProbe(t, ext, "child_order_probe", `{"first":"`+member+`"}`)
			if took, _ := got["abortTook"].(float64); took > 1.5 {
				t.Fatalf("abort_bash waited %.2f s behind the held %s", took, member)
			}
		})
	}
}
