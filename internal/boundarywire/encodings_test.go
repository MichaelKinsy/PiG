// SPDX-License-Identifier: MIT

package boundarywire

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func keyedMessages(n int) []extension.AgentMessage {
	messages := make([]extension.AgentMessage, n)
	for i := range messages {
		messages[i] = extension.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(fmt.Sprintf("prompt %d", i)), Timestamp: int64(i)}}
	}
	return messages
}

// pass is one pass over messages that keeps an encoding for each message and one for each record, and reports which of them it found kept.
func pass(t *testing.T, e *Encodings, messages []extension.AgentMessage, records [][]byte) (messagesKept, recordsKept int) {
	t.Helper()
	err := e.Use(func(p *Pass) error {
		for _, message := range messages {
			key, ok := KeyOf(message)
			if !ok {
				t.Fatal("test message has no key")
			}
			if _, kept := p.Message(key); kept {
				messagesKept++
			} else {
				p.KeepMessage(key, MessageEncoding{Message: []byte(`{}`)})
			}
		}
		for _, record := range records {
			if _, kept := p.Record(record); kept {
				recordsKept++
			} else {
				p.KeepRecord(record, []byte(`{}`))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return messagesKept, recordsKept
}

// Retention is bounded by the latest pass: what a pass did not use is forgotten at that pass, so a conversation that shrinks (compaction, a fork, a shorter branch) does not keep the encodings of the messages it lost, and a pass that uses nothing keeps nothing.
func TestEncodingsForgetWhatTheLatestPassDidNotUse(t *testing.T) {
	var e Encodings
	messages := keyedMessages(9)
	records := [][]byte{[]byte(`{"id":"a"}`), []byte(`{"id":"b"}`), []byte(`{"id":"c"}`), []byte(`{"id":"d"}`)}
	pass(t, &e, messages, records)
	if len(e.messages) != 9 || len(e.records) != 4 {
		t.Fatalf("after a pass of 9 messages and 4 records, %d messages and %d records are kept", len(e.messages), len(e.records))
	}

	messagesKept, recordsKept := pass(t, &e, messages[:3], records[:1])
	if messagesKept != 3 || recordsKept != 1 {
		t.Fatalf("a pass of the first 3 messages and 1 record found %d and %d kept; want 3 and 1", messagesKept, recordsKept)
	}
	if len(e.messages) != 3 || len(e.records) != 1 {
		t.Fatalf("after a pass of 3 messages and 1 record, %d messages and %d records are kept; entries the pass did not use must be forgotten", len(e.messages), len(e.records))
	}

	// The messages the shrunk pass forgot are new again, not served from before.
	if messagesKept, _ := pass(t, &e, messages[3:6], nil); messagesKept != 0 {
		t.Fatalf("a pass of messages a shorter pass forgot found %d kept; want 0", messagesKept)
	}
	if len(e.messages) != 3 || len(e.records) != 0 {
		t.Fatalf("after a pass of other 3 messages and no record, %d messages and %d records are kept; want 3 and 0", len(e.messages), len(e.records))
	}

	pass(t, &e, nil, nil)
	if len(e.messages) != 0 || len(e.records) != 0 {
		t.Fatalf("after a pass that used nothing, %d messages and %d records are kept; want none", len(e.messages), len(e.records))
	}
}

// A message edited in place keeps its address, which is its key, so its kept encoding is no longer its encoding. agent.ReplaceMessageInPlace (the message_end replacement) is the one writer of a message the session holds; a pass after it must encode every message again, and the entry records, which the session never changes, stay.
func TestEncodingsDropMessageEncodingsAfterAnInPlaceEdit(t *testing.T) {
	var e Encodings
	messages := keyedMessages(4)
	records := [][]byte{[]byte(`{"id":"a"}`)}
	pass(t, &e, messages, records)
	if messagesKept, recordsKept := pass(t, &e, messages, records); messagesKept != 4 || recordsKept != 1 {
		t.Fatalf("an unchanged conversation found %d messages and %d records kept; want 4 and 1", messagesKept, recordsKept)
	}

	agent.ReplaceMessageInPlace(messages[1], extension.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("edited"), Timestamp: 1}})

	messagesKept, recordsKept := pass(t, &e, messages, records)
	if messagesKept != 0 {
		t.Fatalf("a pass after agent.ReplaceMessageInPlace found %d message encodings kept; want 0: an edited message would be served stale", messagesKept)
	}
	if recordsKept != 1 {
		t.Fatalf("a pass after agent.ReplaceMessageInPlace found %d record encodings kept; want 1: no edit reaches a record", recordsKept)
	}
	if messagesKept, _ := pass(t, &e, messages, records); messagesKept != 4 {
		t.Fatalf("the pass after that found %d messages kept; want 4: one edit drops the encodings once", messagesKept)
	}
}
