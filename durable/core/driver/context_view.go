// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"

	"github.com/MichaelKinsy/PiG/durable"
)

// decodeContextView decodes the core's ContextView JSON (core/context.go contextJSON). Its contributions and messages are
// ai.Message values, an interface type: each one decodes through durable.DecodeMessages by its role.
func decodeContextView(raw []byte) (durable.ContextView, error) {
	var wire struct {
		Head          *durable.EntryRecord  `json:"head"`
		Entries       []durable.EntryRecord `json:"entries"`
		Contributions []json.RawMessage     `json:"contributions"`
		Messages      json.RawMessage       `json:"messages"`
	}
	var view durable.ContextView
	if err := json.Unmarshal(raw, &wire); err != nil {
		return view, err
	}
	view.Head, view.Entries = wire.Head, wire.Entries
	if wire.Contributions != nil {
		view.Contributions = make([][]ai.Message, len(wire.Contributions))
		for i, contribution := range wire.Contributions {
			messages, err := durable.DecodeMessages(contribution)
			if err != nil {
				return view, err
			}
			view.Contributions[i] = messages
		}
	}
	if len(wire.Messages) > 0 {
		messages, err := durable.DecodeMessages(wire.Messages)
		if err != nil {
			return view, err
		}
		view.Messages = messages
	}
	return view, nil
}
