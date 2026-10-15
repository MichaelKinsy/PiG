package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Pi #10650 names console output and a mixed allSettled as boundaries that drop a nested Error's name and message. D145 covers console.log, a
// labelled console call, a fulfilled result beside the rejected one and an Error nested in an object; a top-level console Error keeps errorText.
func TestNestedErrorsKeepTheirMessageInConsoleOutputAndMixedResults(t *testing.T) {
	failing := Tool{Name: "read", Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("boom")
	}}
	fulfilled := Tool{Name: "ok", Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"v":1}`), nil
	}}
	sandbox, err := NewSandbox(SandboxOptions{Tools: []Tool{failing, fulfilled}, TimeoutMs: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	result, err := sandbox.Execute(t.Context(), `
const results = await Promise.allSettled([tools.read({}), tools.ok({})]);
console.log(results);
console.log("results", results);
console.log({ failure: results[0].reason });
return { failure: results[0].reason };`, ExecuteOptions{})
	if err != nil || !result.OK {
		t.Fatalf("result = %+v, %v", result, err)
	}
	const mixed = `[{"status":"rejected","reason":{"name":"Error","message":"boom"}},{"status":"fulfilled","value":{"v":1}}]`
	const nested = `{"failure":{"name":"Error","message":"boom"}}`
	want := []string{mixed, "results " + mixed, nested}
	if len(result.Output) != len(want) {
		t.Fatalf("output = %+v, want %q", result.Output, want)
	}
	for i, line := range want {
		if !result.Output[i].Console || result.Output[i].Text != line {
			t.Fatalf("output[%d] = %+v, want console %s", i, result.Output[i], line)
		}
	}
	if string(result.Value) != nested {
		t.Fatalf("returned value = %s, want %s", result.Value, nested)
	}
}
