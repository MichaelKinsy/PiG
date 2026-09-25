package session

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

type jsonlForkInput struct {
	metadata SessionMetadata
	nextSeq  *int64
	legacy   *LegacyV3Source
}
type jsonlForkIndex struct {
	scalarSeqs    map[string]int64
	firstListSeqs map[string]int64
	parents       map[string]*string
	branchTips    map[string]*string
	configs       map[string]bool
	states        map[string]bool
	selected      map[string]bool
}

func newJsonlForkIndex() *jsonlForkIndex {
	return &jsonlForkIndex{scalarSeqs: map[string]int64{}, firstListSeqs: map[string]int64{}, parents: map[string]*string{}, branchTips: map[string]*string{}, configs: map[string]bool{}, states: map[string]bool{}, selected: map[string]bool{}}
}

func (index *jsonlForkIndex) apply(write CommittedWrite) error {
	switch w := write.(type) {
	case CommittedEntryWrite:
		index.parents[w.Entry.ID] = w.Entry.ParentID
	case CommittedValueSetWrite:
		index.scalarSeqs[physicalKey(w.Namespace, w.Key)] = w.Seq
		return index.applyLane(w.Namespace, w.Key, w.Value, true)
	case CommittedValueDeleteWrite:
		delete(index.scalarSeqs, physicalKey(w.Namespace, w.Key))
		return index.applyLane(w.Namespace, w.Key, nil, false)
	case CommittedListAppendWrite:
		key := physicalKey(w.Namespace, w.Key)
		if _, exists := index.firstListSeqs[key]; !exists {
			index.firstListSeqs[key] = w.Seq
		}
	case CommittedListDeleteWrite:
		delete(index.firstListSeqs, physicalKey(w.Namespace, w.Key))
	}
	return nil
}

func (index *jsonlForkIndex) applyLane(namespace, key string, value any, present bool) error {
	switch namespace {
	case "pi.branch.tip":
		if !present {
			delete(index.branchTips, key)
		} else {
			tip, err := convertStored[*string](value)
			if err != nil {
				return err
			}
			index.branchTips[key] = tip
		}
	case "pi.lane.config":
		if present {
			index.configs[key] = true
		} else {
			delete(index.configs, key)
		}
	case "pi.lane.state":
		if present {
			index.states[key] = true
		} else {
			delete(index.states, key)
		}
	}
	return nil
}

func (index *jsonlForkIndex) selectPlan(options ForkOptions) (ForkCurrentStatePlan, error) {
	if options.Scope == ForkScopeTree {
		return ForkCurrentStatePlan{Scope: ForkScopeTree}, nil
	}
	tip, exists := index.branchTips[options.Branch]
	plan, err := SelectBranchFork(options, BranchForkSource{Tip: tip, HasTip: exists, GetParent: func(id string) (*string, bool) { parent, ok := index.parents[id]; return parent, ok }, SelectEntry: func(id string) { index.selected[id] = true }})
	if err != nil {
		return plan, err
	}
	if !index.configs[options.Branch] || !index.states[options.Branch] {
		return plan, fmt.Errorf("Source branch %s is not a configured AgentLane", jsonQuote(options.Branch))
	}
	return plan, nil
}

func readForkTransactions(ctx context.Context, fs harness.FileSystem, metadata SessionMetadata, boundary *int64, visit func([]CommittedWrite) error) (JsonlStorageHeader, error) {
	reader, err := fs.OpenTextLineReader(ctx, metadata.Path)
	if err != nil {
		return JsonlStorageHeader{}, fmt.Errorf("Failed to open JSONL fork source %s: %w", metadata.Path, err)
	}
	defer reader.Close(ctx)
	parsed, err := readJsonlHeader(ctx, reader, metadata.Path)
	if err != nil {
		return JsonlStorageHeader{}, err
	}
	if parsed.Header == nil {
		return JsonlStorageHeader{}, fmt.Errorf("Invalid JSONL storage %s: expected format 4 header", metadata.Path)
	}
	header := *parsed.Header
	if header.ID != metadata.ID || header.Cwd != metadata.Cwd {
		return header, fmt.Errorf("Session identity does not match header: %s", metadata.ID)
	}
	if header.StorageVersion != JSONLStorageVersion {
		return header, fmt.Errorf("Session %s uses unsupported storage version %d", metadata.ID, header.StorageVersion)
	}
	for {
		line, err := reader.ReadLine(ctx)
		if err != nil {
			return header, fmt.Errorf("Failed to read JSONL fork source %s: %w", metadata.Path, err)
		}
		if line == nil || !line.Terminated {
			break
		}
		writes, err := ParseJsonlTransaction(line.Text)
		if err != nil {
			return header, err
		}
		if boundary != nil && len(writes) > 0 {
			if writes[0].CommittedSeq() >= *boundary {
				break
			}
			if writes[len(writes)-1].CommittedSeq() >= *boundary {
				return header, fmt.Errorf("JSONL transaction crosses fork sequence boundary %d", *boundary)
			}
		}
		if err := visit(writes); err != nil {
			return header, err
		}
	}
	return header, nil
}

