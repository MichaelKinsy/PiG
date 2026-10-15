// SPDX-License-Identifier: MIT

package boundarywire

import (
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Encodings keeps, from one boundary dispatch of a session to the next, the encodings of the messages and entry records of the session's latest preview, so a dispatch encodes only what is new to its preview. A preview's messages are the session's own objects (Cache.Shared), which nothing changes, so an object's encoding stays its encoding. Each pass keeps only what it used. A key holds the objects it names, so it names no other while kept.
type Encodings struct {
	mu       sync.Mutex
	pass     uint64
	messages map[MessageKey]keptMessage
	records  map[recordKey]keptRecord
}

// MessageKey names a message by the objects it holds. A custom message has no key: it is decoded afresh for each projection.
type MessageKey struct {
	system     *ai.SystemMessage
	user       *agent.UserMessage
	assistant  *agent.AssistantMessage
	toolResult *agent.ToolResultMessage
}

// KeyOf returns the key of message, or false for a message without one.
func KeyOf(message extension.AgentMessage) (MessageKey, bool) {
	if message.Custom != nil {
		return MessageKey{}, false
	}
	key := MessageKey{message.System, message.User, message.Assistant, message.ToolResult}
	return key, key != MessageKey{}
}

// MessageEncoding is a message's encoding as a contextMessages element and, when convertToLlm sends it, its llmMessages element.
type MessageEncoding struct {
	Message []byte
	LLM     []byte
	HasLLM  bool
}

type keptMessage struct {
	encoding MessageEncoding
	pass     uint64
}

// recordKey names an entry record by its bytes, which the session never changes.
type recordKey struct {
	start *byte
	size  int
}

type keptRecord struct {
	encoding []byte
	pass     uint64
}

// Pass is one encoding of a preview. Its methods are called only inside Encodings.Use.
type Pass struct {
	encodings *Encodings
}

// Use runs encode as one pass and then forgets every encoding the pass did not use. Passes do not overlap.
func (e *Encodings) Use(encode func(*Pass) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pass++
	err := encode(&Pass{encodings: e})
	for key, kept := range e.messages {
		if kept.pass != e.pass {
			delete(e.messages, key)
		}
	}
	for key, kept := range e.records {
		if kept.pass != e.pass {
			delete(e.records, key)
		}
	}
	return err
}

// Message returns the kept encoding of the message key names.
func (p *Pass) Message(key MessageKey) (MessageEncoding, bool) {
	e := p.encodings
	kept, ok := e.messages[key]
	if ok && kept.pass != e.pass {
		kept.pass = e.pass
		e.messages[key] = kept
	}
	return kept.encoding, ok
}

// KeepMessage keeps encoding for the message key names.
func (p *Pass) KeepMessage(key MessageKey, encoding MessageEncoding) {
	e := p.encodings
	if e.messages == nil {
		e.messages = make(map[MessageKey]keptMessage)
	}
	e.messages[key] = keptMessage{encoding, e.pass}
}

// Record returns the kept encoding of the entry record record.
func (p *Pass) Record(record []byte) ([]byte, bool) {
	if len(record) == 0 {
		return nil, false
	}
	e := p.encodings
	key := recordKey{&record[0], len(record)}
	kept, ok := e.records[key]
	if ok && kept.pass != e.pass {
		kept.pass = e.pass
		e.records[key] = kept
	}
	return kept.encoding, ok
}

// KeepRecord keeps encoding for the entry record record.
func (p *Pass) KeepRecord(record []byte, encoding []byte) {
	if len(record) == 0 {
		return
	}
	e := p.encodings
	if e.records == nil {
		e.records = make(map[recordKey]keptRecord)
	}
	e.records[recordKey{&record[0], len(record)}] = keptRecord{encoding, e.pass}
}
