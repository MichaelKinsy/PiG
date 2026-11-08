package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	local := LocalCopy{Store: existing}
	if !ok {
		local = LocalCopy{Backend: backendAt(d.t, "")}
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
	noError(d.t, d.state.Set(background, session, SyncState{}))
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
			_, err := PullSession(background, PullOptions{SessionID: session, Store: remote, Local: LocalCopy{Store: loser.store()}, State: loser.state})
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

	t.Run("passes the writer's epoch to the remote and reports a refused one as stale_epoch", func(t *testing.T) {
		remote := newRemote(t)
		laptop := newDevice(t, "laptop", remote)
		laptop.commit(seedBatches()[:2])
		shared := lastSeq(laptop.store())
		current := int64(1)
		var epochs []*int64
		// A remote that issues epochs (Radius): refuses an upload whose epoch is not the current one.
		issuing := overriding{SessionSyncMaintenance: remote, putBatch: func(ctx context.Context, sessionID string, after, to Seq, device string, body SyncBody, options PutBatchOptions) (PutBatchResult, error) {
			epochs = append(epochs, options.Epoch)
			if options.Epoch == nil || *options.Epoch != current {
				return PutBatchResult{Type: PutStaleEpoch, Epoch: current}, nil
			}
			return remote.PutBatch(ctx, sessionID, after, to, device, body, options)
		}}
		source := laptop.store()
		upload := func(epoch int64) UploadResult {
			result, err := UploadSession(background, UploadOptions{SessionID: session, Device: "laptop", Source: source, Store: issuing, State: laptop.state, Epoch: &epoch})
			noError(t, err)
			return result
		}
		if got := upload(1).Type; got != Uploaded {
			t.Fatalf("upload = %s, want uploaded", got)
		}
		// The remote gave the session to another writer: this device's epoch is stale.
		current = 2
		laptop.commit([]rows{entry(100)})
		same(t, "stale upload", upload(1), UploadResult{Type: StaleEpoch, At: shared, Epoch: 2})
		one := int64(1)
		same(t, "epochs", epochs, []*int64{&one, &one})
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

		_, err := PullSession(background, PullOptions{SessionID: session, Store: remote, Local: LocalCopy{Store: survived}, State: laptop.state})
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
		_, err := PullSession(background, PullOptions{SessionID: session, Store: flaky, Local: LocalCopy{Store: local}, State: viewer.state})
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
		_, err = PullSession(background, PullOptions{SessionID: session, Store: remote, Local: LocalCopy{Store: local}, State: viewer.state})
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
		_, err := PullSession(background, PullOptions{SessionID: session, Store: remote, Local: LocalCopy{Store: stale.store()}, State: stale.state})
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
}

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
		options := PullOptions{SessionID: session, Store: remote, Local: LocalCopy{Store: local}, State: viewer.state}
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
		empty, err := remote.Sessions(background, nil)
		noError(t, err)
		same(t, "sessions", empty, SessionChanges{Sessions: []SessionSummary{}, Cursor: "", More: false})
		laptop := newDevice(t, "laptop", remote)
		laptop.commit([]rows{entry(1)}, "s2")
		laptop.upload("s2")
		noError(t, remote.PutMeta(background, "s1", deltaObject("title", "first")))
		noError(t, remote.PutMeta(background, "s1", deltaObject("title", "renamed", "origin", "laptop")))
		changes, err := remote.Sessions(background, nil)
		noError(t, err)
		same(t, "sessions", changes.Sessions, []SessionSummary{
			{ID: "s1", Head: LogStart, Meta: deltaObject("title", "renamed", "origin", "laptop")},
			{ID: "s2", Head: lastSeq(laptop.store("s2")), Meta: nil},
		})
		meta, err := remote.GetMeta(background, "s1")
		noError(t, err)
		same(t, "meta", meta, deltaObject("title", "renamed", "origin", "laptop"))
		meta, err = remote.GetMeta(background, "s2")
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

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return epoch.Add(c.now)
}

func (c *clock) elapsed() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) SetTimer(callback func(), delay time.Duration) (disarm func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{at: c.now + delay, callback: callback, armed: true}
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
			Now:     time.Now,
			OnError: func(err error) { t.Errorf("unexpected flush error: %v", err) },
			Flush:   flush,
			// The timer fired already: stopping it does nothing.
			SetTimer: func(callback func(), _ time.Duration) func() {
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
