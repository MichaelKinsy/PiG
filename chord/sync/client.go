package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/src/sync/client.ts
//
// Session sync over a Chord store's outbox: upload the commits after the last sequence a copy shares with a remote into a
// slot the remote creates once, pull a copy forward with ApplyBatch, restore from an archive, fold history into snapshots.
// The remote and the host's sync state are interfaces (remote.go).
//
// Bounded memory: bodies are streams (batch.go). An upload reads the outbox a page at a time and stops a batch at
// BatchBytes; a pull applies a batch in chunks of at most ApplyBytes; a snapshot is exported and imported as it streams.
// Nothing holds a whole batch, snapshot or store.
//
// Async contract: Pi's functions are async and run on one thread; these block until their promise would settle, on the
// caller's goroutine, and start none. Context comes first, as Go convention, where Pi passes it last. A stream body is
// closed on every path that stops reading it (Pi's generator `return()`).

const (
	// outboxPage is the commits read from the outbox per page at most.
	outboxPage = 100
	// pageBytes is about how much one outbox page holds: the count of the next page follows the size of the commits read.
	pageBytes = 1024 * 1024
	// BatchBytes is where an upload ends a batch: once its lines (before compression) reach this size. One commit larger
	// than this is a batch of its own.
	BatchBytes = 8 * 1024 * 1024
	// ApplyBytes is how much of a batch a pull holds before it applies it: commits are applied in chunks of about this size.
	ApplyBytes = 4 * 1024 * 1024
)

// SyncState is what a host keeps for each copy of a session it syncs: the last sequence the copy shares with the remote.
// Commits after it are the copy's own and not uploaded yet. When this is lost, a copy without commits of its own takes the
// remote's head; one with commits finds out by uploading.
type SyncState struct {
	// SyncedHead is the last sequence the copy shares with the remote; nil when unknown.
	SyncedHead *Seq
	// Pulling is set while a pull applies a batch or snapshot ending here: a local head up to it holds the remote's commits,
	// not the copy's own, even when the pull stopped partway.
	Pulling *Seq
}

// sharedHead is the last sequence a copy shares with the remote, counting commits an interrupted pull applied.
func sharedHead(state *SyncState, local Seq) Seq {
	if state != nil && state.Pulling != nil && local <= *state.Pulling {
		return local
	}
	if state != nil && state.SyncedHead != nil {
		return *state.SyncedHead
	}
	return LogStart
}

// SyncStateStore keeps the SyncState of each session; Get is nil for a session it knows nothing of.
type SyncStateStore interface {
	Get(ctx context.Context, sessionID string) (*SyncState, error)
	Set(ctx context.Context, sessionID string, state SyncState) error
}

// StoreHead is a store's head as a sequence: `LastSeq`, 0 for a store without commits.
func StoreHead(s store.Store) Seq { return s.LastSeq() }

// OutboxUnavailableError is the store keeping no outbox, or its outbox no longer holding the commits a sync needs.
type OutboxUnavailableError struct{ Message string }

func (err *OutboxUnavailableError) Error() string { return err.Message }

func outboxOf(s store.Store) (*store.Outbox, error) {
	outbox := s.Outbox()
	if outbox == nil {
		return nil, &OutboxUnavailableError{"The session's store has no outbox: open it with { outbox: true } to sync it"}
	}
	return outbox, nil
}

// ReadOutbox is the outbox commits after `after`, a page of at most limit.
func ReadOutbox(ctx context.Context, s store.Store, after Seq, limit int64) ([]store.OutboxCommit, error) {
	outbox, err := outboxOf(s)
	if err != nil {
		return nil, err
	}
	if floor := outbox.Floor(); after < floor {
		return nil, &OutboxUnavailableError{fmt.Sprintf("The outbox starts after %d, past the synced head %d", floor, after)}
	}
	return outbox.Read(after, limit, ctx)
}

// UploadType is how an upload ended.
type UploadType string

