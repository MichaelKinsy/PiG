// SPDX-License-Identifier: MIT

package abi

import (
	"encoding/binary"
	"errors"
	"strconv"
)

// Event is one input of a session (ABI sections 5 and 11). Payload and Rows alias the host's input buffer and are valid
// only during the Step call that receives them; a core package that keeps any of it copies it first.
type Event struct {
	Kind    EventKind
	Now     float64 // the Harness clock; the core never reads another clock
	ID      uint32  // effect, tx, timer or request ID, by kind
	Phase   uint8   // model_bytes phase, tool_progress kind, tool_done/hook_done/phase_done outcome
	Skipped uint32  // tool_progress
	WaitID  uint32  // tool_progress kind 1: the progress_ack to answer
	Payload []byte  // JSON, bytes, or empty
	Rows    []ReadRows
}

// Wire returns the payload bytes of ev that a host writes into in_reserve.
func (ev Event) Wire() []byte {
	switch ev.Kind {
	case EventRows:
		return AppendRows(nil, ev.Rows)
	case EventModelEvent, EventTx, EventAPI:
		return append(binary.LittleEndian.AppendUint32(nil, ev.ID), ev.Payload...)
	case EventModelBytes, EventToolDone, EventHookDone, EventPhaseDone:
		out := binary.LittleEndian.AppendUint32(nil, ev.ID)
		return append(append(out, ev.Phase), ev.Payload...)
	case EventToolProgress:
		out := binary.LittleEndian.AppendUint32(nil, ev.ID)
		out = append(out, ev.Phase)
		out = binary.LittleEndian.AppendUint32(out, ev.Skipped)
		out = binary.LittleEndian.AppendUint32(out, ev.WaitID)
		return append(out, ev.Payload...)
	case EventTimer:
		return binary.LittleEndian.AppendUint32(nil, ev.ID)
	default:
		return ev.Payload
	}
}

// DecodeEvent is the inverse of Event.Wire: it reads the payload a host wrote into in_reserve. Payload aliases wire.
func DecodeEvent(kind EventKind, now float64, wire []byte) (Event, error) {
	ev := Event{Kind: kind, Now: now}
	p := NewPayload(wire)
	switch kind {
	case EventRows:
		rows, err := DecodeRows(wire)
		ev.Rows = rows
		return ev, err
	case EventModelEvent, EventTx, EventAPI:
		ev.ID = p.U32()
		ev.Payload = p.Rest()
	case EventModelBytes, EventToolDone, EventHookDone, EventPhaseDone:
		ev.ID = p.U32()
		ev.Phase = p.U8()
		ev.Payload = p.Rest()
	case EventToolProgress:
		ev.ID = p.U32()
		ev.Phase = p.U8()
		ev.Skipped = p.U32()
		ev.WaitID = p.U32()
		ev.Payload = p.Rest()
	case EventTimer:
		ev.ID = p.U32()
	case EventOpen, EventSubmit, EventAbort, EventRegistry, EventClose, EventInspect:
		ev.Payload = wire
	default:
		return ev, errors.New("abi: unknown event kind " + strconv.Itoa(int(kind)))
	}
	return ev, p.Err()
}
