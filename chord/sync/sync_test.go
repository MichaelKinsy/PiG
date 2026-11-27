package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	stdsync "sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/test/sync/sync.test.ts: the sync protocol over stores with an outbox: slots created once, uploads
// from the outbox, pulls through `ApplyBatch`, archives for stores whose outbox does not reach back, folds into snapshots.
// The stores hold a plain table: sync knows nothing of what a store keeps.
//
// Go forms: Pi's `overriding(store, {...})` Proxy is a struct embedding the remote with optional method overrides; Pi's
// monkeypatch of `chord.replay` is the unexported `apply` of PullOptions; sleeping on promises is waiting on channels.

const session = "session-1"

// overriding is a remote with some methods replaced.
type overriding struct {
	SessionSyncMaintenance
	putBatch func(ctx context.Context, sessionID string, after, to Seq, device string, body SyncBody, options PutBatchOptions) (PutBatchResult, error)
	getBatch func(ctx context.Context, sessionID string, after Seq) (SyncBody, error)
}

func (remote overriding) PutBatch(ctx context.Context, sessionID string, after, to Seq, device string, body SyncBody, options PutBatchOptions) (PutBatchResult, error) {
	if remote.putBatch != nil {
		return remote.putBatch(ctx, sessionID, after, to, device, body, options)
	}
	return remote.SessionSyncMaintenance.PutBatch(ctx, sessionID, after, to, device, body, options)
}

func (remote overriding) GetBatch(ctx context.Context, sessionID string, after Seq) (SyncBody, error) {
	if remote.getBatch != nil {
		return remote.getBatch(ctx, sessionID, after)
	}
	return remote.SessionSyncMaintenance.GetBatch(ctx, sessionID, after)
}

// device is one device with its copy of a session, which it continues as its own. A copy starts empty: its first pull
// restores it into a fresh backend, or its first commit opens a store with an outbox.
type device struct {
	t      *testing.T
	name   string
	remote SessionSyncMaintenance
	state  *memoryState
	copies map[string]store.Store
}

func newDevice(t *testing.T, name string, remote SessionSyncMaintenance) *device {
	return &device{t: t, name: name, remote: remote, state: newMemoryState(), copies: map[string]store.Store{}}
}

// store is the copy's store, opened on first use.
func (d *device) store(sessionID ...string) store.Store {
	id := first(sessionID)
	s, ok := d.copies[id]
	if !ok {
		s = sessionStore(d.t)
		d.copies[id] = s
	}
	return s
}

func first(sessionID []string) string {
	if len(sessionID) > 0 {
		return sessionID[0]
	}
	return session
}

func (d *device) commit(batches []rows, sessionID ...string) {
	commitAll(d.t, d.store(sessionID...), batches...)
}

func (d *device) upload(sessionID ...string) UploadResult {
	d.t.Helper()
	result, err := UploadSession(background, UploadOptions{
		SessionID: first(sessionID), Device: d.name, Source: d.store(sessionID...), Store: d.remote, State: d.state,
	})
	noError(d.t, err)
	return result
}

func (d *device) pull(sessionID ...string) Seq {
	d.t.Helper()
	id := first(sessionID)
	existing, ok := d.copies[id]
	local := LocalCopy{Kind: LocalCopyStore, Store: existing}
	if !ok {
		local = LocalCopy{Kind: LocalCopyEmpty, Backend: backendAt(d.t, "")}
	}
	result, err := PullSession(background, PullOptions{SessionID: id, Store: d.remote, Local: local, State: d.state})
	noError(d.t, err)
	if !ok {
		d.t.Cleanup(func() { _ = result.Store.Close(background) })
		d.copies[id] = result.Store
	}
	return result.Head
}

// forget drops the copy, as a host does before restoring it fresh.
func (d *device) forget() {
	s := d.copies[session]
	delete(d.copies, session)
	noError(d.t, d.state.Delete(background, session))
	if s != nil {
		noError(d.t, s.Close(background))
	}
}

// rename renames a copy: the new session id has no remote history, so no sync state either.
func (d *device) rename(from, to string) {
	d.copies[to] = d.copies[from]
	delete(d.copies, from)
	delete(d.state.states, from)
}

// seedBatches is ten batches of rows: new ids and updates of earlier ones.
func seedBatches() []rows {
	return []rows{
		{row("a", 1)},
		{row("b", 1), row("c", 1)},
		{row("a", 2)},
		{row("d", 1)},
		{row("b", 2)},
		{row("e", 1)},
		{row("a", 3)},
		{row("f", 1)},
		{row("g", 1), row("c", 2)},
		{row("h", 1)},
	}
}

// entry is one row a single device adds.
func entry(id int64) rows { return rows{row(fmt.Sprintf("x%d", id), id)} }

func slot(after, to Seq, device string) BatchInfo {
	return BatchInfo{After: after, To: to, Device: device}
}

func lastSeq(s store.Store) Seq { return s.LastSeq() }

func same[T any](t *testing.T, what string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %+v, want %+v", what, got, want)
	}
}

func listing(t *testing.T, remote SessionSyncStore, sessionID string) RemoteListing {
	t.Helper()
	found, err := remote.List(background, sessionID)
	noError(t, err)
	return found
}

func TestBatches(t *testing.T) {
	t.Run("round-trip outbox commits through gzip JSON lines", func(t *testing.T) {
		s := sessionStore(t)
		commitAll(t, s, seedBatches()...)
		commits, err := ReadOutbox(background, s, LogStart, 1000)
		noError(t, err)
		if len(commits) == 0 {
			t.Fatal("no commits")
		}
		decoded, err := collect(t, DecodeBatch(EncodeBatch(SliceCommits(commits))))
		noError(t, err)
		same(t, "decoded", decoded, commits)
	})
}

func TestRemoteHistory(t *testing.T) {
	found := RemoteListing{
		Batches:   []BatchInfo{{After: 0, To: 4, Device: "a", Folded: true}, slot(4, 10, "a"), slot(10, 12, "b")},
		Snapshots: []Seq{4},
	}
	snapshot := Seq(4)

	t.Run("restores from the newest snapshot along the chain of slots", func(t *testing.T) {
		same(t, "history", RemoteHistoryOf(found, nil), RemoteHistory{Snapshot: &snapshot, Batches: []BatchInfo{slot(4, 10, "a"), slot(10, 12, "b")}, Head: 12})
		if head := RemoteHead(found); head != 12 {
			t.Fatalf("head = %d, want 12", head)
		}
		same(t, "empty history", RemoteHistoryOf(RemoteListing{}, nil), RemoteHistory{Batches: []BatchInfo{}, Head: 0})
	})

	t.Run("continues a copy from its head, also from inside a batch", func(t *testing.T) {
		chain := RemoteHistory{Batches: []BatchInfo{slot(4, 10, "a"), slot(10, 12, "b")}, Head: 12}
		for _, from := range []Seq{4, 7} {
			same(t, fmt.Sprintf("history from %d", from), RemoteHistoryOf(found, &from), chain)
		}
		twelve := Seq(12)
		same(t, "history from 12", RemoteHistoryOf(found, &twelve), RemoteHistory{Batches: []BatchInfo{}, Head: 12})
	})

	t.Run("restores from the newest archive slot along the chain, past an older snapshot; a copy gets the chain as it is", func(t *testing.T) {
		archived := slot(10, 12, "b")
		archived.Archive = true
		withArchive := RemoteListing{
			Snapshots: found.Snapshots,
			Batches:   []BatchInfo{found.Batches[0], found.Batches[1], archived, slot(12, 15, "b")},
		}
		same(t, "restore from archive", RemoteHistoryOf(withArchive, nil), RemoteHistory{Batches: []BatchInfo{archived, slot(12, 15, "b")}, Head: 15})
		four := Seq(4)
		same(t, "copy from 4", RemoteHistoryOf(withArchive, &four), RemoteHistory{
			Batches: []BatchInfo{slot(4, 10, "a"), archived, slot(12, 15, "b")},
			Head:    15,
		})
	})
}