const (
	// Uploaded: the commits up to Head were uploaded.
	Uploaded UploadType = "uploaded"
	// Idle: nothing was committed since the last upload.
	Idle UploadType = "idle"
	// Diverged: another device continued the session from At first. This copy's commits after At are a different history:
	// the host renames the copy to a new session (forked from this one at At) and uploads it there, and the session itself
	// restores into a fresh store.
	Diverged UploadType = "diverged"
	// Behind: the copy lost commits it had uploaded, for example to a power loss: its Head is below SyncedHead. Its next
	// commits would reuse their sequences, so nothing is uploaded. Pull what it lost before the copy commits again; a pull
	// applies from the local head.
	Behind UploadType = "behind"
	// StaleEpoch: the remote refused this device's Epoch: another device holds the right to write the session now. Like
	// Diverged, the copy's commits after At are a different history, so the host forks the copy (S7).
	StaleEpoch UploadType = "stale_epoch"
)

// UploadResult is how an upload ended; the members its Type names are set.
type UploadResult struct {
	Type       UploadType
	Head       Seq
	At         Seq
	SyncedHead Seq
	Epoch      int64
}

// UploadOptions are what UploadSession takes.
type UploadOptions struct {
	SessionID string
	Device    string
	// Source is the copy's store, with its outbox on.
	Source store.Store
	Store  SessionSyncStore
	State  SyncStateStore
	// Epoch is the writer's epoch, from whatever grants it (the remote, Radius); sent with every slot. Nil sends none.
	Epoch *int64
}

// UploadSession uploads what this copy committed since it last shared the remote's history, from the store's outbox, and
// trims the outbox through what the remote acknowledged. Every device continues a session as its own; whichever creates
// the slot first continues the session, so an upload that is acknowledged stays.
//
// A session the remote does not have yet, whose outbox does not reach back to its first commit (turned on later, or
// trimmed, as for a copy renamed after it diverged), starts with an archive slot: the store's `Export` at its head.
//
// A slot this device created already is an upload whose response was lost: the remote has the history up to its end, and
// the upload goes on from there.
func UploadSession(ctx context.Context, options UploadOptions) (UploadResult, error) {
	outbox, err := outboxOf(options.Source)
	if err != nil {
		return UploadResult{}, err
	}
	saved, err := options.State.Get(ctx, options.SessionID)
	if err != nil {
		return UploadResult{}, err
	}
	synced := sharedHead(saved, StoreHead(options.Source))
	var uploaded *Seq
	for {
		// Read again on every pass: a slot this device already holds may end past what the copy still has.
		head := StoreHead(options.Source)
		if head < synced {
			return UploadResult{Type: Behind, Head: head, SyncedHead: synced}, nil
		}
		var to Seq
		var body SyncBody
		archive := false
		if synced == LogStart && outbox.Floor() > 0 {
			snapshot, err := exportSnapshot(ctx, options.Source)
			if err != nil {
				return UploadResult{}, err
			}
			to, body, archive = snapshot.to, snapshot.body, true
		} else {
			end, ok, err := outboxRange(ctx, options.Source, synced)
			if err != nil {
				return UploadResult{}, err
			}
			if !ok {
				if uploaded == nil {
					return UploadResult{Type: Idle}, nil
				}
				return UploadResult{Type: Uploaded, Head: *uploaded}, nil
			}
			to = end
			body = EncodeBatch(newOutboxCommits(options.Source, synced, to))
		}
		after := synced
		result, err := options.Store.PutBatch(ctx, options.SessionID, after, to, options.Device, body, PutBatchOptions{Archive: archive, Epoch: options.Epoch})
		// A remote that answered without reading the body leaves an archive export holding the commit queue.
		err = errors.Join(err, body.Close())
		if err != nil {
			return UploadResult{}, err
		}
		switch {
		case result.Type == PutStaleEpoch:
			return UploadResult{Type: StaleEpoch, At: after, Epoch: result.Epoch}, nil
		case result.Type == PutNotHead:
			// A history that does not end at `after` is another one too: the remote never held this copy's past `after`.
			return UploadResult{Type: Diverged, At: after}, nil
		case result.Type == PutExists && result.Device != options.Device:
			return UploadResult{Type: Diverged, At: after}, nil
		}
		if result.Type == PutCreated {
			synced = to
		} else {
			synced = result.To
		}
		shared := synced
		if err := options.State.Set(ctx, options.SessionID, SyncState{SyncedHead: &shared}); err != nil {
			return UploadResult{}, err
		}
		if trimmable := min(synced, options.Source.LastSeq()); trimmable > outbox.Floor() {
			if err := outbox.Trim(trimmable, ctx); err != nil {
				return UploadResult{}, err
			}
		}
		if result.Type == PutCreated {
			reached := to
			uploaded = &reached
		}
	}
}

