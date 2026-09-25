// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package search_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/search"
)

var errSearch = errors.New("search failed")

type fakeSessionSearchService struct {
	sessions      []search.SessionSearchHit
	queries       []search.SearchQuery
	notifications []string
	removed       []string
	syncErr       error
	removeErr     error
	closeErr      error
}

func (service *fakeSessionSearchService) SearchSessions(query search.SearchQuery) ([]search.SessionSearchHit, error) {
	service.queries = append(service.queries, query)
	return service.sessions, nil
}

func (service *fakeSessionSearchService) Sync() error {
	return service.syncErr
}

func (service *fakeSessionSearchService) Notify(sessionID string) {
	service.notifications = append(service.notifications, sessionID)
}

func (service *fakeSessionSearchService) Remove(sessionID string) error {
	service.removed = append(service.removed, sessionID)
	return service.removeErr
}

func (service *fakeSessionSearchService) Close() error {
	return service.closeErr
}

type fakeEntrySearchService struct {
	*fakeSessionSearchService
	entries []search.EntrySearchHit
}

func (service *fakeEntrySearchService) SearchEntries(query search.SearchQuery) ([]search.EntrySearchHit, error) {
	service.queries = append(service.queries, query)
	return service.entries, nil
}

var (
	_ search.SessionSearchService = (*fakeSessionSearchService)(nil)
	_ search.SessionSearchService = (*fakeEntrySearchService)(nil)
	_ search.EntrySearchService   = (*fakeEntrySearchService)(nil)
)

func TestSessionSearchServiceContract(t *testing.T) {
	t.Parallel()

	limit := 7
	score := 0.75
	snippet := "matching text"
	query := search.SearchQuery{Text: "needle", Limit: &limit}
	sessions := []search.SessionSearchHit{{
		SessionID: "session-1",
		Score:     &score,
		Top: &search.SessionSearchTop{
			EntryID:   "entry-1",
			Snippet:   &snippet,
			Timestamp: 1_700_000_000_000,
		},
	}}
	service := &fakeSessionSearchService{sessions: sessions}

	got, err := service.SearchSessions(query)
	if err != nil {
		t.Fatalf("SearchSessions() error = %v", err)
	}
	if !reflect.DeepEqual(got, sessions) {
		t.Fatalf("SearchSessions() = %#v, want %#v", got, sessions)
	}
	if !reflect.DeepEqual(service.queries, []search.SearchQuery{query}) {
		t.Fatalf("SearchSessions() queries = %#v, want %#v", service.queries, []search.SearchQuery{query})
	}

	service.Notify("session-1")
	if !reflect.DeepEqual(service.notifications, []string{"session-1"}) {
		t.Fatalf("Notify() sessions = %#v, want [session-1]", service.notifications)
	}
	if err := service.Remove("session-1"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if !reflect.DeepEqual(service.removed, []string{"session-1"}) {
		t.Fatalf("Remove() sessions = %#v, want [session-1]", service.removed)
	}
	if err := service.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestSearchEntriesIsOptional(t *testing.T) {
	t.Parallel()

	base := &fakeSessionSearchService{}
	if _, ok := any(base).(search.EntrySearchService); ok {
		t.Fatal("SessionSearchService unexpectedly requires entry search")
	}

	snippet := "entry match"
	score := 1.25
	entries := []search.EntrySearchHit{{
		SessionID: "session-1",
		EntryID:   "entry-1",
		Timestamp: 1_700_000_000_001,
		Snippet:   &snippet,
		Score:     &score,
	}}
	service := &fakeEntrySearchService{
		fakeSessionSearchService: &fakeSessionSearchService{},
		entries:                  entries,
	}
	got, err := service.SearchEntries(search.SearchQuery{Text: "entry"})
	if err != nil {
		t.Fatalf("SearchEntries() error = %v", err)
	}
	if !reflect.DeepEqual(got, entries) {
		t.Fatalf("SearchEntries() = %#v, want %#v", got, entries)
	}
}

func TestSessionSearchServicePropagatesOperationErrors(t *testing.T) {
	t.Parallel()

	service := &fakeSessionSearchService{
		syncErr:   errSearch,
		removeErr: errSearch,
		closeErr:  errSearch,
	}
	if err := service.Sync(); !errors.Is(err, errSearch) {
		t.Fatalf("Sync() error = %v, want %v", err, errSearch)
	}
	if err := service.Remove("session-1"); !errors.Is(err, errSearch) {
		t.Fatalf("Remove() error = %v, want %v", err, errSearch)
	}
	if err := service.Close(); !errors.Is(err, errSearch) {
		t.Fatalf("Close() error = %v, want %v", err, errSearch)
	}
}
