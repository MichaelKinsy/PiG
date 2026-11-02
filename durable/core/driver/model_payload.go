// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// payloadKey names one open onPayload call: the model_context effect and the call's number within it.
type payloadKey struct {
	effect uint32
	call   int
}

// payloadAnswer is the core's answer to an onPayload call: the body to send (sent), or the abort that ended the chain.
type payloadAnswer struct {
	payload json.RawMessage
	sent    bool
	aborted bool
}

// onPayload is the provider's onPayload of a generation request (generation.ts providerPayload): it sends the body the provider
// built to the core as a model_event payload element and returns the body the core's beforeProviderRequest chain answers with.
func (m *Models) onPayload(ctx context.Context, call *sqlhost.Call) func(any, *ai.Model) (any, error) {
	return func(body any, _ *ai.Model) (any, error) {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("model_context %d payload: %w", call.ID, err)
		}
		m.mu.Lock()
		if m.payloads == nil {
			m.payloads, m.nextCall = map[payloadKey]chan payloadAnswer{}, map[uint32]int{}
		}
		n := m.nextCall[call.ID]
		m.nextCall[call.ID]++
		key := payloadKey{call.ID, n}
		answer := make(chan payloadAnswer, 1)
		m.payloads[key] = answer
		m.mu.Unlock()
		defer func() {
			m.mu.Lock()
			delete(m.payloads, key)
			m.mu.Unlock()
		}()
		//portlint:allow mapkeyorder driver-to-core wire (durable/core/host/PROTOCOL.md): the core reads the members by name
		event, err := json.Marshal([]any{map[string]any{"type": "payload", "call": n, "payload": json.RawMessage(raw)}})
		if err != nil {
			return nil, err
		}
		call.Post(abi.Event{Kind: abi.EventModelEvent, ID: call.ID, Payload: event})
		select {
		case a := <-answer:
			if a.aborted {
				return nil, context.Canceled
			}
			if !a.sent {
				return nil, nil
			}
			// Pi's hook returns what JSON.parse read from the core's answer and the provider sends JSON.stringify of it; the providers
			// take JSON text as that value (payloadReplacement), so the answer stays text.
			if !json.Valid(a.payload) {
				return nil, fmt.Errorf("model_context %d payload answer: not JSON", call.ID)
			}
			return a.payload, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// answerPayload delivers the core's modelPayload answer to the onPayload call it names.
func (m *Models) answerPayload(effect uint32, call int, a payloadAnswer) {
	m.mu.Lock()
	ch := m.payloads[payloadKey{effect, call}]
	m.mu.Unlock()
	if ch != nil {
		ch <- a
	}
}
