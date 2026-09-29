package runtime

// Ports packages/agent/src/harness/runtime/restore.ts.

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// ClassifiedLaneStorage distinguishes absent storage, a plain Branch, and a complete configured lane. Tip is present for branch and lane; Configuration and LaneState are present only for lane.
type ClassifiedLaneStorage struct {
	Kind          string
	Tip           *session.StoredValue[*string]
	Configuration *session.StoredValue[session.LaneConfiguration]
	LaneState     *session.StoredValue[session.LaneState]
}

// RestoredLane is one name/state pair from restoreSession's ordered Map. A slice preserves its iteration order in Go.
type RestoredLane struct {
	Name  string
	State LaneState
}

func copyTipID(id *string) *string {
	if id == nil {
		return nil
	}
	return new(*id)
}

func stateMatchesIntent(intent session.OperationIntent, state session.OperationState) bool {
	summary := strings.HasPrefix(string(state.At), "summary.")
	switch intent.Kind {
	case session.OperationKindCompaction:
		return summary && state.Task.Boundary.Kind == session.BoundaryFinish
	case session.OperationKindNavigation:
		if state.At == session.AtNavigationReadyToCommit {
			return !intent.Summarize && sameOptionalString(state.TargetID, intent.TargetID) && sameOptionalString(state.Label, intent.Label)
		}
		return intent.Summarize && summary && state.Task.Boundary.Kind == session.BoundaryCommitNavigation &&
			intent.TargetID != nil && state.Task.Boundary.TargetID == *intent.TargetID &&
			sameOptionalString(state.Task.Boundary.Label, intent.Label) && sameOptionalString(state.Task.CustomInstructions, intent.CustomInstructions)
	default:
		return state.At != session.AtNavigationReadyToCommit && (!summary || state.Task.Boundary.Kind == session.BoundaryResumeCheckpoint)
	}
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func restoreQuote(value string) string {
	// JSON.stringify leaves HTML characters and line separators literal. Escaping by rune also keeps literal backslash-u text distinct from Unicode escapes.
	var out strings.Builder
	out.WriteByte('"')
	for _, char := range value {
		switch char {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(char)
		case '\b':
			out.WriteString(`\b`)
		case '\t':
			out.WriteString(`\t`)
		case '\n':
			out.WriteString(`\n`)
		case '\f':
			out.WriteString(`\f`)
		case '\r':
			out.WriteString(`\r`)
		default:
			if char < 0x20 {
				const hex = "0123456789abcdef"
				out.WriteString(`\u00`)
				out.WriteByte(hex[char>>4])
				out.WriteByte(hex[char&15])
			} else {
				out.WriteRune(char)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

func missingLaneValue(lane, namespace string) error {
	return &session.SessionInvariantError{Message: "Lane " + restoreQuote(lane) + " is missing " + namespace}
}

func classifyLaneStorage(lane string, values ClassifiedLaneStorage) (ClassifiedLaneStorage, error) {
	switch {
	case values.Tip == nil && values.Configuration == nil && values.LaneState == nil:
		values.Kind = "absent"
	case values.Tip != nil && values.Configuration == nil && values.LaneState == nil:
		values.Kind = "branch"
	case values.Tip == nil:
		return ClassifiedLaneStorage{}, missingLaneValue(lane, "branch.tip")
	case values.Configuration == nil:
		return ClassifiedLaneStorage{}, missingLaneValue(lane, "lane.config")
	case values.LaneState == nil:
		return ClassifiedLaneStorage{}, missingLaneValue(lane, "lane.state")
	default:
		values.Kind = "lane"
	}
	return values, nil
}

// readRestoreValues preserves the left-to-right read order of Promise.all's input array. MemoryStorage and JsonlStorage compute their reads synchronously before returning resolved Promises (packages/agent/src/harness/session/memory.ts:67-74 and session/jsonl/storage.ts:196-203). All reads finish before the mutation capability is released, including on error.
func readRestoreValues(reads ...func() error) error {
	var firstError error
	for _, read := range reads {
		if err := read(); err != nil && firstError == nil {
			firstError = err
		}
	}
	return firstError
}

// ReadLaneStorage reads and classifies one lane's control values without a mutation barrier. Callers needing a coherent view supply a Session mutation reader. Invalid address keys return an error.
func ReadLaneStorage(ctx context.Context, reader session.SessionReader, lane string) (ClassifiedLaneStorage, error) {
	// All three lane values share this key. Validate it before reading so a constructor failure is returned.
	tipAddress, err := session.NewValue[*string](session.BranchTip("").Namespace, lane)
	if err != nil {
		return ClassifiedLaneStorage{}, err
	}
	var stored ClassifiedLaneStorage
	err = readRestoreValues(
		func() (err error) { stored.Tip, err = session.GetValue(ctx, reader, tipAddress); return },
		func() (err error) {
			stored.Configuration, err = session.GetValue(ctx, reader, session.LaneConfig(lane))
			return
		},
		func() (err error) {
			stored.LaneState, err = session.GetValue(ctx, reader, session.LaneStateValue(lane))
			return
		},
	)
	if err != nil {
		return ClassifiedLaneStorage{}, err
	}
	return classifyLaneStorage(lane, stored)
}

// RestoreSession restores every complete configured lane in one coherent Session read, in inventory order. Plain Branches are omitted. Restore does not write or start effects.
func RestoreSession(ctx context.Context, opened session.Session) ([]RestoredLane, error) {
	return session.Mutate(ctx, opened, func(ctx context.Context, reader session.SessionMutator) ([]RestoredLane, error) {
		var tips []session.StoredValue[*string]
		var configurations []session.StoredValue[session.LaneConfiguration]
		var states []session.StoredValue[session.LaneState]
		err := readRestoreValues(
			func() (err error) {
				tips, err = session.ScanValues(ctx, reader, session.BranchTipInventoryPrefix())
				return
			},
			func() (err error) {
				configurations, err = session.ScanValues(ctx, reader, session.LaneConfig(""))
				return
			},
			func() (err error) { states, err = session.ScanValues(ctx, reader, session.LaneStateValue("")); return },
		)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(tips))
		byLane := make(map[string]*ClassifiedLaneStorage, len(tips))
		valuesFor := func(name string) *ClassifiedLaneStorage {
			values := byLane[name]
			if values == nil {
				values = &ClassifiedLaneStorage{}
				byLane[name] = values
				names = append(names, name)
			}
			return values
		}
		for _, tip := range tips {
			valuesFor(tip.Address.Key).Tip = &tip
		}
		for _, configuration := range configurations {
			valuesFor(configuration.Address.Key).Configuration = &configuration
		}
		for _, state := range states {
			valuesFor(state.Address.Key).LaneState = &state
		}
		restored := make([]RestoredLane, 0, len(names))
		for _, lane := range names {
			stored, err := classifyLaneStorage(lane, *byLane[lane])
			if err != nil {
				return nil, err
			}
			if stored.Kind != "lane" {
				continue
			}
			state, err := RestoreLaneState(ctx, reader, lane, stored)
			if err != nil {
				return nil, err
			}
			restored = append(restored, RestoredLane{Name: lane, State: state})
		}
		return restored, nil
	})
}

// RestoreLane restores one configured lane without starting work or interpreting referenced payloads.
func RestoreLane(ctx context.Context, opened session.Session, lane string) (LaneState, error) {
	return session.Mutate(ctx, opened, func(ctx context.Context, reader session.SessionMutator) (LaneState, error) {
		stored, err := ReadLaneStorage(ctx, reader, lane)
		if err != nil {
			return LaneState{}, err
		}
		switch stored.Kind {
		case "absent":
			return LaneState{}, missingLaneValue(lane, "branch.tip")
		case "branch":
			return LaneState{}, missingLaneValue(lane, "lane.config")
		}
		return RestoreLaneState(ctx, reader, lane, stored)
	})
}

// RestoreLaneState reconstructs a complete configured lane's control projection, validating current-operation identity, ownership, and intent/state reachability. Nullable string values are copied; object payload references are retained. stored must be the lane variant of ReadLaneStorage.
func RestoreLaneState(ctx context.Context, reader session.SessionReader, lane string, stored ClassifiedLaneStorage) (LaneState, error) {
	var operation *session.Operation
	if operationID := stored.LaneState.Value.CurrentOperationID; operationID != nil {
		// Metadata and state share the same operation key, which may come from corrupt persisted lane control.
		metaAddress, err := session.NewValue[session.OperationMeta](session.OperationMetaValue("").Namespace, *operationID)
		if err != nil {
			return LaneState{}, err
		}
		var meta *session.StoredValue[session.OperationMeta]
		var state *session.StoredValue[session.OperationState]
		err = readRestoreValues(
			func() (err error) {
				meta, err = session.GetValue(ctx, reader, metaAddress)
				return
			},
			func() (err error) {
				state, err = session.GetValue(ctx, reader, session.OperationStateValue(*operationID))
				return
			},
		)
		if err != nil {
			return LaneState{}, err
		}
		message := ""
		switch {
		case meta == nil:
			message = "Operation " + *operationID + " is missing op.meta"
		case state == nil:
			message = "Operation " + *operationID + " is missing op.state"
		case meta.Value.OperationID != *operationID:
			message = "Operation " + *operationID + " metadata names operation " + restoreQuote(meta.Value.OperationID)
		case meta.Value.Lane != lane:
			message = "Operation " + *operationID + " belongs to lane " + restoreQuote(meta.Value.Lane) + ", not " + restoreQuote(lane)
		case !stateMatchesIntent(meta.Value.Intent, state.Value):
			message = fmt.Sprintf("Operation %s intent %s does not match state %s", *operationID, meta.Value.Intent.Kind, state.Value.At)
		}
		if message != "" {
			return LaneState{}, &session.SessionInvariantError{Message: message}
		}
		operation = &session.Operation{Meta: meta.Value, State: state.Value}
	}
	return LaneState{
		TipID: copyTipID(stored.Tip.Value), Configuration: stored.Configuration.Value, Inbox: stored.LaneState.Value.Inbox,
		LastOperationID: copyTipID(stored.LaneState.Value.LastOperationID), Operation: operation,
	}, nil
}
