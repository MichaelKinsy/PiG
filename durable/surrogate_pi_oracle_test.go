package durable

// pi: packages/durable/src/documents.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi stores every one of these values as JSON.stringify writes it, and a Pi string can hold a lone UTF-16 surrogate: the text
// holds its escape (\ud800). A Go string holds the unit as WTF-8, which encoding/json turns into U+FFFD, so two different
// units became one stored text (and one AddressId). The wanted text below is produced by Pi itself (JSON.stringify of the value
// the Pi-shaped literal describes, and documents.ts addressId), never by a Go encoder.

const (
	loneHigh  = "\xed\xa0\x80" // \ud800
	loneHigh2 = "\xed\xa0\x81" // \ud801
	loneLow   = "\xed\xb0\x80" // \udc00
)

type surrogateCase struct {
	name string
	got  func() ([]byte, error)
	// pi is the JSON text of the value in Pi's member order, escapes spelled out.
	pi string
	// address marks a case whose pi text is a DocumentAddress: Pi's answer is documents.ts addressId of it.
	address bool
}

func surrogateCases() []surrogateCase {
	str := func(value string) *string { return &value }
	seq := func(value int) *int { return &value }
	head := EntryId(7)
	var overrides CompactionOverrides
	overrides.Set(loneHigh+"/a", CompactionPolicyOverride{ReserveTokens: seq(5)})
	overrides.Set("b/"+loneLow, CompactionPolicyOverride{})
	checkpoint := loneHigh
	result := "r" + loneLow
	reason := loneHigh2
	return []surrogateCase{
		{name: "task state checkpoint", got: func() ([]byte, error) {
			return TaskState[string, string]{Status: TaskRunning, Checkpoint: &checkpoint}.MarshalJSON()
		}, pi: `{"status":"running","checkpoint":"\ud800"}`},
		{name: "task state outcome result", got: func() ([]byte, error) {
			return TaskState[string, string]{Status: TaskTerminal, Outcome: &TaskOutcome[string]{Status: OutcomeCompleted, Result: &result}}.MarshalJSON()
		}, pi: `{"status":"terminal","outcome":{"status":"completed","result":"r\udc00"}}`},
		{name: "task outcome error and reason", got: func() ([]byte, error) {
			return TaskOutcome[string]{Status: OutcomeFailed, Error: &TaskOutcomeError{Message: loneHigh + "x"}, Reason: &reason}.MarshalJSON()
		}, pi: `{"status":"failed","error":{"message":"\ud800x"},"reason":"\ud801"}`},
		{name: "entry draft kind data and head", got: func() ([]byte, error) {
			return EntryDraft{Kind: loneHigh, Data: map[string]any{"k" + loneLow: loneHigh2}, Head: &head}.MarshalJSON()
		}, pi: `{"kind":"\ud800","data":{"k\udc00":"\ud801"},"head":7}`},
		{name: "context edit messages", got: func() ([]byte, error) {
			return ContextEdit{Target: 3, Action: EditReplace, Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(loneHigh), Timestamp: 1}}}.MarshalJSON()
		}, pi: `{"target":3,"action":"replace","messages":[{"role":"user","content":"\ud800","timestamp":1}]}`},
		{name: "tool control handoff and addTools", got: func() ([]byte, error) {
			return ToolControl{AddTools: []string{loneHigh}, Handoff: str(loneLow)}.MarshalJSON()
		}, pi: `{"addTools":["\ud800"],"handoff":"\udc00"}`},
		{name: "compaction model overrides keys", got: func() ([]byte, error) { return overrides.MarshalJSON() },
			pi: `{"\ud800/a":{"reserveTokens":5},"b/\udc00":{}}`},
		{name: "document content value", got: func() ([]byte, error) {
			value := delta.JsonObjectOf("a"+loneHigh, loneLow)
			return DocumentContent{Version: 1, Kind: ContentBase, Value: value}.MarshalJSON()
		}, pi: `{"version":1,"kind":"base","value":{"a\ud800":"\udc00"}}`},
		{name: "address id of a lone-surrogate kind", got: func() ([]byte, error) {
			return []byte(AddressId(DocumentAddress{Kind: loneHigh, Scope: DocumentRecordScope{Kind: ScopeSession}})), nil
		}, pi: `{"kind":"\ud800","scope":{"kind":"session"}}`, address: true},
		{name: "address id of a lone-surrogate key", got: func() ([]byte, error) {
			return []byte(AddressId(DocumentAddress{Kind: "k", Scope: DocumentRecordScope{Kind: ScopeConversation, ConversationId: 4}, Key: str(loneLow)})), nil
		}, pi: `{"kind":"k","scope":{"kind":"conversation","conversationId":4},"key":"\udc00"}`, address: true},
	}
}

