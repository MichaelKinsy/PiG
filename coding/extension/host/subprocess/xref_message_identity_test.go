package subprocess

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: Pi runs every extension in one realm, so the message an extension passes to another on pi.events is the very object that extension holds for it (message_end's event.message), whichever extension runs first.
//
// One message_end, numbered by the host, reaches two Node extensions, packed in one process and isolated in two. The second extension compares what it receives on pi.events with its own message_end message: Pi says true.
func TestPiEventsMessageIsTheReceiversOwnObject(t *testing.T) {
	skipWithoutNodeExtensions(t)
	t.Parallel()
	const sender = `export default pi => {
  pi.on("message_end", (event) => { pi.events.emit("message", event.message); });
};`
	receiver := `export default pi => {
  let own, result = "none";
  pi.on("message_end", (event) => { own = event.message; });
  pi.events.on("message", (data) => { result = String(data === own); });
` + xrefTool("result", `return { content: [{ type: "text", text: result }] };`) + `
};`
	for _, isolation := range [][]string{{"", ""}, {"isolated", "isolated"}} {
		t.Run(fmt.Sprintf("%q", isolation), func(t *testing.T) {
			_, exts := xrefFixture(t, isolation, sender, receiver)
			message := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("hello"), Timestamp: 1}}
			ctx := extension.WithMessageSequences(context.Background(), func() []uint64 { return []uint64{9001} })
			// The receiver holds its message first, as the extension that ran first does in Pi's one realm; then the sender passes its own on the bus.
			for _, name := range []string{"xref-1", "xref-0"} {
				for _, handler := range exts[name].Handlers["message_end"] {
					if _, err := handler(extension.MessageEndEvent{Type: "message_end", Message: message}, ctx); err != nil {
						t.Fatalf("%s message_end: %v", name, err)
					}
				}
			}
			if got := xrefRunTool(t, exts["xref-1"], "result"); got != "true" {
				t.Fatalf("the message received on pi.events === the receiver's message_end message: %s, want true (Pi)", got)
			}
		})
	}
}
