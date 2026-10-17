package codemode

import "testing"

// D145 must not fail where Pi succeeds. Pi's JSON.stringify never reads an Error's name or message, nor asks a Proxy for its prototype, so a
// value whose name getter or getPrototypeOf trap throws still serializes in Pi. The replacer then writes what Pi writes.
func TestNestedErrorsWithUnreadableNamesSerializeAsPiDoes(t *testing.T) {
	sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	result, err := sandbox.Execute(t.Context(), `
class Unnamed extends Error { get name() { throw new Error("no name"); } }
const unnamed = new Unnamed("m"); unnamed.code = 1;
const opaque = new Proxy({ a: 1 }, { getPrototypeOf() { throw new Error("no prototype"); } });
text({ unnamed, opaque });
console.log({ unnamed, opaque });
return { unnamed, opaque };`, ExecuteOptions{})
	if err != nil || !result.OK {
		t.Fatalf("result = %+v, %v", result, err)
	}
	const want = `{"unnamed":{"code":1},"opaque":{"a":1}}`
	if string(result.Value) != want || len(result.Output) != 2 || result.Output[0].Text != want || result.Output[1].Text != want {
		t.Fatalf("value = %s, output = %+v, want %s", result.Value, result.Output, want)
	}
}
