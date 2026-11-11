package ai

import (
	"encoding/json"
	"testing"
)

// normalizeThreadMessage is Pi's hashThreadPrefix replacer (anthropic-messages.ts:339-346) over a message that is already JSON text:
// `cache_control` members are dropped, a string `content` becomes one text block, and every other member keeps its position, as
// JSON.stringify writes an object's members in insertion order.
func TestNormalizeThreadMessageKeepsMemberOrder(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"content before role", `{"content":"hi","role":"user"}`, `{"content":[{"type":"text","text":"hi"}],"role":"user"}`},
		{"role before content, marker dropped", `{"role":"user","cache_control":{"type":"ephemeral"},"content":"hi"}`, `{"role":"user","content":[{"type":"text","text":"hi"}]}`},
		{"other members keep their order", `{"z":1,"a":{"y":true,"b":null},"m":[3,2,1]}`, `{"z":1,"a":{"y":true,"b":null},"m":[3,2,1]}`},
		{"blocks of an array", `{"role":"assistant","content":[{"type":"text","text":"x","cache_control":{"type":"ephemeral"}},{"text":"y","type":"text"}]}`, `{"role":"assistant","content":[{"type":"text","text":"x"},{"text":"y","type":"text"}]}`},
		{"empty containers stay empty", `{"b":[],"a":{}}`, `{"b":[],"a":{}}`},
		{"array-index keys come first, as in JavaScript", `{"b":1,"2":2,"1":3}`, `{"1":3,"2":2,"b":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeThreadMessage(json.RawMessage(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("normalizeThreadMessage(%s)\n got %s\nwant %s", tc.in, got, tc.want)
			}
		})
	}
}
