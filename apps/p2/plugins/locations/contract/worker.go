package contract

import (
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	"github.com/MichaelKinsy/PiG/durable"
)

// MoveFailure is the last move that failed.
type MoveFailure struct {
	To    contract.LocationValue `json:"to"`
	Error string                 `json:"error"`
}

// MoveState is the main agent's move, waiting for its turn to end (the worker applies it), and the last one that failed.
// Kept on conversation 1 for the session, whichever top-level conversation (the main one or a `/tree` branch) asked for it.
type MoveState struct {
	Pending *contract.LocationValue `json:"pending,omitempty"`
	Failed  *MoveFailure            `json:"failed,omitempty"`
}

// MoveKind is the kind of LocationMoveDoc.
const MoveKind = "p2.location-move"

// LocationMoveDoc is the Durable document other worker plugins read (the workspace reopens its backend when a pending move
// lands).
var LocationMoveDoc = durable.DefineDoc(durable.DocDefinition[MoveState]{
	CommonDocDefinition: durable.CommonDocDefinition[MoveState]{
		Kind:    MoveKind,
		Version: 1,
		Initial: func() MoveState { return MoveState{} },
		CheckpointWhen: func(_ MoveState, _ []durable.Op, info durable.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 64
		},
	},
	DocumentSemantics: durable.NewLatestConversationSemantics(durable.ForkInitial).DocumentSemantics,
})