// outboxRange is where the next batch after `after` ends: the outbox read a page at a time, holding one page, until the
// lines reach BatchBytes (one commit at least). ok is false when the outbox has nothing after `after`.
func outboxRange(ctx context.Context, s store.Store, after Seq) (to Seq, ok bool, err error) {
	pages := &outboxPages{store: s, after: after, limit: outboxPage}
	bytes := 0
	for {
		page, err := pages.next(ctx)
		if err != nil {
			return 0, false, err
		}
		if len(page) == 0 {
			return to, ok, nil
		}
		for _, commit := range page {
			size, err := encodedSize(commit)
			if err != nil {
				return 0, false, err
			}
			if ok && bytes+size > BatchBytes {
				return to, true, nil
			}
			bytes += size
			to, ok = commit.Seq, true
		}
		if bytes >= BatchBytes {
			return to, ok, nil
		}
	}
}

// outboxCommits is the outbox commits after `after` through `to`, a page at a time, as the body is read.
type outboxCommits struct {
	pages *outboxPages
	at    Seq
	to    Seq
	page  []store.OutboxCommit
	done  bool
}

func newOutboxCommits(s store.Store, after, to Seq) *outboxCommits {
	return &outboxCommits{pages: &outboxPages{store: s, after: after, limit: outboxPage}, at: after, to: to}
}

func (source *outboxCommits) Next(ctx context.Context) (store.OutboxCommit, bool, error) {
	for !source.done {
		if len(source.page) == 0 {
			if source.at >= source.to {
				source.done = true
				break
			}
			page, err := source.pages.next(ctx)
			if err != nil {
				source.done = true
				return store.OutboxCommit{}, false, err
			}
			if len(page) == 0 {
				source.done = true
				return store.OutboxCommit{}, false, &OutboxUnavailableError{fmt.Sprintf("The outbox ends at %d, before %d", source.at, source.to)}
			}
			source.page = page
		}
		commit := source.page[0]
		source.page = source.page[1:]
		if commit.Seq > source.to {
			source.done = true
			break
		}
		source.at = commit.Seq
		return commit, true, nil
	}
	return store.OutboxCommit{}, false, nil
}

func (source *outboxCommits) Close() error {
	source.done = true
	return nil
}

// outboxPages is pages of the outbox after a sequence, each about pageBytes: the outbox reads by count, so the next page's
// count follows the commits read so far (outboxPage to start, one commit at least). A run of large commits reads one at a
// time instead of outboxPage of them at once.
type outboxPages struct {
	store store.Store
	after Seq
	limit int64
	done  bool
}

func (pages *outboxPages) next(ctx context.Context) ([]store.OutboxCommit, error) {
	if pages.done {
		return nil, nil
	}
	page, err := ReadOutbox(ctx, pages.store, pages.after, pages.limit)
	if err != nil {
		return nil, err
	}
	if int64(len(page)) < pages.limit {
		pages.done = true
	}
	if len(page) == 0 {
		return page, nil
	}
	pages.after = page[len(page)-1].Seq
	total := 0
	for _, commit := range page {
		size, err := encodedSize(commit)
		if err != nil {
			return nil, err
		}
		total += size
	}
	average := max(1, total/len(page))
	pages.limit = max(1, min(outboxPage, int64(pageBytes/average)))
	return page, nil
}

