// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// maxKeptLists and maxKeptMessages bound the message lists kept for the delta form of model_context. A list is the whole
// decoded conversation (about 4.5 KB a message: a 1000-turn history is 18 MB), and the core extends only an earlier effect of
// the conversation it is running, so the count alone let a long run retain a full history per model call. The newest list is
// always kept; older ones go first once the retained messages pass the budget (about 75 MB). A request that names its
// conversation protects that conversation's newest list: ABI section 6 lets the host drop a list only once a later effect
// extends it or the conversation unloads, so the budget skips it and drops the oldest list that is not the newest of its
// conversation. Requests without a conversation ID (the core does not send one yet) keep the older rule.
const (
	maxKeptLists    = 1024
	maxKeptMessages = 16384
)

// Models runs model_context and deferred effects over a durable.Models.
type Models struct {
	// monotonic is the clock durations are measured on (performance.now in pi-ai); it defaults to now.
	monotonic func() float64
	models    func() durable.Models
	report    func(error)
	now       func() float64

	mu     sync.Mutex
	kept   map[uint32][]ai.Message
	order  []uint32
	owner  map[uint32]int64 // the conversation of a kept list that names one
	newest map[int64]uint32 // each conversation's newest kept list
	total  int              // messages across kept

	ranges     map[int64]*decodedRange // the messages each conversation's last request decoded
	rangeOrder []int64                 // the conversations of ranges, oldest request first
	rangeTotal int                     // messages across ranges
}

// decodedRange is the message list a conversation's last model request decoded and the identity of the stored bytes each message
// was decoded from. A message with the same identity in the next request of the conversation is not decoded again.
type decodedRange struct {
	lineage uint64
	idents  []history.MessageIdent
	stored  []bool
	list    []ai.Message
}

// NewModels returns the executors. report receives failures that do not fail an effect; now stamps a synthesized error message.
func NewModels(models func() durable.Models, now func() float64, report func(error)) *Models {
	if report == nil {
		report = func(error) {}
	}
	return &Models{models: models, now: now, report: report, kept: map[uint32][]ai.Message{}, ranges: map[int64]*decodedRange{}, owner: map[uint32]int64{}, newest: map[int64]uint32{}}
}

// Handlers returns the sqlhost handlers for the effect kinds this package serves.
func (m *Models) Handlers() map[abi.EffectKind]sqlhost.Handler {
	return map[abi.EffectKind]sqlhost.Handler{
		abi.EffectModelContext: m.modelContext,
		abi.EffectDeferred:     m.deferred,
	}
}

