// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// pi: packages/durable/src/storage/scan.ts scanStart, storage/memory.ts, storage/sqlite/storage.ts (42a3497d and later): a read the caller's request got
// wrong throws StorageRequestError, which fails only that read; the facade revives the name and leaves the Session open.
func TestInvalidReadRequestsAreNamedStorageRequestError(t *testing.T) {
	query := func(order any) *jv.Object {
		q := jv.NewObject()
		q.Set("order", order)
		return q
	}
	cursor := func(after any, order any) *jv.Object {
		c := jv.NewObject()
		c.Set("after", after)
		if order != nil {
			c.Set("order", order)
		}
		return c
	}
	m := newMirror()
	h := &handler{mir: m}
	docsCursor := jv.NewObject()
	docsCursor.Set("query", func() *jv.Object { q := jv.NewObject(); q.Set("at", "current"); return q }())
	docsCursor.Set("cursor", cursor("x", nil))
	for name, err := range map[string]error{
		"an invalid scan order":             third(scanStart(query("sideways"), nil, "ascending")),
		"a cursor without an ID":            third(scanStart(jv.NewObject(), cursor("x", nil), "ascending")),
		"a cursor with an invalid order":    third(scanStart(jv.NewObject(), cursor(3.0, "up"), "ascending")),
		"a cursor of the other order":       third(scanStart(query("descending"), cursor(3.0, "ascending"), "ascending")),
		"an unknown conversation (entry)":   func() error { _, _, err := m.EntryIn(9, 1); return err }(),
		"an unknown conversation (marker)":  func() error { _, err := m.LatestHeadMarker(9, 1); return err }(),
		"an unknown conversation (context)": func() error { _, err := h.contextView(9, 0); return err }(),
		"an unknown conversation (bounds)":  func() error { _, _, _, err := h.contextBounds(9, 0); return err }(),
		"a document cursor without an ID":   func() error { _, err := h.storageAPI("scanDocuments", docsCursor); return err }(),
		"an unknown conversation (scan)": func() error {
			_, _, err := m.scanEntries(session.EntryQuery{ConversationID: 9}, 5, 0, false)
			return err
		}(),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		if got := errorName(err); got != "StorageRequestError" {
			t.Errorf("%s: named %q (%v), want StorageRequestError", name, got, err)
		}
	}
}

func third[A any, B any](_ A, _ B, c error) error { return c }