// encodedSize is a commit's line size before compression, as BatchBytes counts it.
func encodedSize(commit store.OutboxCommit) (int, error) {
	text, err := appendWrites(nil, commit)
	if err != nil {
		return 0, err
	}
	return len(text) + 64, nil
}

// exportedSnapshot is a snapshot of a store being exported: where it ends, its body, and closing it for when the body is
// not read to the end.
type exportedSnapshot struct {
	to   Seq
	body SyncBody
}

// exportSnapshot is the store's state as a streamed snapshot body. The export holds the store's commit queue from its first
// part until the body is read to the end or closed, so `to` (its manifest's LastSeq) is exactly what it holds.
func exportSnapshot(ctx context.Context, s store.Store) (exportedSnapshot, error) {
	export, err := s.Export(ctx)
	if err != nil {
		return exportedSnapshot{}, err
	}
	first, ok, err := export.Next(ctx)
	if err != nil {
		return exportedSnapshot{}, errors.Join(err, export.Close())
	}
	manifest, isManifest := first.(*store.ManifestPart)
	if !ok || !isManifest {
		return exportedSnapshot{}, errors.Join(errors.New("A store export does not start with its manifest"), export.Close())
	}
	return exportedSnapshot{to: manifest.LastSeq, body: EncodeSnapshot(&manifestFirst{manifest: manifest, rest: export})}, nil
}

// manifestFirst is the parts of an export whose first part was taken: the manifest, then the rest.
type manifestFirst struct {
	manifest store.ArchivePart
	rest     store.PartIterator
}

func (parts *manifestFirst) Next(ctx context.Context) (store.ArchivePart, bool, error) {
	if parts.manifest != nil {
		manifest := parts.manifest
		parts.manifest = nil
		return manifest, true, nil
	}
	return parts.rest.Next(ctx)
}

func (parts *manifestFirst) Close() error { return parts.rest.Close() }

// applyFunc applies commits to a store: store.ApplyBatch.
type applyFunc func(target *store.ChordStore, commits []store.OutboxCommit, ctx context.Context) error

// applyBody applies a batch body to s as it streams, in chunks of about ApplyBytes, skipping the commits the store has (a
// pull resuming inside a batch). Each chunk is one ApplyBatch transaction; a pull stopped between chunks resumes after the
// last applied one.
func applyBody(ctx context.Context, s *store.ChordStore, body SyncBody, apply applyFunc) error {
	lines := DecodeBatchLines(body)
	defer lines.Close()
	var chunk []store.OutboxCommit
	bytes := 0
	for {
		line, ok, err := lines.Next(ctx)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		if line.Commit.Seq <= s.LastSeq() {
			continue
		}
		chunk = append(chunk, line.Commit)
		bytes += line.Bytes
		if bytes >= ApplyBytes {
			if err := apply(s, chunk, ctx); err != nil {
				return err
			}
			chunk, bytes = nil, 0
		}
	}
	if len(chunk) > 0 {
		return apply(s, chunk, ctx)
	}
	return nil
}

// UnsyncedCommitsError is this copy having commits it has not uploaded: upload them first, which may find that the copy
// diverged.
type UnsyncedCommitsError struct {
	SessionID  string
	LocalHead  Seq
	SyncedHead Seq
}

func (err *UnsyncedCommitsError) Error() string {
	return fmt.Sprintf("This copy of session %s is at %d, past what it shares with the remote (%d)", err.SessionID, err.LocalHead, err.SyncedHead)
}

// LocalCopy is the copy a pull applies to: an open store (Store), or an empty backend (Backend) to restore the session
// into.
type LocalCopy struct {
	Store   store.Store
	Backend *store.Backend
}

// PullOptions are what PullSession takes.
type PullOptions struct {
	SessionID string
	Store     SessionSyncStore
	Local     LocalCopy
	State     SyncStateStore
	// apply replaces store.ApplyBatch where a test stops a pull between the chunks of a batch.
	apply applyFunc
}