func TestSyncingASessionBetweenDevices(t *testing.T) {
	t.Run("uploads what was committed since the last upload, trims the outbox, and uploads nothing when nothing was", func(t *testing.T) {
		remote := newRemote(t)
		laptop := newDevice(t, "laptop", remote)
		batches := seedBatches()
		laptop.commit(batches[:4])
		four := lastSeq(laptop.store())
		same(t, "first upload", laptop.upload(), UploadResult{Type: Uploaded, Head: four})
		if floor := laptop.store().Outbox().Floor(); floor != four {
			t.Fatalf("floor = %d, want %d", floor, four)
		}
		same(t, "second upload", laptop.upload(), UploadResult{Type: Idle})
		laptop.commit(batches[4:])
		ten := lastSeq(laptop.store())
		same(t, "third upload", laptop.upload(), UploadResult{Type: Uploaded, Head: ten})
		same(t, "slots", listing(t, remote, session).Batches, []BatchInfo{slot(0, four, "laptop"), slot(four, ten, "laptop")})
	})

	t.Run("hands a session back and forth: each device continues it as its own", func(t *testing.T) {
		remote := newRemote(t)
		laptop, phone := newDevice(t, "laptop", remote), newDevice(t, "phone", remote)
		batches := seedBatches()
		laptop.commit(batches[:5])
		laptop.upload()

		if head := phone.pull(); head != lastSeq(laptop.store()) {
			t.Fatalf("pulled head = %d, want %d", head, lastSeq(laptop.store()))
		}
		same(t, "phone copy", reads(t, phone.store()), reads(t, laptop.store()))
		phone.commit(batches[5:7])
		if got := phone.upload().Type; got != Uploaded {
			t.Fatalf("phone upload = %s, want uploaded", got)
		}

		laptop.pull()
		same(t, "laptop copy", reads(t, laptop.store()), reads(t, phone.store()))
		laptop.commit(batches[7:])
		if got := laptop.upload().Type; got != Uploaded {
			t.Fatalf("laptop upload = %s, want uploaded", got)
		}
		phone.pull()
		same(t, "phone copy", reads(t, phone.store()), reads(t, laptop.store()))
		var devices []string
		for _, batch := range listing(t, remote, session).Batches {
			devices = append(devices, batch.Device)
		}
		same(t, "devices", devices, []string{"laptop", "phone", "laptop"})
	})

	t.Run("keeps a copy that only pulls in step, with nothing left to upload", func(t *testing.T) {
		remote := newRemote(t)
		laptop, viewer := newDevice(t, "laptop", remote), newDevice(t, "viewer", remote)
		for _, writes := range seedBatches() {
			laptop.commit([]rows{writes})
			laptop.upload()
			viewer.pull()
		}
		same(t, "viewer copy", reads(t, viewer.store()), reads(t, laptop.store()))
		same(t, "viewer upload", viewer.upload(), UploadResult{Type: Idle})
	})

	for _, order := range [][2]string{{"phone", "laptop"}, {"laptop", "phone"}} {
		firstName, secondName := order[0], order[1]
		t.Run(fmt.Sprintf("lets the %s keep the session when both continued it and the %s uploaded first", firstName, firstName), func(t *testing.T) {
			remote := newRemote(t)
			devices := map[string]*device{"laptop": newDevice(t, "laptop", remote), "phone": newDevice(t, "phone", remote)}
			batches := seedBatches()
			devices["laptop"].commit(batches[:5])
			devices["laptop"].upload()
			devices["phone"].pull()
			shared := lastSeq(devices["laptop"].store())
			// Both continue without hearing from each other.
			devices["laptop"].commit(batches[5:7])
			devices["phone"].commit([]rows{entry(100)})

			winner, loser := devices[firstName], devices[secondName]
			if got := winner.upload().Type; got != Uploaded {
				t.Fatalf("winner upload = %s, want uploaded", got)
			}
			same(t, "loser upload", loser.upload(), UploadResult{Type: Diverged, At: shared})
			_, err := PullSession(background, PullOptions{SessionID: session, Store: remote, Local: LocalCopy{Kind: LocalCopyStore, Store: loser.store()}, State: loser.state})
			_ = isError[*UnsyncedCommitsError](t, err)

			// The loser's copy becomes a new session, uploaded whole as an archive (its outbox was trimmed through
			// `shared`); the session restores fresh on that device.
			loser.rename(session, "fork-1")
			forked := loser.store("fork-1")
			same(t, "fork upload", loser.upload("fork-1"), UploadResult{Type: Uploaded, Head: lastSeq(forked)})
			if batch := listing(t, remote, "fork-1").Batches[0]; batch.After != 0 || !batch.Archive {
				t.Fatalf("fork's first slot = %+v, want an archive after 0", batch)
			}
			fork := newDevice(t, secondName, remote)
			fork.pull("fork-1")
			same(t, "fork copy", reads(t, fork.store("fork-1")), reads(t, forked))
			loser.pull()
			same(t, "loser copy", reads(t, loser.store()), reads(t, winner.store()))
		})
	}

	t.Run("goes on after an upload whose response was lost", func(t *testing.T) {
		remote := newRemote(t)
		laptop, replica := newDevice(t, "laptop", remote), newDevice(t, "replica", remote)
		batches := seedBatches()
		laptop.commit(batches[:5])
		laptop.upload()
		five := lastSeq(laptop.store())
		laptop.commit(batches[5:7])
		seven := lastSeq(laptop.store())
		// The remote creates the slot, but its response is lost: no sync state is written, nothing is trimmed.
		lossy := overriding{SessionSyncMaintenance: remote, putBatch: func(ctx context.Context, sessionID string, after, to Seq, device string, body SyncBody, options PutBatchOptions) (PutBatchResult, error) {
			if _, err := remote.PutBatch(ctx, sessionID, after, to, device, body, options); err != nil {
				return PutBatchResult{}, err
			}
			return PutBatchResult{}, errors.New("response lost")
		}}
		_, err := UploadSession(background, UploadOptions{SessionID: session, Device: "laptop", Source: laptop.store(), Store: lossy, State: laptop.state})
		if err == nil || !strings.Contains(err.Error(), "response lost") {
			t.Fatalf("err = %v, want response lost", err)
		}
		stored, err := laptop.state.Get(background, session)
		noError(t, err)
		same(t, "state", stored, &SyncState{SyncedHead: &five})
		laptop.commit(batches[7:])
		ten := lastSeq(laptop.store())
		same(t, "upload", laptop.upload(), UploadResult{Type: Uploaded, Head: ten})
		same(t, "slots", listing(t, remote, session).Batches, []BatchInfo{slot(0, five, "laptop"), slot(five, seven, "laptop"), slot(seven, ten, "laptop")})
		replica.pull()
		same(t, "replica copy", reads(t, replica.store()), reads(t, laptop.store()))
	})

	t.Run("reports a remote that lost the session's history as remote_empty, and starts over from a full copy", func(t *testing.T) {
		laptop := newDevice(t, "laptop", newRemote(t))
		laptop.commit(seedBatches()[:4])
		laptop.upload()
		synced := lastSeq(laptop.store())
		laptop.commit(seedBatches()[4:])
		// The remote's copy was deleted while the bookmark still says `synced`.
		emptied := newRemote(t)
		upload := func() UploadResult {
			result, err := UploadSession(background, UploadOptions{
				SessionID: session,
				Device:    "laptop",
				Source:    laptop.store(),
				Store:     emptied,
				State:     laptop.state,
			})
			noError(t, err)
			return result
		}
		same(t, "remote_empty", upload(), UploadResult{Type: RemoteEmpty, At: synced})
		// The host forgets the bookmark: the next upload is the whole store, as an archive (the outbox was trimmed).
		noError(t, laptop.state.Delete(background, session))
		source := laptop.store()
		same(t, "reupload", upload(), UploadResult{Type: Uploaded, Head: lastSeq(source)})
		same(t, "slots", listing(t, emptied, session).Batches, []BatchInfo{{After: 0, To: lastSeq(source), Device: "laptop", Archive: true}})
		replica := newDevice(t, "replica", emptied)
		replica.pull()
		same(t, "replica copy", reads(t, replica.store()), reads(t, source))
	})

	t.Run("reports a slot the remote refuses for good, keeping its bookmark and outbox", func(t *testing.T) {
		remote := newRemote(t)
		laptop := newDevice(t, "laptop", remote)
		laptop.commit(seedBatches()[:3])
		refusing := overriding{
			SessionSyncMaintenance: remote,
			putBatch: func(context.Context, string, Seq, Seq, string, SyncBody, PutBatchOptions) (PutBatchResult, error) {
				return PutBatchResult{Type: PutRefused, Reason: RefusalNextIDRegression}, nil
			},
		}
		source := laptop.store()
		result, err := UploadSession(background, UploadOptions{
			SessionID: session,
			Device:    "laptop",
			Source:    source,
			Store:     refusing,
			State:     laptop.state,
		})
		noError(t, err)
		same(t, "refused", result, UploadResult{Type: Refused, Reason: RefusalNextIDRegression})
		gotState, err := laptop.state.Get(background, session)
		noError(t, err)
		if gotState != nil {
			t.Fatalf("state = %+v, want nil", gotState)
		}
		if floor := source.Outbox().Floor(); floor != 0 {
			t.Fatalf("outbox floor = %d, want 0", floor)
		}
		// Nothing was written: the remote that accepts takes the same slot.
		if got := laptop.upload().Type; got != Uploaded {
			t.Fatalf("upload = %s, want uploaded", got)
		}
	})

	t.Run("refuses to upload a copy that lost uploaded commits, and pulls them back", func(t *testing.T) {
		remote := newRemote(t)
		batches := seedBatches()
		laptop := newDevice(t, "laptop", remote)
		laptop.commit(batches[:7])
		laptop.upload()
		uploaded := lastSeq(laptop.store())
		// A power loss kept only the first commits: a store that holds less than the sync state says.
		survived := sessionStore(t)
		commitAll(t, survived, batches[:5]...)
		upload := func() UploadResult {
			result, err := UploadSession(background, UploadOptions{SessionID: session, Device: "laptop", Source: survived, Store: remote, State: laptop.state})
			noError(t, err)
			return result
		}
		same(t, "upload", upload(), UploadResult{Type: Behind, Head: lastSeq(survived), SyncedHead: uploaded})

		_, err := PullSession(background, PullOptions{SessionID: session, Store: remote, Local: LocalCopy{Kind: LocalCopyStore, Store: survived}, State: laptop.state})
		noError(t, err)
		same(t, "survived copy", reads(t, survived), reads(t, laptop.store()))
		commitAll(t, survived, batches[7:]...)
		if got := upload().Type; got != Uploaded {
			t.Fatalf("upload = %s, want uploaded", got)
		}
	})

	t.Run("creates slots only where the history ends", func(t *testing.T) {
		remote := newRemote(t)
		batches := seedBatches()
		laptop := newDevice(t, "laptop", remote)
		laptop.commit(batches[:5])
		laptop.upload()
		head := lastSeq(laptop.store())
		// A copy that believes it shares 1 with the remote: no slot starts there, and the history ends further on.
		desktop := newDevice(t, "desktop", remote)
		desktop.commit(batches[:2])
		one := Seq(1)
		noError(t, desktop.state.Set(background, session, SyncState{SyncedHead: &one}))
		commits, err := ReadOutbox(background, desktop.store(), 1, 100)
		noError(t, err)
		result, err := remote.PutBatch(background, session, 1, commits[len(commits)-1].Seq, "desktop", EncodeBatch(SliceCommits(commits)), PutBatchOptions{})
		noError(t, err)
		same(t, "put", result, PutBatchResult{Type: PutNotHead, Head: head})
		same(t, "upload", desktop.upload(), UploadResult{Type: Diverged, At: 1})
		same(t, "slots", listing(t, remote, session).Batches, []BatchInfo{slot(0, head, "laptop")})
	})

	t.Run("resumes a pull that stopped inside a batch", func(t *testing.T) {
		remote := newRemote(t)
		laptop := newDevice(t, "laptop", remote)
		laptop.commit(seedBatches()[:3])
		laptop.upload()
		laptop.commit(seedBatches()[3:])
		laptop.upload()

		// The viewer applied the first batch, then lost the connection while reading the second.
		viewer := newDevice(t, "viewer", remote)
		flaky := overriding{SessionSyncMaintenance: remote, getBatch: func(ctx context.Context, sessionID string, after Seq) (SyncBody, error) {
			if after > 0 {
				return nil, errors.New("connection lost")
			}
			return remote.GetBatch(ctx, sessionID, after)
		}}
		local := opened(t, backendAt(t, ""), true)
		_, err := PullSession(background, PullOptions{SessionID: session, Store: flaky, Local: LocalCopy{Kind: LocalCopyStore, Store: local}, State: viewer.state})
		if err == nil || !strings.Contains(err.Error(), "connection lost") {
			t.Fatalf("err = %v, want connection lost", err)
		}
		if lastSeq(local) <= 0 {
			t.Fatal("the first batch was not applied")
		}
		// The commits past what it shares are the remote's, not its own: nothing to upload, and the pull resumes.
		result, err := UploadSession(background, UploadOptions{SessionID: session, Device: "viewer", Source: local, Store: remote, State: viewer.state})
		noError(t, err)
		same(t, "upload", result, UploadResult{Type: Idle})
		_, err = PullSession(background, PullOptions{SessionID: session, Store: remote, Local: LocalCopy{Kind: LocalCopyStore, Store: local}, State: viewer.state})
		noError(t, err)
		same(t, "viewer copy", reads(t, local), reads(t, laptop.store()))
	})

	t.Run("folds the history into a snapshot that restores the session, and keeps its slots taken", func(t *testing.T) {
		remote := newRemote(t)
		laptop, stale, replica := newDevice(t, "laptop", remote), newDevice(t, "stale", remote), newDevice(t, "replica", remote)
		for index, writes := range seedBatches() {
			laptop.commit([]rows{writes})
			laptop.upload()
			if index == 3 {
				stale.pull()
			}
		}
		head := lastSeq(laptop.store())
		fold := func() (Seq, bool) {
			to, folded, err := FoldSession(background, FoldOptions{SessionID: session, Store: remote, Scratch: backendAt(t, filepath.Join(t.TempDir(), "scratch.sqlite"))})
			noError(t, err)
			return to, folded
		}
		if to, folded := fold(); !folded || to != head {
			t.Fatalf("fold = %d, %v, want %d", to, folded, head)
		}
		same(t, "snapshots", listing(t, remote, session).Snapshots, []Seq{head})

		replica.pull()
		same(t, "replica copy", reads(t, replica.store()), reads(t, laptop.store()))
		// A copy behind the fold cannot continue through folded batches: it restores instead.
		at := lastSeq(stale.store())
		_, err := PullSession(background, PullOptions{SessionID: session, Store: remote, Local: LocalCopy{Kind: LocalCopyStore, Store: stale.store()}, State: stale.state})
		_ = isError[*HistoryUnavailableError](t, err)
		// Nor can it take a folded slot: its own commits are a different history.
		stale.commit([]rows{entry(300)})
		same(t, "stale upload", stale.upload(), UploadResult{Type: Diverged, At: at})
		stale.forget()
		stale.pull()
		same(t, "stale copy", reads(t, stale.store()), reads(t, laptop.store()))

		// Uploads go on after the fold, and the replica follows them; so does a second fold and a fresh restore.
		laptop.commit([]rows{entry(100), entry(101)})
		laptop.upload()
		laptop.commit([]rows{entry(102)})
		laptop.upload()
		replica.pull()
		same(t, "replica copy", reads(t, replica.store()), reads(t, laptop.store()))
		if to, folded := fold(); !folded || to != lastSeq(laptop.store()) {
			t.Fatalf("second fold = %d, %v, want %d", to, folded, lastSeq(laptop.store()))
		}
		restored := newDevice(t, "restored", remote)
		restored.pull()
		same(t, "restored copy", reads(t, restored.store()), reads(t, laptop.store()))
	})

	t.Run("reports a batch that vanished while folding", func(t *testing.T) {
		remote := newRemote(t)
		laptop := newDevice(t, "laptop", remote)
		for _, writes := range seedBatches()[:3] {
			laptop.commit([]rows{writes})
			laptop.upload()
		}
		vanishing := overriding{SessionSyncMaintenance: remote, getBatch: func(context.Context, string, Seq) (SyncBody, error) { return nil, nil }}
		_, _, err := FoldSession(background, FoldOptions{SessionID: session, Store: vanishing, Scratch: backendAt(t, filepath.Join(t.TempDir(), "scratch.sqlite"))})
		_ = isError[*HistoryUnavailableError](t, err)
	})

	t.Run("refuses a store without an outbox", func(t *testing.T) {
		remote := newRemote(t)
		plain := opened(t, backendAt(t, ""), false)
		_, err := UploadSession(background, UploadOptions{SessionID: session, Device: "laptop", Source: plain, Store: remote, State: newMemoryState()})
		_ = isError[*OutboxUnavailableError](t, err)
	})

	t.Run("uploads an archive first for a session whose outbox was turned on late", func(t *testing.T) {
		remote := newRemote(t)
		file := filepath.Join(t.TempDir(), "session.sqlite")
		before, err := store.OpenStore(backendAt(t, file), background, store.StoreOptions{})
		noError(t, err)
		commitAll(t, before, seedBatches()[:4]...)
		noError(t, before.Close(background))
		// Reopening with the outbox turns it on from the current head: the earlier commits are not in it.
		source := opened(t, backendAt(t, file), true)
		if floor := source.Outbox().Floor(); floor != lastSeq(source) {
			t.Fatalf("floor = %d, want %d", floor, lastSeq(source))
		}
		commitAll(t, source, seedBatches()[4:]...)
		result, err := UploadSession(background, UploadOptions{SessionID: session, Device: "laptop", Source: source, Store: remote, State: newMemoryState()})
		noError(t, err)
		same(t, "upload", result, UploadResult{Type: Uploaded, Head: lastSeq(source)})
		same(t, "slots", listing(t, remote, session).Batches, []BatchInfo{{After: 0, To: lastSeq(source), Device: "laptop", Archive: true}})
		replica := newDevice(t, "replica", remote)
		replica.pull()
		same(t, "replica copy", reads(t, replica.store()), reads(t, source))
	})

	t.Run("goes on from a full state after the outbox was turned off and on again", func(t *testing.T) {
		remote := newRemote(t)
		file := filepath.Join(t.TempDir(), "session.sqlite")
		state := newMemoryState()
		upload := func(source store.Store) (UploadResult, error) {
			return UploadSession(background, UploadOptions{SessionID: session, Device: "laptop", Source: source, Store: remote, State: state})
		}
		outboxOn := true
		first, err := store.OpenStore(backendAt(t, file), background, store.StoreOptions{Outbox: &outboxOn})
		noError(t, err)
		commitAll(t, first, seedBatches()[:4]...)
		synced := lastSeq(first)
		res, err := upload(first)
		noError(t, err)
		same(t, "first upload", res, UploadResult{Type: Uploaded, Head: synced})
		noError(t, first.Close(background))
		// A replica in step with the remote now.
		replica := newDevice(t, "replica", remote)
		replica.pull()

		// Off: the outbox's commits are dropped, and the store commits on without one.
		outboxOff := false
		off, err := store.OpenStore(backendAt(t, file), background, store.StoreOptions{Outbox: &outboxOff})
		noError(t, err)
		commitAll(t, off, seedBatches()[4:7]...)
		noError(t, off.Close(background))
		// On again: the outbox starts at the store's head, past what the remote holds.
		source := opened(t, backendAt(t, file), true)
		if floor := source.Outbox().Floor(); floor <= synced {
			t.Fatalf("floor = %d, want > %d", floor, synced)
		}
		res, err = upload(source)
		noError(t, err)
		same(t, "archive upload", res, UploadResult{Type: Uploaded, Head: lastSeq(source)})
		batches := listing(t, remote, session).Batches
		wantArchive := slot(synced, lastSeq(source), "laptop")
		wantArchive.Archive = true
		same(t, "archive slot", batches[len(batches)-1], wantArchive)

		// Commits after it are batches again.
		archived := lastSeq(source)
		commitAll(t, source, seedBatches()[7:]...)
		res, err = upload(source)
		noError(t, err)
		same(t, "batch after archive", res, UploadResult{Type: Uploaded, Head: lastSeq(source)})
		batches = listing(t, remote, session).Batches
		same(t, "batch slot", batches[len(batches)-1], slot(archived, lastSeq(source), "laptop"))

		// A restore starts from the archive slot; a copy behind it cannot apply it and restores fresh.
		fresh := newDevice(t, "fresh", remote)
		fresh.pull()
		same(t, "fresh copy", reads(t, fresh.store()), reads(t, source))
		_, err = PullSession(background, PullOptions{SessionID: session, Store: remote, Local: LocalCopy{Kind: LocalCopyStore, Store: replica.store()}, State: replica.state})
		_ = isError[*HistoryUnavailableError](t, err)

		// Folding covers every slot, also those before the archive slot, and restores the same.
		to, folded, err := FoldSession(background, FoldOptions{SessionID: session, Store: remote, Scratch: backendAt(t, filepath.Join(t.TempDir(), "scratch.sqlite"))})
		noError(t, err)
		if !folded || to != lastSeq(source) {
			t.Fatalf("fold = (%d, %v), want (%d, true)", to, folded, lastSeq(source))
		}
		for index, batch := range listing(t, remote, session).Batches {
			if !batch.Folded {
				t.Fatalf("batch[%d] (after %d) folded = false, want true", index, batch.After)
			}
		}
		foldedCopy := newDevice(t, "folded", remote)
		foldedCopy.pull()
		same(t, "folded copy", reads(t, foldedCopy.store()), reads(t, source))
	})

	t.Run("finds its own slots when its sync state is lost, and goes on from a full state after them", func(t *testing.T) {
		remote := newRemote(t)
		file := filepath.Join(t.TempDir(), "session.sqlite")
		outboxOn := true
		first, err := store.OpenStore(backendAt(t, file), background, store.StoreOptions{Outbox: &outboxOn})
		noError(t, err)
		commitAll(t, first, seedBatches()[:4]...)
		upload := func(source store.Store, state SyncStateStore) (UploadResult, error) {
			return UploadSession(background, UploadOptions{SessionID: session, Device: "laptop", Source: source, Store: remote, State: state})
		}
		_, err = upload(first, newMemoryState())
		noError(t, err)
		noError(t, first.Close(background))
		outboxOff := false
		off, err := store.OpenStore(backendAt(t, file), background, store.StoreOptions{Outbox: &outboxOff})
		noError(t, err)
		commitAll(t, off, seedBatches()[4:]...)
		noError(t, off.Close(background))
		source := opened(t, backendAt(t, file), true)
		// No sync state: the archive at 0 finds this device's slot, and the next archive goes after it.
		res, err := upload(source, newMemoryState())
		noError(t, err)
		same(t, "upload after lost state", res, UploadResult{Type: Uploaded, Head: lastSeq(source)})
		fresh := newDevice(t, "fresh", remote)
		fresh.pull()
		same(t, "fresh copy", reads(t, fresh.store()), reads(t, source))
	})
}

