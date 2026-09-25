package session

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/utils"
	"github.com/MichaelKinsy/PiG/ai"
)

type legacyV3Record struct {
	Type             string             `json:"type"`
	ID               string             `json:"id"`
	ParentID         *string            `json:"parentId"`
	Timestamp        string             `json:"timestamp"`
	Message          agent.AgentMessage `json:"message"`
	CustomType       string             `json:"customType"`
	Content          json.RawMessage    `json:"content"`
	Data             json.RawMessage    `json:"data"`
	Details          json.RawMessage    `json:"details"`
	Display          bool               `json:"display"`
	FromID           string             `json:"fromId"`
	Summary          string             `json:"summary"`
	FirstKeptEntryID string             `json:"firstKeptEntryId"`
	TokensBefore     int                `json:"tokensBefore"`
	Usage            *ai.Usage          `json:"usage"`
	FromHook         bool               `json:"fromHook"`
	Provider         string             `json:"provider"`
	ModelID          string             `json:"modelId"`
	ThinkingLevel    ai.ThinkingLevel   `json:"thinkingLevel"`
	ActiveToolNames  []string           `json:"activeToolNames"`
	Name             *string            `json:"name"`
	TargetID         string             `json:"targetId"`
	Label            *string            `json:"label"`
}

type legacyV3Index struct {
	ID               string
	ParentID         *string
	MappedID         *string
	Type             string
	Seq              int64
	FromID           string
	FirstKeptEntryID string
	Provider         string
	ModelID          string
	ThinkingLevel    ai.ThinkingLevel
	ActiveToolNames  []string
	TargetID         string
	Label            *string
}

// LegacyV3Source retains structural metadata and reopens captured payloads for each pass.
type LegacyV3Source struct {
	Header        JsonlStorageHeader
	ImportedUsage ai.Usage
	NextSeq       int64
	Values        []CommittedValueSetWrite
	fs            harness.FileSystem
	path          string
	entries       map[string]legacyV3Index
	order         []string
}

func normalizeLegacyV3Header(ctx context.Context, fs harness.FileSystem, legacy LegacyV3SessionHeader) (JsonlStorageHeader, error) {
	created, err := parseLegacyTimestamp(legacy.Timestamp)
	if err != nil {
		return JsonlStorageHeader{}, err
	}
	header := JsonlStorageHeader{V: JSONLFormatVersion, Kind: "header", ID: legacy.ID, StorageVersion: JSONLStorageVersion, CreatedAt: created, Cwd: legacy.Cwd}
	if legacy.ParentSession != nil {
		lines, err := fs.ReadTextLines(ctx, *legacy.ParentSession, &harness.ReadTextLinesOptions{MaxLines: new(1)})
		if err == nil && len(lines) > 0 {
			parsed, err := ParseJsonlSessionHeader(lines[0])
			if err == nil {
				if parsed.Header != nil {
					header.ParentSessionID = parsed.Header.ID
				} else {
					header.ParentSessionID = parsed.Legacy.ID
				}
				return header, nil
			}
		}
		header.LegacyParentSessionPath = *legacy.ParentSession
	}
	return header, nil
}

func parseLegacyV3Entry(line string) (legacyV3Record, error) {
	var record legacyV3Record
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return record, fmt.Errorf("Invalid legacy v3 JSONL record: not valid JSON: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return record, err
	}
	if string(fields["id"]) == "null" && retainedLegacyType(record.Type) {
		return record, fmt.Errorf("Legacy v3 entry reference has no retained ancestor: null")
	}
	switch record.Type {
	case "message", "custom", "custom_message", "branch_summary", "compaction", "model_change", "thinking_level_change", "active_tools_change", "session_info", "label":
		return record, nil
	default:
		return record, fmt.Errorf("Unsupported legacy v3 record type: %s", record.Type)
	}
}

func retainedLegacyType(kind string) bool {
	switch kind {
	case "model_change", "thinking_level_change", "active_tools_change", "session_info", "label":
		return false
	default:
		return true
	}
}

func (source *LegacyV3Source) resolve(id *string) (*string, error) {
	if id == nil {
		return nil, nil
	}
	entry, ok := source.entries[*id]
	if !ok {
		return nil, fmt.Errorf("Missing legacy v3 entry reference: %s", *id)
	}
	return entry.MappedID, nil
}

func (source *LegacyV3Source) branchFromID(id string) (*string, error) {
	if id == "root" {
		return nil, nil
	}
	return source.resolve(&id)
}