// PullResult is the store a pull ends with, which the caller owns and closes, and its head.
type PullResult struct {
	Store store.Store
	Head  Seq
}

// pullBody is a body a pull applies: where it ends, whether it is an archive, and how to read it.
type pullBody struct {
	to      Seq
	archive bool
	read    func() (SyncBody, error)
}

// PullSession applies what the remote has after the local head; into an empty backend, that restores the session (from an
// archive when the history starts with one) and opens its store with an outbox. It returns the store, which the caller
// owns and closes. Nothing may hold the store's claims (no `StoreStorage`, no Harness): ApplyBatch refuses then.
//
// A copy with commits it has not uploaded fails with UnsyncedCommitsError instead of getting the remote's commits spliced
// onto its own. A copy whose history folding removed fails with HistoryUnavailableError and restores into a fresh backend.
func PullSession(ctx context.Context, options PullOptions) (result PullResult, err error) {
	apply := options.apply
	if apply == nil {
		apply = store.ApplyBatch
	}
	listing, err := options.Store.List(ctx, options.SessionID)
	if err != nil {
		return PullResult{}, err
	}
	target := options.Local.Store
	local := target != nil
	at := func() Seq {
		if target == nil {
			return LogStart
		}
		return StoreHead(target)
	}
	saved, err := options.State.Get(ctx, options.SessionID)
	if err != nil {
		return PullResult{}, err
	}
	if synced := sharedHead(saved, at()); at() > synced {
		return PullResult{}, &UnsyncedCommitsError{SessionID: options.SessionID, LocalHead: at(), SyncedHead: synced}
	}
	from := at()
	history := RemoteHistoryOf(listing, &from)
	defer func() {
		// A store this pull opened is the caller's only on success.
		if err != nil && !local && target != nil {
			_ = target.Close(context.WithoutCancel(ctx))
		}
	}()
	var bodies []pullBody
	if history.Snapshot != nil {
		snapshot := *history.Snapshot
		bodies = append(bodies, pullBody{to: snapshot, archive: true, read: func() (SyncBody, error) {
			return options.Store.GetSnapshot(ctx, options.SessionID, snapshot)
		}})
	}
	for _, batch := range history.Batches {
		bodies = append(bodies, pullBody{to: batch.To, archive: batch.Archive, read: func() (SyncBody, error) {
			return options.Store.GetBatch(ctx, options.SessionID, batch.After)
		}})
	}
	for _, each := range bodies {
		if each.to <= at() {
			continue
		}
		body, err := each.read()
		if err != nil {
			return PullResult{}, err
		}
		if body == nil {
			return PullResult{}, &HistoryUnavailableError{Head: at()}
		}
		pulling, syncedHead := each.to, at()
		if err := options.State.Set(ctx, options.SessionID, SyncState{SyncedHead: &syncedHead, Pulling: &pulling}); err != nil {
			return PullResult{}, errors.Join(err, body.Close())
		}
		if each.archive {
			// An archive restores a whole store: only into an empty backend.
			if target != nil || local {
				return PullResult{}, errors.Join(&HistoryUnavailableError{Head: at()}, body.Close())
			}
			outbox := true
			target, err = store.ImportStore(options.Local.Backend, DecodeSnapshot(body), ctx, store.StoreOptions{Outbox: &outbox})
			if err != nil {
				target = nil
				return PullResult{}, err
			}
		} else {
			if target == nil {
				opened, err := openWithOutbox(ctx, options.Local.Backend)
				if err != nil {
					return PullResult{}, errors.Join(err, body.Close())
				}
				target = opened
			}
			chord, ok := target.(*store.ChordStore)
			if !ok {
				return PullResult{}, errors.Join(fmt.Errorf("sync: a pull applies to a *store.ChordStore, got %T", target), body.Close())
			}
			if err := applyBody(ctx, chord, body, apply); err != nil {
				return PullResult{}, err
			}
		}
		synced := at()
		if err := options.State.Set(ctx, options.SessionID, SyncState{SyncedHead: &synced}); err != nil {
			return PullResult{}, err
		}
	}
	if target == nil {
		opened, err := openWithOutbox(ctx, options.Local.Backend)
		if err != nil {
			return PullResult{}, err
		}
		target = opened
	}
	head := at()
	if head < RemoteHead(listing) {
		return PullResult{}, &HistoryUnavailableError{Head: head}
	}
	if err := options.State.Set(ctx, options.SessionID, SyncState{SyncedHead: &head}); err != nil {
		return PullResult{}, err
	}
	// What the remote holds needs no upload: the outbox keeps only this copy's own commits.
	if outbox := target.Outbox(); outbox != nil && head > outbox.Floor() {
		if err := outbox.Trim(head, ctx); err != nil {
			return PullResult{}, err
		}
	}
	return PullResult{Store: target, Head: head}, nil
}