func TestRemoteBodies(t *testing.T) {
	t.Run("closes a body file at its end, and on Close also before the first read", func(t *testing.T) {
		opened, closed := 0, 0
		remote := NewFileSystemSyncRemote(countingFiles{opened: &opened, closes: &closed}, t.TempDir())
		source := sessionStore(t)
		commitAll(t, source, seedBatches()[:2]...)
		_, err := UploadSession(background, UploadOptions{SessionID: session, Device: "laptop", Source: source, Store: remote, State: newMemoryState()})
		noError(t, err)

		readBody, err := remote.GetBatch(background, session, LogStart)
		noError(t, err)
		// Read to the end WITHOUT calling Close, pinning that fileBody closes the reader at EOF itself.
		commits := DecodeBatch(readBody)
		count := 0
		for {
			_, ok, err := commits.Next(background)
			noError(t, err)
			if !ok {
				break
			}
			count++
		}
		if count == 0 {
			t.Fatal("expected commits")
		}
		if opened != 1 || closed != 1 {
			t.Fatalf("readers after full read = (opened %d, closed %d), want (1, 1)", opened, closed)
		}

		unread, err := remote.GetBatch(background, session, LogStart)
		noError(t, err)
		if opened != 2 || closed != 1 {
			t.Fatalf("readers before unread close = (opened %d, closed %d), want (2, 1)", opened, closed)
		}
		noError(t, unread.Close())
		noError(t, unread.Close())
		if opened != 2 || closed != 2 {
			t.Fatalf("readers after unread close = (opened %d, closed %d), want (2, 2)", opened, closed)
		}
		_, ok, err := unread.Next(background)
		noError(t, err)
		if ok {
			t.Fatal("expected closed body Next to return ok=false")
		}
	})

	t.Run("closes a body a pull read and then refused", func(t *testing.T) {
		opened, closed := 0, 0
		remote := NewFileSystemSyncRemote(countingFiles{opened: &opened, closes: &closed}, t.TempDir())
		laptop := newDevice(t, "laptop", remote)
		laptop.commit(seedBatches()[:2])
		laptop.upload()
		replica := newDevice(t, "replica", remote)
		replica.pull()
		head := RemoteHead(listing(t, remote, session))
		_, err := remote.PutBatch(background, session, head, head+1, "laptop", EncodeBatch(SliceCommits(nil)), PutBatchOptions{Archive: true})
		noError(t, err)

		_, err = PullSession(background, PullOptions{SessionID: session, Store: remote, Local: LocalCopy{Kind: LocalCopyStore, Store: replica.store()}, State: replica.state})
		_ = isError[*HistoryUnavailableError](t, err)
		if opened < 2 || closed != opened {
			t.Fatalf("readers after refused pull = (opened %d, closed %d), want opened >= 2 and closed == opened", opened, closed)
		}
	})

	t.Run("closes an archive body when PullSession fails in ImportStore before reading it", func(t *testing.T) {
		opened, closed := 0, 0
		remote := NewFileSystemSyncRemote(countingFiles{opened: &opened, closes: &closed}, t.TempDir())
		// A remote whose history starts with an archive slot at 0.
		source := sessionStore(t)
		commitAll(t, source, seedBatches()[:2]...)
		noError(t, source.Outbox().Trim(lastSeq(source), background))
		_, err := UploadSession(background, UploadOptions{SessionID: session, Device: "laptop", Source: source, Store: remote, State: newMemoryState()})
		noError(t, err)

		pullCtx, cancel := context.WithCancel(background)
		defer cancel()
		state := &cancelOnSetState{cancel: cancel}

		_, err = PullSession(pullCtx, PullOptions{
			SessionID: session,
			Store:     remote,
			Local:     LocalCopy{Kind: LocalCopyEmpty, Backend: backendAt(t, "")},
			State:     state,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("PullSession error = %v, want context.Canceled", err)
		}
		if opened != 1 || closed != 1 {
			t.Fatalf("readers after aborted archive pull = (opened %d, closed %d), want (1, 1)", opened, closed)
		}
	})
}

type cancelOnSetState struct {
	cancel context.CancelFunc
}

func (*cancelOnSetState) Get(context.Context, string) (*SyncState, error) { return nil, nil }

func (s *cancelOnSetState) Set(context.Context, string, SyncState) error {
	s.cancel()
	return nil
}

func (*cancelOnSetState) Delete(context.Context, string) error { return nil }

func TestBoundedMemory(t *testing.T) {
	// bigBatches is rows of size characters each, one per commit: about count * size bytes of history.
	bigBatches := func(count, size int) []rows {
		batches := make([]rows, count)
		for index := range batches {
			batches[index] = rows{store.Row{"id": fmt.Sprintf("big%d", index), "n": int64(index), "pad": strings.Repeat("x", size)}}
		}
		return batches
	}

	t.Run("ends a batch at BATCH_BYTES, so a large history uploads in several slots", func(t *testing.T) {
		remote := newRemote(t)
		laptop := newDevice(t, "laptop", remote)
		// 24 commits of ~1 MB: about three batches of BatchBytes (8 MB).
		laptop.commit(bigBatches(24, 1024*1024))
		same(t, "upload", laptop.upload(), UploadResult{Type: Uploaded, Head: lastSeq(laptop.store())})
		slots := listing(t, remote, session).Batches
		if len(slots) < 3 {
			t.Fatalf("slots = %d, want at least 3", len(slots))
		}
		for index, each := range slots {
			if index > 0 && each.After != slots[index-1].To {
				t.Fatalf("slot %d starts at %d, want %d", index, each.After, slots[index-1].To)
			}
		}
		replica := newDevice(t, "replica", remote)
		replica.pull()
		same(t, "replica copy", reads(t, replica.store()), reads(t, laptop.store()))
	})

	t.Run("applies a large batch in chunks of ApplyBytes, and a pull stopped between them resumes", func(t *testing.T) {
		remote := newRemote(t)
		laptop := newDevice(t, "laptop", remote)
		// 6 commits of ~1 MB in one batch: about two apply chunks of ApplyBytes (4 MB).
		laptop.commit(bigBatches(6, 1024*1024))
		laptop.upload()
		if got := len(listing(t, remote, session).Batches); got != 1 {
			t.Fatalf("slots = %d, want 1", got)
		}
		viewer := newDevice(t, "viewer", remote)
		local := sessionStore(t)
		applied := 0
		options := PullOptions{SessionID: session, Store: remote, Local: LocalCopy{Kind: LocalCopyStore, Store: local}, State: viewer.state}
		options.apply = func(target *store.ChordStore, commits []store.OutboxCommit, ctx context.Context) error {
			applied++
			if applied == 2 {
				return errors.New("power cut")
			}
			return store.ApplyBatch(target, commits, ctx)
		}
		_, err := PullSession(background, options)
		if err == nil || !strings.Contains(err.Error(), "power cut") {
			t.Fatalf("err = %v, want power cut", err)
		}
		midway := lastSeq(local)
		if midway <= 0 || midway >= lastSeq(laptop.store()) {
			t.Fatalf("midway = %d, want between 0 and %d", midway, lastSeq(laptop.store()))
		}
		_, err = PullSession(background, options)
		noError(t, err)
		// The first chunk was applied, the second refused, then the resumed pull applied the rest in more chunks.
		if applied < 3 {
			t.Fatalf("applied = %d, want at least 3", applied)
		}
		same(t, "local copy", reads(t, local), reads(t, laptop.store()))
	})
}

func TestHostPieces(t *testing.T) {
	t.Run("lists sessions and keeps the last metadata written", func(t *testing.T) {
		remote := newRemote(t)
		// Pi's test holds the remote as the SessionSyncStore it is read through (packages/chord/test/sync/sync.test.ts:685).
		var syncStore SessionSyncStore = remote
		empty, err := syncStore.Sessions(background, nil)
		noError(t, err)
		same(t, "sessions", empty, SessionChanges{Sessions: []SessionSummary{}, Cursor: "", More: false})
		laptop := newDevice(t, "laptop", remote)
		laptop.commit([]rows{entry(1)}, "s2")
		laptop.upload("s2")
		noError(t, syncStore.PutMeta(background, "s1", deltaObject("title", "first")))
		noError(t, syncStore.PutMeta(background, "s1", deltaObject("title", "renamed", "origin", "laptop")))
		changes, err := syncStore.Sessions(background, nil)
		noError(t, err)
		same(t, "sessions", changes.Sessions, []SessionSummary{
			{ID: "s1", Head: LogStart, Meta: deltaObject("title", "renamed", "origin", "laptop")},
			{ID: "s2", Head: lastSeq(laptop.store("s2")), Meta: nil},
		})
		meta, err := syncStore.GetMeta(background, "s1")
		noError(t, err)
		same(t, "meta", meta, deltaObject("title", "renamed", "origin", "laptop"))
		meta, err = syncStore.GetMeta(background, "s2")
		noError(t, err)
		if meta != nil {
			t.Fatalf("meta of s2 = %v, want none", meta)
		}
	})

	t.Run("refuses session IDs that leave the root", func(t *testing.T) {
		remote := newRemote(t)
		for _, id := range []string{"..", "../x", "a/b", "", ".hidden"} {
			_, err := remote.List(background, id)
			if err == nil || !strings.Contains(err.Error(), "Invalid session ID") {
				t.Fatalf("List(%q) = %v, want an invalid session ID", id, err)
			}
		}
	})

	t.Run("keeps sync state in files", func(t *testing.T) {
		state := NewFileSyncStateStore(osFiles{}, filepath.Join(t.TempDir(), "sync"))
		got, err := state.Get(background, session)
		noError(t, err)
		if got != nil {
			t.Fatalf("state = %+v, want none", got)
		}
		three, seven := Seq(3), Seq(7)
		noError(t, state.Set(background, session, SyncState{SyncedHead: &three, Pulling: &seven}))
		got, err = state.Get(background, session)
		noError(t, err)
		same(t, "state", got, &SyncState{SyncedHead: &three, Pulling: &seven})
		noError(t, state.Set(background, session, SyncState{}))
		got, err = state.Get(background, session)
		noError(t, err)
		same(t, "state", got, &SyncState{})
		noError(t, state.Delete(background, session))
		got, err = state.Get(background, session)
		noError(t, err)
		if got != nil {
			t.Fatalf("state = %+v, want none", got)
		}
	})

	t.Run("leaves no temporary file behind after a write", func(t *testing.T) {
		directory := t.TempDir()
		remote := NewFileSystemSyncRemote(osFiles{}, directory)
		noError(t, remote.PutMeta(background, "s1", deltaObject("title", "x")))
		entries, err := os.ReadDir(filepath.Join(directory, "s1"))
		noError(t, err)
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".tmp") {
				t.Fatalf("temporary file %s left behind", entry.Name())
			}
		}
	})
}