func (source *LegacyV3Source) index(record legacyV3Record, line int) error {
	if _, exists := source.entries[record.ID]; exists {
		return fmt.Errorf("Duplicate legacy v3 entry id: %s", record.ID)
	}
	parent, err := source.resolve(record.ParentID)
	if err != nil {
		return fmt.Errorf("Legacy v3 entry %s has a missing or forward parent at line %d: %s", record.ID, line, *record.ParentID)
	}
	indexed := legacyV3Index{ID: record.ID, ParentID: record.ParentID, MappedID: parent, Type: record.Type, FromID: record.FromID, FirstKeptEntryID: record.FirstKeptEntryID, Provider: record.Provider, ModelID: record.ModelID, ThinkingLevel: record.ThinkingLevel, ActiveToolNames: record.ActiveToolNames, TargetID: record.TargetID, Label: record.Label}
	if retainedLegacyType(record.Type) {
		timestamp, err := parseLegacyTimestamp(record.Timestamp)
		if err != nil {
			return err
		}
		id, err := UUIDv7(&timestamp)
		if err != nil {
			return err
		}
		indexed.MappedID = &id
		indexed.Seq = source.NextSeq
		source.NextSeq++
	}
	source.entries[record.ID] = indexed
	source.order = append(source.order, record.ID)
	var usage *ai.Usage
	if record.Type == "compaction" || record.Type == "branch_summary" {
		usage = record.Usage
	}
	if record.Type == "message" {
		if record.Message.Assistant != nil {
			usage = record.Message.Assistant.Usage
		}
		if record.Message.ToolResult != nil {
			usage = record.Message.ToolResult.Usage
		}
	}
	if usage != nil {
		source.ImportedUsage = utils.AddUsage(source.ImportedUsage, *usage)
	}
	return nil
}

// ReadLegacyV3Source scans complete records without rewriting the source file.
func ReadLegacyV3Source(ctx context.Context, fs harness.FileSystem, path string) (*LegacyV3Source, error) {
	reader, err := fs.OpenTextLineReader(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("Failed to open legacy v3 source %s: %w", path, err)
	}
	defer reader.Close(ctx)
	parsed, err := readJsonlHeader(ctx, reader, path)
	if err != nil {
		return nil, err
	}
	if parsed.Legacy == nil {
		return nil, fmt.Errorf("Invalid legacy v3 JSONL storage %s: expected format 3 header", path)
	}
	header, err := normalizeLegacyV3Header(ctx, fs, *parsed.Legacy)
	if err != nil {
		return nil, err
	}
	source := &LegacyV3Source{Header: header, NextSeq: 1, ImportedUsage: utils.EmptyUsage(), fs: fs, path: path, entries: map[string]legacyV3Index{}}
	var name *string
	for {
		line, err := reader.ReadLine(ctx)
		if err != nil {
			return nil, fmt.Errorf("Failed to read legacy v3 source: %w", err)
		}
		if line == nil || !line.Terminated {
			break
		}
		record, err := parseLegacyV3Entry(line.Text)
		if err != nil {
			return nil, err
		}
		if err := source.index(record, len(source.order)+2); err != nil {
			return nil, err
		}
		if record.Type == "session_info" {
			name = record.Name
		}
	}
	if err := source.normalizeValues(name); err != nil {
		return nil, err
	}
	return source, nil
}

func (source *LegacyV3Source) addValue(write ValueSetWrite) {
	source.Values = append(source.Values, CommittedValueSetWrite{Seq: source.NextSeq, Namespace: write.Namespace, Key: write.Key, Value: write.Value})
	source.NextSeq++
}

func (source *LegacyV3Source) normalizeValues(name *string) error {
	if name != nil && *name != "" {
		source.addValue(SetValue(SessionName, *name))
	}
	labels := map[string]string{}
	order := []string{}
	for _, id := range source.order {
		entry := source.entries[id]
		if entry.Type != "label" {
			continue
		}
		target, err := source.resolve(&entry.TargetID)
		if err != nil {
			return err
		}
		if target == nil {
			continue
		}
		if entry.Label != nil && *entry.Label != "" {
			if _, ok := labels[*target]; !ok {
				order = append(order, *target)
			}
			labels[*target] = *entry.Label
		} else {
			delete(labels, *target)
			order = slices.DeleteFunc(order, func(id string) bool { return id == *target })
		}
	}
	for _, id := range order {
		source.addValue(SetValue(EntryLabel(id), labels[id]))
	}
	var last *string
	if len(source.order) > 0 {
		last = &source.order[len(source.order)-1]
	}
	tip, err := source.resolve(last)
	if err != nil {
		return err
	}
	source.addValue(SetValue(BranchTip("main"), tip))
	if config := source.selectedConfiguration(last); config != nil {
		source.addValue(SetValue(LaneConfig("main"), *config))
		source.addValue(SetValue(LaneStateValue("main"), LaneState{Inbox: []InboxItem{}}))
	}
	return nil
}