func indexForkInput(ctx context.Context, fs harness.FileSystem, input jsonlForkInput) (*jsonlForkIndex, int64, error) {
	index := newJsonlForkIndex()
	if input.legacy != nil {
		entries, err := input.legacy.EntryStructures()
		if err != nil {
			return nil, 0, err
		}
		for _, entry := range entries {
			index.parents[entry.ID] = entry.ParentID
		}
		for _, value := range input.legacy.Values {
			if err := index.apply(value); err != nil {
				return nil, 0, err
			}
		}
		return index, input.legacy.NextSeq, nil
	}
	highest := int64(0)
	header, err := readForkTransactions(ctx, fs, input.metadata, input.nextSeq, func(writes []CommittedWrite) error {
		for _, write := range writes {
			if err := index.apply(write); err != nil {
				return err
			}
		}
		if len(writes) > 0 {
			highest = writes[len(writes)-1].CommittedSeq()
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if input.nextSeq != nil {
		return index, *input.nextSeq, nil
	}
	next := max(int64(1), highest+1)
	if header.NextSeq != nil {
		next = max(next, *header.NextSeq)
	}
	return index, next, nil
}

func (index *jsonlForkIndex) project(write CommittedWrite, plan ForkCurrentStatePlan, copied func(string) bool) (CommittedWrite, bool, error) {
	switch w := write.(type) {
	case CommittedEntryWrite:
		return w, copied(w.Entry.ID), nil
	case CommittedValueSetWrite:
		if index.scalarSeqs[physicalKey(w.Namespace, w.Key)] == w.Seq {
			return ProjectForkCurrentStateWrite(w, plan, copied)
		}
	case CommittedListAppendWrite:
		first, ok := index.firstListSeqs[physicalKey(w.Namespace, w.Key)]
		if ok && w.Seq >= first {
			return ProjectForkCurrentStateWrite(w, plan, copied)
		}
	}
	return nil, false, nil
}

func runJsonlFork(ctx context.Context, fs harness.FileSystem, input jsonlForkInput, path string, header JsonlStorageHeader, options ForkOptions) error {
	index, next, err := indexForkInput(ctx, fs, input)
	if err != nil {
		return err
	}
	if input.legacy != nil && options.Scope == ForkScopeBranch && options.EntryID != nil {
		id, err := input.legacy.TranslateForkEntryID(*options.EntryID)
		if err != nil {
			return err
		}
		options.EntryID = &id
	}
	plan, err := index.selectPlan(options)
	if err != nil {
		return err
	}
	copied := func(id string) bool { return plan.Scope == ForkScopeTree || index.selected[id] }
	header.NextSeq = &next
	return publishJsonl(ctx, fs, path, header, func(appendWrites func([]CommittedWrite) error) error {
		visit := func(write CommittedWrite) error {
			projected, ok, err := index.project(write, plan, copied)
			if err != nil || !ok {
				return err
			}
			return appendWrites([]CommittedWrite{projected})
		}
		if input.legacy != nil {
			return input.legacy.Writes(ctx, copied, visit)
		}
		_, err := readForkTransactions(ctx, fs, input.metadata, &next, func(writes []CommittedWrite) error {
			for _, write := range writes {
				if err := visit(write); err != nil {
					return err
				}
			}
			return nil
		})
		return err
	})
}
