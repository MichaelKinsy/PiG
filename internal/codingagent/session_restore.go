// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"errors"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Ports packages/coding-agent/src/core/session-manager.ts (_loadEntries and _buildIndex).
// A header owns restored identity. Headerless entries are current-version data.
func newSessionFromEntries(cwd, id string, entries []json.RawMessage) (*Session, error) {
	var header *SessionHeader
	for _, raw := range entries {
		var base SessionEntryBase
		if err := json.Unmarshal(raw, &base); err != nil {
			return nil, err
		}
		if base.Type == "session" {
			var h SessionHeader
			if err := json.Unmarshal(raw, &h); err != nil {
				return nil, err
			}
			header = &h
			break
		}
	}
	if header != nil && header.Version < CurrentSessionVersion {
		var err error
		entries, err = migrateSessionEntries(entries, header.Version)
		if err != nil {
			return nil, err
		}
		header.Version = CurrentSessionVersion
	}
	var s *Session
	if header != nil {
		s = NewSession(header.ID, cwd)
		s.header = *header
	} else {
		var option *string
		if id != "" {
			option = &id
		}
		var err error
		s, err = newSessionWithOptions(cwd, option, "")
		if err != nil {
			return nil, err
		}
	}
	for _, raw := range entries {
		var base SessionEntryBase
		if err := json.Unmarshal(raw, &base); err != nil {
			return nil, err
		}
		if base.Type == "session" {
			continue
		}
		s.stats.add(raw, base.Type)
		entry := NewSessionEntry(raw, base)
		s.entries = append(s.entries, entry)
		s.byID[base.ID] = entry
		entryID := base.ID
		s.leafID = &entryID
	}
	s.hasConversation = s.stats.stats.UserMessages > 0 || s.stats.stats.AssistantMessages > 0
	return s, nil
}

var sessionIDPattern = lazyregexp.New(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// newSessionWithOptions preserves the distinction between an omitted and an explicit session ID.
func newSessionWithOptions(cwd string, id *string, parentSession string) (*Session, error) {
	var value string
	if id == nil {
		var err error
		value, err = generateSessionID()
		if err != nil {
			return nil, err
		}
	} else {
		if !sessionIDPattern.MatchString(*id) {
			return nil, errors.New("Session id must be non-empty, contain only alphanumeric characters, '-', '_', and '.', and start and end with an alphanumeric character")
		}
		value = *id
	}
	s := NewSession(value, cwd)
	s.header.ParentSession = parentSession
	return s, nil
}

// migrateSessionEntries applies Pi's v1 tree and v2 custom-message migrations, preserving JSON member order.
func migrateSessionEntries(entries []json.RawMessage, version int) ([]json.RawMessage, error) {
	entries = slices.Clone(entries)
	var previous *string
	ids := make(map[string]struct{})
	for i, raw := range entries {
		var probe struct {
			SessionEntryBase
			FirstKeptEntryIndex *int            `json:"firstKeptEntryIndex"`
			Message             json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, err
		}
		var err error
		if probe.Type == "session" {
			entries[i], err = replaceJSONField(raw, "version", CurrentSessionVersion)
			if err != nil {
				return nil, err
			}
			continue
		}
		if version < 2 {
			id, idErr := generateID(ids)
			if idErr != nil {
				return nil, idErr
			}
			ids[id] = struct{}{}
			raw, err = replaceJSONField(raw, "id", id)
			if err != nil {
				return nil, err
			}
			raw, err = replaceJSONField(raw, "parentId", previous)
			if err != nil {
				return nil, err
			}
			previous = &id
			entries[i] = raw
			if index := probe.FirstKeptEntryIndex; probe.Type == "compaction" && index != nil {
				if *index >= 0 && *index < len(entries) {
					var target SessionEntryBase
					if err := json.Unmarshal(entries[*index], &target); err != nil {
						return nil, err
					}
					if target.Type != "session" && target.ID != "" {
						raw, err = replaceJSONField(raw, "firstKeptEntryId", target.ID)
						if err != nil {
							return nil, err
						}
					}
				}
				raw, err = rewriteJSONField(raw, "firstKeptEntryIndex", nil, true)
				if err != nil {
					return nil, err
				}
			}
		}
		if version < 3 && probe.Type == "message" && len(probe.Message) != 0 {
			var message struct {
				Role string `json:"role"`
			}
			if err := json.Unmarshal(probe.Message, &message); err != nil {
				return nil, err
			}
			if message.Role == "hookMessage" {
				messageRaw, err := replaceJSONField(probe.Message, "role", "custom")
				if err != nil {
					return nil, err
				}
				raw, err = replaceJSONField(raw, "message", json.RawMessage(messageRaw))
				if err != nil {
					return nil, err
				}
			}
		}
		entries[i] = raw
	}
	return entries, nil
}

func generateID(ids map[string]struct{}) (string, error) {
	for range 100 {
		id, err := uuid.NewRandom()
		if err != nil {
			return "", err
		}
		short := id.String()[:8]
		if _, found := ids[short]; !found {
			return short, nil
		}
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