func (source *LegacyV3Source) selectedConfiguration(id *string) *LaneConfiguration {
	remaining := map[string]bool{"model_change": true, "thinking_level_change": true, "active_tools_change": true}
	config := &LaneConfiguration{ActiveToolNames: []string{}}
	for id != nil && len(remaining) > 0 {
		entry := source.entries[*id]
		if remaining[entry.Type] {
			delete(remaining, entry.Type)
			switch entry.Type {
			case "model_change":
				config.Model = ModelRef{Provider: entry.Provider, ModelID: entry.ModelID}
			case "thinking_level_change":
				config.ThinkingLevel = entry.ThinkingLevel
			case "active_tools_change":
				config.ActiveToolNames = slices.Clone(entry.ActiveToolNames)
			}
		}
		id = entry.ParentID
	}
	if remaining["model_change"] || remaining["thinking_level_change"] {
		return nil
	}
	return config
}

// EntryStructures returns the captured reminted entry identities and ancestry.
func (source *LegacyV3Source) EntryStructures() ([]EntryStructure, error) {
	entries := []EntryStructure{}
	for _, id := range source.order {
		entry := source.entries[id]
		if !retainedLegacyType(entry.Type) {
			continue
		}
		parent, err := source.resolve(entry.ParentID)
		if err != nil {
			return nil, err
		}
		entries = append(entries, EntryStructure{ID: *entry.MappedID, ParentID: parent, Seq: entry.Seq})
	}
	return entries, nil
}

// TranslateForkEntryID requires an actual retained legacy node.
func (source *LegacyV3Source) TranslateForkEntryID(id string) (string, error) {
	entry, ok := source.entries[id]
	if !ok {
		return "", fmt.Errorf("Legacy v3 fork entry does not exist: %s", id)
	}
	if !retainedLegacyType(entry.Type) {
		return "", fmt.Errorf("Legacy v3 fork entry is not a retained entry: %s", id)
	}
	return *entry.MappedID, nil
}

func (source *LegacyV3Source) retainedTail(entry legacyV3Index) ([]legacyV3Index, error) {
	path := []legacyV3Index{}
	for id := entry.ParentID; id != nil; {
		ancestor := source.entries[*id]
		path = append(path, ancestor)
		if *id == entry.FirstKeptEntryID {
			return path, nil
		}
		id = ancestor.ParentID
	}
	return nil, fmt.Errorf("Legacy v3 compaction %s firstKeptEntryId is not on its parent branch: %s", entry.ID, entry.FirstKeptEntryID)
}

func (source *LegacyV3Source) requiredTailIDs(selected func(string) bool) (map[string]bool, error) {
	required := map[string]bool{}
	for _, id := range source.order {
		entry := source.entries[id]
		if entry.Type != "compaction" || (selected != nil && !selected(*entry.MappedID)) {
			continue
		}
		tail, err := source.retainedTail(entry)
		if err != nil {
			return nil, err
		}
		for _, ancestor := range tail {
			if retainedLegacyType(ancestor.Type) && ancestor.Type != "custom" {
				required[ancestor.ID] = true
			}
		}
	}
	return required, nil
}

func (source *LegacyV3Source) customMessage(record legacyV3Record) (agent.AgentMessage, error) {
	timestamp, err := parseLegacyTimestamp(record.Timestamp)
	if err != nil {
		return agent.AgentMessage{}, err
	}
	message := map[string]any{"role": "custom", "customType": record.CustomType, "content": record.Content, "display": record.Display, "timestamp": timestamp}
	if record.Details != nil {
		message["details"] = record.Details
	}
	return agent.AgentMessage{Custom: message}, nil
}

func (source *LegacyV3Source) contextMessage(record legacyV3Record) (*agent.AgentMessage, error) {
	if record.Type == "message" {
		return &record.Message, nil
	}
	if record.Type == "custom_message" {
		message, err := source.customMessage(record)
		return &message, err
	}
	timestamp, err := parseLegacyTimestamp(record.Timestamp)
	if err != nil {
		return nil, err
	}
	if record.Type == "compaction" {
		message := compactionSummaryMessage(record.Summary, record.TokensBefore, timestamp)
		return &message, nil
	}
	if record.Type == "branch_summary" && record.Summary != "" {
		from, err := source.branchFromID(record.FromID)
		if err != nil {
			return nil, err
		}
		message := branchSummaryMessage(record.Summary, from, timestamp)
		return &message, nil
	}
	return nil, nil
}

