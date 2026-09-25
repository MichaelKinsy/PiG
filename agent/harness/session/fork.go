package session

import "fmt"

// ForkSourceSnapshot is one coherent source read for a streaming fork.
type ForkSourceSnapshot struct {
	Entries      []Entry
	ScalarValues []StoredValue[any]
	// EntriesIncomplete reports that a backend supplied only the requested
	// branch rather than the full tree (upstream entriesComplete: false).
	EntriesIncomplete bool
}

// ForkDestinationSnapshot is the complete logical state of a fork destination.
// Entries keep the upstream selection order.
type ForkDestinationSnapshot struct {
	Entries      []Entry
	ScalarValues []StoredValue[any]
	NextSeq      int64
}

func storedValuesInNamespace(values []StoredValue[any], namespace string) []StoredValue[any] {
	var matched []StoredValue[any]
	for _, stored := range values {
		if stored.Address.Namespace == namespace {
			matched = append(matched, stored)
		}
	}
	return matched
}

func findStoredValue(values []StoredValue[any], address StoredAddressBase) *StoredValue[any] {
	for index := range values {
		if values[index].Address.Namespace == address.Namespace && values[index].Address.Key == address.Key {
			return &values[index]
		}
	}
	return nil
}

// CreateForkSnapshot builds the complete logical state of a forked destination
// session, re-sequencing projected scalar values after the copied entries.
func CreateForkSnapshot(source ForkSourceSnapshot, options ForkOptions) (ForkDestinationSnapshot, error) {
	sourceEntries := make(map[string]Entry, len(source.Entries))
	for _, entry := range source.Entries {
		sourceEntries[entry.ID] = entry
	}
	sourceTips := storedValuesInNamespace(source.ScalarValues, "pi.branch.tip")
	if err := validateForkSourceSnapshot(source, sourceEntries, sourceTips, options); err != nil {
		return ForkDestinationSnapshot{}, err
	}
	entryIDs, plan, err := selectForkContents(source.Entries, sourceEntries, sourceTips, options)
	if err != nil {
		return ForkDestinationSnapshot{}, err
	}
	copied := make(map[string]bool, len(entryIDs))
	entries := make([]Entry, 0, len(entryIDs))
	var maxSeq int64
	for _, id := range entryIDs {
		entry := sourceEntries[id]
		copied[id] = true
		entries = append(entries, entry)
		maxSeq = max(maxSeq, entry.Seq)
	}
	nextSeq := maxSeq + 1
	var scalarValues []StoredValue[any]
	for _, stored := range source.ScalarValues {
		write := CommittedValueSetWrite{Seq: stored.Seq, Namespace: stored.Address.Namespace, Key: stored.Address.Key, Value: stored.Value}
		projected, ok, err := ProjectForkCurrentStateWrite(write, plan, func(id string) bool { return copied[id] })
		if err != nil {
			return ForkDestinationSnapshot{}, err
		}
		if !ok {
			continue
		}
		value := projected.(CommittedValueSetWrite)
		scalarValues = append(scalarValues, StoredValue[any]{Address: MustValue[any](value.Namespace, value.Key), Value: value.Value, Seq: nextSeq})
		nextSeq++
	}
	return ForkDestinationSnapshot{Entries: entries, ScalarValues: scalarValues, NextSeq: nextSeq}, nil
}

func selectForkContents(ordered []Entry, sourceEntries map[string]Entry, sourceTips []StoredValue[any], options ForkOptions) ([]string, ForkCurrentStatePlan, error) {
	if options.Scope == ForkScopeTree {
		ids := make([]string, 0, len(ordered))
		seen := map[string]bool{}
		for _, entry := range ordered {
			if !seen[entry.ID] {
				seen[entry.ID] = true
				ids = append(ids, entry.ID)
			}
		}
		return ids, ForkCurrentStatePlan{Scope: ForkScopeTree}, nil
	}
	var ids []string
	source := BranchForkSource{
		GetParent: func(entryID string) (*string, bool) {
			entry, ok := sourceEntries[entryID]
			return entry.ParentID, ok
		},
		SelectEntry: func(entryID string) { ids = append(ids, entryID) },
	}
	for _, tip := range sourceTips {
		if tip.Address.Key == options.Branch {
			value, err := convertStored[*string](tip.Value)
			if err != nil {
				return nil, ForkCurrentStatePlan{}, err
			}
			source.Tip, source.HasTip = value, true
			break
		}
	}
	plan, err := SelectBranchFork(options, source)
	return ids, plan, err
}

func validateForkSourceSnapshot(source ForkSourceSnapshot, sourceEntries map[string]Entry, sourceTips []StoredValue[any], options ForkOptions) error {
	tipKeys := map[string]bool{}
	for _, tip := range sourceTips {
		tipKeys[tip.Address.Key] = true
	}
	for _, stored := range source.ScalarValues {
		namespace := stored.Address.Namespace
		if (namespace == "pi.lane.config" || namespace == "pi.lane.state") && !tipKeys[stored.Address.Key] {
			return fmt.Errorf("Source session branch %s is missing branch.tip", jsonQuote(stored.Address.Key))
		}
	}
	for _, tip := range sourceTips {
		if err := validateForkSourceTip(source, sourceEntries, tip, options); err != nil {
			return err
		}
	}
	return nil
}

func validateForkSourceTip(source ForkSourceSnapshot, sourceEntries map[string]Entry, tip StoredValue[any], options ForkOptions) error {
	name := tip.Address.Key
	configuration := findStoredValue(source.ScalarValues, LaneConfig(name).StoredAddressBase)
	state := findStoredValue(source.ScalarValues, LaneStateValue(name).StoredAddressBase)
	if (configuration == nil) != (state == nil) {
		return fmt.Errorf("Source session branch %s has incomplete lane state", jsonQuote(name))
	}
	if options.Scope == ForkScopeBranch && name == options.Branch && configuration == nil {
		return fmt.Errorf("Source branch %s is not a configured AgentLane", jsonQuote(options.Branch))
	}
	if source.EntriesIncomplete && options.Scope != ForkScopeTree {
		return nil
	}
	value, err := convertStored[*string](tip.Value)
	if err != nil {
		return err
	}
	if value != nil {
		if _, ok := sourceEntries[*value]; !ok {
			return fmt.Errorf("Source session branch %s has an unknown tip", jsonQuote(name))
		}
	}
	return nil
}
