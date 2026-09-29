package runtime

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi packages/agent/src/harness/runtime/lane.ts:1480-1494 preserves a message's string content unless images require blocks; an empty string contributes no text block.
func TestQueuedUserContentPreservesVariantUntilImagesAreAdded(t *testing.T) {
	isolateHarnessTest(t)
	image := ai.ImageContent{Data: "aW1hZ2U=", MimeType: "image/png"}
	for _, content := range []ai.UserContent{ai.UserText(""), ai.UserText("hello"), ai.UserContentBlocks{}, ai.UserContentBlocks{ai.TextContent{Text: "hello"}}} {
		for _, images := range [][]ai.ImageContent{nil, {image}} {
			fixture := newAdmissionFixture(t, nil, nil)
			message := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: content, Timestamp: 1}}
			before, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			id, err := fixture.lane.SteerWithImages(t.Context(), message, images)
			requireHarnessOK(t, err)
			pending := readHarnessValue(t, fixture.session, session.PendingEntryValue(id))
			if pending == nil || pending.Value.Message.User == nil {
				t.Fatal("queued user missing")
			}
			want := content
			if len(images) > 0 {
				blocks := ai.UserContentBlocks{}
				switch content := content.(type) {
				case ai.UserText:
					if content != "" {
						blocks = append(blocks, ai.TextContent{Text: string(content)})
					}
				case ai.UserContentBlocks:
					blocks = append(blocks, content...)
				}
				blocks = append(blocks, image)
				want = blocks
			}
			if got := pending.Value.Message.User.Content; !reflect.DeepEqual(got, want) {
				t.Fatalf("queued %T with %d images = %#v; want %#v", content, len(images), got, want)
			}
			after, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatalf("queue mutated caller: %s -> %s", before, after)
			}
		}
	}
}
