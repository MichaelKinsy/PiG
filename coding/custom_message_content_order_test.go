package coding

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

const (
	orderedContent = `[{"type":"text","text":"hello"},{"type":"image","mimeType":"image/png","data":"aGk="}]`
	orderedDetails = `{"z":1,"a":{"y":1,"b":2}}`
)

// pi.sendMessage stores the content blocks and details an extension wrote with their members in the order it wrote them: Pi appends the objects themselves
// and JSON.stringify writes insertion order. The host reads the message from the wire, whichever SDK sent it.
func TestSendMessageStoresContentBlocksInTheOrderTheExtensionWroteThem(t *testing.T) {
	var ref extension.CustomMessageRef
	if err := json.Unmarshal([]byte(`{"customType":"order","content":`+orderedContent+`,"display":true,"details":`+orderedDetails+`}`), &ref); err != nil {
		t.Fatal(err)
	}
	h := newBoundaryHarness(t, harnessOptions{}, boundaryReply("done", ai.StopReasonStop, 0))
	if err := h.session.SendMessage(ref, nil); err != nil {
		t.Fatal(err)
	}
	var stored string
	for _, entry := range h.session.Inner().GetEntries() {
		if entry.Base().Type == "custom_message" {
			stored = string(entry.Raw())
		}
	}
	if !strings.Contains(stored, `"content":`+orderedContent) || !strings.Contains(stored, `"details":`+orderedDetails) {
		t.Errorf("the stored entry does not hold the content and details as written\n got %s", stored)
	}
}

// A custom message read back from the wire or from the session file keeps its content blocks in the order they were written: in the entry, in the message
// (the context an extension and the provider conversion read), and when either is written again.
func TestCustomMessageContentKeepsItsMemberOrderWhenRead(t *testing.T) {
	entry := `{"type":"custom_message","id":"e1","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","customType":"order","content":` + orderedContent + `,"display":true,"details":` + orderedDetails + `}`
	var read icodingagent.CustomMessageEntry
	if err := json.Unmarshal([]byte(entry), &read); err != nil {
		t.Fatal(err)
	}
	written, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `"content":`+orderedContent) {
		t.Errorf("the entry does not write its content as it read it\n got %s", written)
	}
	var message agent.AgentMessage
	if err := json.Unmarshal([]byte(`{"role":"custom","customType":"order","content":`+orderedContent+`,"display":true,"details":`+orderedDetails+`,"timestamp":1}`), &message); err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(message.Custom["content"])
	if err != nil || string(content) != orderedContent {
		t.Errorf("the message does not hold its content as it read it: %s (%v)", content, err)
	}
	// A message with a member of the wrong type, which the decoder reads leniently (a record from session history that Pi keeps as parsed), keeps its content order too.
	var lax agent.AgentMessage
	if err := json.Unmarshal([]byte(`{"role":"custom","customType":"order","content":`+orderedContent+`,"display":true,"details":`+orderedDetails+`,"timestamp":"x"}`), &lax); err != nil {
		t.Fatal(err)
	}
	content, err = json.Marshal(lax.Custom["content"])
	if err != nil || string(content) != orderedContent {
		t.Errorf("the leniently read message does not hold its content as it read it: %s (%v)", content, err)
	}
}
