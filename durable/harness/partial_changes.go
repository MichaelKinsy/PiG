// Ports packages/durable/src/harness/partial-changes.ts: what a streamed answer changed in its partial message between two partial commits, so a commit costs the new text.

package harness

import (
	"errors"
	"fmt"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// toolCallChanges is the argument changes of one tracked tool call since the last commit, and its snapshot.
type toolCallChanges struct {
	changes []ai.JSONChange
	// snapshot is the call's arguments as the partial held them, read only when the call falls back to its snapshot.
	snapshot func() (durable.JsonValue, error)
}

// PartialCommit is what one partial commit writes besides the partial message itself; from PartialChanges.Bind.
type PartialCommit struct {
	// plan is the plan AssignJson writes the message with: text appends, and the arguments it leaves to the changes.
	plan *AssignPlan
	// toolCalls is each tracked tool call by content index.
	toolCalls map[int]toolCallChanges
}

// PartialCut is what the events observed up to one moment changed since the last cut, before it is bound to the partial message of that
// moment; from PartialChanges.Cut.
type PartialCut struct {
	// text is the text and thinking appended to each tracked block, by content index.
	text map[int]*trackedText
	// toolCalls is the argument changes of each tracked tool call, by content index.
	toolCalls map[int][]ai.JSONChange
}

type trackedText struct {
	field string
	text  []byte
}

// trackedToolCall is what a tracked tool call changed since the last commit, and whether any commit took its changes yet.
type trackedToolCall struct {
	parser    ai.StreamingJSONParser
	changes   []ai.JSONChange
	committed bool
}

// PartialChanges collects, from a response's events, what changed in its partial message since the last commit, so the commit appends new
// text instead of reassigning growing strings. Reassigning makes Chord compare the old and new string, which costs the length of the string
// per commit.
//
//   - Text and thinking: the text_delta and thinking_delta events. The commit appends them where the committed string plus the deltas has the
//     new string's length, and compares otherwise. A block's end event drops them, because providers may replace the text when a block ends.
//   - Tool-call arguments: a StreamingJSONParser per call, fed with the raw argument text of each toolcall_delta, reports what each piece
//     changed. The commit applies those changes to the committed arguments in order and leaves the arguments out of the message assignment,
//     so the committed arguments are exactly the parsed text so far. toolcall_end ends the tracking; the call's last commit assigns its final
//     arguments.
//
// The event consumer and the commit run on different goroutines, so the state is under a mutex. The changes describe the partial message of
// the last event observed, and a commit writes the partial it is given, so the caller keeps the two paired: it observes an event and
// records that event's partial in one step, and cuts the changes and takes the recorded partial in one step (Cut, Bind).
type PartialChanges struct {
	mu        sync.Mutex
	text      map[int]*trackedText
	toolCalls map[int]*trackedToolCall
}

// NewPartialChanges returns a collector with nothing tracked.
func NewPartialChanges() *PartialChanges {
	return &PartialChanges{text: map[int]*trackedText{}, toolCalls: map[int]*trackedToolCall{}}
}

// Observe records what event changed.
func (changes *PartialChanges) Observe(event ai.AssistantMessageEvent) {
	changes.mu.Lock()
	defer changes.mu.Unlock()
	switch event := event.(type) {
	case ai.TextDeltaEvent:
		changes.appendText(event.ContentIndex, "text", event.Delta)
	case ai.ThinkingDeltaEvent:
		changes.appendText(event.ContentIndex, "thinking", event.Delta)
	case ai.ToolCallStartEvent:
		changes.toolCalls[event.ContentIndex] = &trackedToolCall{}
	case ai.ToolCallDeltaEvent:
		call := changes.toolCalls[event.ContentIndex]
		if call == nil {
			return
		}
		for _, change := range call.parser.Append(event.Delta) {
			// Arguments are an object: any other root shows as no arguments, so the snapshot is committed instead.
			if len(change.Path) == 0 {
				if _, isRecord := change.Value.(map[string]any); change.Kind != "set" || !isRecord {
					delete(changes.toolCalls, event.ContentIndex)
					return
				}
			}
			call.changes = append(call.changes, change)
		}
	case ai.TextEndEvent:
		delete(changes.text, event.ContentIndex)
	case ai.ThinkingEndEvent:
		delete(changes.text, event.ContentIndex)
	case ai.ToolCallEndEvent:
		delete(changes.toolCalls, event.ContentIndex)
	}
}

func (changes *PartialChanges) appendText(index int, field, delta string) {
	if pending := changes.text[index]; pending != nil && pending.field == field {
		pending.text = append(pending.text, delta...)
		return
	}
	changes.text[index] = &trackedText{field: field, text: []byte(delta)}
}

// Cut takes what the events observed so far changed since the last cut. It describes the partial message of the last event observed, not
// of any later one: bind it to a copy of exactly that partial with Bind. A cut is only as good as that pairing, since a string that grew by
// as much as the changes did is appended their characters, not its own, so a partial one event behind or ahead of the changes would commit
// text no stream produced.
func (changes *PartialChanges) Cut() *PartialCut {
	changes.mu.Lock()
	defer changes.mu.Unlock()
	cut := &PartialCut{text: changes.text, toolCalls: make(map[int][]ai.JSONChange, len(changes.toolCalls))}
	changes.text = map[int]*trackedText{}
	for index, call := range changes.toolCalls {
		cut.toolCalls[index] = call.changes
		call.changes = nil
	}
	return cut
}

// BindPartial binds cut to partial, the partial message it describes, for a commit: it returns the JSON object of partial, whose tracked tool
// calls have no arguments, because the commit writes them from the changes, and what the commit takes. It builds the object without
// encoding and decoding the text and thinking of the blocks or the arguments of the tracked calls, which grow with the stream: every commit
// would otherwise cost the length of the message. Call Restore if the commit fails.
func (changes *PartialChanges) BindPartial(cut *PartialCut, partial *ai.AssistantMessage) (durable.JsonObject, *PartialCommit, error) {
	owned := partial.Observe()
	message, err := partialObject(owned, cut.toolCalls)
	if err != nil {
		return nil, nil, err
	}
	blocks := map[any]*AssignPlan{}
	for index, pending := range cut.text {
		blocks[index] = &AssignPlan{Kind: planFields, Fields: map[any]*AssignPlan{pending.field: {Kind: planAppend, Text: string(pending.text)}}}
	}
	taken := &PartialCommit{toolCalls: map[int]toolCallChanges{}}
	for index, pieces := range cut.toolCalls {
		var call ai.ToolCall
		isCall := false
		if index >= 0 && index < len(owned.Content) {
			call, isCall = owned.Content[index].(ai.ToolCall)
		}
		if !isCall {
			// The partial has no such tool call: the changes wait for a later one.
			changes.restoreCall(index, pieces)
			continue
		}
		taken.toolCalls[index] = toolCallChanges{changes: pieces, snapshot: snapshotOf(call)}
		blocks[index] = &AssignPlan{Kind: planFields, Fields: map[any]*AssignPlan{"arguments": {Kind: planKeep}}}
	}
	taken.plan = &AssignPlan{Kind: planFields, Fields: map[any]*AssignPlan{"content": {Kind: planFields, Fields: blocks}}}
	return message, taken, nil
}

// snapshotOf is the arguments of call as they encode, read when the call falls back to its snapshot.
func snapshotOf(call ai.ToolCall) func() (durable.JsonValue, error) {
	return func() (durable.JsonValue, error) {
		object, err := durable.ToJsonObject(call)
		if err != nil {
			return nil, err
		}
		arguments, _ := object.Get("arguments")
		return arguments, nil
	}
}

// partialObject is the JSON object of partial, as durable.ToJsonObject(partial) returns it, except that the arguments of the tool calls in
// tracked are empty. The text and thinking of its blocks are not encoded: they are the strings the partial holds, where the encoding and the
// decoding copy them, and the commit that appends to them copies nothing. A string that is not valid UTF-8 is encoded, as the encoding
// writes it.
func partialObject(partial *ai.AssistantMessage, tracked map[int][]ai.JSONChange) (durable.JsonObject, error) {
	skeleton := *partial
	if partial.Content != nil {
		skeleton.Content = make([]ai.AssistantContentBlock, len(partial.Content))
	}
	type grown struct {
		index int
		field string
		text  string
	}
	var grownStrings []grown
	for index, block := range partial.Content {
		switch block := block.(type) {
		case ai.TextContent:
			if utf8.ValidString(block.Text) {
				grownStrings = append(grownStrings, grown{index, "text", block.Text})
				block.Text = ""
			}
			skeleton.Content[index] = block
		case ai.ThinkingContent:
			if utf8.ValidString(block.Thinking) {
				grownStrings = append(grownStrings, grown{index, "thinking", block.Thinking})
				block.Thinking = ""
			}
			skeleton.Content[index] = block
		case ai.ToolCall:
			if _, isTracked := tracked[index]; isTracked {
				block = ai.ToolCall{ID: block.ID, Name: block.Name, Arguments: ai.JsonObject{}, ThoughtSignature: block.ThoughtSignature, Namespace: block.Namespace, ArgumentsError: block.ArgumentsError}
			}
			skeleton.Content[index] = block
		default:
			skeleton.Content[index] = block
		}
	}
	message, err := durable.ToJsonObject(&skeleton)
	if err != nil {
		return nil, err
	}
	content, _ := message.Value("content").([]any)
	for _, entry := range grownStrings {
		block, ok := content[entry.index].(*delta.JsonObject)
		if !ok {
			return nil, fmt.Errorf("partial content[%d] encodes as %T, not an object", entry.index, content[entry.index])
		}
		block.Set(entry.field, entry.text)
	}
	return message, nil
}

// Uncut puts back what cut took, when no commit is made of it: the argument changes wait for the next cut, and the appended text is
// dropped, so the next commit of that text compares it.
func (changes *PartialChanges) Uncut(cut *PartialCut) {
	for index, pieces := range cut.toolCalls {
		changes.restoreCall(index, pieces)
	}
}

// restoreCall puts the argument changes of a tracked call that no commit applied ahead of those observed since.
func (changes *PartialChanges) restoreCall(index int, pieces []ai.JSONChange) {
	changes.mu.Lock()
	defer changes.mu.Unlock()
	if call := changes.toolCalls[index]; call != nil {
		call.changes = append(append([]ai.JSONChange(nil), pieces...), call.changes...)
	}
}

// Commit writes message with what taken took into generation.message: the message leaf by leaf with the plan, then the argument changes. A
// tracked call whose block is new here gets its arguments from its changes, which then start at its first piece; when the committed message
// lost a call whose changes were already committed (another writer reset it), the call falls back to its snapshot from now on.
func (changes *PartialChanges) Commit(generation *delta.Object, message durable.JsonObject, taken *PartialCommit) error {
	changes.mu.Lock()
	defer changes.mu.Unlock()
	committedBlocks := 0
	if committed := generation.Object("message"); committed != nil {
		if content := committed.Array("content"); content != nil {
			committedBlocks = content.Len()
		}
	}
	messageContent, _ := message.Value("content").([]any)
	type applied struct {
		index   int
		changes []ai.JSONChange
	}
	var apply []applied
	for index, taken := range taken.toolCalls {
		call := changes.toolCalls[index]
		if call == nil {
			continue
		}
		if index >= committedBlocks && call.committed {
			// The earlier changes are not in the committed message: commit the snapshot and stop tracking the call.
			delete(changes.toolCalls, index)
			snapshot, err := taken.snapshot()
			if err != nil {
				return err
			}
			messageContent[index].(*delta.JsonObject).Set("arguments", snapshot)
			continue
		}
		call.committed = true
		apply = append(apply, applied{index, taken.changes})
	}
	if err := assignSlot(objectSlot{generation, "message"}, message, taken.plan); err != nil {
		return err
	}
	content := generation.Object("message").Array("content")
	for _, entry := range apply {
		block := content.Object(entry.index)
		for _, change := range entry.changes {
			if err := applyArgumentChange(block, change); err != nil {
				return err
			}
		}
	}
	return nil
}

// Restore puts back what a failed commit took, so the next commit applies it first.
func (changes *PartialChanges) Restore(taken *PartialCommit) {
	for index, entry := range taken.toolCalls {
		changes.restoreCall(index, entry.changes)
	}
}

// applyArgumentChange applies one argument change to the committed arguments of block.
func applyArgumentChange(block *delta.Object, change ai.JSONChange) error {
	if len(change.Path) == 0 {
		// Only an object root is tracked, and only a set starts it.
		if change.Kind == "set" {
			return block.Set("arguments", change.Value)
		}
		return nil
	}
	var parent any = block.Object("arguments")
	for _, segment := range change.Path[:len(change.Path)-1] {
		switch container := parent.(type) {
		case *delta.Object:
			parent = container.Get(segment.(string))
		case *delta.Array:
			parent = container.Get(segment.(int))
		default:
			return errors.New("partial arguments path leaves the committed arguments")
		}
	}
	key := change.Path[len(change.Path)-1]
	switch container := parent.(type) {
	case *delta.Object:
		name, ok := key.(string)
		if !ok {
			return fmt.Errorf("partial arguments key %v is not a member name", key)
		}
		if change.Kind == "set" {
			return container.Set(name, change.Value)
		}
		return delta.AppendText(container, name, change.Text)
	case *delta.Array:
		index, ok := key.(int)
		if !ok {
			return fmt.Errorf("partial arguments key %v is not an element index", key)
		}
		value := change.Value
		if change.Kind != "set" {
			// AppendText takes object members; a string in an array is reassigned, which compares it whole.
			previous, _ := container.Get(index).(string)
			value = jsstring.Concat(previous, change.Text)
		}
		if index == container.Len() {
			_, err := container.Push(value)
			return err
		}
		return container.Set(index, value)
	}
	return errors.New("partial arguments path leaves the committed arguments")
}
