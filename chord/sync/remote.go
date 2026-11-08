package sync

import (
	"context"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/sync/remote.ts

// JsonObject is a JSON object, the host's description of a session (SessionSyncStore.PutMeta): Pi's
// `{ [key: string]: unknown }`, which Go reads as the ordered JSON object of Chord's delta package.
type JsonObject = delta.JsonObject

// Seq is a store's commit sequence (`Store.LastSeq`, an outbox commit's Seq); 0 is before every commit.
type Seq = int64

// LogStart is the sequence before every commit: a store without commits is at it.
const LogStart Seq = 0

// BatchInfo is a slot of a session's remote history: the batch of outbox commits after After, up to To, uploaded by
// Device. A store creates each slot once and never replaces it, so the device whose upload creates it continues the
// session from After, and a device that finds it taken by another one holds a different history.
type BatchInfo struct {
	After  Seq
	To     Seq
	Device string
	// Archive: the body is a store archive at To instead of commits: the first upload of a session whose outbox does not
	// reach back to its first commit. Only the slot after 0 can be one.
	Archive bool
	// Folded: folding dropped its commits into a snapshot; the slot stays taken.
	Folded bool
}

// RemoteListing is what a remote store holds for one session. A snapshot is a store archive at its sequence, folded from
// batches.
type RemoteListing struct {
	Batches   []BatchInfo
	Snapshots []Seq
}

// PutBatchType is how PutBatch ended.
type PutBatchType string

const (
	// PutCreated: the slot was created.
	PutCreated PutBatchType = "created"
	// PutExists: the slot is taken (To, Device); by this device, after an upload whose response was lost, or by another.
	PutExists PutBatchType = "exists"
	// PutNotHead: no slot starts at After, and the history does not end there (Head): a slot there would branch the chain
	// inside a batch or leave a gap. The uploader's history is not the remote's from After on.
	PutNotHead PutBatchType = "not_head"
	// PutStaleEpoch: the upload's epoch is not the session's current one (Epoch): another device was given the right to
	// write the session since this one was. Who grants it, and when, is the remote's (RFC 0074's single writer); Chord only
	// reports it.
	PutStaleEpoch PutBatchType = "stale_epoch"
)

// PutBatchResult is the outcome of PutBatch; the members its Type names are set.
type PutBatchResult struct {
	Type   PutBatchType
	To     Seq
	Device string
	Head   Seq
	Epoch  int64
}

// PutBatchOptions are what an upload tells the remote besides its slot.
type PutBatchOptions struct {
	// Archive: the body is a store archive instead of commits.
	Archive bool
	// Epoch is the writer's epoch: a remote that issues epochs refuses a stale one (PutStaleEpoch); others ignore it. Nil
	// sends none.
	Epoch *int64
}

// SessionSummary is a session as the remote's listing describes it.
type SessionSummary struct {
	ID string
	// Head is where its history ends; LogStart for a session with a description and no history.
	Head Seq
	// Meta is the host's description of the session; nil when it has none.
	Meta *JsonObject
}

// SessionChanges are the sessions changed after a cursor, and the cursor to continue from.
type SessionChanges struct {
	Sessions []SessionSummary
	// Cursor is passed to the next call; opaque.
	Cursor string
	// More: more changes follow right away: call again with Cursor.
	More bool
}

// SessionSyncStore is the client's view of a remote store. Clients never delete or fold.
type SessionSyncStore interface {
	// PutBatch creates the slot after `after` unless it exists or the history does not end at `after`. body is a stream
	// read once; a remote that answers without reading it (exists, not_head) leaves it unread: the caller closes it.
	PutBatch(ctx context.Context, sessionID string, after, to Seq, device string, body SyncBody, options PutBatchOptions) (PutBatchResult, error)
	// GetBatch is a batch's body; nil when its slot is folded or absent.
	GetBatch(ctx context.Context, sessionID string, after Seq) (SyncBody, error)
	// GetSnapshot is a snapshot's body; nil when there is none.
	GetSnapshot(ctx context.Context, sessionID string, to Seq) (SyncBody, error)
	List(ctx context.Context, sessionID string) (RemoteListing, error)
	// Sessions are the sessions changed after cursor (all of them without one). A remote may also list sessions that did
	// not change, so a caller compares what it gets with what it has.
	Sessions(ctx context.Context, cursor *string) (SessionChanges, error)
	// PutMeta is the host's description of a session (title, location, which device started it), outside its history: the
	// last write wins, and it may be ahead of or behind the batches.
	PutMeta(ctx context.Context, sessionID string, meta *JsonObject) error
	// GetMeta is the description; nil when there is none.
	GetMeta(ctx context.Context, sessionID string) (*JsonObject, error)
}

// SessionSyncMaintenance is the remote's own side: folding history into snapshots, which clients never do.
type SessionSyncMaintenance interface {
	SessionSyncStore
	PutSnapshot(ctx context.Context, sessionID string, to Seq, body SyncBody) error
	// FoldBatches drops the bodies of these slots, keeping the slots taken so nobody can create them again.
	FoldBatches(ctx context.Context, sessionID string, afters []Seq) error
	DeleteSnapshots(ctx context.Context, sessionID string, snapshots []Seq) error
}

// RemoteHistory is what a copy applies to reach the remote's head.
type RemoteHistory struct {
	// Snapshot is the snapshot a restore starts from; nil for none.
	Snapshot *Seq
	// Batches are the batches to apply in order. The first may start before the copy's head, when a pull stopped inside
	// it; its commits at or below the head are skipped.
	Batches []BatchInfo
	// Head is where applying them ends.
	Head Seq
}

// HistoryUnavailableError is the remote's history no longer continuing from a copy's head, after folding: restore into a
// fresh store.
type HistoryUnavailableError struct{ Head Seq }

func (err *HistoryUnavailableError) Error() string {
	return fmt.Sprintf("The remote history does not continue from %d", err.Head)
}

// chainOf is the slots by the sequence they start at: slots are created once, so at most one starts at each.
func chainOf(listing RemoteListing) map[Seq]BatchInfo {
	chain := make(map[Seq]BatchInfo, len(listing.Batches))
	for _, batch := range listing.Batches {
		chain[batch.After] = batch
	}
	return chain
}

// RemoteHistoryOf is the history a copy at `from` applies, or a restore without `from` (nil, or LogStart). The history is
// a single chain.
func RemoteHistoryOf(listing RemoteListing, from *Seq) RemoteHistory {
	chain := chainOf(listing)
	var snapshot *Seq
	var at Seq
	batches := []BatchInfo{}
	if from == nil || *from == LogStart {
		if len(listing.Snapshots) > 0 {
			newest := slices.Max(listing.Snapshots)
			snapshot = &newest
			at = newest
		}
	} else {
		at = *from
		// A pull that stopped inside a batch finishes it; only one batch can contain the head.
		if index := slices.IndexFunc(listing.Batches, func(batch BatchInfo) bool { return batch.After < *from && *from < batch.To }); index >= 0 {
			batches = append(batches, listing.Batches[index])
			at = listing.Batches[index].To
		}
	}
	for next, ok := chain[at]; ok; next, ok = chain[at] {
		batches = append(batches, next)
		at = next.To
	}
	return RemoteHistory{Snapshot: snapshot, Batches: batches, Head: at}
}

// RemoteHead is the furthest head the remote's history reaches.
func RemoteHead(listing RemoteListing) Seq {
	chain := chainOf(listing)
	at := LogStart
	for next, ok := chain[at]; ok; next, ok = chain[at] {
		at = next.To
	}
	return at
}
