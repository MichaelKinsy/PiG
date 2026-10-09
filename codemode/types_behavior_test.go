package codemode_test

// Pins the string values of the unions in packages/codemode/src/types.ts (CodemodeCallStatus "ok" | "error" |
// "cancelled", the failure kinds "script" | "timeout" | "aborted" | "sandbox", CodemodeOutputItem types "text" | "image")
// by observing them on real executions. sandbox.test.ts compares against the exported constants, so a changed literal
// passes it while every consumer that reads the kind or status as a string sees a different value.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
)

func TestResultKindsStatusesAndOutputTypesAreTheStringsTypesTSDeclares(t *testing.T) {
	fail := codemode.Tool{Name: "fail", Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("tool failed")
	}}
	sandbox := newSandbox(t, 10_000, echo, fail)
	result := run(t, sandbox, `
		text("hello");
		image("data:image/png;base64,`+pngBase64+`");
		await tools.echo({ a: 1 });
		try { await tools.fail({}); } catch {}
		return 1;
	`)
	if !result.OK {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Output) != 2 || string(result.Output[0].Type) != "text" || string(result.Output[1].Type) != "image" {
		t.Fatalf("output = %+v, want a text then an image item", result.Output)
	}
	if codemode.OutputItemText != result.Output[0].Type || codemode.OutputItemImage != result.Output[1].Type {
		t.Fatalf("output types = %q, %q, want the OutputItemText and OutputItemImage constants", result.Output[0].Type, result.Output[1].Type)
	}
	if len(result.Calls) != 2 || string(result.Calls[0].Status) != "ok" || string(result.Calls[1].Status) != "error" {
		t.Fatalf("calls = %+v, want statuses ok then error", result.Calls)
	}

	if kind := string(run(t, sandbox, "throw new Error('x')").Error.Kind); kind != "script" {
		t.Errorf("thrown error kind = %q", kind)
	}
	if kind := string(run(t, newSandbox(t, 200), "while (true) {}").Error.Kind); kind != "timeout" {
		t.Errorf("timeout kind = %q", kind)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	aborted, err := sandbox.Execute(canceled, "return 1", codemode.ExecuteOptions{})
	if err != nil || aborted.Error == nil || string(aborted.Error.Kind) != "aborted" {
		t.Errorf("aborted result = %+v, %v", aborted, err)
	}
	broken, err := codemode.NewSandbox(codemode.SandboxOptions{Wasm: func() ([]byte, error) { return nil, errors.New("no wasm") }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broken.Close() })
	if kind := string(run(t, broken, "return 1").Error.Kind); kind != "sandbox" {
		t.Errorf("wasm failure kind = %q", kind)
	}

	slow := codemode.Tool{Name: "slow", Execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	timed := newSandbox(t, 200, slow)
	cancelled := run(t, timed, "tools.slow(); return 1")
	if !cancelled.OK || len(cancelled.Calls) != 1 || string(cancelled.Calls[0].Status) != "cancelled" {
		t.Errorf("an unawaited call abandoned at script end = %+v, want status cancelled", cancelled)
	}
}

// types.ts:47 CodemodeOutputItem is { type: "text"; text } | { type: "image"; data; mimeType }: the Go `type` is the named OutputItemType with exactly those
// two constants, and each item encodes only its own members.
func TestOutputItemTypeIsTheClosedPiUnion(t *testing.T) {
	if codemode.OutputItemText != "text" || codemode.OutputItemImage != "image" {
		t.Fatalf("constants = %q, %q", codemode.OutputItemText, codemode.OutputItemImage)
	}
	for _, c := range []struct {
		item codemode.OutputItem
		want string
	}{
		{codemode.OutputItem{Type: codemode.OutputItemText, Text: "hi"}, `{"type":"text","text":"hi"}`},
		{codemode.OutputItem{Type: codemode.OutputItemText, Text: "hi", Console: true}, `{"type":"text","text":"hi","console":true}`},
		{codemode.OutputItem{Type: codemode.OutputItemImage, Data: "AAAA", MimeType: "image/png"}, `{"type":"image","data":"AAAA","mimeType":"image/png"}`},
	} {
		got, err := json.Marshal(c.item)
		if err != nil || string(got) != c.want {
			t.Errorf("encoded = %s (%v), want %s", got, err, c.want)
		}
	}
}
