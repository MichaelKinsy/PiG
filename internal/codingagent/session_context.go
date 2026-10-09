// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Ports packages/coding-agent/src/core/session-manager.ts (buildSessionContext).
// BuildSessionContext defaults to the last entry, falls back there for an unknown leaf, and treats an explicitly nil leaf as an empty branch.
// It returns the messages and settings of [BuildSessionProjection], as session-manager.ts buildSessionContext does.
func BuildSessionContext(entries []SessionEntry, leafID *string, byID map[string]SessionEntry) SessionContext {
	projection := BuildSessionProjection(entries, leafID, byID)
	messages := projection.Messages
	if messages == nil {
		messages = []agent.AgentMessage{}
	}
	return SessionContext{Messages: messages, ThinkingLevel: projection.ThinkingLevel, Model: projection.Model}
}

// GetSessionContextSettings returns the latest model and thinking settings on a root-to-leaf path without projecting or decoding message bodies. It mirrors session-manager.ts getSessionContextSettings.
func GetSessionContextSettings(path []SessionEntry) (thinkingLevel string, model *SessionContextModel) {
	thinkingLevel = "off"
	thinkingFound, modelFound := false, false
	for i := len(path) - 1; i >= 0 && (!thinkingFound || !modelFound); i-- {
		entry := path[i]
		var thinking struct {
			ThinkingLevel string `json:"thinkingLevel"`
		}
		var modelEntry struct {
			Provider string `json:"provider"`
			ModelID  string `json:"modelId"`
		}
		var message struct {
			Message struct {
				Role     string `json:"role"`
				Provider string `json:"provider"`
				Model    string `json:"model"`
			} `json:"message"`
		}
		var target any
		switch entry.Base().Type {
		case "thinking_level_change":
			if thinkingFound {
				continue
			}
			target = &thinking
		case "model_change":
			if modelFound {
				continue
			}
			target = &modelEntry
		case "message":
			if modelFound {
				continue
			}
			target = &message
		default:
			continue
		}
		// upstream: packages/coding-agent/src/core/session-manager.ts:loadEntriesFromFile
		if json.Unmarshal(entry.Raw(), target) != nil {
			continue
		}
		switch entry.Base().Type {
		case "thinking_level_change":
			thinkingLevel = thinking.ThinkingLevel
			thinkingFound = true
		case "model_change":
			model = &SessionContextModel{Provider: modelEntry.Provider, ModelID: modelEntry.ModelID}
			modelFound = true
		case "message":
			if message.Message.Role == "assistant" {
				model = &SessionContextModel{Provider: message.Message.Provider, ModelID: message.Message.Model}
				modelFound = true
			}
		}
	}
	return thinkingLevel, model
}

// BuildContextEntries returns the selected branch's compaction-aware entry list, including state-only entries.
func BuildContextEntries(entries []SessionEntry, leafID *string, byID map[string]SessionEntry) []SessionEntry {
	result := buildContextEntries(buildSessionPath(entries, leafID, byID), asMessage)
	if result == nil {
		return []SessionEntry{}
	}
	return result
}

// LastLeaf is Pi's undefined leafId: the branch ends at the last entry. A nil leafID is Pi's null (the empty branch before the first
// entry), and a leafID that names no entry also falls back to the last one.
func LastLeaf() *string { return new("") }

// buildEntryIndex is session-manager.ts buildEntryIndex: the caller's index when it passes one, else one built from entries.
func buildEntryIndex(entries []SessionEntry, byID map[string]SessionEntry) map[string]SessionEntry {
	if byID != nil {
		return byID
	}
	index := make(map[string]SessionEntry, len(entries))
	for _, entry := range entries {
		index[entry.Base().ID] = entry
	}
	return index
}

func buildSessionPath(entries []SessionEntry, leafID *string, byID map[string]SessionEntry) []SessionEntry {
	if leafID == nil {
		return nil
	}
	index := buildEntryIndex(entries, byID)
	if len(entries) == 0 {
		return nil
	}
	leaf := entries[len(entries)-1]
	if *leafID != "" {
		if selected, ok := index[*leafID]; ok {
			leaf = selected
		}
	}
	var path []SessionEntry
	for {
		path = append(path, leaf)
		if leaf.Base().ParentID == nil || *leaf.Base().ParentID == "" {
			break
		}
		parent, ok := index[*leaf.Base().ParentID]
		if !ok {
			break
		}
		leaf = parent
	}
	slices.Reverse(path)
	return path
}