type recordingFlushFiles struct {
	osFiles
	mu         *stdsync.Mutex
	operations *[]string
}

func (f recordingFlushFiles) FlushFile(ctx context.Context, path string) error {
	f.mu.Lock()
	*f.operations = append(*f.operations, "flush "+path)
	f.mu.Unlock()
	return f.osFiles.FlushFile(ctx, path)
}

func (f recordingFlushFiles) RenameFile(ctx context.Context, source, destination string) error {
	f.mu.Lock()
	*f.operations = append(*f.operations, "rename "+source)
	f.mu.Unlock()
	return f.osFiles.RenameFile(ctx, source, destination)
}

func TestTheDirectoryRemote(t *testing.T) {
	bytes := func() SyncBody { return once([]byte{1, 2, 3}) }

	t.Run("creates a slot once when two uploads of a session race for it", func(t *testing.T) {
		remote := newRemote(t)
		var wg stdsync.WaitGroup
		var laptop, desktop PutBatchResult
		var laptopErr, desktopErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			laptop, laptopErr = remote.PutBatch(background, session, 0, 5, "laptop", bytes(), PutBatchOptions{})
		}()
		go func() {
			defer wg.Done()
			desktop, desktopErr = remote.PutBatch(background, session, 0, 7, "desktop", bytes(), PutBatchOptions{})
		}()
		wg.Wait()
		noError(t, laptopErr)
		noError(t, desktopErr)
		// Either goroutine may acquire the per-session queue first in Go.
		if laptop.Type == PutCreated {
			same(t, "desktop", desktop, PutBatchResult{Type: PutExists, To: 5, Device: "laptop"})
			same(t, "slots", listing(t, remote, session).Batches, []BatchInfo{slot(0, 5, "laptop")})
		} else {
			same(t, "desktop", desktop, PutBatchResult{Type: PutCreated})
			same(t, "laptop", laptop, PutBatchResult{Type: PutExists, To: 7, Device: "desktop"})
			same(t, "slots", listing(t, remote, session).Batches, []BatchInfo{slot(0, 7, "desktop")})
		}
	})

	t.Run("finds the head from its file, also when the file is behind or missing", func(t *testing.T) {
		root := t.TempDir()
		remote := NewFileSystemSyncRemote(osFiles{}, root)
		_, err := remote.PutBatch(background, session, 0, 5, "laptop", bytes(), PutBatchOptions{})
		noError(t, err)
		_, err = remote.PutBatch(background, session, 5, 9, "laptop", bytes(), PutBatchOptions{})
		noError(t, err)
		got, err := remote.PutBatch(background, session, 3, 12, "laptop", bytes(), PutBatchOptions{})
		noError(t, err)
		same(t, "not_head", got, PutBatchResult{Type: PutNotHead, Head: 9})

		headPath := filepath.Join(root, session, "head.json")
		// Written after each slot: where the history ends.
		raw, err := os.ReadFile(headPath)
		noError(t, err)
		var parsed headFile
		noError(t, json.Unmarshal(raw, &parsed))
		if parsed.Head != 9 {
			t.Fatalf("head.json = %d, want 9", parsed.Head)
		}
		// A crash between a slot and the head file: the file names the slot before.
		noError(t, os.WriteFile(headPath, []byte(`{"head":5}`), 0o644))
		got, err = remote.PutBatch(background, session, 3, 12, "laptop", bytes(), PutBatchOptions{})
		noError(t, err)
		same(t, "not_head after stale head.json", got, PutBatchResult{Type: PutNotHead, Head: 9})
		// A directory written before head files.
		noError(t, os.Remove(headPath))
		changes, err := remote.Sessions(background, nil)
		noError(t, err)
		heads := make([]Seq, len(changes.Sessions))
		for i, s := range changes.Sessions {
			heads[i] = s.Head
		}
		same(t, "session heads", heads, []Seq{9})
		got, err = remote.PutBatch(background, session, 9, 12, "laptop", bytes(), PutBatchOptions{})
		noError(t, err)
		same(t, "created", got, PutBatchResult{Type: PutCreated})
	})

	t.Run("flushes every file to disk before renaming it into place", func(t *testing.T) {
		var mu stdsync.Mutex
		var ops []string
		files := recordingFlushFiles{mu: &mu, operations: &ops}
		remote := NewFileSystemSyncRemote(files, t.TempDir())
		_, err := remote.PutBatch(background, session, 0, 5, "laptop", bytes(), PutBatchOptions{})
		noError(t, err)
		var renamed []string
		for _, op := range ops {
			if strings.HasPrefix(op, "rename ") {
				renamed = append(renamed, op)
			}
		}
		// The body, the slot, the head file.
		if len(renamed) != 3 {
			t.Fatalf("renamed = %v (len %d), want 3", renamed, len(renamed))
		}
		for _, rename := range renamed {
			path := strings.TrimPrefix(rename, "rename ")
			flushIdx := slices.Index(ops, "flush "+path)
			renameIdx := slices.Index(ops, rename)
			if flushIdx < 0 || flushIdx >= renameIdx {
				t.Fatalf("flush index %d, rename index %d for %s in %v", flushIdx, renameIdx, path, ops)
			}
		}
	})
}