func TestLoneSurrogatesEncodeAsPiDoes(t *testing.T) {
	cases := surrogateCases()
	type op struct {
		Op   string `json:"op"`
		Text string `json:"text"`
	}
	ops := make([]op, len(cases))
	for index, c := range cases {
		ops[index] = op{Op: "stringify", Text: c.pi}
		if c.address {
			ops[index].Op = "addressId"
		}
	}
	input, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), testenv.RequireQualifiedNode(t), "--disable-warning=ExperimentalWarning", "testdata/surrogate_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi []string
	if err := json.Unmarshal(output, &pi); err != nil || len(pi) != len(cases) {
		t.Fatalf("oracle output: %v (%d answers for %d cases)", err, len(pi), len(cases))
	}
	for index, c := range cases {
		got, err := c.got()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(got) != pi[index] {
			t.Errorf("%s:\n got %s\nwant %s (Pi)", c.name, got, pi[index])
		}
	}
}

func TestAddressIdKeepsDifferentLoneSurrogatesApart(t *testing.T) {
	scope := DocumentRecordScope{Kind: ScopeSession}
	if AddressId(DocumentAddress{Kind: loneHigh, Scope: scope}) == AddressId(DocumentAddress{Kind: loneHigh2, Scope: scope}) {
		t.Fatal(`kinds "\ud800" and "\ud801" give one AddressId`)
	}
}

// What a storage wrote (the escape) reads back as the unit, not as U+FFFD.
func TestLoneSurrogatesDecodeAsJSONParseDoes(t *testing.T) {
	var draft EntryDraft
	if err := draft.UnmarshalJSON([]byte(`{"kind":"\ud800","data":"\udc00"}`)); err != nil {
		t.Fatal(err)
	}
	if draft.Kind != loneHigh || draft.Data != loneLow {
		t.Errorf("entry draft = %q %q, want the units kept", draft.Kind, draft.Data)
	}
	var edit ContextEdit
	if err := edit.UnmarshalJSON([]byte(`{"target":3,"action":"replace","messages":[{"role":"user","content":"\ud800","timestamp":1}]}`)); err != nil {
		t.Fatal(err)
	}
	if message, ok := edit.Messages[0].(ai.UserMessage); !ok || message.Content != ai.UserText(loneHigh) {
		t.Errorf("context edit messages = %#v, want the unit kept", edit.Messages)
	}
	var content DocumentContent
	if err := content.UnmarshalJSON([]byte(`{"version":1,"kind":"base","value":{"a\ud800":"\udc00"}}`)); err != nil {
		t.Fatal(err)
	}
	if unit, _ := content.Value.Get("a" + loneHigh); unit != loneLow {
		t.Errorf("document content value = %v, want the units kept", content.Value)
	}
	var overrides CompactionOverrides
	if err := overrides.UnmarshalJSON([]byte(`{"\ud800/a":{"reserveTokens":5},"b/\udc00":{}}`)); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for model := range overrides.All() {
		keys = append(keys, model)
	}
	if len(keys) != 2 || keys[0] != loneHigh+"/a" || keys[1] != "b/"+loneLow {
		t.Errorf("modelOverrides keys = %q, want the units kept", keys)
	}
	var state TaskState[string, string]
	if err := unmarshalOrdered([]byte(`{"status":"terminal","checkpoint":"\ud800","outcome":{"status":"failed","error":{"message":"\udc00"},"reason":"\ud801"}}`), &state); err != nil {
		t.Fatal(err)
	}
	if *state.Checkpoint != loneHigh || state.Outcome.Error.Message != loneLow || *state.Outcome.Reason != loneHigh2 {
		t.Errorf("task state = %+v, want the units kept", state)
	}
}
