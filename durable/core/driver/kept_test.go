package driver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// The delta form keeps each request's message list (a whole decoded conversation). A run of model calls on a long history
// must not retain one list per call: the retained messages stay within the budget, the newest list stays extendable, and an
// evicted one is a protocol failure (W1-1000 grew 18 MB per measured turn on the native build before the budget).
func TestKeptMessageListsAreBoundedByMessages(t *testing.T) {
	m := NewModels(nil, func() float64 { return 0 }, nil)
	const perList = 5000
	var sb strings.Builder
	sb.WriteString(`{"messages":[`)
	for i := range perList {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"role":"user","content":"hello","timestamp":1}`)
	}
	sb.WriteString(`]}`)
	full := payload.ModelContextEffect{Context: json.RawMessage(sb.String())}
	for id := uint32(1); id <= 10; id++ {
		if _, err := m.messages(id, full, nil); err != nil {
			t.Fatal(err)
		}
		if m.total > maxKeptMessages && len(m.order) > 1 {
			t.Fatalf("after %d calls %d messages are kept in %d lists, budget %d", id, m.total, len(m.order), maxKeptMessages)
		}
	}
	if len(m.order) != maxKeptMessages/perList || m.order[len(m.order)-1] != 10 {
		t.Fatalf("kept lists %v, want the newest %d", m.order, maxKeptMessages/perList)
	}
	one := uint32(1)
	if _, err := m.messages(11, payload.ModelContextEffect{Extends: &one}, nil); err == nil {
		t.Fatal("extending an evicted list succeeded")
	}
	newest := uint32(10)
	list, err := m.messages(12, payload.ModelContextEffect{Extends: &newest, Append: json.RawMessage(`[{"role":"user","content":"more","timestamp":2}]`)}, nil)
	if err != nil || len(list) != perList+1 {
		t.Fatalf("extending the newest list: %d messages, %v", len(list), err)
	}
	if m.total != 2*perList+perList+1 {
		t.Fatalf("total %d, want the kept lists' messages %d", m.total, 3*perList+1)
	}
}

// ABI section 6 lets the host drop a kept list only once a later effect extends it or the conversation unloads: with conversation
// IDs the budget drops superseded lists and never the newest list of a conversation, so an idle conversation can still be extended.
func TestKeptNewestListOfEachConversationSurvivesTheBudget(t *testing.T) {
	m := NewModels(nil, func() float64 { return 0 }, nil)
	const perList = 9000
	var sb strings.Builder
	sb.WriteString(`{"messages":[`)
	for i := range perList {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"role":"user","content":"hello","timestamp":1}`)
	}
	sb.WriteString(`]}`)
	request := func(conversation int64) payload.ModelContextEffect {
		return payload.ModelContextEffect{ConversationID: &conversation, Context: json.RawMessage(sb.String())}
	}
	// In this order: a map literal would range in a random one, and the budget drops the oldest superseded list.
	for _, call := range []struct {
		id           uint32
		conversation int64
	}{{1, 1}, {2, 2}, {3, 1}} {
		if _, err := m.messages(call.id, request(call.conversation), nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := m.kept[1]; ok {
		t.Fatal("conversation 1's superseded list was kept past the budget")
	}
	if _, ok := m.kept[2]; !ok || m.total != 2*perList {
		t.Fatalf("conversation 2's newest list was dropped (kept %v, %d messages): an idle conversation could no longer be extended", m.order, m.total)
	}
	two := uint32(2)
	list, err := m.messages(4, payload.ModelContextEffect{ConversationID: new(int64(2)), Extends: &two, Append: json.RawMessage(`[{"role":"user","content":"more","timestamp":2}]`)}, nil)
	if err != nil || len(list) != perList+1 {
		t.Fatalf("extending the idle conversation's list: %d messages, %v", len(list), err)
	}
}

// A conversation's next request decodes only the messages whose stored bytes changed: the messages before the first change
// are the ones the previous request decoded, the rest are decoded again, and another conversation has its own list.
func TestRangeMessagesDecodeOnlyWhatChangedSinceTheConversationsLastRequest(t *testing.T) {
	user := func(text string) string { return `{"role":"user","content":"` + text + `","timestamp":1}` }
	rng := func(conv int64, stored []bool, texts ...string) *sqlhost.MessageRange {
		r := &sqlhost.MessageRange{Conv: conv, Idents: make([]history.MessageIdent, len(texts)), Stored: stored}
		for _, text := range texts {
			r.Msgs = append(r.Msgs, []byte(user(text)))
		}
		return r
	}
	text := func(m ai.Message) string { return string(m.(ai.UserMessage).Content.(ai.UserText)) }
	m := NewModels(nil, func() float64 { return 0 }, nil)
	if _, err := m.rangeMessages(1, rng(1, []bool{true, true}, "a", "b")); err != nil {
		t.Fatal(err)
	}
	// The bytes of the messages with an identity the conversation's last request had are not read: a second request that
	// carries other bytes for them still yields what was decoded first. Only the new message is decoded.
	grown, err := m.rangeMessages(2, rng(1, []bool{true, true, true}, "X", "Y", "c"))
	if err != nil {
		t.Fatal(err)
	}
	if len(grown) != 3 || text(grown[0]) != "a" || text(grown[1]) != "b" || text(grown[2]) != "c" {
		t.Fatalf("grown list %v", grown)
	}
	// A synthesized message (no stored bytes) is decoded each time, and so is everything after it.
	again, err := m.rangeMessages(3, rng(1, []bool{true, false, true}, "Q", "x", "z"))
	if err != nil {
		t.Fatal(err)
	}
	if text(again[0]) != "a" || text(again[1]) != "x" || text(again[2]) != "z" {
		t.Fatalf("list after a synthesized message %v", again)
	}
	other, err := m.rangeMessages(4, rng(2, []bool{true}, "o"))
	if err != nil || text(other[0]) != "o" {
		t.Fatalf("another conversation: %v %v", other, err)
	}
	if _, err := m.rangeMessages(5, rng(1, []bool{true}, "a")); err != nil {
		t.Fatal(err)
	}
	if m.rangeTotal != 2 {
		t.Fatalf("%d messages held, want the last list of each conversation (1 + 1)", m.rangeTotal)
	}
}
