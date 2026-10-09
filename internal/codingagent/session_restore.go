// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"errors"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// Ports packages/coding-agent/src/core/session-manager.ts (_loadEntries and _buildIndex).
// A header owns restored identity. Headerless entries are current-version data.
func newSessionFromEntries(cwd, id string, entries []FileEntry) (*Session, error) {
	header, err := findSessionHeader(entries)
	if err != nil {
		return nil, err
	}
	if header != nil && header.Version < CurrentSessionVersion {
		if err := MigrateSessionEntries(entries); err != nil {
			return nil, err
		}
		// The migrated header is the record MigrateSessionEntries wrote, found as before by its type: its version is the current one in its
		// fields and in the record it writes (a header carrying the version-2 record would report version 2 to an extension's getHeader).
		if header, err = findSessionHeader(entries); err != nil {
			return nil, err
		}
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
		s, err = newSessionWithOptions(cwd, option, "")
		if err != nil {
			return nil, err
		}
	}
	for _, record := range entries {
		entry, isEntry := record.(SessionEntry)
		if !isEntry {
			continue
		}
		if _, undescribed := entry.(RawEntry); undescribed {
			var base SessionEntryBase
			if err := json.Unmarshal(entry.Raw(), &base); err != nil {
				return nil, err
			}
		}
		base := entry.Base()
		if base.Type == "session" {
			continue
		}
		s.stats.add(entry.Raw(), base.Type)
		s.entries = append(s.entries, entry)
		s.byID[base.ID] = entry
		entryID := base.ID
		s.leafID = &entryID
	}
	s.hasConversation = s.stats.stats.UserMessages > 0 || s.stats.stats.AssistantMessages > 0
	return s, nil
}

// findSessionHeader is the first record of type "session", nil when there is none. _loadEntries finds the header by its type alone, so a
// record of that type whose id is missing or ill-typed, which is no SessionHeader member, still owns the identity.
func findSessionHeader(entries []FileEntry) (*SessionHeader, error) {
	for _, entry := range entries {
		if entry.Raw() == nil {
			continue
		}
		switch record := entry.(type) {
		case SessionHeader:
			return &record, nil
		case SessionEntry:
			if record.Base().Type != "session" {
				continue
			}
			header, err := sessionentry.DecodeSessionHeader(record.Raw())
			if err != nil {
				return nil, err
			}
			return &header, nil
		}
	}
	return nil, nil
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

// MigrateSessionEntries applies Pi's v1 tree and v2 custom-message migrations, preserving JSON member order. The version is the first session header's `version`, 1 when the entries have no header or the header omits it (session-manager.ts migrateToCurrentVersion); the entries migrate in place and entries already at the current version stay unchanged.
func MigrateSessionEntries(entries []FileEntry) error {
	version := 1
	for _, entry := range entries {
		raw := entry.Raw()
		var header struct {
			Type    string `json:"type"`
			Version *int   `json:"version"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return err
		}
		if header.Type == "session" {
			if header.Version != nil {
				version = *header.Version
			}
			break
		}
	}
	if version >= CurrentSessionVersion {
		return nil
	}
	var previous *string
	ids := make(map[string]struct{})
	for i, entry := range entries {
		raw := entry.Raw()
		var probe struct {
			SessionEntryBase
			FirstKeptEntryIndex json.RawMessage `json:"firstKeptEntryIndex"`
			Message             json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return err
		}
		var err error
		if probe.Type == "session" {
			raw, err = replaceJSONField(raw, "version", CurrentSessionVersion)
			if err != nil {
				return err
			}
			entries[i] = sessionentry.DecodeFileEntry(raw)
			continue
		}
		if version < 2 {
			id, idErr := generateID(ids)
			if idErr != nil {
				return idErr
			}
			ids[id] = struct{}{}
			raw, err = replaceJSONField(raw, "id", id)
			if err != nil {
				return err
			}
			raw, err = replaceJSONField(raw, "parentId", previous)
			if err != nil {
				return err
			}
			previous = &id
			entries[i] = sessionentry.DecodeFileEntry(raw)
			// upstream: session-manager.ts migrateV1ToV2 (`typeof comp.firstKeptEntryIndex === "number"`, then `entries[index]`): another type is left alone, and a number that is no entry index (fractional, negative, past the end) only drops the member.
			var indexNumber float64
			if probe.Type == "compaction" && len(probe.FirstKeptEntryIndex) > 0 && probe.FirstKeptEntryIndex[0] != '"' && string(probe.FirstKeptEntryIndex) != "null" && json.Unmarshal(probe.FirstKeptEntryIndex, &indexNumber) == nil {
				if index := int(indexNumber); float64(index) == indexNumber && index >= 0 && index < len(entries) {
					var target SessionEntryBase
					if err := json.Unmarshal(entries[index].Raw(), &target); err != nil {
						return err
					}
					if target.Type != "session" && target.ID != "" {
						raw, err = replaceJSONField(raw, "firstKeptEntryId", target.ID)
						if err != nil {
							return err
						}
					}
				}
				raw, err = rewriteJSONField(raw, "firstKeptEntryIndex", nil, true)
				if err != nil {
					return err
				}
			}
		}
		if version < 3 && probe.Type == "message" && len(probe.Message) != 0 {
			var message struct {
				Role string `json:"role"`
			}
			if err := json.Unmarshal(probe.Message, &message); err != nil {
				return err
			}
			if message.Role == "hookMessage" {
				messageRaw, err := replaceJSONField(probe.Message, "role", "custom")
				if err != nil {
					return err
				}
				raw, err = replaceJSONField(raw, "message", json.RawMessage(messageRaw))
				if err != nil {
					return err
				}
			}
		}
		entries[i] = sessionentry.DecodeFileEntry(raw)
	}
	return nil
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
