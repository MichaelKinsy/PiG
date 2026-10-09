package codemode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Ports packages/codemode/test/sandbox.test.ts "reports a broken bridge as a sandbox error" (#10444). Upstream's
// raw-worker.ts fixture posts the JSON message passed as the script source; here a replacement prelude sends the same
// payloads through the bridge function, so each reaches the host the way the real prelude's would.
func TestReportsABrokenBridgeAsASandboxError(t *testing.T) {
	cases := []struct {
		name    string
		bridge  string
		message string
	}{
		{"writes not an array", `"done", true, "1", "null"`, "store writes are not an array"},
		{"malformed entry", `"done", true, "1", "[1]"`, "store writes contain a malformed entry"},
		{"entry key not a string", `"done", true, "1", "[[1]]"`, "store writes contain a malformed entry"},
		{"entry value not a string", `"done", true, "1", "[[\"k\", 1]]"`, "store writes contain a malformed entry"},
		{"entry too long", `"done", true, "1", "[[\"k\", \"1\", \"2\"]]"`, "store writes contain a malformed entry"},
		{"store value not JSON", `"done", true, "1", "[[\"k\", \"{\"]]"`, `store value for "k" is not valid JSON`},
		{"writes not JSON", `"done", true, "1", "["`, "store writes is not valid JSON"},
		{"return value not JSON", `"done", true, "{", "[]"`, "return value is not valid JSON"},
		{"error not an object", `"done", false, "5"`, "script error is not an object"},
		{"error null", `"done", false, "null"`, "script error is not an object"},
		{"error without message", `"done", false, "{}"`, "script error is malformed"},
		{"error name not a string", `"done", false, "{\"message\":\"m\",\"name\":1}"`, "script error is malformed"},
		{"error array", `"done", false, "[]"`, "script error is malformed"},
		{"error not JSON", `"done", false, "{"`, "script error is not valid JSON"},
		{"unknown message", `"nonsense"`, "unknown message from the worker"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: 10_000})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sandbox.Close() })
			sandbox.prelude = `(function (bridge) { bridge(` + tc.bridge + `); return { settle() {}, run() {}, stalled() {} }; })`
			result, err := sandbox.Execute(context.Background(), "return 1", ExecuteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result.OK || result.Error == nil || result.Error.Kind != ErrorSandbox {
				t.Fatalf("result = %+v (error %+v), want a sandbox error", result, result.Error)
			}
			if want := "Sandbox bridge broken: " + tc.message; !strings.Contains(result.Error.Message, want) {
				t.Fatalf("message = %q, want it to contain %q", result.Error.Message, want)
			}
		})
	}
}

// A well-formed payload still settles: the checks reject only what the real prelude cannot send.
func TestAcceptsWellFormedBridgePayloads(t *testing.T) {
	writes, err := parseStoreWrites(`[["a","[1]"],["b"]]`)
	if err != nil {
		t.Fatal(err)
	}
	if string(writes.Set["a"]) != "[1]" || len(writes.Delete) != 1 || writes.Delete[0] != "b" {
		t.Fatalf("writes = %+v", writes)
	}
	failure, err := parseScriptError(`{"name":"RangeError","message":"m","stack":"s"}`)
	if err != nil {
		t.Fatal(err)
	}
	if failure.Kind != ErrorScript || failure.Name != "RangeError" || failure.Message != "m" || failure.Stack != "s" {
		t.Fatalf("failure = %+v", failure)
	}
	failure, err = parseScriptError(`{"message":"only"}`)
	if err != nil || failure.Name != "" || failure.Message != "only" {
		t.Fatalf("failure = %+v, %v", failure, err)
	}
}

// A call id that is still pending cannot start another call: upstream handleMessage ("duplicate call id").
func TestReportsADuplicateCallIDAsASandboxError(t *testing.T) {
	sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: 10_000, Tools: []Tool{{Name: "wait", Execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	sandbox.prelude = `(function (bridge) { bridge("call", 1, "wait", "[]"); bridge("call", 1, "wait", "[]"); return { settle() {}, run() {}, stalled() {} }; })`
	result, err := sandbox.Execute(context.Background(), "return 1", ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error == nil || result.Error.Kind != ErrorSandbox || !strings.Contains(result.Error.Message, "Sandbox bridge broken: duplicate call id 1") {
		t.Fatalf("result = %+v (error %+v)", result, result.Error)
	}
}
