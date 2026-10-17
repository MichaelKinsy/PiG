package codemode

import (
	"context"
	"testing"
)

// A QuickJS string holds an unpaired surrogate as a three-byte WTF-8 sequence. console output keeps it as the JavaScript string does; a conversion
// to valid UTF-8 would make it U+FFFD in the tool result and the session file, where Pi writes the escape \ud800.
func TestConsoleOutputKeepsALoneSurrogate(t *testing.T) {
	sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	result, err := sandbox.Execute(context.Background(), `console.log("\ud800 é"); return 1`, ExecuteOptions{})
	if err != nil || !result.OK || len(result.Output) != 1 {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if got, want := result.Output[0].Text, "\xed\xa0\x80 é"; got != want {
		t.Fatalf("console output = %q, want %q", got, want)
	}
}

// A thrown error keeps the UTF-16 units of its name, message and stack: Pi decodes the prelude's JSON with JSON.parse
// (execution.ts parseScriptError), which keeps an unpaired surrogate; encoding/json turns it into U+FFFD
// (rev-pin-codemode Review 2, note 5).
func TestAThrownErrorKeepsALoneSurrogateInItsFields(t *testing.T) {
	sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	result, err := sandbox.Execute(context.Background(), `const e = new Error("a\ud800b"); e.name = "N\udc00"; throw e`, ExecuteOptions{})
	if err != nil || result.OK || result.Error == nil {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if got, want := result.Error.Message, "a\xed\xa0\x80b"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if got, want := result.Error.Name, "N\xed\xb0\x80"; got != want {
		t.Errorf("name = %q, want %q", got, want)
	}
}
