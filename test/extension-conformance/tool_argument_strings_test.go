package extensionconformance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestConformance_ToolArgumentStringsAgreeAcrossSDKs: a tool receives its arguments as JSON, and Pi's are JavaScript values, so an argument string can hold an unpaired UTF-16 surrogate (a model's
// output cut inside a pair, a lone half pasted by a user). The Go, Node and Python SDKs hand it back unchanged. A Rust `Value` cannot hold one, so the Rust SDK decodes it as U+FFFD, as Go's
// decoder does for a `string`; what it must not do is refuse the frame, which ended its connection and killed the extension. echo_params returns the arguments as its details, and a second call after the
// first proves the extension is still serving.
func TestConformance_ToolArgumentStringsAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	const args = `{"nul":"a\u0000b","ls":"a\u2028b","pair":"\ud83d\ude00","esc":"\"\\\/\b\f\n\r\t","ctl":"\u001f\u007f","bom":"\ufeffx","amp":"<>&","é":3,"":2,"lone":"x\ud800y","low":"\udc00","swapped":"\ude00\ud83d","k\ud800":1}`
	// The members that carry no unpaired surrogate, exactly as every SDK reports them.
	exact := map[string]string{
		"nul": `"a\u0000b"`, "ls": `"a\u2028b"`, "pair": `"😀"`, "esc": `"\"\\/\b\f\n\r\t"`, "ctl": `"\u001f` + "\x7f" + `"`,
		"bom": "\"\ufeffx\"", "amp": `"\u003c\u003e\u0026"`, "é": `3`, "": `2`,
	}
	// The members that do: kept as UTF-16 units by Go, Node and Python, and as U+FFFD by Rust.
	lone := map[string]string{"lone": `"x\ud800y"`, "low": `"\udc00"`, "swapped": `"\ude00\ud83d"`, "k\uFFFD": `1`}
	replaced := strings.NewReplacer(`\ud800`, "\uFFFD", `\udc00`, "\uFFFD", `\ude00`, "\uFFFD", `\ud83d`, "\uFFFD")

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			tool, ok := findTool(h.runner, "echo_params")
			if !ok {
				t.Fatal("echo_params not registered")
			}
			result, err := tool.Definition.Execute(ctx, "tc-strings", json.RawMessage(args), nil)
			if err != nil {
				t.Fatalf("echo_params with an unpaired surrogate: %v", err)
			}
			encoded, err := json.Marshal(result.Details)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatalf("details %s: %v", encoded, err)
			}
			for key, want := range exact {
				if string(got[key]) != want {
					t.Errorf("member %q = %s, want %s", key, got[key], want)
				}
			}
			for key, want := range lone {
				if strings.HasPrefix(tc.name, "subprocess-rust") {
					want = replaced.Replace(want)
					key = replaced.Replace(key)
				}
				if string(got[key]) != want {
					t.Errorf("member %q = %s, want %s", key, got[key], want)
				}
			}
			if _, err := tool.Definition.Execute(ctx, "tc-after", json.RawMessage(`{"text":"after"}`), nil); err != nil {
				t.Fatalf("the extension stopped serving after the call: %v", err)
			}
		})
	}
}
