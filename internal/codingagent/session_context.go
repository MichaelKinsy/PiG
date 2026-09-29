// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// SessionContext contains model-visible messages and the settings on their full branch path.
type SessionContext struct {
	Messages      []agent.AgentMessage `json:"messages"`
	ThinkingLevel string               `json:"thinkingLevel"`
	Model         *SessionContextModel `json:"model"`
}

// SessionContextModel is the model identity last selected on a branch.
type SessionContextModel struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// Ports packages/coding-agent/src/core/session-manager.ts (buildSessionContext).
// BuildSessionContext defaults to the last entry, falls back there for an unknown leaf, and treats an explicitly nil leaf as an empty branch.
func BuildSessionContext(entries []SessionEntry, leafID ...*string) SessionContext {
	path := buildSessionPath(entries, leafID...)
	messages := BuildSessionProjection(path).Messages
	if messages == nil {
		messages = []agent.AgentMessage{}
	}
	thinking, model := GetSessionContextSettings(path)
	return SessionContext{Messages: messages, ThinkingLevel: thinking, Model: model}
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
		switch entry.Base.Type {
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
		switch entry.Base.Type {
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
func BuildContextEntries(entries []SessionEntry, leafID ...*string) []SessionEntry {
	result := buildContextEntries(buildSessionPath(entries, leafID...), SessionEntry.AsMessage)
	if result == nil {
		return []SessionEntry{}
	}
	return result
}

func buildSessionPath(entries []SessionEntry, leafID ...*string) []SessionEntry {
	if len(entries) == 0 || len(leafID) > 0 && leafID[0] == nil {
		return nil
	}
	byID := make(map[string]SessionEntry, len(entries))
	for _, entry := range entries {
		byID[entry.Base.ID] = entry
	}
	leaf := entries[len(entries)-1]
	if len(leafID) > 0 && *leafID[0] != "" {
		if selected, ok := byID[*leafID[0]]; ok {
			leaf = selected
		}
	}
	var path []SessionEntry
	for {
		path = append(path, leaf)
		if leaf.Base.ParentID == nil || *leaf.Base.ParentID == "" {
			break
		}
		parent, ok := byID[*leaf.Base.ParentID]
		if !ok {
			break
		}
		leaf = parent
	}
	slices.Reverse(path)
	return path
}
