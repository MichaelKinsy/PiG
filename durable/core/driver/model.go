// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// maxKeptLists bounds the message lists kept for the delta form of model_context. The core never extends an effect this far back.
const maxKeptLists = 1024

// Models runs model_context and deferred effects over a durable.Models.
type Models struct {
	models durable.Models
	report func(error)
	now    func() float64

	mu    sync.Mutex
	kept  map[uint32][]ai.Message
	order []uint32
}

// NewModels returns the executors. report receives failures that do not fail an effect; now stamps a synthesized error message.
func NewModels(models durable.Models, now func() float64, report func(error)) *Models {
	if report == nil {
		report = func(error) {}
	}
	return &Models{models: models, now: now, report: report, kept: map[uint32][]ai.Message{}}
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
func (m *Models) messages(id uint32, p payload.ModelContextEffect) ([]ai.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []ai.Message
	if p.Extends != nil {
		base, ok := m.kept[*p.Extends]
		if !ok {
			return nil, fmt.Errorf("model_context %d extends effect %d, whose message list the host no longer holds", id, *p.Extends)
		}
		delete(m.kept, *p.Extends)
		m.order = removeID(m.order, *p.Extends)
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
	if len(m.order) > maxKeptLists {
		delete(m.kept, m.order[0])
		m.order = m.order[1:]
	}
	return list, nil
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
	list, err := m.messages(call.ID, p)
	if err != nil {
		return err
	}
	model := m.models.GetModel(p.Model.Provider, p.Model.ModelID)
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
	stream := m.models.StreamSimple(ctx, model, ai.Context{Messages: list}, options)
	for event := range stream.Events(ctx) {
		body, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("model_context %d event: %w", call.ID, err)
		}
		call.Post(abi.Event{Kind: abi.EventModelEvent, ID: call.ID, Payload: append(append(make([]byte, 0, len(body)+2), '['), append(body, ']')...)})
	}
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
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
	model := m.models.GetModel(p.Model.Provider, p.Model.ModelID)
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
		if err := m.models.CancelDeferred(ctx, model, handle); err != nil {
			outcome(true, wireError(err))
			return nil
		}
		outcome(false, nil)
		return nil
	}
	message := m.models.FetchDeferred(ctx, model, handle)
	if ctx.Err() != nil {
		return nil
	}
	outcome(false, message)
	return nil
}