// messages resolves the message list of one request: the full list, or the list of an earlier effect plus the appended
// messages. A delta that names a list the host no longer holds is a protocol failure.
func (m *Models) messages(id uint32, p payload.ModelContextEffect, rng *sqlhost.MessageRange) ([]ai.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rng != nil {
		return m.rangeMessages(id, rng)
	}
	var list []ai.Message
	if p.Extends != nil {
		base, ok := m.kept[*p.Extends]
		if !ok {
			return nil, fmt.Errorf("model_context %d extends effect %d, whose message list the host no longer holds", id, *p.Extends)
		}
		m.total -= len(base)
		m.forget(*p.Extends)
		appended, err := durable.DecodeMessages(p.Append)
		if err != nil {
			return nil, fmt.Errorf("model_context %d append: %w", id, err)
		}
		base = append(base, appended...)
		list = base
	} else {
		var ctx struct {
			Messages json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal(p.Context, &ctx); err != nil {
			return nil, fmt.Errorf("model_context %d context: %w", id, err)
		}
		decoded, err := durable.DecodeMessages(ctx.Messages)
		if err != nil {
			return nil, fmt.Errorf("model_context %d messages: %w", id, err)
		}
		list = decoded
	}
	m.kept[id] = list
	m.order = append(m.order, id)
	m.total += len(list)
	if p.ConversationID != nil {
		m.owner[id] = *p.ConversationID
		m.newest[*p.ConversationID] = id
	}
	for len(m.order) > 1 && (len(m.order) > maxKeptLists || m.total > maxKeptMessages) {
		victim := m.evictable()
		if victim < 0 {
			break
		}
		m.total -= len(m.kept[m.order[victim]])
		m.forget(m.order[victim])
	}
	return list, nil
}

// rangeMessages decodes the messages of a committed context a host derived, from the first one that differs from the
// conversation's previous request: the context of a conversation grows at its end, so a turn of model calls decodes each
// new message once instead of the whole history per call. A range of the previous one's lineage agrees with it on every
// message both have (sqlhost.MessageRange), so only another lineage is compared; a list that keeps every message of the
// previous one is extended in place, past the length handed out, so a request still reading the previous list sees no change.
func (m *Models) rangeMessages(id uint32, rng *sqlhost.MessageRange) ([]ai.Message, error) {
	prev := m.ranges[rng.Conv]
	same := 0
	if prev != nil && rng.Lineage != 0 && prev.lineage == rng.Lineage {
		same = min(len(prev.list), len(rng.Idents))
	} else if prev != nil {
		for same < len(prev.idents) && same < len(rng.Idents) && rng.Stored[same] && prev.stored[same] && prev.idents[same] == rng.Idents[same] {
			same++
		}
	}
	n := len(rng.Idents)
	var list []ai.Message
	if prev != nil && same == len(prev.list) {
		list = prev.list
	} else {
		list = make([]ai.Message, same, n)
		if same > 0 {
			copy(list, prev.list[:same])
		}
	}
	for i := same; i < n; i++ {
		message, err := durable.DecodeMessage(rng.Msgs[i])
		if err != nil {
			return nil, fmt.Errorf("model_context %d message[%d]: %w", id, i, err)
		}
		list = append(list, message)
	}
	if prev == nil {
		m.rangeOrder = append(m.rangeOrder, rng.Conv)
	} else {
		m.rangeTotal -= len(prev.list)
	}
	m.ranges[rng.Conv] = &decodedRange{lineage: rng.Lineage, idents: rng.Idents, stored: rng.Stored, list: list}
	m.rangeTotal += len(list)
	for len(m.rangeOrder) > 1 && m.rangeTotal > maxKeptMessages {
		old := m.rangeOrder[0]
		if old == rng.Conv {
			break
		}
		m.rangeTotal -= len(m.ranges[old].list)
		delete(m.ranges, old)
		m.rangeOrder = m.rangeOrder[1:]
	}
	return list[:n:n], nil
}

// evictable is the position of the oldest list the budget may drop: not the newest list, and not the newest of its
// conversation. It is -1 when every list is protected.
func (m *Models) evictable() int {
	for i, id := range m.order[:len(m.order)-1] {
		if conv, ok := m.owner[id]; ok && m.newest[conv] == id {
			continue
		}
		return i
	}
	return -1
}

// forget drops one kept list and its bookkeeping.
func (m *Models) forget(id uint32) {
	delete(m.kept, id)
	m.order = removeID(m.order, id)
	if conv, ok := m.owner[id]; ok {
		if m.newest[conv] == id {
			delete(m.newest, conv)
		}
		delete(m.owner, id)
	}
}

func removeID(ids []uint32, id uint32) []uint32 {
	for i, v := range ids {
		if v == id {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return ids
}

func (m *Models) modelContext(ctx context.Context, call *sqlhost.Call) error {
	var p payload.ModelContextEffect
	if err := json.Unmarshal(call.Payload, &p); err != nil {
		return fmt.Errorf("model_context payload: %w", err)
	}
	list, err := m.messages(call.ID, p, call.Range)
	if err != nil {
		return err
	}
	model := m.models().GetModel(p.Model.Provider, p.Model.ModelID)
	if model == nil {
		m.post(call, errorEvent(p.Model, fmt.Sprintf("Model %s/%s is not available", p.Model.Provider, p.Model.ModelID), m.now()))
		return nil
	}
	var options ai.StreamOptions
	if len(p.Options) > 0 {
		if err := json.Unmarshal(p.Options, &options); err != nil {
			return fmt.Errorf("model_context options: %w", err)
		}
	}
	// generation.ts passes the invocation's signal: an extension-owned provider callback (a faux step) reads options.Signal.
	options.Signal = ctx
	startedAt, started := m.now(), m.mono()
	// The decoded messages are shared with the next request of the conversation and nothing modifies them, so the provider
	// layer need not copy the whole history for each call.
	stream := m.models().StreamSimple(ctx, model, ai.Context{Messages: list, Frozen: true}, options)
	// The stream is consumed to its end, also after an abort: the provider ends it on its signal with the aborted message, which
	// the core waits for (generation.ts consumes the stream to its end and classifies afterwards). Iterating under ctx would stop at
	// the abort and leave the core's request unanswered, and its generation's abort invocation never runs.
	for event := range stream.Events(context.WithoutCancel(ctx)) {
		body, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("model_context %d event: %w", call.ID, err)
		}
		body = withDuration(body, event, startedAt, started, m.mono())
		call.Post(abi.Event{Kind: abi.EventModelEvent, ID: call.ID, Payload: append(append(make([]byte, 0, len(body)+2), '['), append(body, ']')...)})
	}
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// withDuration sets durationMs on the final message of a done or error event from the host clock, as pi-ai's
// AssistantMessageEventStream does (utils/event-stream.ts: milliseconds from the stream's creation, rounded, never
// negative), unless the message predates the stream (its timestamp against the wall clock at creation). The host
// clock replaces the ai stream's own measurement, so a fixed clock (the gate's detenv) gives 0, as Pi's virtual
// performance.now does. body is the event's JSON, returned as is when nothing changes.
func withDuration(body []byte, event ai.AssistantMessageEvent, startedAt, started, now float64) []byte {
	var message *ai.AssistantMessage
	switch e := event.(type) {
	case ai.DoneEvent:
		message = e.Message
	case *ai.DoneEvent:
		message = e.Message
	case ai.ErrorEvent:
		message = e.Error
	case *ai.ErrorEvent:
		message = e.Error
	}
	if message == nil {
		return body
	}
	stamped := *message
	if float64(message.Timestamp) < startedAt {
		// Under the host clock the message predates the stream: untimed, whatever the ai stream measured.
		if message.DurationMs == nil {
			return body
		}
		stamped.DurationMs = nil
	} else {
		stamped.DurationMs = new(int64(math.Max(0, math.Round(now-started))))
	}
	var out any
	switch e := event.(type) {
	case ai.DoneEvent:
		e.Message = &stamped
		out = e
	case *ai.DoneEvent:
		copied := *e
		copied.Message = &stamped
		out = copied
	case ai.ErrorEvent:
		e.Error = &stamped
		out = e
	case *ai.ErrorEvent:
		copied := *e
		copied.Error = &stamped
		out = copied
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return body
	}
	return encoded
}

func (m *Models) mono() float64 {
	if m.monotonic != nil {
		return m.monotonic()
	}
	return m.now()
}

func (m *Models) post(call *sqlhost.Call, eventJSON []byte) {
	call.Post(abi.Event{Kind: abi.EventModelEvent, ID: call.ID, Payload: append(append([]byte{'['}, eventJSON...), ']')})
}

func errorEvent(model payload.ModelRef, message string, now float64) []byte {
	//portlint:allow mapkeyorder the event body is read by field name; no reader depends on key order
	body, _ := json.Marshal(map[string]any{
		"type": "error", "reason": "error",
		"error": map[string]any{
			"role": "assistant", "content": []any{}, "api": "", "provider": model.Provider, "model": model.ModelID,
			"usage":      map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
			"stopReason": "error", "errorMessage": message, "timestamp": now,
		},
	})
	return body
}

func (m *Models) deferred(ctx context.Context, call *sqlhost.Call) error {
	var p payload.DeferredEffect
	if err := json.Unmarshal(call.Payload, &p); err != nil {
		return fmt.Errorf("deferred payload: %w", err)
	}
	outcome := func(thrown bool, value any) {
		body, _ := json.Marshal(value)
		phase := abi.OutcomeResult
		if thrown {
			phase = abi.OutcomeThrown
		}
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: phase, Payload: body})
	}
	model := m.models().GetModel(p.Model.Provider, p.Model.ModelID)
	if model == nil {
		outcome(true, wireError(fmt.Errorf("Model %s/%s is not available", p.Model.Provider, p.Model.ModelID)))
		return nil
	}
	var handle ai.DeferredHandle
	if err := json.Unmarshal(p.Handle, &handle); err != nil {
		outcome(true, wireError(err))
		return nil
	}
	if p.Op == "cancel" {
		if err := m.models().CancelDeferred(ctx, model, handle); err != nil {
			outcome(true, wireError(err))
			return nil
		}
		outcome(false, nil)
		return nil
	}
	message := m.models().FetchDeferred(ctx, model, handle)
	outcome(false, message)
	return nil
}