// clock is a fake timer wheel: advance fires the due timers at their own times, each callback returning before the next.
type clock struct {
	mu     stdsync.Mutex
	now    time.Duration
	timers []*fakeTimer
}

type fakeTimer struct {
	at       time.Duration
	callback func()
	armed    bool
}

var epoch = time.Unix(1_000_000, 0)

func (c *clock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return epoch.Add(c.now).UnixMilli()
}

func (c *clock) elapsed() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) SetTimer(callback func(), delayMs int64) (disarm func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{at: c.now + time.Duration(delayMs)*time.Millisecond, callback: callback, armed: true}
	c.timers = append(c.timers, timer)
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		timer.armed = false
	}
}

// advance runs the timers due within d, each at its own time, and returns once their callbacks returned.
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	end := c.now + d
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *fakeTimer
		for _, timer := range c.timers {
			if timer.armed && timer.at <= end && (next == nil || timer.at < next.at) {
				next = timer
			}
		}
		if next == nil {
			c.now = end
			c.mu.Unlock()
			return
		}
		c.now = next.at
		next.armed = false
		c.mu.Unlock()
		next.callback()
	}
}

func TestFlushScheduler(t *testing.T) {
	ms := func(values ...int) []time.Duration {
		out := make([]time.Duration, len(values))
		for index, value := range values {
			out[index] = time.Duration(value) * time.Millisecond
		}
		return out
	}
	fail := func(t *testing.T) func(error) {
		return func(err error) { t.Errorf("unexpected flush error: %v", err) }
	}

	t.Run("flushes some time after a commit, sooner after an urgent one, once per window", func(t *testing.T) {
		fake := &clock{}
		var flushes []time.Duration
		scheduler := NewFlushScheduler(FlushSchedulerOptions{
			Now: fake.Now, SetTimer: fake.SetTimer, OnError: fail(t),
			Flush: func() error {
				flushes = append(flushes, fake.elapsed())
				return nil
			},
		})
		scheduler.Notify(false)
		scheduler.Notify(false)
		fake.advance(4000 * time.Millisecond)
		same(t, "flushes", flushes, []time.Duration(nil))
		scheduler.Notify(true)
		fake.advance(200 * time.Millisecond)
		same(t, "flushes", flushes, ms(4200))
		fake.advance(5000 * time.Millisecond)
		same(t, "flushes", flushes, ms(4200))
		noError(t, scheduler.Close(background))
		same(t, "flushes", flushes, ms(4200, 9200))
	})

	t.Run("keeps the urgency of commits that arrived during a flush", func(t *testing.T) {
		fake := &clock{}
		var mu stdsync.Mutex
		var flushes []time.Duration
		entered, release := make(chan struct{}, 2), make(chan struct{})
		scheduler := NewFlushScheduler(FlushSchedulerOptions{
			Now: fake.Now, SetTimer: fake.SetTimer, OnError: fail(t),
			Flush: func() error {
				mu.Lock()
				flushes = append(flushes, fake.elapsed())
				mu.Unlock()
				entered <- struct{}{}
				<-release
				return nil
			},
		})
		scheduler.Notify(false)
		advanced := make(chan struct{})
		go func() {
			defer close(advanced)
			fake.advance(5000 * time.Millisecond)
		}()
		<-entered
		scheduler.Notify(false)
		scheduler.Notify(true)
		close(release)
		<-advanced
		fake.advance(200 * time.Millisecond)
		<-entered
		mu.Lock()
		defer mu.Unlock()
		same(t, "flushes", flushes, ms(5000, 5200))
	})

	t.Run("retries a failed flush with backoff until it succeeds, without another commit", func(t *testing.T) {
		fake := &clock{}
		var attempts []time.Duration
		var errs []error
		scheduler := NewFlushScheduler(FlushSchedulerOptions{
			Now: fake.Now, SetTimer: fake.SetTimer,
			OnError: func(err error) { errs = append(errs, err) },
			Flush: func() error {
				attempts = append(attempts, fake.elapsed())
				if len(attempts) < 3 {
					return errors.New("offline")
				}
				return nil
			},
		})
		scheduler.Notify(true)
		fake.advance(5000 * time.Millisecond)
		same(t, "attempts", attempts, ms(200, 1200, 3200))
		if len(errs) != 2 {
			t.Fatalf("errors = %d, want 2", len(errs))
		}
		fake.advance(60_000 * time.Millisecond)
		if len(attempts) != 3 {
			t.Fatalf("attempts = %d, want 3", len(attempts))
		}
	})

	t.Run("doubles its retry delay from RetryMs up to MaxRetryMs and no further", func(t *testing.T) {
		fake := &clock{}
		var attempts []time.Duration
		scheduler := NewFlushScheduler(FlushSchedulerOptions{
			Now: fake.Now, SetTimer: fake.SetTimer, OnError: func(error) {}, UrgentMs: 100, RetryMs: 500, MaxRetryMs: 3000,
			Flush: func() error {
				attempts = append(attempts, fake.elapsed())
				if len(attempts) < 7 {
					return errors.New("offline")
				}
				return nil
			},
		})
		scheduler.Notify(true)
		fake.advance(30_000 * time.Millisecond)
		// 100, then +500, +1000, +2000, then the cap: +3000 (not 4000, 8000), +3000, +3000.
		same(t, "attempts", attempts, ms(100, 600, 1600, 3600, 6600, 9600, 12600))
	})

	t.Run("caps the retry delay at a minute by default", func(t *testing.T) {
		fake := &clock{}
		var attempts []time.Duration
		scheduler := NewFlushScheduler(FlushSchedulerOptions{
			Now: fake.Now, SetTimer: fake.SetTimer, OnError: func(error) {},
			Flush: func() error {
				attempts = append(attempts, fake.elapsed())
				if len(attempts) < 10 {
					return errors.New("offline")
				}
				return nil
			},
		})
		scheduler.Notify(true)
		fake.advance(300_000 * time.Millisecond)
		// 200, then +1s, +2s, +4s, +8s, +16s, +32s, then the cap: +60s (not 64s, 128s), +60s.
		same(t, "attempts", attempts, ms(200, 1200, 3200, 7200, 15200, 31200, 63200, 123200, 183200, 243200))
	})

	t.Run("flushes a last time at close, and reports the failure", func(t *testing.T) {
		fake := &clock{}
		scheduler := NewFlushScheduler(FlushSchedulerOptions{
			Now: fake.Now, SetTimer: fake.SetTimer, OnError: fail(t),
			Flush: func() error { return errors.New("offline") },
		})
		scheduler.Notify(false)
		if err := scheduler.Close(background); err == nil || err.Error() != "offline" {
			t.Fatalf("Close = %v, want offline", err)
		}
		// Closed: a commit schedules nothing.
		scheduler.Notify(true)
		fake.advance(10_000 * time.Millisecond)
	})
}

