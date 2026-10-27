package extensionconformance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestConformance_MessageHelpersAgreeAcrossSDKs: messageRole and messageText are PiG's reading aid over Pi's flat, role-discriminated message payload (docs/extension-api-parity.md: identical in
// every SDK). Each case below is an event payload and the pinned answer; all SDKs must give it, so a payload the four implementations read differently (a non-string role, a block that is
// not an object, a text field that is not a string) shows up as one SDK disagreeing with the table.
func TestConformance_MessageHelpersAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	type tcase struct {
		name       string
		payload    string
		role, text string
	}
	cases := []tcase{
		{"no message", `{"type":"message_end"}`, "", ""},
		{"message null", `{"message":null}`, "", ""},
		{"message string", `{"message":"assistant"}`, "", ""},
		{"message array", `{"message":[{"role":"user"}]}`, "", ""},
		{"message number", `{"message":5}`, "", ""},
		{"assistant blocks", `{"message":{"role":"assistant","content":[{"type":"text","text":"a"},{"type":"toolCall","name":"x"},{"type":"text","text":"b"}]}}`, "assistant", "ab"},
		{"user string content", `{"message":{"role":"user","content":"hello"}}`, "user", "hello"},
		{"empty content array", `{"message":{"role":"user","content":[]}}`, "user", ""},
		{"missing content", `{"message":{"role":"user"}}`, "user", ""},
		{"missing role", `{"message":{"content":[{"type":"text","text":"x"}]}}`, "", "x"},
		{"numeric role", `{"message":{"role":5,"content":"x"}}`, "", "x"},
		{"null role", `{"message":{"role":null,"content":"x"}}`, "", "x"},
		{"object role", `{"message":{"role":{"name":"user"},"content":"x"}}`, "", "x"},
		{"array role", `{"message":{"role":["user"],"content":"x"}}`, "", "x"},
		{"numeric content", `{"message":{"role":"user","content":5}}`, "user", ""},
		{"object content", `{"message":{"role":"user","content":{"type":"text","text":"x"}}}`, "user", ""},
		{"null content", `{"message":{"role":"user","content":null}}`, "user", ""},
		{"non-object blocks", `{"message":{"role":"user","content":["x",5,null,[],{"type":"text","text":"ok"}]}}`, "user", "ok"},
		{"text not a string", `{"message":{"role":"user","content":[{"type":"text","text":5},{"type":"text","text":null},{"type":"text"},{"type":"text","text":"k"}]}}`, "user", "k"},
		{"type not a string", `{"message":{"role":"user","content":[{"type":5,"text":"no"},{"type":["text"],"text":"no"},{"type":"text","text":"yes"}]}}`, "user", "yes"},
		{"thinking and images skipped", `{"message":{"role":"assistant","content":[{"type":"thinking","thinking":"hm","text":"no"},{"type":"image","data":"d"},{"type":"text","text":"shown"}]}}`, "assistant", "shown"},
		{"empty text blocks", `{"message":{"role":"assistant","content":[{"type":"text","text":""},{"type":"text","text":""}]}}`, "assistant", ""},
		{"unicode", `{"message":{"role":"user","content":[{"type":"text","text":"日本"},{"type":"text","text":"😀é"}]}}`, "user", "日本😀é"},
		{"order and no separator", `{"message":{"role":"toolResult","content":[{"type":"text","text":"1"},{"type":"text","text":"2"},{"type":"text","text":"3"}]}}`, "toolResult", "123"},
		{"nested message ignored", `{"message":{"message":{"role":"user","content":"deep"},"role":"custom"}}`, "custom", ""},
	}

	// run returns the reported role and text as JSON values, so an SDK that answers with another type (Node's numeric role) fails its case and the remaining cases still run.
	run := func(t *testing.T, h *harness, c tcase) (role, text any) {
		t.Helper()
		before := len(*h.notify)
		if !h.runner.ExecuteCommand(context.Background(), "message_probe", c.payload) {
			t.Fatal("fixture has no message_probe command")
		}
		var reported string
		pollUntilConformance(t, "message_probe: no report", func() bool {
			for _, n := range (*h.notify)[before:] {
				if rest, ok := strings.CutPrefix(n, "message:"); ok {
					reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
					return true
				}
			}
			return false
		})
		var got struct{ Role, Text any }
		if err := json.Unmarshal([]byte(reported), &got); err != nil {
			t.Fatalf("%s is not JSON: %v", reported, err)
		}
		return got.Role, got.Text
	}

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
			for _, c := range cases {
				if role, text := run(t, h, c); role != c.role || text != c.text {
					t.Errorf("%s: messageRole/messageText = %#v/%#v, want %q/%q", c.name, role, text, c.role, c.text)
				}
			}
		})
	}
}
