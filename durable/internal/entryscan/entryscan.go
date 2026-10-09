// Package entryscan is the read-only range seam between the Harness context derivation and the storages that retain
// decoded entries.
package entryscan

import (
	"context"

	"github.com/MichaelKinsy/PiG/durable"
)

// Ranger is implemented by a storage that serves a conversation's visible entry range from decoded entries it retains.
type Ranger interface {
	// VisibleEntryRange returns the entries of query's conversation, through its fork ancestry, whose IDs lie within the
	// query's inclusive bounds, oldest first. It is the ascending concatenation of the pages ScanEntries returns for the
	// same query.
	//
	// The result shares its entries, and the values they reference, with the storage: callers must not modify any of
	// it. Storage.ScanEntries keeps returning detached copies.
	VisibleEntryRange(ctx context.Context, query durable.EntryQuery) ([]durable.EntryRecord, error)
	// CountVisibleEntries returns len(VisibleEntryRange(ctx, query)) without building the range.
	CountVisibleEntries(ctx context.Context, query durable.EntryQuery) (int, error)
}
