package subprocess

import (
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/boundarywire"
)

// boundaryEventWire returns a turn_end or agent_before_settle event in a form that encodes to the same JSON as the event, with the projected part of its context preview encoded once per dispatch (boundarywire). Other events, and events of a dispatch without a cache, are returned unchanged.
func boundaryEventWire(event any, cache *boundarywire.Cache) any {
	if cache == nil {
		return event
	}
	switch e := event.(type) {
	case extension.TurnEndEvent:
		if e.BoundaryState == nil {
			return event
		}
		// The turn's base members come first, then the boundary's (extension.TurnEndEvent.MarshalJSON).
		return turnEndWire{Type: e.Type, TurnIndex: e.TurnIndex, Message: e.Message, ToolResults: e.ToolResults, MessageEntryID: e.MessageEntryID, ToolResultEntryIds: e.ToolResultEntryIds, Outcome: e.Outcome, Entries: e.Entries, Continue: e.Continue, Context: boundaryContextWire{cache, e.Context}}
	case *extension.AgentBeforeSettleEvent:
		if e == nil {
			return event
		}
		return agentBeforeSettleWire{Type: e.Type, Outcome: e.Outcome, Entries: e.Entries, Continue: e.Continue, Context: boundaryContextWire{cache, e.Context}}
	}
	return event
}

// turnEndWire has extension.TurnEndEvent's members in its encoding order.
type turnEndWire struct {
	Type               string                           `json:"type"`
	TurnIndex          int                              `json:"turnIndex"`
	Message            extension.AgentMessage           `json:"message"`
	ToolResults        []extension.ToolResultMessage    `json:"toolResults"`
	MessageEntryID     string                           `json:"messageEntryId"`
	ToolResultEntryIds []string                         `json:"toolResultEntryIds"`
	Outcome            extension.AgentActivityOutcome   `json:"outcome"`
	Entries            []extension.SessionBoundaryDraft `json:"entries"`
	Continue           bool                             `json:"continue"`
	Context            boundaryContextWire              `json:"context"`
}

// agentBeforeSettleWire has extension.AgentBeforeSettleEvent's members in its encoding order.
type agentBeforeSettleWire struct {
	Type     string                           `json:"type"`
	Outcome  extension.AgentActivityOutcome   `json:"outcome"`
	Entries  []extension.SessionBoundaryDraft `json:"entries"`
	Continue bool                             `json:"continue"`
	Context  boundaryContextWire              `json:"context"`
}

// boundaryContextWire encodes an extension.BoundaryContextPreview. Its projected part (contextEntries, contextMessages, llmMessages) comes from the dispatch's cache when an earlier preview from the same inputs was encoded.
type boundaryContextWire struct {
	cache   *boundarywire.Cache
	preview extension.BoundaryContextPreview
}

// boundaryProjectedWire is the projected part of extension.BoundaryContextPreview, in its order. E is extension.ProjectedSessionEntry or projectedEntryWire.
type boundaryProjectedWire[E any] struct {
	ContextEntries  []E                      `json:"contextEntries"`
	ContextMessages []extension.AgentMessage `json:"contextMessages"`
	LLMMessages     []ai.Message             `json:"llmMessages"`
}

// projectedEntryWire is extension.ProjectedSessionEntry with its source as the entry's record (boundarywire.Cache.Sources): Pi's handler receives the session's entry object, whose members are in the record's order.
type projectedEntryWire struct {
	SourceEntry json.RawMessage          `json:"sourceEntry"`
	Messages    []extension.AgentMessage `json:"messages"`
}

// boundaryTailWire is the rest of extension.BoundaryContextPreview, in its order.
type boundaryTailWire struct {
	PendingMessages []extension.AgentMessage `json:"pendingMessages"`
	CanContinue     bool                     `json:"canContinue"`
}

func (w boundaryContextWire) MarshalJSON() ([]byte, error) { return w.appendJSON(nil) }

// appendJSON appends the preview's encoding to dst.
func (w boundaryContextWire) appendJSON(dst []byte) ([]byte, error) {
	projected, ok := w.cache.Encoded(w.preview)
	if !ok {
		var encoded []byte
		var err error
		if encodings, sources, kept := w.cache.Kept(w.preview); kept {
			encoded, err = encodeKeptProjection(encodings, sources, w.preview)
		} else if sources, recorded := w.cache.Sources(w.preview); recorded {
			entries := make([]projectedEntryWire, len(sources))
			for i, source := range sources {
				entries[i] = projectedEntryWire{source, w.preview.ContextEntries[i].Messages}
			}
			encoded, err = json.Marshal(boundaryProjectedWire[projectedEntryWire]{entries, w.preview.ContextMessages, w.preview.LLMMessages})
		} else {
			encoded, err = json.Marshal(boundaryProjectedWire[extension.ProjectedSessionEntry]{w.preview.ContextEntries, w.preview.ContextMessages, w.preview.LLMMessages})
		}
		if err != nil {
			return nil, err
		}
		projected = encoded
		w.cache.Store(w.preview, projected)
	}
	tail, err := json.Marshal(boundaryTailWire{w.preview.PendingMessages, w.preview.CanContinue})
	if err != nil {
		return nil, err
	}
	// Both halves are objects: join {projected...} and {tail...} into one.
	dst = slices.Grow(dst, len(projected)+len(tail))
	dst = append(dst, projected[:len(projected)-1]...)
	dst = append(dst, ',')
	return append(dst, tail[1:]...), nil
}

