package sdk

import (
	"errors"
	"math"
	"slices"
)

// FileOperations is Pi's FileOperations (core/compaction/utils.ts): the files a branch's tool calls read, wrote and edited. Each list is a JavaScript Set in
// insertion order, so a value is added once (SameValueZero) and, because a branch summary's recorded details can hold any JSON value, an element is not always a
// string.
type FileOperations struct {
	Read    []any
	Written []any
	Edited  []any
}

// BranchPreparation is Pi's BranchPreparation.
type BranchPreparation struct {
	// Messages are the messages extracted for summarization, in chronological order.
	Messages []map[string]any
	// FileOps are the file operations extracted from tool calls and from earlier branch summaries' details.
	FileOps FileOperations
	// TotalTokens is the estimated size of Messages.
	TotalTokens int
}

// CollectEntriesResult is Pi's CollectEntriesResult.
type CollectEntriesResult struct {
	// Entries are the entries to summarize, in chronological order.
	Entries []map[string]any
	// CommonAncestorID is the deepest entry on both the old and the new path, or nil.
	CommonAncestorID *string
}

// SessionReader is the part of Pi's ReadonlySessionManager that CollectEntriesForBranchSummary reads. [ReadonlySessionManager] is one.
type SessionReader interface {
	GetEntry(id string) (map[string]any, error)
	GetBranch(fromID *string) ([]map[string]any, error)
}

var _ SessionReader = ReadonlySessionManager{}

