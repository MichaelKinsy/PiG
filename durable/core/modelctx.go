// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// modelContextPayload builds the payload of a model request (ABI section 6, effect 3, and section 10.2). A request carries
// the whole transcript, so the payload names the committed context the model reads (conversation, head marker, tail) and the
// host derives its messages from the records in storage: the core neither copies the transcript into the effect nor keeps
// a list of what the host holds. A request whose messages a beforeRequest hook replaced carries them as text.
func (h *handler) modelContextPayload(req *turn.StreamRequest) []byte {
	head, tail := req.Context.Bounds()
	named := req.Replaced == nil && req.Conversation <= abi.MaxRecipeID && head <= abi.MaxRecipeID && tail <= abi.MaxRecipeID
	size := 512 + len(req.Model.Provider) + len(req.Model.ModelID)
	var listed []*jv.Object
	if !named && req.Replaced == nil {
		listed = req.Context.Messages()
		for _, m := range listed {
			size += len(jv.Append(nil, m)) + 1
		}
	}
	r := abi.NewRecipe(h.payloadBuffer(size))
	out := append(r.Text(), `{"model":{"provider":`...)
	out = jv.AppendString(out, req.Model.Provider)
	out = append(out, `,"modelId":`...)
	out = jv.AppendString(out, req.Model.ModelID)
	out = append(out, `},"conversationId":`...)
	out = jv.AppendNumber(out, float64(req.Conversation))
	out = append(out, `,"context":{"messages":[`...)
	switch {
	case req.Replaced != nil:
		for i, m := range req.Replaced {
			if i > 0 {
				out = append(out, ',')
			}
			out = jv.Append(out, m)
		}
	case named:
		r.SetText(out)
		r.Range(int64(req.Conversation), head, tail)
		out = r.Text()
	default:
		for i, m := range listed {
			if i > 0 {
				out = append(out, ',')
			}
			out = jv.Append(out, m)
		}
	}
	out = append(out, `]},"options":`...)
	out = jv.Append(out, req.Options)
	r.SetText(append(out, '}'))
	return r.Bytes()
}