var _ = delta.NewJsonObject

// Pi's close() clears its timer on the one thread, so the callback never runs after close. A time.AfterFunc callback that
// has started cannot be stopped: Close must still win, and wait for a flush underway.
func TestFlushSchedulerCloseAgainstAFiredTimer(t *testing.T) {
	newScheduler := func(flush func() error) (*FlushScheduler, func()) {
		var fired func()
		scheduler := NewFlushScheduler(FlushSchedulerOptions{
			Now:     func() int64 { return time.Now().UnixMilli() },
			OnError: func(err error) { t.Errorf("unexpected flush error: %v", err) },
			Flush:   flush,
			// The timer fired already: stopping it does nothing.
			SetTimer: func(callback func(), _ int64) func() {
				fired = callback
				return func() {}
			},
		})
		return scheduler, func() { fired() }
	}

	t.Run("a callback that starts after Close does not flush", func(t *testing.T) {
		var flushes int
		scheduler, fire := newScheduler(func() error { flushes++; return nil })
		scheduler.Notify(false)
		noError(t, scheduler.Close(background))
		fire()
		if flushes != 1 {
			t.Fatalf("flushes = %d, want 1 (Close's own)", flushes)
		}
	})

	t.Run("Close waits for a flush underway", func(t *testing.T) {
		var mu stdsync.Mutex
		var inFlight, most, flushes int
		entered, release := make(chan struct{}, 2), make(chan struct{})
		scheduler, fire := newScheduler(func() error {
			mu.Lock()
			inFlight++
			most = max(most, inFlight)
			flushes++
			first := flushes == 1
			mu.Unlock()
			if first {
				entered <- struct{}{}
				<-release
			}
			mu.Lock()
			inFlight--
			mu.Unlock()
			return nil
		})
		scheduler.Notify(false)
		callbackDone := make(chan struct{})
		go func() { defer close(callbackDone); fire() }()
		<-entered
		closed := make(chan error, 1)
		go func() { closed <- scheduler.Close(background) }()
		for {
			scheduler.mu.Lock()
			shut := scheduler.closed
			scheduler.mu.Unlock()
			if shut {
				break
			}
			runtime.Gosched()
		}
		select {
		case err := <-closed:
			t.Fatalf("Close returned (%v) while a flush was underway", err)
		case <-time.After(100 * time.Millisecond):
		}
		close(release)
		noError(t, <-closed)
		<-callbackDone
		mu.Lock()
		defer mu.Unlock()
		if most != 1 || flushes != 2 {
			t.Fatalf("most in flight = %d, flushes = %d, want 1 and 2", most, flushes)
		}
	})
}