// marshalEventWire returns json.Marshal(wire). A boundary event's context, the part that grows with the session, is appended as it is encoded instead of passing through the encoder again: it is json.Marshal output already, compact and escaped, so the encoder's compaction of a Marshaler's output would copy it unchanged.
func marshalEventWire(wire any) ([]byte, error) {
	// The members before context, in the order the event has them in Pi (the base event, then outcome, entries and continue); context is last.
	var head any
	var context boundaryContextWire
	switch w := wire.(type) {
	case turnEndWire:
		head = struct {
			Type               string                           `json:"type"`
			TurnIndex          int                              `json:"turnIndex"`
			Message            extension.AgentMessage           `json:"message"`
			ToolResults        []extension.ToolResultMessage    `json:"toolResults"`
			MessageEntryID     string                           `json:"messageEntryId"`
			ToolResultEntryIds []string                         `json:"toolResultEntryIds"`
			Outcome            extension.AgentActivityOutcome   `json:"outcome"`
			Entries            []extension.SessionBoundaryDraft `json:"entries"`
			Continue           bool                             `json:"continue"`
		}{w.Type, w.TurnIndex, w.Message, w.ToolResults, w.MessageEntryID, w.ToolResultEntryIds, w.Outcome, w.Entries, w.Continue}
		context = w.Context
	case agentBeforeSettleWire:
		head = struct {
			Type     string                           `json:"type"`
			Outcome  extension.AgentActivityOutcome   `json:"outcome"`
			Entries  []extension.SessionBoundaryDraft `json:"entries"`
			Continue bool                             `json:"continue"`
		}{w.Type, w.Outcome, w.Entries, w.Continue}
		context = w.Context
	case extension.ContextEvent:
		return marshalContextWire(w.Type, w.Messages)
	case extension.ContextWithSystemEvent:
		return marshalContextWire(w.Type, w.Messages)
	default:
		return json.Marshal(wire)
	}
	headJSON, err := json.Marshal(head)
	if err != nil {
		return nil, err
	}
	// {head..., "context": context}
	out := headJSON[:len(headJSON)-1]
	out = append(out, `,"context":`...)
	if out, err = context.appendJSON(out); err != nil {
		return nil, err
	}
	return append(out, '}'), nil
}

// encodeKeptProjection encodes the projected part of preview as json.Marshal encodes boundaryProjectedWire[projectedEntryWire], from the encodings of its messages and records kept from the session's previous dispatch: only what is new to the history is encoded.
func encodeKeptProjection(encodings *boundarywire.Encodings, sources []json.RawMessage, preview extension.BoundaryContextPreview) ([]byte, error) {
	var out []byte
	err := encodings.Use(func(pass *boundarywire.Pass) error {
		messages := make([]boundarywire.MessageEncoding, len(preview.ContextMessages))
		// size bounds the encoding: each message is written in its entry and in contextMessages, each record with its entry's members.
		size := len(`{"contextEntries":null,"contextMessages":null,"llmMessages":null}`)
		llmCount := 0
		for i, message := range preview.ContextMessages {
			encoding, err := keptMessageEncoding(pass, message)
			if err != nil {
				return err
			}
			messages[i] = encoding
			size += 2*len(encoding.Message) + len(encoding.LLM) + len(",,,")
			if encoding.HasLLM {
				llmCount++
			}
		}
		records := make([][]byte, len(sources))
		for i, source := range sources {
			record, ok := pass.Record(source)
			if !ok {
				var err error
				if record, err = json.Marshal(source); err != nil {
					return err
				}
				pass.KeepRecord(source, record)
			}
			records[i] = record
			size += len(record) + len(`{"sourceEntry":,"messages":[]},`)
		}
		// llmMessages is convertToLlm of contextMessages; a preview whose llmMessages are not is encoded whole.
		if llmCount != len(preview.LLMMessages) {
			var err error
			out, err = json.Marshal(boundaryProjectedWire[projectedEntryWire]{projectedEntries(sources, preview), preview.ContextMessages, preview.LLMMessages})
			return err
		}
		out = make([]byte, 0, size)
		out = append(out, `{"contextEntries":`...)
		if preview.ContextEntries == nil {
			out = append(out, "null"...)
		} else {
			out = append(out, '[')
			next := 0
			for i, entry := range preview.ContextEntries {
				if i > 0 {
					out = append(out, ',')
				}
				out = append(out, `{"sourceEntry":`...)
				out = append(out, records[i]...)
				out = append(out, `,"messages":`...)
				if entry.Messages == nil {
					out = append(out, "null"...)
				} else {
					out = append(out, '[')
					for j, message := range entry.Messages {
						if j > 0 {
							out = append(out, ',')
						}
						// An entry's messages are the context messages in order (session-manager.ts buildSessionProjection).
						if next < len(messages) && sameKeyedMessage(message, preview.ContextMessages[next]) {
							out = append(out, messages[next].Message...)
						} else {
							encoded, err := marshalAgentMessage(message)
							if err != nil {
								return err
							}
							out = append(out, encoded...)
						}
						next++
					}
					out = append(out, ']')
				}
				out = append(out, '}')
			}
			out = append(out, ']')
		}
		out = append(out, `,"contextMessages":`...)
		if preview.ContextMessages == nil {
			out = append(out, "null"...)
		} else {
			out = append(out, '[')
			for i, encoding := range messages {
				if i > 0 {
					out = append(out, ',')
				}
				out = append(out, encoding.Message...)
			}
			out = append(out, ']')
		}
		out = append(out, `,"llmMessages":`...)
		if preview.LLMMessages == nil {
			out = append(out, "null"...)
		} else {
			out = append(out, '[')
			first := true
			for _, encoding := range messages {
				if !encoding.HasLLM {
					continue
				}
				if !first {
					out = append(out, ',')
				}
				first = false
				out = append(out, encoding.LLM...)
			}
			out = append(out, ']')
		}
		out = append(out, '}')
		return nil
	})
	return out, err
}

