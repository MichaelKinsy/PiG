package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func recordUserContent(actions *[]string, content any, deliverAs string) error {
	text, ok := content.(string)
	if !ok {
		raw, err := json.Marshal(content)
		if err != nil {
			return err
		}
		text = string(raw)
	}
	*actions = append(*actions, fmt.Sprintf("sendUserMessage:%s:%s", text, deliverAs))
	return nil
}

// Source: AgentSession.sendUserMessage and ExtensionAPI.sendUserMessage accept
// string | (TextContent | ImageContent)[] without discarding any image payload.
func TestUserMessageContentAcrossSDKs(t *testing.T) {
	payload := `[{"type":"text","text":"first"},{"type":"image","data":"aW1hZ2U=","mimeType":"image/png"},{"type":"text","text":"second"}]`
	var decoded any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	want := "sendUserMessage:" + string(canonical) + ":followUp"
	cases := allHarnessCases()
	for _, tc := range cases {
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
			command, ok := findCommand(h.runner, "send_user_message")
			if !ok {
				t.Fatal("command missing")
			}
			if err := command.Handler(context.Background(), payload); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return len(*h.actions) > 0 })
			if got := *h.actions; len(got) != 1 || got[0] != want {
				t.Fatalf("host calls %q, want one %q", got, want)
			}
		})
	}
}
