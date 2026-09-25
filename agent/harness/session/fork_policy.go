package session

import (
	"fmt"
	"strings"
)

// ForkCurrentStatePlan is the scope-specific projection plan for current
// state: tree scope, or one Branch with its destination tip.
type ForkCurrentStatePlan struct {
	Scope          string
	Branch         string
	DestinationTip *string
}

// BranchForkSource answers the lookups branch selection needs. HasTip is false
// for an unknown Branch; Tip nil is an empty Branch. GetParent reports ok
// false for a missing entry.
type BranchForkSource struct {
	Tip         *string
	HasTip      bool
	GetParent   func(entryID string) (parentID *string, ok bool)
	SelectEntry func(entryID string)
}

// SelectBranchFork walks the source tip ancestry, selects the forked path, and
// returns the destination tip.
func SelectBranchFork(options ForkOptions, source BranchForkSource) (ForkCurrentStatePlan, error) {
	if !source.HasTip {
		return ForkCurrentStatePlan{}, fmt.Errorf("Unknown source branch: %s", options.Branch)
	}
	requested := source.Tip
	if options.EntryID != nil {
		requested = options.EntryID
	}
	found := requested == nil
	var destinationTip *string
	for entryID := source.Tip; entryID != nil; {
		parentID, ok := source.GetParent(*entryID)
		if !ok {
			return ForkCurrentStatePlan{}, fmt.Errorf("Corrupt source branch: missing parent %s", *entryID)
		}
		switch {
		case requested != nil && *entryID == *requested:
			found = true
			if options.Position == ForkPositionBefore {
				destinationTip = parentID
			} else {
				destinationTip = entryID
				source.SelectEntry(*entryID)
			}
		case found:
			source.SelectEntry(*entryID)
		}
		entryID = parentID
	}
	if !found {
		return ForkCurrentStatePlan{}, fmt.Errorf("Fork entry %s is not on source branch %s", describeEntryID(requested), jsonQuote(options.Branch))
	}
	return ForkCurrentStatePlan{Scope: ForkScopeBranch, Branch: options.Branch, DestinationTip: destinationTip}, nil
}

func describeEntryID(id *string) string {
	if id == nil {
		return "null"
	}
	return *id
}

// ProjectForkCurrentStateWrite projects one current scalar row or surviving
// list element (CommittedValueSetWrite or CommittedListAppendWrite) into
// destination state. ok is false when the row is excluded; an error rejects a
// surviving row in an undeclared reserved namespace.
func ProjectForkCurrentStateWrite(write CommittedWrite, plan ForkCurrentStatePlan, isEntryCopied func(entryID string) bool) (CommittedWrite, bool, error) {
	namespace, key := committedAddress(write)
	tree := plan.Scope == ForkScopeTree
	switch namespace {
	case "pi.session.name":
		return write, true, nil
	case "pi.entry.label":
		return write, isEntryCopied(key), nil
	case "pi.branch.tip":
		if tree {
			return write, true, nil
		}
		return withCommittedValue(write, plan.DestinationTip), key == plan.Branch, nil
	case "pi.lane.config":
		return write, tree || key == plan.Branch, nil
	case "pi.lane.state":
		return withCommittedValue(write, LaneState{Inbox: []InboxItem{}}), tree || key == plan.Branch, nil
	case "pi.result":
		return nil, false, nil
	}
	if strings.HasPrefix(namespace, "pi.op.") || strings.HasPrefix(namespace, "pi.pending.") {
		return nil, false, nil
	}
	if namespace == "pi" || strings.HasPrefix(namespace, "pi.") {
		return nil, false, fmt.Errorf("Unknown reserved fork namespace: %s", namespace)
	}
	return write, tree, nil
}

func committedAddress(write CommittedWrite) (namespace, key string) {
	switch value := write.(type) {
	case CommittedValueSetWrite:
		return value.Namespace, value.Key
	case CommittedListAppendWrite:
		return value.Namespace, value.Key
	case CommittedValueDeleteWrite:
		return value.Namespace, value.Key
	case CommittedListDeleteWrite:
		return value.Namespace, value.Key
	default:
		return "", ""
	}
}

func withCommittedValue(write CommittedWrite, value any) CommittedWrite {
	switch typed := write.(type) {
	case CommittedValueSetWrite:
		typed.Value = value
		return typed
	case CommittedListAppendWrite:
		typed.Value = value
		return typed
	default:
		return write
	}
}
