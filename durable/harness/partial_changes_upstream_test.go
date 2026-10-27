package harness

// pi: packages/durable/src/harness/partial-changes.ts

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// upstream: packages/durable/test/harness-partial-changes.test.ts (#9265): partial commits reassigned the growing strings of a streamed answer, and Chord compared each one whole.

// partialStream is a response streaming one text block (started empty) and then one tool call, as a provider produces it: the partial message it mutates and the events it emits. Partial commits go into a tracked document, as Durable's generation does.
type partialStream struct {
	t       *testing.T
	partial *ai.AssistantMessage
	changes *PartialChanges
	tracker *delta.Tracker
	args    *ai.ToolCallArgumentsStream
	ops     [][]delta.Op
}

func newPartialStream(t *testing.T) *partialStream {
	return &partialStream{
		t:       t,
		partial: &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{}}, API: "test", Provider: "test", Model: "test", StopReason: ai.StopReasonPending},
		changes: NewPartialChanges(),
		tracker: delta.Track(delta.JsonObjectOf("generation", delta.JsonObjectOf("attempt", float64(1)))),
		args:    ai.NewToolCallArgumentsStream(),
	}
}

func (stream *partialStream) text(piece string) {
	block := stream.partial.Content[0].(ai.TextContent)
	block.Text += piece
	stream.partial.Content[0] = block
	stream.changes.Observe(ai.TextDeltaEvent{ContentIndex: 0, Delta: piece, Partial: stream.partial})
}

func (stream *partialStream) argument(piece string) {
	if len(stream.partial.Content) == 1 {
		stream.partial.Content = append(stream.partial.Content, ai.ToolCall{ID: "c1", Name: "write", Arguments: ai.JsonObject{}})
		stream.changes.Observe(ai.ToolCallStartEvent{ContentIndex: 1, Partial: stream.partial})
	}
	call := stream.partial.Content[1].(ai.ToolCall)
	call.AppendStreamedArguments(stream.args, piece)
	stream.partial.Content[1] = call
	stream.changes.Observe(ai.ToolCallDeltaEvent{ContentIndex: 1, Delta: piece, Partial: stream.partial})
}

// endBlocks is the provider ending both blocks.
func (stream *partialStream) endBlocks() {
	stream.changes.Observe(ai.TextEndEvent{ContentIndex: 0, Content: stream.partial.Content[0].(ai.TextContent).Text, Partial: stream.partial})
	stream.changes.Observe(ai.ToolCallEndEvent{ContentIndex: 1, ToolCall: stream.partial.Content[1].(ai.ToolCall), Partial: stream.partial})
}

// commit is one partial commit, as the generation's throttle makes it; fail aborts it like a rejected commit.
func (stream *partialStream) commit(fail bool) {
	stream.t.Helper()
	stream.commitCut(stream.changes.Cut(), stream.partial, fail)
}

// commitCut is a partial commit of partial with what cut took.
func (stream *partialStream) commitCut(cut *PartialCut, partial *ai.AssistantMessage, fail bool) {
	stream.t.Helper()
	message, err := durable.ToJsonObject(partial)
	if err != nil {
		stream.t.Fatal(err)
	}
	taken := stream.changes.Bind(cut, message)
	change := stream.tracker.BeginChange()
	if err := stream.changes.Commit(change.State().Object("generation"), message, taken); err != nil {
		stream.t.Fatal(err)
	}
	if fail {
		change.Abort()
		stream.changes.Restore(taken)
		return
	}
	prepared, err := change.Prepare()
	if err != nil {
		stream.t.Fatal(err)
	}
	stream.ops = append(stream.ops, slices.Clone(prepared.Ops()))
	if err := stream.tracker.Adopt(prepared); err != nil {
		stream.t.Fatal(err)
	}
}

// committed is the text of block 0 and the arguments of block 1 in the committed message.
func (stream *partialStream) committed() (string, any) {
	stream.t.Helper()
	message := stream.tracker.Value().Value("generation").(*delta.JsonObject).Value("message").(*delta.JsonObject)
	content := message.Value("content").([]any)
	var text string
	if len(content) > 0 {
		text, _ = content[0].(*delta.JsonObject).Value("text").(string)
	}
	var arguments any
	if len(content) > 1 {
		arguments = content[1].(*delta.JsonObject).Value("arguments")
	}
	return text, arguments
}

