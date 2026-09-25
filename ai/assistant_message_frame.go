package ai

// Mirrors upstream .upstream/current/packages/ai/src/utils/assistant-message-frame.ts.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// AssistantMessageFrameType discriminates the compact frame union.
type AssistantMessageFrameType string

const (
	FrameTypeStart              AssistantMessageFrameType = "start"
	FrameTypeTextStart          AssistantMessageFrameType = "text_start"
	FrameTypeTextDelta          AssistantMessageFrameType = "text_delta"
	FrameTypeTextEnd            AssistantMessageFrameType = "text_end"
	FrameTypeThinkingStart      AssistantMessageFrameType = "thinking_start"
	FrameTypeThinkingDelta      AssistantMessageFrameType = "thinking_delta"
	FrameTypeThinkingEnd        AssistantMessageFrameType = "thinking_end"
	FrameTypeToolCallStart      AssistantMessageFrameType = "toolcall_start"
	FrameTypeToolCallCheckpoint AssistantMessageFrameType = "toolcall_checkpoint"
	FrameTypeToolCallDelta      AssistantMessageFrameType = "toolcall_delta"
	FrameTypeToolCallEnd        AssistantMessageFrameType = "toolcall_end"
)

// AssistantMessageFrame is compact, replayable assistant-message progress.
// Terminal settlement is intentionally excluded and must be persisted
// separately. The union is closed like AssistantMessageEvent.
type AssistantMessageFrame interface {
	FrameType() AssistantMessageFrameType
	assistantMessageFrame()
}

type StartFrame struct {
	Partial AssistantMessage `json:"partial"`
}

type TextStartFrame struct {
	ContentIndex int         `json:"contentIndex"`
	Content      TextContent `json:"content"`
}

