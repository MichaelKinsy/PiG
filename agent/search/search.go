// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package search defines the session and entry search contracts exported by the agent package.
package search

// SearchQuery contains the text to search and an optional result limit.
type SearchQuery struct {
	Text  string
	Limit *int
}

// SessionSearchTop is the highest-ranking entry attached to a session hit.
type SessionSearchTop struct {
	EntryID   string
	Snippet   *string
	Timestamp float64
}

// SessionSearchHit is one session-level search result.
type SessionSearchHit struct {
	SessionID string
	Score     *float64
	Top       *SessionSearchTop
}

// EntrySearchHit is one entry-level search result.
type EntrySearchHit struct {
	SessionID string
	EntryID   string
	Timestamp float64
	Snippet   *string
	Score     *float64
}

// SessionSearchService searches and maintains a session search index. Asynchronous upstream operations block until completion and return errors.
type SessionSearchService interface {
	SearchSessions(query SearchQuery) ([]SessionSearchHit, error)
	Sync() error
	Notify(sessionID string)
	Remove(sessionID string) error
	Close() error
}

// EntrySearchService is the optional entry-search capability of a SessionSearchService implementation.
type EntrySearchService interface {
	SearchEntries(query SearchQuery) ([]EntrySearchHit, error)
}