// CollectEntriesForBranchSummary is Pi's collectEntriesForBranchSummary (core/compaction/branch-summarization.ts): the entries from oldLeafID back to its
// common ancestor with targetID, oldest first. A compaction on the way is included, not a boundary. With no old position there is nothing to summarize.
func CollectEntriesForBranchSummary(session SessionReader, oldLeafID *string, targetID string) (CollectEntriesResult, error) {
	if oldLeafID == nil || *oldLeafID == "" {
		return CollectEntriesResult{Entries: []map[string]any{}}, nil
	}
	oldBranch, err := session.GetBranch(oldLeafID)
	if err != nil {
		return CollectEntriesResult{}, err
	}
	oldPath := map[string]bool{}
	for _, entry := range oldBranch {
		if id, ok := entry["id"].(string); ok {
			oldPath[id] = true
		}
	}
	targetPath, err := session.GetBranch(&targetID)
	if err != nil {
		return CollectEntriesResult{}, err
	}
	var commonAncestorID *string
	for _, t := range slices.Backward(targetPath) {
		if id, ok := t["id"].(string); ok && oldPath[id] {
			commonAncestorID = &id
			break
		}
	}
	entries := []map[string]any{}
	current := *oldLeafID
	for current != "" && (commonAncestorID == nil || current != *commonAncestorID) {
		entry, err := session.GetEntry(current)
		if err != nil {
			return CollectEntriesResult{}, err
		}
		if entry == nil {
			break
		}
		entries = append(entries, entry)
		current, _ = entry["parentId"].(string)
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return CollectEntriesResult{Entries: entries, CommonAncestorID: commonAncestorID}, nil
}

// PrepareBranchEntries is Pi's prepareBranchEntries (branch-summarization.ts): entries (oldest first) become the messages to summarize, newest first until
// tokenBudget (0 is no limit) is reached, with the file operations of every entry, including those that did not fit. A branch summary Pi wrote (not one an
// extension did) contributes the files it recorded. An entry the budget stops at is dropped, unless it is a compaction or a branch summary and the messages
// so far use under 90% of the budget. Timestamps are `new Date(timestamp).getTime()`, which the host's date parser answers ([ParseDate]).
func PrepareBranchEntries(entries []map[string]any, tokenBudget int) (BranchPreparation, error) {
	return prepareBranchEntries(entries, tokenBudget, nil)
}

func prepareBranchEntries(entries []map[string]any, tokenBudget int, standIns map[rune]bool) (BranchPreparation, error) {
	var newestFirst []map[string]any
	var fileOps FileOperations
	totalTokens := 0
	for _, entry := range entries {
		if entry["type"] != "branch_summary" || jsTruthy(entry["fromHook"]) || !jsTruthy(entry["details"]) {
			continue
		}
		details, _ := entry["details"].(map[string]any)
		if read, ok := details["readFiles"].([]any); ok {
			for _, file := range read {
				fileOps.Read = addToSet(fileOps.Read, file)
			}
		}
		if modified, ok := details["modifiedFiles"].([]any); ok {
			for _, file := range modified {
				fileOps.Edited = addToSet(fileOps.Edited, file)
			}
		}
	}
	for _, entry := range slices.Backward(entries) {

		message, err := messageFromEntry(entry)
		if err != nil {
			return BranchPreparation{}, err
		}
		if message == nil {
			continue
		}
		extractFileOpsFromMessage(message, &fileOps)
		tokens := estimateTokens(message, standIns)
		if tokenBudget > 0 && totalTokens+tokens > tokenBudget {
			if entry["type"] == "compaction" || entry["type"] == "branch_summary" {
				if float64(totalTokens) < float64(tokenBudget)*0.9 {
					newestFirst = append(newestFirst, message)
					totalTokens += tokens
				}
			}
			break
		}
		newestFirst = append(newestFirst, message)
		totalTokens += tokens
	}
	messages := make([]map[string]any, len(newestFirst))
	for i, message := range newestFirst {
		messages[len(newestFirst)-1-i] = message
	}
	return BranchPreparation{Messages: messages, FileOps: fileOps, TotalTokens: totalTokens}, nil
}

// messageFromEntry is Pi's getMessageFromEntry (branch-summarization.ts): a tool result adds nothing, because its context is in the assistant's call, and the
// entry types that do not carry conversation content add nothing.
func messageFromEntry(entry map[string]any) (map[string]any, error) {
	switch entry["type"] {
	case "message":
		message, present := entry["message"]
		if message == nil {
			if present {
				return nil, errors.New("TypeError: Cannot read properties of null (reading 'role')")
			}
			return nil, errors.New("TypeError: Cannot read properties of undefined (reading 'role')")
		}
		object, ok := message.(map[string]any)
		if !ok {
			return nil, errors.New("TypeError: a session entry's message is not an object")
		}
		if object["role"] == "toolResult" {
			return nil, nil
		}
		return object, nil
	case "custom_message":
		timestamp, err := dateTime(entry)
		if err != nil {
			return nil, err
		}
		message := map[string]any{"role": "custom", "timestamp": timestamp}
		copyMembers(message, entry, "customType", "content", "display", "details")
		return message, nil
	case "branch_summary":
		timestamp, err := dateTime(entry)
		if err != nil {
			return nil, err
		}
		message := map[string]any{"role": "branchSummary", "timestamp": timestamp}
		copyMembers(message, entry, "summary", "fromId")
		return message, nil
	case "compaction":
		timestamp, err := dateTime(entry)
		if err != nil {
			return nil, err
		}
		message := map[string]any{"role": "compactionSummary", "timestamp": timestamp}
		copyMembers(message, entry, "summary", "tokensBefore")
		return message, nil
	}
	return nil, nil
}

// copyMembers sets the named members of from that are present: Pi's message has `undefined` for an absent one, which JSON drops.
func copyMembers(to, from map[string]any, names ...string) {
	for _, name := range names {
		if value, present := from[name]; present {
			to[name] = value
		}
	}
}

// dateTime is `new Date(entry.timestamp).getTime()`: a string is parsed by the host, a number is clipped to the time range, `null` is the epoch and
// anything else, including an absent timestamp, is NaN.
func dateTime(entry map[string]any) (float64, error) {
	switch timestamp := entry["timestamp"].(type) {
	case string:
		ms, ok, err := ParseDate(timestamp)
		if err != nil {
			return 0, err
		}
		if !ok {
			return math.NaN(), nil
		}
		return ms, nil
	case float64:
		if math.IsNaN(timestamp) || math.Abs(timestamp) > 8.64e15 {
			return math.NaN(), nil
		}
		return math.Trunc(timestamp) + 0, nil
	case bool:
		if timestamp {
			return 1, nil
		}
		return 0, nil
	case nil:
		if _, present := entry["timestamp"]; present {
			return 0, nil
		}
	}
	return math.NaN(), nil
}

// extractFileOpsFromMessage is Pi's extractFileOpsFromMessage (utils.ts): the tool calls of an assistant message. Pi also reads the calls a codemode script
// made from a tool result, but prepareBranchEntries never passes one.
func extractFileOpsFromMessage(message map[string]any, fileOps *FileOperations) {
	if message["role"] == "assistant" {
		blocks, _ := message["content"].([]any)
		for _, raw := range blocks {
			block, ok := raw.(map[string]any)
			if !ok || block["type"] != "toolCall" {
				continue
			}
			_, hasArguments := block["arguments"]
			_, hasName := block["name"]
			if hasArguments && hasName {
				addFileOp(block["name"], block["arguments"], fileOps)
			}
		}
	}
}

func addFileOp(toolName, arguments any, fileOps *FileOperations) {
	object, _ := arguments.(map[string]any)
	path, _ := object["path"].(string)
	if path == "" {
		return
	}
	switch toolName {
	case "read":
		fileOps.Read = addToSet(fileOps.Read, path)
	case "write":
		fileOps.Written = addToSet(fileOps.Written, path)
	case "edit":
		fileOps.Edited = addToSet(fileOps.Edited, path)
	}
}

// addToSet is `Set.prototype.add`: the value joins at the end unless an equal one (SameValueZero) is in. Objects and arrays decoded from JSON are all distinct.
func addToSet(set []any, value any) []any {
	for _, existing := range set {
		switch value := value.(type) {
		case string, bool, nil:
			if existing == value {
				return set
			}
		case float64:
			if other, ok := existing.(float64); ok && (other == value || (math.IsNaN(other) && math.IsNaN(value))) {
				return set
			}
		}
	}
	return append(set, value)
}