type TextDeltaFrame struct {
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

type TextEndFrame struct {
	ContentIndex  int    `json:"contentIndex"`
	Content       string `json:"content"`
	TextSignature string `json:"textSignature,omitempty"`
}

type ThinkingStartFrame struct {
	ContentIndex int             `json:"contentIndex"`
	Content      ThinkingContent `json:"content"`
}

type ThinkingDeltaFrame struct {
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

type ThinkingEndFrame struct {
	ContentIndex      int    `json:"contentIndex"`
	Content           string `json:"content"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
	Redacted          bool   `json:"redacted,omitempty"`
}

type ToolCallStartFrame struct {
	ContentIndex int      `json:"contentIndex"`
	ToolCall     ToolCall `json:"toolCall"`
}

type ToolCallCheckpointFrame struct {
	ContentIndex int    `json:"contentIndex"`
	JSON         string `json:"json"`
}

type ToolCallDeltaFrame struct {
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
}

type ToolCallEndFrame struct {
	ContentIndex     int        `json:"contentIndex"`
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Arguments        JsonObject `json:"arguments"`
	ThoughtSignature string     `json:"thoughtSignature,omitempty"`
	Namespace        string     `json:"namespace,omitempty"`
}

func (StartFrame) FrameType() AssistantMessageFrameType         { return FrameTypeStart }
func (TextStartFrame) FrameType() AssistantMessageFrameType     { return FrameTypeTextStart }
func (TextDeltaFrame) FrameType() AssistantMessageFrameType     { return FrameTypeTextDelta }
func (TextEndFrame) FrameType() AssistantMessageFrameType       { return FrameTypeTextEnd }
func (ThinkingStartFrame) FrameType() AssistantMessageFrameType { return FrameTypeThinkingStart }
func (ThinkingDeltaFrame) FrameType() AssistantMessageFrameType { return FrameTypeThinkingDelta }
func (ThinkingEndFrame) FrameType() AssistantMessageFrameType   { return FrameTypeThinkingEnd }
func (ToolCallStartFrame) FrameType() AssistantMessageFrameType { return FrameTypeToolCallStart }
func (ToolCallCheckpointFrame) FrameType() AssistantMessageFrameType {
	return FrameTypeToolCallCheckpoint
}
func (ToolCallDeltaFrame) FrameType() AssistantMessageFrameType { return FrameTypeToolCallDelta }
func (ToolCallEndFrame) FrameType() AssistantMessageFrameType   { return FrameTypeToolCallEnd }

func (StartFrame) assistantMessageFrame()              {}
func (TextStartFrame) assistantMessageFrame()          {}
func (TextDeltaFrame) assistantMessageFrame()          {}
func (TextEndFrame) assistantMessageFrame()            {}
func (ThinkingStartFrame) assistantMessageFrame()      {}
func (ThinkingDeltaFrame) assistantMessageFrame()      {}
func (ThinkingEndFrame) assistantMessageFrame()        {}
func (ToolCallStartFrame) assistantMessageFrame()      {}
func (ToolCallCheckpointFrame) assistantMessageFrame() {}
func (ToolCallDeltaFrame) assistantMessageFrame()      {}
func (ToolCallEndFrame) assistantMessageFrame()        {}

func (frame StartFrame) MarshalJSON() ([]byte, error) {
	type plain StartFrame
	return marshalAssistantMessageFrame(frame.FrameType(), plain(frame))
}

func (frame TextStartFrame) MarshalJSON() ([]byte, error) {
	type plain TextStartFrame
	return marshalAssistantMessageFrame(frame.FrameType(), plain(frame))
}

func (frame TextDeltaFrame) MarshalJSON() ([]byte, error) {
	type plain TextDeltaFrame
	return marshalAssistantMessageFrame(frame.FrameType(), plain(frame))
}

func (frame TextEndFrame) MarshalJSON() ([]byte, error) {
	type plain TextEndFrame
	return marshalAssistantMessageFrame(frame.FrameType(), plain(frame))
}

func (frame ThinkingStartFrame) MarshalJSON() ([]byte, error) {
	type plain ThinkingStartFrame
	return marshalAssistantMessageFrame(frame.FrameType(), plain(frame))
}

func (frame ThinkingDeltaFrame) MarshalJSON() ([]byte, error) {
	type plain ThinkingDeltaFrame
	return marshalAssistantMessageFrame(frame.FrameType(), plain(frame))
}

func (frame ThinkingEndFrame) MarshalJSON() ([]byte, error) {
	type plain ThinkingEndFrame
	return marshalAssistantMessageFrame(frame.FrameType(), plain(frame))
}

func (frame ToolCallStartFrame) MarshalJSON() ([]byte, error) {
	type plain ToolCallStartFrame
	return marshalAssistantMessageFrame(frame.FrameType(), plain(frame))
}

func (frame ToolCallCheckpointFrame) MarshalJSON() ([]byte, error) {
	type plain ToolCallCheckpointFrame
	return marshalAssistantMessageFrame(frame.FrameType(), plain(frame))
}

func (frame ToolCallDeltaFrame) MarshalJSON() ([]byte, error) {
	type plain ToolCallDeltaFrame
	return marshalAssistantMessageFrame(frame.FrameType(), plain(frame))
}

func (frame ToolCallEndFrame) MarshalJSON() ([]byte, error) {
	type plain ToolCallEndFrame
	return marshalAssistantMessageFrame(frame.FrameType(), plain(frame))
}

func marshalAssistantMessageFrame(frameType AssistantMessageFrameType, frame any) ([]byte, error) {
	body, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	fields["type"] = json.RawMessage(fmt.Sprintf("%q", frameType))
	return json.Marshal(fields)
}

const (
	blockKindText     = "text"
	blockKindThinking = "thinking"
	blockKindToolCall = "toolCall"
)

type encoderBlockState struct {
	kind string
	// Text and thinking blocks: characters visible in the start snapshot and
	// characters carried by deltas consumed so far. Go counts bytes where Pi
	// counts UTF-16 units; both are consistent because the snapshot text is a
	// concatenation of whole deltas.
	coveredChars int
	deltaChars   int
	// Tool-call blocks.
	caughtUp          bool
	catchupJSON       string
	snapshotArguments string
}

type reducerBlockState struct {
	kind  string
	ended bool
	json  string
}

// AssistantMessageFrameEncoder encodes one assistant stream. Event partials
// may be a shared live accumulator; the encoder uses per-block offsets to
// avoid replaying deltas already visible when an older queued event is
// consumed. The zero value is ready to use.
type AssistantMessageFrameEncoder struct {
	started  bool
	terminal bool
	blocks   map[int]*encoderBlockState
}

// Encode converts one stream event into a frame. It returns a nil frame when
// the event adds nothing replayable (terminal events and covered deltas).
func (encoder *AssistantMessageFrameEncoder) Encode(event AssistantMessageEvent) (AssistantMessageFrame, error) {
	if encoder.terminal {
		return nil, fmt.Errorf("Assistant message event %s follows a terminal event", event.EventType())
	}
	switch event := event.(type) {
	case StartEvent:
		return encoder.encodeStart(event)
	case DoneEvent:
		if !encoder.started {
			return nil, errors.New("Assistant message done event appears before start")
		}
		encoder.terminal = true
		return nil, nil
	case ErrorEvent:
		encoder.terminal = true
		return nil, nil
	}
	if !encoder.started {
		return nil, fmt.Errorf("Assistant message %s event appears before start", event.EventType())
	}
	return encoder.encodeContentEvent(event)
}

func (encoder *AssistantMessageFrameEncoder) encodeStart(event StartEvent) (AssistantMessageFrame, error) {
	if encoder.started {
		return nil, errors.New("Assistant message stream contains more than one start event")
	}
	if event.Partial == nil {
		return nil, errors.New("Assistant message start event has no partial message")
	}
	encoder.started = true
	return StartFrame{Partial: cloneStartMessage(*event.Partial)}, nil
}

func (encoder *AssistantMessageFrameEncoder) encodeContentEvent(event AssistantMessageEvent) (AssistantMessageFrame, error) {
	switch event := event.(type) {
	case TextStartEvent:
		return encoder.encodeTextStart(event)
	case TextDeltaEvent:
		return encoder.encodeTextDelta(event.ContentIndex, event.Delta, blockKindText)
	case TextEndEvent:
		return encoder.encodeTextEnd(event)
	case ThinkingStartEvent:
		return encoder.encodeThinkingStart(event)
	case ThinkingDeltaEvent:
		return encoder.encodeTextDelta(event.ContentIndex, event.Delta, blockKindThinking)
	case ThinkingEndEvent:
		return encoder.encodeThinkingEnd(event)
	case ToolCallStartEvent:
		return encoder.encodeToolCallStart(event)
	case ToolCallDeltaEvent:
		return encoder.encodeToolCallDelta(event)
	case ToolCallEndEvent:
		return encoder.encodeToolCallEnd(event)
	default:
		return nil, fmt.Errorf("Unsupported assistant message event %s", event.EventType())
	}
}

func (encoder *AssistantMessageFrameEncoder) encodeTextStart(event TextStartEvent) (AssistantMessageFrame, error) {
	block, err := eventBlock(event.EventType(), event.ContentIndex, event.Partial)
	if err != nil {
		return nil, err
	}
	content, ok := block.(TextContent)
	if !ok {
		return nil, wrongEventBlock(event.EventType(), block, event.ContentIndex)
	}
	if err := encoder.startBlock(event.ContentIndex, &encoderBlockState{kind: blockKindText, coveredChars: len(content.Text)}); err != nil {
		return nil, err
	}
	return TextStartFrame{ContentIndex: event.ContentIndex, Content: content}, nil
}

func (encoder *AssistantMessageFrameEncoder) encodeTextEnd(event TextEndEvent) (AssistantMessageFrame, error) {
	block, err := eventBlock(event.EventType(), event.ContentIndex, event.Partial)
	if err != nil {
		return nil, err
	}
	content, ok := block.(TextContent)
	if !ok {
		return nil, wrongEventBlock(event.EventType(), block, event.ContentIndex)
	}
	if err := encoder.endBlock(event.ContentIndex, blockKindText); err != nil {
		return nil, err
	}
	return TextEndFrame{ContentIndex: event.ContentIndex, Content: event.Content, TextSignature: content.TextSignature}, nil
}

func (encoder *AssistantMessageFrameEncoder) encodeThinkingStart(event ThinkingStartEvent) (AssistantMessageFrame, error) {
	block, err := eventBlock(event.EventType(), event.ContentIndex, event.Partial)
	if err != nil {
		return nil, err
	}
	content, ok := block.(ThinkingContent)
	if !ok {
		return nil, wrongEventBlock(event.EventType(), block, event.ContentIndex)
	}
	if err := encoder.startBlock(event.ContentIndex, &encoderBlockState{kind: blockKindThinking, coveredChars: len(content.Thinking)}); err != nil {
		return nil, err
	}
	return ThinkingStartFrame{ContentIndex: event.ContentIndex, Content: content}, nil
}

func (encoder *AssistantMessageFrameEncoder) encodeThinkingEnd(event ThinkingEndEvent) (AssistantMessageFrame, error) {
	block, err := eventBlock(event.EventType(), event.ContentIndex, event.Partial)
	if err != nil {
		return nil, err
	}
	content, ok := block.(ThinkingContent)
	if !ok {
		return nil, wrongEventBlock(event.EventType(), block, event.ContentIndex)
	}
	if err := encoder.endBlock(event.ContentIndex, blockKindThinking); err != nil {
		return nil, err
	}
	return ThinkingEndFrame{
		ContentIndex:      event.ContentIndex,
		Content:           event.Content,
		ThinkingSignature: content.ThinkingSignature,
		Redacted:          content.Redacted,
	}, nil
}

func (encoder *AssistantMessageFrameEncoder) encodeToolCallStart(event ToolCallStartEvent) (AssistantMessageFrame, error) {
	block, err := eventBlock(event.EventType(), event.ContentIndex, event.Partial)
	if err != nil {
		return nil, err
	}
	toolCall, ok := block.(ToolCall)
	if !ok {
		return nil, wrongEventBlock(event.EventType(), block, event.ContentIndex)
	}
	snapshot, err := serializedToolArguments(toolCall.Arguments)
	if err != nil {
		return nil, err
	}
	cloned, err := cloneFrameToolCall(toolCall)
	if err != nil {
		return nil, err
	}
	state := &encoderBlockState{kind: blockKindToolCall, caughtUp: snapshot == emptyParsedToolArguments}
	if !state.caughtUp {
		state.snapshotArguments = snapshot
	}
	if err := encoder.startBlock(event.ContentIndex, state); err != nil {
		return nil, err
	}
	return ToolCallStartFrame{ContentIndex: event.ContentIndex, ToolCall: cloned}, nil
}

func (encoder *AssistantMessageFrameEncoder) encodeToolCallDelta(event ToolCallDeltaEvent) (AssistantMessageFrame, error) {
	state, err := encoder.block(event.ContentIndex, blockKindToolCall)
	if err != nil {
		return nil, err
	}
	if state.caughtUp {
		if event.Delta == "" {
			return nil, nil
		}
		return ToolCallDeltaFrame{ContentIndex: event.ContentIndex, Delta: event.Delta}, nil
	}
	state.catchupJSON += event.Delta
	arguments := parseStreamingJsonObject(state.catchupJSON)
	serialized, err := serializedToolArguments(arguments)
	if err != nil {
		return nil, err
	}
	// Legacy grammar calls include the initial input in toolcall_start, but
	// their JSON delta stream still begins at an empty input. Its parsed
	// arguments can therefore extend, rather than exactly reproduce, the start
	// snapshot.
	if serialized != state.snapshotArguments && !isJsonPrefix(map[string]any(parseStreamingJsonObject(state.snapshotArguments)), map[string]any(arguments)) {
		return nil, nil
	}
	state.caughtUp = true
	state.snapshotArguments = ""
	checkpoint := state.catchupJSON
	state.catchupJSON = ""
	if checkpoint == "" {
		return nil, nil
	}
	return ToolCallCheckpointFrame{ContentIndex: event.ContentIndex, JSON: checkpoint}, nil
}

func (encoder *AssistantMessageFrameEncoder) encodeToolCallEnd(event ToolCallEndEvent) (AssistantMessageFrame, error) {
	block, err := eventBlock(event.EventType(), event.ContentIndex, event.Partial)
	if err != nil {
		return nil, err
	}
	if _, ok := block.(ToolCall); !ok {
		return nil, wrongEventBlock(event.EventType(), block, event.ContentIndex)
	}
	arguments, err := cloneToolArguments(event.ToolCall.Arguments)
	if err != nil {
		return nil, err
	}
	if err := encoder.endBlock(event.ContentIndex, blockKindToolCall); err != nil {
		return nil, err
	}
	return ToolCallEndFrame{
		ContentIndex:     event.ContentIndex,
		ID:               event.ToolCall.ID,
		Name:             event.ToolCall.Name,
		Arguments:        arguments,
		ThoughtSignature: event.ToolCall.ThoughtSignature,
		Namespace:        event.ToolCall.Namespace,
	}, nil
}

func (encoder *AssistantMessageFrameEncoder) startBlock(contentIndex int, state *encoderBlockState) error {
	if err := assertContentIndex(contentIndex); err != nil {
		return err
	}
	if _, exists := encoder.blocks[contentIndex]; exists {
		return fmt.Errorf("Assistant message block %d starts more than once", contentIndex)
	}
	if encoder.blocks == nil {
		encoder.blocks = map[int]*encoderBlockState{}
	}
	encoder.blocks[contentIndex] = state
	return nil
}

func (encoder *AssistantMessageFrameEncoder) block(contentIndex int, kind string) (*encoderBlockState, error) {
	if err := assertContentIndex(contentIndex); err != nil {
		return nil, err
	}
	state, exists := encoder.blocks[contentIndex]
	if !exists {
		return nil, fmt.Errorf("Assistant message %s block %d has not started", kind, contentIndex)
	}
	if state.kind != kind {
		return nil, fmt.Errorf("Assistant message block %d is %s, not %s", contentIndex, state.kind, kind)
	}
	return state, nil
}

func (encoder *AssistantMessageFrameEncoder) endBlock(contentIndex int, kind string) error {
	if _, err := encoder.block(contentIndex, kind); err != nil {
		return err
	}
	delete(encoder.blocks, contentIndex)
	return nil
}

func (encoder *AssistantMessageFrameEncoder) encodeTextDelta(contentIndex int, delta, kind string) (AssistantMessageFrame, error) {
	state, err := encoder.block(contentIndex, kind)
	if err != nil {
		return nil, err
	}
	deltaStart := state.deltaChars
	state.deltaChars += len(delta)
	covered := max(0, state.coveredChars-deltaStart)
	if covered >= len(delta) {
		return nil, nil
	}
	uncovered := delta[covered:]
	if kind == blockKindText {
		return TextDeltaFrame{ContentIndex: contentIndex, Delta: uncovered}, nil
	}
	return ThinkingDeltaFrame{ContentIndex: contentIndex, Delta: uncovered}, nil
}

func cloneStartMessage(message AssistantMessage) AssistantMessage {
	return AssistantMessage{
		Content:               []AssistantContentBlock{},
		API:                   message.API,
		Provider:              message.Provider,
		Model:                 message.Model,
		ResponseModel:         message.ResponseModel,
		ResponseID:            message.ResponseID,
		ProviderThinkingLevel: message.ProviderThinkingLevel,
		Diagnostics:           cloneDiagnostics(message.Diagnostics),
		Usage:                 cloneUsage(message.Usage),
		StopReason:            StopReasonPending,
		Timestamp:             message.Timestamp,
	}
}

func cloneUsage(usage Usage) Usage {
	if usage.CacheWrite1h != nil {
		usage.CacheWrite1h = new(*usage.CacheWrite1h)
	}
	if usage.Reasoning != nil {
		usage.Reasoning = new(*usage.Reasoning)
	}
	return usage
}

func cloneFrameToolCall(toolCall ToolCall) (ToolCall, error) {
	arguments, err := cloneToolArguments(toolCall.Arguments)
	if err != nil {
		return ToolCall{}, err
	}
	toolCall.Arguments = arguments
	return toolCall, nil
}

// cloneToolArguments deep-copies tool arguments. Go's nil map is the zero value
// of Pi's empty arguments object.
func cloneToolArguments(arguments JsonObject) (JsonObject, error) {
	if arguments == nil {
		return JsonObject{}, nil
	}
	normalized, err := normalizeJSONValue(arguments)
	if err != nil {
		return nil, errors.New("Tool-call arguments are not JSON-serializable")
	}
	object, _ := normalized.(map[string]any)
	return JsonObject(object), nil
}

func assertContentIndex(contentIndex int) error {
	if contentIndex < 0 {
		return fmt.Errorf("Invalid assistant message frame contentIndex: %d", contentIndex)
	}
	return nil
}

func eventBlock(eventType AssistantEventType, contentIndex int, partial *AssistantMessage) (AssistantContentBlock, error) {
	if err := assertContentIndex(contentIndex); err != nil {
		return nil, err
	}
	if partial == nil || contentIndex >= len(partial.Content) || partial.Content[contentIndex] == nil {
		return nil, fmt.Errorf("%s event has no content block at index %d", eventType, contentIndex)
	}
	return partial.Content[contentIndex], nil
}

func wrongEventBlock(eventType AssistantEventType, block AssistantContentBlock, contentIndex int) error {
	return fmt.Errorf("%s event points to %s block at index %d", eventType, block.contentType(), contentIndex)
}

func serializedToolArguments(arguments JsonObject) (string, error) {
	if arguments == nil {
		arguments = JsonObject{}
	}
	serialized, err := json.Marshal(arguments)
	if err != nil {
		return "", errors.New("Tool-call arguments are not JSON-serializable")
	}
	return string(serialized), nil
}

var emptyParsedToolArguments = func() string {
	serialized, _ := serializedToolArguments(parseStreamingJsonObject(""))
	return serialized
}()

// isJsonPrefix reports whether current extends snapshot: strings by prefix,
// arrays element-wise with room to grow, objects key-wise.
func isJsonPrefix(snapshot, current any) bool {
	switch snapshot := snapshot.(type) {
	case string:
		text, ok := current.(string)
		return ok && strings.HasPrefix(text, snapshot)
	case []any:
		return isJsonArrayPrefix(snapshot, current)
	case map[string]any:
		return isJsonObjectPrefix(snapshot, current)
	case JsonObject:
		return isJsonObjectPrefix(snapshot, current)
	default:
		return jsonObjectIs(snapshot, current)
	}
}

func isJsonArrayPrefix(snapshot []any, current any) bool {
	values, ok := current.([]any)
	if !ok || len(snapshot) > len(values) {
		return false
	}
	for index, value := range snapshot {
		if !isJsonPrefix(value, values[index]) {
			return false
		}
	}
	return true
}

func isJsonObjectPrefix(snapshot map[string]any, current any) bool {
	var record map[string]any
	switch current := current.(type) {
	case map[string]any:
		record = current
	case JsonObject:
		record = current
	default:
		return false
	}
	for key, value := range snapshot {
		currentValue, exists := record[key]
		if !exists || !isJsonPrefix(value, currentValue) {
			return false
		}
	}
	return true
}

// jsonObjectIs mirrors Object.is for decoded JSON scalars.
func jsonObjectIs(left, right any) bool {
	leftNumber, leftIsNumber := left.(float64)
	rightNumber, rightIsNumber := right.(float64)
	if leftIsNumber && rightIsNumber {
		return leftNumber == rightNumber && math.Signbit(leftNumber) == math.Signbit(rightNumber)
	}
	return left == right
}

// ReduceAssistantMessageFrames replays compact frames without mutating them.
// It returns a nil message when the frames contain no start frame.
func ReduceAssistantMessageFrames(frames []AssistantMessageFrame) (*AssistantMessage, error) {
	reducer := assistantMessageFrameReducer{states: map[int]*reducerBlockState{}}
	for _, frame := range frames {
		if err := reducer.apply(frame); err != nil {
			return nil, err
		}
	}
	return reducer.finish()
}

type assistantMessageFrameReducer struct {
	message          *AssistantMessage
	frameBeforeStart AssistantMessageFrameType
	states           map[int]*reducerBlockState
}

func (reducer *assistantMessageFrameReducer) apply(frame AssistantMessageFrame) error {
	if start, ok := frame.(StartFrame); ok {
		return reducer.start(start)
	}
	if reducer.message == nil {
		if reducer.frameBeforeStart == "" {
			reducer.frameBeforeStart = frame.FrameType()
		}
		return nil
	}
	switch frame := frame.(type) {
	case TextStartFrame:
		return reducer.appendBlock(frame.ContentIndex, frame.Content, blockKindText)
	case ThinkingStartFrame:
		return reducer.appendBlock(frame.ContentIndex, frame.Content, blockKindThinking)
	case ToolCallStartFrame:
		return reducer.appendToolCall(frame)
	case TextDeltaFrame, TextEndFrame, ThinkingDeltaFrame, ThinkingEndFrame:
		return reducer.applyTextFrame(frame)
	case ToolCallCheckpointFrame, ToolCallDeltaFrame, ToolCallEndFrame:
		return reducer.applyToolCallFrame(frame)
	default:
		return fmt.Errorf("Unsupported assistant message frame %s", frame.FrameType())
	}
}

func (reducer *assistantMessageFrameReducer) start(frame StartFrame) error {
	if reducer.message != nil {
		return errors.New("Assistant message frame sequence contains more than one start frame")
	}
	if reducer.frameBeforeStart != "" {
		return fmt.Errorf("%s frame appears before the start frame", reducer.frameBeforeStart)
	}
	message := frame.Partial.cloneMessage().(AssistantMessage)
	message.Usage = cloneUsage(message.Usage)
	if message.Content == nil {
		message.Content = []AssistantContentBlock{}
	}
	reducer.message = &message
	return nil
}

func (reducer *assistantMessageFrameReducer) appendToolCall(frame ToolCallStartFrame) error {
	toolCall, err := cloneFrameToolCall(frame.ToolCall)
	if err != nil {
		return err
	}
	return reducer.appendBlock(frame.ContentIndex, toolCall, blockKindToolCall)
}

func (reducer *assistantMessageFrameReducer) appendBlock(contentIndex int, block AssistantContentBlock, kind string) error {
	if err := assertContentIndex(contentIndex); err != nil {
		return err
	}
	if length := len(reducer.message.Content); contentIndex != length {
		reason := "would leave a gap"
		if contentIndex < length {
			reason = "already exists"
		}
		return fmt.Errorf("Cannot start assistant message block at index %d: %s", contentIndex, reason)
	}
	reducer.message.Content = append(reducer.message.Content, block)
	reducer.states[contentIndex] = &reducerBlockState{kind: kind}
	return nil
}

func (reducer *assistantMessageFrameReducer) activeBlock(contentIndex int, kind string, frameType AssistantMessageFrameType) (AssistantContentBlock, *reducerBlockState, error) {
	if err := assertContentIndex(contentIndex); err != nil {
		return nil, nil, err
	}
	state, exists := reducer.states[contentIndex]
	if !exists || contentIndex >= len(reducer.message.Content) {
		return nil, nil, fmt.Errorf("%s frame has no started block at index %d", frameType, contentIndex)
	}
	block := reducer.message.Content[contentIndex]
	if state.kind != kind || block.contentType() != kind {
		return nil, nil, fmt.Errorf("%s frame expected %s block at index %d, found %s", frameType, kind, contentIndex, block.contentType())
	}
	if state.ended {
		return nil, nil, fmt.Errorf("%s frame follows the end of block at index %d", frameType, contentIndex)
	}
	return block, state, nil
}

func (reducer *assistantMessageFrameReducer) applyTextFrame(frame AssistantMessageFrame) error {
	switch frame := frame.(type) {
	case TextDeltaFrame:
		return reducer.updateText(frame.ContentIndex, frame.FrameType(), false, func(block *TextContent) {
			block.Text += frame.Delta
		})
	case TextEndFrame:
		return reducer.updateText(frame.ContentIndex, frame.FrameType(), true, func(block *TextContent) {
			block.Text = frame.Content
			block.TextSignature = frame.TextSignature
		})
	case ThinkingDeltaFrame:
		return reducer.updateThinking(frame.ContentIndex, frame.FrameType(), false, func(block *ThinkingContent) {
			block.Thinking += frame.Delta
		})
	case ThinkingEndFrame:
		return reducer.updateThinking(frame.ContentIndex, frame.FrameType(), true, func(block *ThinkingContent) {
			block.Thinking = frame.Content
			block.ThinkingSignature = frame.ThinkingSignature
			block.Redacted = frame.Redacted
		})
	default:
		return fmt.Errorf("Unsupported assistant message frame %s", frame.FrameType())
	}
}

func (reducer *assistantMessageFrameReducer) updateText(contentIndex int, frameType AssistantMessageFrameType, end bool, update func(*TextContent)) error {
	block, state, err := reducer.activeBlock(contentIndex, blockKindText, frameType)
	if err != nil {
		return err
	}
	text := block.(TextContent)
	update(&text)
	reducer.message.Content[contentIndex] = text
	state.ended = end
	return nil
}

func (reducer *assistantMessageFrameReducer) updateThinking(contentIndex int, frameType AssistantMessageFrameType, end bool, update func(*ThinkingContent)) error {
	block, state, err := reducer.activeBlock(contentIndex, blockKindThinking, frameType)
	if err != nil {
		return err
	}
	thinking := block.(ThinkingContent)
	update(&thinking)
	reducer.message.Content[contentIndex] = thinking
	state.ended = end
	return nil
}

func (reducer *assistantMessageFrameReducer) applyToolCallFrame(frame AssistantMessageFrame) error {
	switch frame := frame.(type) {
	case ToolCallCheckpointFrame:
		return reducer.updateToolCall(frame.ContentIndex, frame.FrameType(), func(toolCall *ToolCall, state *reducerBlockState) error {
			state.json = frame.JSON
			toolCall.Arguments = parseStreamingJsonObject(frame.JSON)
			return nil
		})
	case ToolCallDeltaFrame:
		return reducer.updateToolCall(frame.ContentIndex, frame.FrameType(), func(_ *ToolCall, state *reducerBlockState) error {
			state.json += frame.Delta
			return nil
		})
	case ToolCallEndFrame:
		return reducer.updateToolCall(frame.ContentIndex, frame.FrameType(), func(toolCall *ToolCall, state *reducerBlockState) error {
			return endToolCall(toolCall, state, frame)
		})
	default:
		return fmt.Errorf("Unsupported assistant message frame %s", frame.FrameType())
	}
}

func endToolCall(toolCall *ToolCall, state *reducerBlockState, frame ToolCallEndFrame) error {
	arguments, err := cloneToolArguments(frame.Arguments)
	if err != nil {
		return err
	}
	toolCall.ID = frame.ID
	toolCall.Name = frame.Name
	toolCall.Arguments = arguments
	toolCall.ThoughtSignature = frame.ThoughtSignature
	toolCall.Namespace = frame.Namespace
	state.ended = true
	return nil
}

func (reducer *assistantMessageFrameReducer) updateToolCall(contentIndex int, frameType AssistantMessageFrameType, update func(*ToolCall, *reducerBlockState) error) error {
	block, state, err := reducer.activeBlock(contentIndex, blockKindToolCall, frameType)
	if err != nil {
		return err
	}
	toolCall := block.(ToolCall)
	if err := update(&toolCall, state); err != nil {
		return err
	}
	reducer.message.Content[contentIndex] = toolCall
	return nil
}

func (reducer *assistantMessageFrameReducer) finish() (*AssistantMessage, error) {
	if reducer.message == nil {
		return nil, nil
	}
	for contentIndex, state := range reducer.states {
		if state.kind != blockKindToolCall || state.ended || state.json == "" {
			continue
		}
		toolCall := reducer.message.Content[contentIndex].(ToolCall)
		toolCall.Arguments = parseStreamingJsonObject(state.json)
		reducer.message.Content[contentIndex] = toolCall
	}
	return reducer.message, nil
}