// openWithOutbox opens the store of an empty backend with its outbox on.
func openWithOutbox(ctx context.Context, backend *store.Backend) (store.Store, error) {
	if backend == nil {
		return nil, errors.New("sync: a pull into an empty copy needs a backend")
	}
	outbox := true
	return store.OpenStore(backend, ctx, store.StoreOptions{Outbox: &outbox})
}

// FoldOptions are what FoldSession takes.
type FoldOptions struct {
	SessionID string
	Store     SessionSyncMaintenance
	// Scratch is an empty backend to restore the session into: a file or a Durable Object's SQLite storage, not `:memory:`,
	// so the restored store is not held in memory.
	Scratch *store.Backend
}

// FoldSession folds a session's history into one snapshot: restore it into Scratch, upload its `Export` as a snapshot, drop
// the commits of the batches it covers, keeping their slots taken, and delete older snapshots. The remote runs this, not
// clients. It returns where the snapshot ends; folded is false when the history has nothing to fold (fewer than two bodies).
func FoldSession(ctx context.Context, options FoldOptions) (to Seq, folded bool, err error) {
	listing, err := options.Store.List(ctx, options.SessionID)
	if err != nil {
		return 0, false, err
	}
	history := RemoteHistoryOf(listing, nil)
	bodies := len(history.Batches)
	if history.Snapshot != nil {
		bodies++
	}
	if bodies < 2 {
		return 0, false, nil
	}
	restored, err := PullSession(ctx, PullOptions{
		SessionID: options.SessionID, Store: options.Store, Local: LocalCopy{Backend: options.Scratch}, State: &scratchState{},
	})
	if err != nil {
		return 0, false, err
	}
	defer func() { err = errors.Join(err, restored.Store.Close(context.WithoutCancel(ctx))) }()
	snapshot, err := exportSnapshot(ctx, restored.Store)
	if err != nil {
		return 0, false, err
	}
	err = options.Store.PutSnapshot(ctx, options.SessionID, snapshot.to, snapshot.body)
	if err = errors.Join(err, snapshot.body.Close()); err != nil {
		return 0, false, err
	}
	var covered []Seq
	for _, batch := range history.Batches {
		if batch.To <= snapshot.to {
			covered = append(covered, batch.After)
		}
	}
	if err := options.Store.FoldBatches(ctx, options.SessionID, covered); err != nil {
		return 0, false, err
	}
	var older []Seq
	for _, at := range listing.Snapshots {
		if at < snapshot.to {
			older = append(older, at)
		}
	}
	if err := options.Store.DeleteSnapshots(ctx, options.SessionID, older); err != nil {
		return 0, false, err
	}
	return snapshot.to, true, nil
}

// scratchState is sync state for a restore nobody continues.
type scratchState struct{ state *SyncState }

func (scratch *scratchState) Get(context.Context, string) (*SyncState, error) {
	return scratch.state, nil
}

func (scratch *scratchState) Set(_ context.Context, _ string, state SyncState) error {
	scratch.state = &state
	return nil
}