func partialJSONOf(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func pieces(text string, size int) []string {
	var result []string
	for index := 0; index < len(text); index += size {
		result = append(result, text[index:min(index+size, len(text))])
	}
	return result
}

const partialArguments = `{"path":"src/a.ts","n":1234,"ok":true,"content":"line \"one\"\n\tline \\ two é 😀\n","edits":[{"oldText":"a","newText":"b\nc"},{"oldText":"","newText":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}],"nested":{"deep":{"list":[1,[2,"three"],{"four":null}]}}}`

func TestPartialChangesCommitArgumentsEqualToTheFinalParseAtEveryPieceSizeAndCommitRhythm(t *testing.T) {
	want := decodePartialArguments(t, partialArguments)
	for _, size := range []int{1, 2, 3, 5, 7, 16} {
		for _, every := range []int{1, 3, 10} {
			stream := newPartialStream(t)
			for index, piece := range pieces(partialArguments, size) {
				stream.argument(piece)
				if index%every == every-1 {
					stream.commit(false)
				}
			}
			stream.commit(false)
			if _, got := stream.committed(); partialJSONOf(t, got) != partialJSONOf(t, want) {
				t.Fatalf("size %d, every %d: committed %s, want %s", size, every, partialJSONOf(t, got), partialJSONOf(t, want))
			}
		}
	}
}

func decodePartialArguments(t *testing.T, text string) *delta.JsonObject {
	t.Helper()
	var object delta.JsonObject
	if err := json.Unmarshal([]byte(text), &object); err != nil {
		t.Fatal(err)
	}
	return &object
}

func TestPartialChangesHandleANumberEndingAndAStringStartingInOnePieceAndEscapesSplitAcrossPieces(t *testing.T) {
	stream := newPartialStream(t)
	for _, piece := range []string{`{"n":1`, `2,"s":"a`, `b\`, `u00e9\"`, `q\u`, `00`, `41`, `","t":tr`, `ue,`, `"u":"\ud83d`, `\ude00"}`} {
		stream.argument(piece)
		stream.commit(false)
	}
	_, got := stream.committed()
	if want := `{"n":12,"s":"abé\"qA","t":true,"u":"😀"}`; partialJSONOf(t, got) != want {
		t.Fatalf("committed %s, want %s", partialJSONOf(t, got), want)
	}
}

func TestPartialChangesAppendGrowingArgumentAndTextStringsInsteadOfReassigningThem(t *testing.T) {
	stream := newPartialStream(t)
	stream.text("Hello")
	stream.argument(`{"path":"a.ts","content":"x`)
	stream.commit(false)
	for range 20 {
		stream.text(" more")
		stream.argument("yz")
		stream.commit(false)
	}
	want := []delta.Op{
		{"a", []any{"generation", "message", "content", 0, "text"}, " more"},
		{"a", []any{"generation", "message", "content", 1, "arguments", "content"}, "yz"},
	}
	for _, ops := range stream.ops[1:] {
		if !reflect.DeepEqual(ops, want) {
			t.Fatalf("ops = %v, want %v", ops, want)
		}
	}
	text, arguments := stream.committed()
	if text != "Hello"+strings.Repeat(" more", 20) || partialJSONOf(t, arguments) != `{"path":"a.ts","content":"x`+strings.Repeat("yz", 20)+`"}` {
		t.Fatalf("committed %q %s", text, partialJSONOf(t, arguments))
	}
}

func TestPartialChangesApplyTheChangesOfAFailedCommitInTheNextOne(t *testing.T) {
	stream := newPartialStream(t)
	stream.argument(`{"a":"x`)
	stream.commit(false)
	stream.argument(`y","b":[1`)
	stream.commit(true)
	stream.argument(`,2],"c":"z"}`)
	stream.commit(false)
	if _, got := stream.committed(); partialJSONOf(t, got) != `{"a":"xy","b":[1,2],"c":"z"}` {
		t.Fatalf("committed %s", partialJSONOf(t, got))
	}
}

func TestPartialChangesReApplyAFailedCommitsChangesEvenWhenItWouldHaveAddedTheCall(t *testing.T) {
	stream := newPartialStream(t)
	stream.commit(false)
	stream.argument(`{"a":"x`)
	stream.commit(true)
	stream.argument(`y"`)
	stream.commit(false)
	if _, got := stream.committed(); partialJSONOf(t, got) != `{"a":"xy"}` {
		t.Fatalf("committed %s", partialJSONOf(t, got))
	}
}

func TestPartialChangesFallBackToTheSnapshotWhenTheCommittedMessageLostACallWhoseChangesWereCommitted(t *testing.T) {
	stream := newPartialStream(t)
	stream.argument(`{"a":"x`)
	stream.commit(false)
	// Another writer resets the generation's partial.
	change := stream.tracker.BeginChange()
	if err := change.State().Set("generation", delta.JsonObjectOf("attempt", float64(1))); err != nil {
		t.Fatal(err)
	}
	prepared, err := change.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.tracker.Adopt(prepared); err != nil {
		t.Fatal(err)
	}
	stream.argument(`y"`)
	stream.commit(false)
	if _, got := stream.committed(); partialJSONOf(t, got) != `{"a":"xy"}` {
		t.Fatalf("committed %s", partialJSONOf(t, got))
	}
	stream.argument(`,"b":2}`)
	stream.commit(false)
	if _, got := stream.committed(); partialJSONOf(t, got) != `{"a":"xy","b":2}` {
		t.Fatalf("committed %s", partialJSONOf(t, got))
	}
}

func TestPartialChangesCommitTheFinalArgumentsAfterTheCallEndsAndDropTextAppendsWhenABlockEnds(t *testing.T) {
	stream := newPartialStream(t)
	stream.text("draft")
	stream.argument(`{"a":"x`)
	stream.commit(false)
	// Providers may replace a block's text and arguments as it ends; the last commit assigns what they ended with.
	stream.text(" more")
	stream.partial.Content[0] = ai.TextContent{Text: "replaced"}
	stream.partial.Content[1] = ai.ToolCall{ID: "c1", Name: "write", Arguments: ai.JsonObject{"a": "final"}}
	stream.endBlocks()
	stream.commit(false)
	text, arguments := stream.committed()
	if text != "replaced" || partialJSONOf(t, arguments) != `{"a":"final"}` {
		t.Fatalf("committed %q %s", text, partialJSONOf(t, arguments))
	}
}
