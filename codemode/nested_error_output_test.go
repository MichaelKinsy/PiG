package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Pi #10650 (open): a nested tool rejection is an Error inside the VM, but codemode's output serialization showed `reason: {}` because name and message are not enumerable.
func TestNestedToolErrorsKeepTheirMessageInSerializedOutput(t *testing.T) {
	failing := Tool{Name: "read", Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("Example actionable read failure")
	}}
	sandbox, err := NewSandbox(SandboxOptions{Tools: []Tool{failing}, TimeoutMs: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	result, err := sandbox.Execute(t.Context(), `
const results = await Promise.allSettled([tools.read({path: "missing.txt"})]);
text(results);
text(results[0].reason.message);
return results;`, ExecuteOptions{})
	if err != nil || !result.OK {
		t.Fatalf("result = %+v, %v", result, err)
	}
	const want = `[{"status":"rejected","reason":{"name":"Error","message":"Example actionable read failure"}}]`
	if string(result.Value) != want {
		t.Fatalf("returned value = %s, want %s", result.Value, want)
	}
	if len(result.Output) != 2 || result.Output[0].Text != want || result.Output[1].Text != "Example actionable read failure" {
		t.Fatalf("output = %+v, want the same JSON and then the message", result.Output)
	}
}

// D145 adds name and message without dropping the fields Pi already writes: an Error's own enumerable properties follow them.
func TestNestedErrorsKeepTheirEnumerableFields(t *testing.T) {
	sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	result, err := sandbox.Execute(t.Context(), `
const coded = new Error("boom"); coded.code = "ENOENT";
class NotFound extends Error { constructor(message) { super(message); this.name = "NotFound"; this.status = 404; } }
const errors = [coded, new NotFound("nf")];
text(errors);
return errors;`, ExecuteOptions{})
	if err != nil || !result.OK {
		t.Fatalf("result = %+v, %v", result, err)
	}
	const want = `[{"name":"Error","message":"boom","code":"ENOENT"},{"name":"NotFound","message":"nf","status":404}]`
	if string(result.Value) != want || len(result.Output) != 1 || result.Output[0].Text != want {
		t.Fatalf("value = %s, output = %+v, want %s", result.Value, result.Output, want)
	}
}
