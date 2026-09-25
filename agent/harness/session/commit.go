package session

import "fmt"

// CommittedWrite is one write with its assigned sequence: CommittedEntryWrite,
// CommittedUsageWrite, CommittedValueSetWrite, CommittedValueDeleteWrite,
// CommittedListAppendWrite, or CommittedListDeleteWrite.
type CommittedWrite interface{ CommittedSeq() int64 }

// CommittedEntryWrite is an inserted entry with its seq and timestamp.
type CommittedEntryWrite struct{ Entry Entry }

// CommittedUsageWrite is an inserted ledger row with its seq.
type CommittedUsageWrite struct{ Row UsageRow }

// CommittedValueSetWrite is a sequenced scalar set.
type CommittedValueSetWrite struct {
	Seq       int64
	Namespace string
	Key       string
	Value     any
}

// CommittedValueDeleteWrite is a sequenced scalar delete.
type CommittedValueDeleteWrite struct {
	Seq       int64
	Namespace string
	Key       string
}

// CommittedListAppendWrite is a sequenced list append.
type CommittedListAppendWrite struct {
	Seq       int64
	Namespace string
	Key       string
	Value     any
}

// CommittedListDeleteWrite is a sequenced whole-list delete.
type CommittedListDeleteWrite struct {
	Seq       int64
	Namespace string
	Key       string
}

func (write CommittedEntryWrite) CommittedSeq() int64       { return write.Entry.Seq }
func (write CommittedUsageWrite) CommittedSeq() int64       { return write.Row.Seq }
func (write CommittedValueSetWrite) CommittedSeq() int64    { return write.Seq }
func (write CommittedValueDeleteWrite) CommittedSeq() int64 { return write.Seq }
func (write CommittedListAppendWrite) CommittedSeq() int64  { return write.Seq }
func (write CommittedListDeleteWrite) CommittedSeq() int64  { return write.Seq }

// PreparedCommit is a sequenced transaction and its result without stats.
type PreparedCommit struct {
	Writes []CommittedWrite
	Result CommitResult
}

// CommitValidationState answers the id lookups commit validation needs.
type CommitValidationState struct {
	HasEntryOrUsageID func(id string) bool
	HasEntryID        func(id string) bool
}

// InsertEntry constructs an entry insert write.
func InsertEntry(entry Entry) EntryWrite { return EntryWrite{Entry: entry} }

// InsertUsage constructs a ledger insert write; storage assigns the seq.
func InsertUsage(row UsageRow) UsageWrite { return UsageWrite{Row: row} }

// CommitWrite assigns one write its sequence and the transaction timestamp.
func CommitWrite(write Write, seq, timestamp int64) (CommittedWrite, error) {
	switch value := write.(type) {
	case EntryWrite:
		return CommittedEntryWrite{Entry: MaterializeCommittedEntry(value.Entry, seq, timestamp)}, nil
	case UsageWrite:
		row := value.Row
		row.Seq = seq
		return CommittedUsageWrite{Row: row}, nil
	case ValueSetWrite:
		return CommittedValueSetWrite{Seq: seq, Namespace: value.Namespace, Key: value.Key, Value: value.Value}, nil
	case ValueDeleteWrite:
		return CommittedValueDeleteWrite{Seq: seq, Namespace: value.Namespace, Key: value.Key}, nil
	case ListAppendWrite:
		return CommittedListAppendWrite{Seq: seq, Namespace: value.Namespace, Key: value.Key, Value: value.Value}, nil
	case ListDeleteWrite:
		return CommittedListDeleteWrite{Seq: seq, Namespace: value.Namespace, Key: value.Key}, nil
	default:
		return nil, fmt.Errorf("unknown write %T", write)
	}
}

// MaterializeCommittedEntry stamps a transaction entry with seq and timestamp.
func MaterializeCommittedEntry(entry Entry, seq, timestamp int64) Entry {
	entry.Seq = seq
	entry.Timestamp = timestamp
	return entry
}

// PrepareStorageCommit sequences writes from firstSeq at one timestamp.
func PrepareStorageCommit(writes []Write, firstSeq, timestamp int64) (PreparedCommit, error) {
	committed := make([]CommittedWrite, 0, len(writes))
	seqs := make([]int64, 0, len(writes))
	for index, write := range writes {
		sequenced, err := CommitWrite(write, firstSeq+int64(index), timestamp)
		if err != nil {
			return PreparedCommit{}, err
		}
		committed = append(committed, sequenced)
		seqs = append(seqs, sequenced.CommittedSeq())
	}
	return PreparedCommit{Writes: committed, Result: CommitResult{FirstSeq: firstSeq, Seqs: seqs, Timestamp: timestamp}}, nil
}

// ValidateCommittedWrites checks sequence monotonicity, the shared entry/usage
// id namespace, and that every entry parent exists earlier.
func ValidateCommittedWrites(writes []CommittedWrite, firstSeq int64, state CommitValidationState) error {
	previousSeq := firstSeq - 1
	transactionIDs := map[string]bool{}
	transactionEntryIDs := map[string]bool{}
	for _, write := range writes {
		seq := write.CommittedSeq()
		if seq <= previousSeq {
			return fmt.Errorf("Non-monotonic storage sequence: %d", seq)
		}
		previousSeq = seq
		id, parentID, isEntry, ok := committedIdentity(write)
		if !ok {
			continue
		}
		if state.HasEntryOrUsageID(id) || transactionIDs[id] {
			return fmt.Errorf("Duplicate entry or usage id: %s", id)
		}
		if isEntry && parentID != nil && !state.HasEntryID(*parentID) && !transactionEntryIDs[*parentID] {
			return fmt.Errorf("Missing parent entry: %s", *parentID)
		}
		transactionIDs[id] = true
		if isEntry {
			transactionEntryIDs[id] = true
		}
	}
	return nil
}

func committedIdentity(write CommittedWrite) (id string, parentID *string, isEntry, ok bool) {
	switch value := write.(type) {
	case CommittedEntryWrite:
		return value.Entry.ID, value.Entry.ParentID, true, true
	case CommittedUsageWrite:
		return value.Row.ID, nil, false, true
	default:
		return "", nil, false, false
	}
}