// keptMessageEncoding returns message's encodings, kept from the previous pass when message has a key.
func keptMessageEncoding(pass *boundarywire.Pass, message extension.AgentMessage) (boundarywire.MessageEncoding, error) {
	key, keyed := boundarywire.KeyOf(message)
	if keyed {
		if encoding, ok := pass.Message(key); ok {
			return encoding, nil
		}
	}
	var encoding boundarywire.MessageEncoding
	var err error
	if encoding.Message, err = marshalAgentMessage(message); err != nil {
		return encoding, err
	}
	if llm := agent.ConvertToLLM([]extension.AgentMessage{message}); len(llm) == 1 {
		if encoding.LLM, err = json.Marshal(llm[0]); err != nil {
			return encoding, err
		}
		encoding.HasLLM = true
	}
	if keyed {
		pass.KeepMessage(key, encoding)
	}
	return encoding, nil
}

// marshalAgentMessage encodes message as an element of a []extension.AgentMessage encodes. It takes the address of its own copy, so a caller's variable does not move to the heap.
func marshalAgentMessage(message extension.AgentMessage) ([]byte, error) {
	return json.Marshal(&message)
}

// sameKeyedMessage reports whether a and b hold the same objects; a custom message is never the same as another, so its encoding is not reused.
func sameKeyedMessage(a, b extension.AgentMessage) bool {
	key, ok := boundarywire.KeyOf(a)
	if !ok {
		return false
	}
	other, ok := boundarywire.KeyOf(b)
	return ok && key == other
}

// projectedEntries pairs each context entry's messages with its record.
func projectedEntries(sources []json.RawMessage, preview extension.BoundaryContextPreview) []projectedEntryWire {
	if preview.ContextEntries == nil {
		return nil
	}
	entries := make([]projectedEntryWire, len(sources))
	for i, source := range sources {
		entries[i] = projectedEntryWire{source, preview.ContextEntries[i].Messages}
	}
	return entries
}

// marshalContextWire returns json.Marshal of a context event: {"type":...,"messages":[...]}. Each message is encoded as an element of the list is and the list is joined from them, so the encoder does not pass the whole conversation through its compaction of a Marshaler's output once more, a cost that grows with the history on every model request.
func marshalContextWire(eventType string, messages []extension.AgentMessage) ([]byte, error) {
	if messages == nil {
		return json.Marshal(struct {
			Type     string                   `json:"type"`
			Messages []extension.AgentMessage `json:"messages"`
		}{eventType, nil})
	}
	typeJSON, err := json.Marshal(eventType)
	if err != nil {
		return nil, err
	}
	encoded := make([][]byte, len(messages))
	size := len(`{"type":,"messages":[]}`) + len(typeJSON) + len(messages)
	for i, message := range messages {
		if encoded[i], err = marshalAgentMessage(message); err != nil {
			return nil, err
		}
		size += len(encoded[i])
	}
	out := make([]byte, 0, size)
	out = append(out, `{"type":`...)
	out = append(out, typeJSON...)
	out = append(out, `,"messages":[`...)
	for i, message := range encoded {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, message...)
	}
	return append(out, ']', '}'), nil
}