func rawOptionalValue(raw json.RawMessage) (*JsonValue, error) {
	if raw == nil {
		return nil, nil
	}
	var value JsonValue
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func (source *LegacyV3Source) normalizeEntry(record legacyV3Record, indexed legacyV3Index, tail []agent.AgentMessage) (CommittedWrite, error) {
	parent, err := source.resolve(indexed.ParentID)
	if err != nil {
		return nil, err
	}
	timestamp, err := parseLegacyTimestamp(record.Timestamp)
	if err != nil {
		return nil, err
	}
	entry := Entry{ID: *indexed.MappedID, ParentID: parent, Seq: indexed.Seq, Timestamp: timestamp, Type: EntryType(record.Type)}
	switch record.Type {
	case "message":
		entry.Message = record.Message
	case "custom_message":
		entry.Type = EntryTypeMessage
		entry.Message, err = source.customMessage(record)
	case "custom":
		entry.CustomType = record.CustomType
		entry.Data, err = rawOptionalValue(record.Data)
	case "branch_summary", "compaction":
		entry.Summary = record.Summary
		entry.Usage = record.Usage
		entry.FromHook = record.FromHook
		entry.Details, err = rawOptionalValue(record.Details)
		if err == nil && record.Type == "branch_summary" {
			entry.FromID, err = source.branchFromID(record.FromID)
		}
		if record.Type == "compaction" {
			entry.RetainedTail = tail
			entry.TokensBefore = record.TokensBefore
		}
	}
	return CommittedEntryWrite{Entry: entry}, err
}

func (source *LegacyV3Source) readCaptured(ctx context.Context, visit func(legacyV3Record, legacyV3Index) error) error {
	reader, err := source.fs.OpenTextLineReader(ctx, source.path)
	if err != nil {
		return fmt.Errorf("Failed to reopen legacy v3 source %s: %w", source.path, err)
	}
	defer reader.Close(ctx)
	parsed, err := readJsonlHeader(ctx, reader, source.path)
	if err != nil {
		return err
	}
	if parsed.Legacy == nil || parsed.Legacy.ID != source.Header.ID || parsed.Legacy.Cwd != source.Header.Cwd {
		return fmt.Errorf("Legacy v3 source header changed")
	}
	for _, id := range source.order {
		indexed := source.entries[id]
		line, err := reader.ReadLine(ctx)
		if err != nil {
			return err
		}
		if line == nil || !line.Terminated {
			return fmt.Errorf("Legacy v3 source ended before captured entries")
		}
		record, err := parseLegacyV3Entry(line.Text)
		if err != nil {
			return err
		}
		if record.ID != indexed.ID || record.Type != indexed.Type {
			return fmt.Errorf("Legacy v3 source changed")
		}
		if err := visit(record, indexed); err != nil {
			return err
		}
	}
	return nil
}

// Writes streams selected captured entries and derived values; each call owns its reader and tail cache.
func (source *LegacyV3Source) Writes(ctx context.Context, selected func(string) bool, yield func(CommittedWrite) error) error {
	required, err := source.requiredTailIDs(selected)
	if err != nil {
		return err
	}
	messages := map[string]agent.AgentMessage{}
	err = source.readCaptured(ctx, func(record legacyV3Record, indexed legacyV3Index) error {
		if required[record.ID] {
			message, err := source.contextMessage(record)
			if err != nil {
				return err
			}
			if message != nil {
				messages[record.ID] = *message
			}
		}
		if !retainedLegacyType(indexed.Type) || (selected != nil && !selected(*indexed.MappedID)) {
			return nil
		}
		tail := []agent.AgentMessage{}
		if indexed.Type == "compaction" {
			ancestors, err := source.retainedTail(indexed)
			if err != nil {
				return err
			}
			for _, ancestor := range ancestors {
				if message, ok := messages[ancestor.ID]; ok {
					tail = append(tail, message)
				}
			}
			slices.Reverse(tail)
		}
		write, err := source.normalizeEntry(record, indexed, tail)
		if err != nil {
			return err
		}
		return yield(write)
	})
	if err != nil {
		return err
	}
	for _, write := range source.Values {
		if err := yield(write); err != nil {
			return err
		}
	}
	return nil
}
