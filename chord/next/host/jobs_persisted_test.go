package host

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/test/next/jobs/persisted.test.ts: persisted jobs (slice 6b §4, D5-D8, R3): run records and state
// documents in the object's store, every transition of §4.2 across a crash (the store closed under a live host, reopened
// by a new one), the clean stop and the close writer, a seal and its retry, the restart limit and its reset, the finish
// failure count, a stored abort, deferred state.
//
// Go forms. Pi replaces `store.commit` for one call (a gate before the step, a refusal, a held queue); `ChordStore.Commit`
// is a method of a concrete type, so a test holds a commit where it can be held without replacing it: the backend
// transaction (`holdNextCommit`, after the step's admission) and the commit queue (`ChordStore.Take`). Each case that
// used the replacement says which. A promise that never settles (`new Promise(() => {})`) is a run that waits for the
// test's end. `RETENTION_MS` and `vi.spyOn(Residency.prototype, "touch")` are `documents.RetentionMs` on the host's
// virtual loop and a check of what the run states are (the spy has no Go form).

var jobImportProgress = mustDefineState(documents.DefineState("import.progress", documents.StateOptions{
	Schema:  contracts.Obj(contracts.Prop("done", contracts.Int())),
	Version: 1,
	Initial: func() *delta.JsonObject { return jsonObject("done", float64(0)) },
	Persist: true,
	CheckpointWhen: func(_ *documents.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
		return info.DeltasSinceBase >= 8
	},
}))

// importOptions is `Import`'s declaration, which the other versions below change.
func importOptions() JobOptions {
	return JobOptions{
		Version: 1,
		Args:    contracts.Obj(contracts.Prop("path", contracts.Str())),
		Result:  contracts.Obj(contracts.Prop("count", contracts.Int())),
		State:   jobImportProgress,
		Persist: &JobPersistence{Resume: ResumeRerun},
	}
}

var (
	jobImport = mustDefineJob(DefineJob("notes.import", importOptions()))
	// jobImportV2 is `Import` at the next version, as a deploy changes it.
	jobImportV2 = mustDefineJob(DefineJob("notes.import", func() JobOptions { o := importOptions(); o.Version = 2; return o }()))
	// jobImportReshaped is `Import` at its version with another result schema (`num` for `int`): another fingerprint.
	jobImportReshaped = mustDefineJob(DefineJob("notes.import", func() JobOptions {
		o := importOptions()
		o.Result = contracts.Obj(contracts.Prop("count", contracts.Num()))
		return o
	}()))
	jobAbandoned = mustDefineJob(DefineJob("notes.abandoned", JobOptions{
		Version: 1,
		Args:    contracts.Obj(contracts.Prop("path", contracts.Str())),
		Result:  contracts.Obj(contracts.Prop("count", contracts.Int())),
		Persist: &JobPersistence{Resume: ResumeAbandon},
	}))
	jobNotesStore = mustContract(contracts.Local("notes.store", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))
)

func countResult(count int) *delta.JsonObject { return jsonObject("count", float64(count)) }

func pathArgs(path string) *delta.JsonObject { return jsonObject("path", path) }

// storeFile is `storeFile()`: a store file opened by the returned function again after each crash or close.
func storeFile(t *testing.T) func() *store.ChordStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "store.sqlite")
	return func() *store.ChordStore {
		t.Helper()
		database, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
		must(t, err)
		opened, err := store.OpenStore(store.SqliteBackend(database), background, store.StoreOptions{})
		must(t, err)
		return opened.(*store.ChordStore)
	}
}

// jobsOpened is what one host over the store sees of the store object's jobs (Pi's `Opened`).
type jobsOpened struct {
	t         *testing.T
	Store     *store.ChordStore
	Errors    *hostErrors
	Imports   JobHandle
	Abandoned JobHandle
	Self      Self
	Host      Host
	mu        sync.Mutex
	finished  []Outcome
	held      []*Reference
}

// Finished is a copy of the outcomes `finish` got.
func (o *jobsOpened) Finished() []Outcome {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]Outcome(nil), o.finished...)
}

// hang is `() => new Promise(() => {})`: a run that never settles until the test ends.
func hang(t *testing.T) func(any, JobCtx) (any, error) {
	return func(any, JobCtx) (any, error) {
		<-t.Context().Done()
		return nil, errors.New("the test ended")
	}
}

// opened is a host over st serving the store type with both jobs (Import or another declaration of it), run and finish
// given.
func opened(
	t *testing.T, st *store.ChordStore, run func(args any, ctx JobCtx) (any, error), finish func(outcome Outcome) error,
	declared ...*JobDefinition,
) *jobsOpened {
	t.Helper()
	return openedWith(t, st, run, finish, nil, declared...)
}

func openedWith(
	t *testing.T, st *store.ChordStore, run func(args any, ctx JobCtx) (any, error), finish func(outcome Outcome) error,
	abandonedRun func(args any, ctx JobCtx) (any, error), declared ...*JobDefinition,
) *jobsOpened {
	t.Helper()
	definition := jobImport
	if len(declared) > 0 {
		definition = declared[0]
	}
	if abandonedRun == nil {
		abandonedRun = func(any, JobCtx) (any, error) { return countResult(0), nil }
	}
	result := &jobsOpened{t: t, Store: st, Errors: &hostErrors{}}
	host := newJobHost(t, HostOptions{Store: st, OnError: result.Errors.push, CloseTimeout: new(200 * time.Millisecond)})
	must(t, host.Load([]HostFacet{{Name: "notes", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(jobNotesStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			result.Imports = self.Job(definition, JobImplementation{
				Run: run,
				Finish: func(outcome Outcome, _ documents.Tx) error {
					if finish != nil {
						if err := finish(outcome); err != nil {
							return err
						}
					}
					result.mu.Lock()
					defer result.mu.Unlock()
					result.finished = append(result.finished, outcome)
					return nil
				},
			})
			result.Abandoned = self.Job(jobAbandoned, JobImplementation{
				Run: abandonedRun,
				Finish: func(outcome Outcome, _ documents.Tx) error {
					result.mu.Lock()
					defer result.mu.Unlock()
					result.finished = append(result.finished, outcome)
					return nil
				},
			})
			result.Self = self
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	})}}, background))
	result.Host = host
	return result
}

// Retry is `ref.retry()` of the store object: it returns at once, the next attempt coming up behind it.
func (o *jobsOpened) Retry() {
	o.t.Helper()
	reference, err := o.Host.Hold(jobNotesStore, jsonObject(), background, GetOptions{})
	must(o.t, err)
	reference.Retry()
	o.held = append(o.held, reference)
}

// Live waits for the store object to be live again.
func (o *jobsOpened) Live() {
	o.t.Helper()
	reference, err := o.Host.Get(jobNotesStore, jsonObject(), background, GetOptions{})
	must(o.t, err)
	reference.Release()
}

// Close closes the host and then the store.
func (o *jobsOpened) Close() {
	o.t.Helper()
	must(o.t, o.Host.Close(background))
	must(o.t, o.Store.Close(background))
}

// Crash is a crash: the store's backend closes under the live host, which is left behind.
func (o *jobsOpened) Crash() { must(o.t, o.Store.Close(background)) }

func (o *jobsOpened) settle() {
	hostDelivered(o.Host)
	nexttest.Flushed()
}

type revision struct {
	Kind    string
	Content any
}

// stateRevisions are the stored revisions of the run state documents, in seq order: their kinds and contents.
func stateRevisions(t *testing.T, st *store.ChordStore) []revision {
	t.Helper()
	rows, err := store.Read(st, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
		return executor.All(`SELECT r.kind, r.content FROM chord_revisions r JOIN chord_documents d ON d.id = r.document_id
					WHERE d.namespace = 'chord.job-states' ORDER BY r.seq`)
	}, background)
	must(t, err)
	out := make([]revision, len(rows))
	for i, row := range rows {
		out[i] = revision{Kind: row["kind"].(string), Content: nexttest.ParseJSON(t, row["content"].(string))}
	}
	return out
}

// stored are the records the store keeps of namespace's documents: their kinds and keys.
func stored(t *testing.T, st *store.ChordStore, namespace string) []string {
	t.Helper()
	rows, err := store.Read(st, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
		return executor.All("SELECT kind, key FROM chord_documents WHERE namespace = ? ORDER BY id", namespace)
	}, background)
	must(t, err)
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row["kind"].(string) + "/" + row["key"].(string)
	}
	return out
}

// record is the current record of run id; nil when there is none.
func record(t *testing.T, st *store.ChordStore, id string) map[string]any {
	t.Helper()
	row, err := store.Read(st, func(executor sqlite.SqliteExecutor) (sqlite.SqliteRow, error) {
		return executor.Get(`SELECT r.content FROM chord_revisions r JOIN chord_documents d ON d.id = r.document_id
					WHERE d.namespace = 'chord.jobs' AND d.key = ? ORDER BY r.seq DESC LIMIT 1`, id)
	}, background)
	must(t, err)
	if row == nil {
		return nil
	}
	var parsed map[string]any
	must(t, json.Unmarshal([]byte(row["content"].(string)), &parsed))
	return parsed
}

// expectRecord is `expect(await record(...)).toMatchObject(want)`: each key of want equals the record's.
func expectRecord(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	if got == nil {
		t.Fatalf("no record, want %v", want)
	}
	for key, value := range want {
		if !nexttest.JSONEqual(got[key], value) {
			t.Fatalf("record[%q] = %v, want %v (record %v)", key, got[key], value, got)
		}
	}
}

func equalStrings(got []string, want ...string) bool {
	return strings.Join(got, "|") == strings.Join(want, "|")
}

// finishedKinds is the kinds of the outcomes finish got.
func (o *jobsOpened) finishedKinds() []string {
	var kinds []string
	for _, outcome := range o.Finished() {
		kinds = append(kinds, string(outcome.Kind))
	}
	return kinds
}

// expectFinished waits for n outcomes of finish.
func (o *jobsOpened) expectFinished(n int) []Outcome {
	o.t.Helper()
	eventually(o.t, "the finished outcomes", func() bool { return len(o.Finished()) >= n })
	return o.Finished()
}

// waitRecord waits for run id's record to match want.
func waitRecord(t *testing.T, st *store.ChordStore, id string, want map[string]any) {
	t.Helper()
	eventually(t, "the record to match", func() bool {
		got := record(t, st, id)
		if got == nil {
			return false
		}
		for key, value := range want {
			if !nexttest.JSONEqual(got[key], value) {
				return false
			}
		}
		return true
	})
}

// persisted.test.ts › records › a run's record and state live in the store while it is unsettled; finish deletes the record, retires the state
func TestPersistedRecordsARunsRecordAndStateLiveInTheStoreWhileItIsUnsettledFinishDeletesTheRecordRetiresTheState(t *testing.T) {
	open := storeFile(t)
	g, changed := newGate(), newGate()
	jobs := opened(t, open(), func(_ any, ctx JobCtx) (any, error) {
		_, err := ctx.State().Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("done", float64(3)) }, ctx, ChangeOptions{})
		if err != nil {
			return nil, err
		}
		changed.Open()
		g.Wait()
		return countResult(3), nil
	}, nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background, "k")
	changed.Wait()
	if got := stored(t, jobs.Store, "chord.jobs"); !equalStrings(got, "notes.import/"+run.ID()) {
		t.Fatalf("records = %v", got)
	}
	if got := stored(t, jobs.Store, "chord.job-states"); !equalStrings(got, "notes.import/"+run.ID()) {
		t.Fatalf("state documents = %v", got)
	}
	expectRecord(t, record(t, jobs.Store, run.ID()), map[string]any{
		"phase": "running", "restarts": float64(0), "key": "k", "args": map[string]any{"path": "a"},
	})
	g.Open()
	outcome := runOutcome(t, run)
	if outcome.Kind != OutcomeDone || outputDone(outcome.State) != 3 {
		t.Fatalf("outcome = %+v", outcome)
	}
	nexttest.RequireJSONEqual(t, outcome.Result, countResult(3))
	eventually(t, "the records to go", func() bool {
		return len(stored(t, jobs.Store, "chord.jobs")) == 0 && len(stored(t, jobs.Store, "chord.job-states")) == 0
	})
	jobs.Close()
}

func outputDone(state any) int { return int(state.(*delta.JsonObject).Value("done").(float64)) }

// persisted.test.ts › crashes and restarts (D6) › a run cut off while running is counted and rerun; a state change resets the count; five in a row end it
func TestPersistedCrashesARunCutOffWhileRunningIsCountedAndRerunFiveInARowEndIt(t *testing.T) {
	open := storeFile(t)
	jobs := opened(t, open(), hang(t), nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running"})
	for restart := 1; restart < 5; restart++ {
		jobs.Crash()
		jobs = opened(t, open(), hang(t), nil)
		waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running", "restarts": float64(restart)})
	}
	jobs.Crash()
	jobs = opened(t, open(), hang(t), nil)
	finished := jobs.expectFinished(1)
	if finished[0].Kind != OutcomeFailed || finished[0].Error.Kind != JobRestartLimit || finished[0].Error.Restarts != 5 {
		t.Fatalf("finished = %+v", finished)
	}
	var interrupted bool
	for _, err := range jobs.Errors.All() {
		if err.Kind == ErrJobInterrupted && err.Job == "notes.import" && err.ID == run.ID() && err.Restarts == 5 {
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatalf("errors = %v", kindsOf(jobs.Errors.All()))
	}
	jobs.Close()
}

// persisted.test.ts › crashes and restarts (D6) › a state change resets the restart count, stored at once
func TestPersistedCrashesAStateChangeResetsTheRestartCountStoredAtOnce(t *testing.T) {
	open := storeFile(t)
	jobs := opened(t, open(), hang(t), nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running"})
	jobs.Crash()
	reset := newGate()
	jobs = opened(t, open(), func(_ any, ctx JobCtx) (any, error) {
		_, err := ctx.State().Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("done", float64(1)) }, ctx,
			ChangeOptions{Persistence: &documents.CommitPersistence{Kind: documents.CommitDeferred}})
		if err != nil {
			return nil, err
		}
		reset.Open()
		<-t.Context().Done()
		return nil, errors.New("the test ended")
	}, nil)
	reset.Wait()
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running", "restarts": float64(0)})
	jobs.Crash()
}

// persisted.test.ts › crashes and restarts (D6) › abandon: a run cut off by a crash finishes failed { aborted, restart } without run
func TestPersistedCrashesAbandonARunCutOffByACrashFinishesFailedAbortedRestartWithoutRun(t *testing.T) {
	open := storeFile(t)
	// Its run never returns before the crash.
	jobs := openedWith(t, open(), hang(t), nil, hang(t))
	run := jobStart(t, jobs.Abandoned, pathArgs("a"), background)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running"})
	jobs.Crash()
	jobs = opened(t, open(), func(any, JobCtx) (any, error) { return countResult(0), nil }, nil)
	var mine []Outcome
	eventually(t, "the abandoned run's finish", func() bool {
		mine = nil
		for _, outcome := range jobs.Finished() {
			if outcome.ID == run.ID() {
				mine = append(mine, outcome)
			}
		}
		return len(mine) == 1
	})
	if mine[0].Kind != OutcomeFailed || mine[0].Error.Kind != JobAborted || mine[0].Error.Reason != AbortedByRestart {
		t.Fatalf("outcome = %+v", mine[0])
	}
	jobs.Close()
}

// persisted.test.ts › versions › a run stored by %s is blocked (version): kept as stored, never run, listed blocked
func TestPersistedVersionsARunStoredByAnotherVersionOrFingerprintIsBlockedKeptAsStoredNeverRunListedBlocked(t *testing.T) {
	for name, declared := range map[string]*JobDefinition{
		"another version": jobImportV2,
		"the same version with another fingerprint": jobImportReshaped,
	} {
		t.Run(name, func(t *testing.T) {
			open := storeFile(t)
			jobs := opened(t, open(), hang(t), nil)
			run := jobStart(t, jobs.Imports, pathArgs("a"), background)
			waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running"})
			jobs.Crash()
			var runs int
			var mu sync.Mutex
			jobs = opened(t, open(), func(any, JobCtx) (any, error) {
				mu.Lock()
				runs++
				mu.Unlock()
				return countResult(1), nil
			}, nil, declared)
			jobs.settle()
			mu.Lock()
			if runs != 0 {
				t.Fatalf("runs = %d", runs)
			}
			mu.Unlock()
			nexttest.RequireJSONEqual(t, jobRunning(t, jobs.Imports), []any{jsonObject("id", run.ID(), "version", float64(1), "phase", "blocked", "reason", "version")})
			// Kept as stored: the crash is not counted, slice 7 maps or resolves it.
			expectRecord(t, record(t, jobs.Store, run.ID()), map[string]any{"version": float64(1), "phase": "running", "restarts": float64(0)})
			jobs.Close()
		})
	}
}

// persisted.test.ts › versions with incompatible state (review regression) › a run of another version is blocked before its state is decoded: no migration, no activation failure
func TestPersistedVersionsWithIncompatibleStateARunOfAnotherVersionIsBlockedBeforeItsStateIsDecoded(t *testing.T) {
	open := storeFile(t)
	jobs := opened(t, open(), hang(t), nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running"})
	jobs.Crash()
	changed := mustDefineState(documents.DefineState("import.progress", documents.StateOptions{
		Schema:         contracts.Obj(contracts.Prop("done", contracts.Str())),
		Version:        2,
		Initial:        func() *delta.JsonObject { return jsonObject("done", "new") },
		Persist:        true,
		CheckpointWhen: func(*documents.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
		Migrate: func(*delta.JsonObject, int) (*delta.JsonObject, error) {
			return nil, errors.New("must not migrate an unsupported run")
		},
	}))
	options := importOptions()
	options.Version, options.State = 2, changed
	declared := mustDefineJob(DefineJob("notes.import", options))
	// The declaration's state differs from `Import`'s: this host only lists the blocked run.
	jobs = opened(t, open(), func(any, JobCtx) (any, error) { return countResult(0), nil }, nil, declared)
	if jobs.Errors.Len() != 0 {
		t.Fatalf("errors = %v", jobs.Errors.All())
	}
	nexttest.RequireJSONEqual(t, jobRunning(t, jobs.Imports), []any{jsonObject("id", run.ID(), "version", float64(1), "phase", "blocked", "reason", "version")})
	// An abort drops it: its record and its undecoded state document are retired.
	if result, err := jobs.Imports.Abort(run.ID(), background); err != nil || result != AbortMarked {
		t.Fatalf("abort = %s, %v", result, err)
	}
	if got := stored(t, jobs.Store, "chord.jobs"); len(got) != 0 {
		t.Fatalf("records = %v", got)
	}
	if got := stored(t, jobs.Store, "chord.job-states"); len(got) != 0 {
		t.Fatalf("state documents = %v", got)
	}
	jobs.Close()
}

// persisted.test.ts › versions without a state (review regression) › an abort of a run blocked under a version without a state retires the old state document too
func TestPersistedVersionsWithoutAStateAnAbortOfARunBlockedUnderAVersionWithoutAStateRetiresTheOldStateDocumentToo(t *testing.T) {
	open := storeFile(t)
	jobs := opened(t, open(), hang(t), nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running"})
	jobs.Crash()
	options := importOptions()
	options.Version, options.State = 2, nil
	stateless := mustDefineJob(DefineJob("notes.import", options))
	jobs = opened(t, open(), func(any, JobCtx) (any, error) { return countResult(0), nil }, nil, stateless)
	nexttest.RequireJSONEqual(t, jobRunning(t, jobs.Imports), []any{jsonObject("id", run.ID(), "version", float64(1), "phase", "blocked", "reason", "version")})
	if result, err := jobs.Imports.Abort(run.ID(), background); err != nil || result != AbortMarked {
		t.Fatalf("abort = %s, %v", result, err)
	}
	if got := stored(t, jobs.Store, "chord.jobs"); len(got) != 0 {
		t.Fatalf("records = %v", got)
	}
	if got := stored(t, jobs.Store, "chord.job-states"); len(got) != 0 {
		t.Fatalf("state documents = %v", got)
	}
	jobs.Close()
}

// heldCommitQueue holds the store's commit queue behind the commit the gate holds: the first commit (a start's) is held
// inside its backend transaction, the queue is taken behind it, and the commit is released: the commits after it wait for
// release. It returns release.
func heldCommitQueue(t *testing.T, st *store.ChordStore, gate *transactionGate) (release func()) {
	t.Helper()
	taken := st.Take(func() {})
	gate.Release()
	return <-taken
}

// persisted.test.ts › a seal during the reservation (review regressions) › a reservation refused at admission after the seal: the next live attempt invokes the run
//
// Pi gates the reservation's commit before it reaches the store; here the commit queue is held (ChordStore.Take) behind
// the start's commit, so the reservation reaches the step only after the seal and is refused there.
func TestPersistedASealDuringTheReservationAReservationRefusedAtAdmissionAfterTheSealTheNextLiveAttemptInvokesTheRun(t *testing.T) {
	st, _, holdNextCommit := faultyStore(t)
	var mu sync.Mutex
	invoked := 0
	jobs := opened(t, st, func(any, JobCtx) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		invoked++
		return countResult(invoked), nil
	}, nil)
	gate := holdNextCommit()
	started := make(chan Run, 1)
	go func() { started <- jobStart(t, jobs.Imports, pathArgs("p"), background) }()
	<-gate.Entered()
	release := heldCommitQueue(t, st, gate)
	run := <-started
	failLive(jobs.Self, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "seal during reservation"}, nil))
	jobs.Retry()
	release()
	if outcome := runOutcome(t, run); outcome.Kind != OutcomeDone {
		t.Fatalf("outcome = %+v", outcome)
	}
	mu.Lock()
	defer mu.Unlock()
	if invoked != 1 {
		t.Fatalf("invoked = %d", invoked)
	}
	jobs.Close()
}

// persisted.test.ts › a seal during the reservation (review regressions) › a reservation queued behind a held commit queue past the seal: the next live attempt invokes the run
func TestPersistedASealDuringTheReservationAReservationQueuedBehindAHeldCommitQueuePastTheSealTheNextLiveAttemptInvokesTheRun(t *testing.T) {
	st, _, holdNextCommit := faultyStore(t)
	var mu sync.Mutex
	invoked := 0
	jobs := opened(t, st, func(any, JobCtx) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		invoked++
		return countResult(invoked), nil
	}, nil)
	// The start's commit takes the queue as it adopts (an export would): the reservation waits behind it.
	gate := holdNextCommit()
	started := make(chan Run, 1)
	go func() { started <- jobStart(t, jobs.Imports, pathArgs("p"), background) }()
	<-gate.Entered()
	release := heldCommitQueue(t, st, gate)
	run := <-started
	jobs.settle()
	failLive(jobs.Self, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "seal queued reservation"}, nil))
	jobs.Retry()
	release()
	if outcome := runOutcome(t, run); outcome.Kind != OutcomeDone {
		t.Fatalf("outcome = %+v", outcome)
	}
	mu.Lock()
	defer mu.Unlock()
	if invoked != 1 {
		t.Fatalf("invoked = %d", invoked)
	}
	jobs.Close()
}

// persisted.test.ts › a seal during the reservation (review regressions) › an abort joined to a reservation that commits after the seal returns marked
func TestPersistedASealDuringTheReservationAnAbortJoinedToAReservationThatCommitsAfterTheSealReturnsMarked(t *testing.T) {
	st, _, holdNextCommit := faultyStore(t)
	jobs := opened(t, st, func(any, JobCtx) (any, error) { return countResult(1), nil }, nil)
	// The start's commit is held first; the gate for the reservation's is set while it is, so the reservation's commit is
	// admitted, then held past the seal (its continuation sees the attempt sealed).
	first := holdNextCommit()
	started := make(chan Run, 1)
	go func() { started <- jobStart(t, jobs.Imports, pathArgs("p"), background) }()
	<-first.Entered()
	second := holdNextCommit()
	first.Release()
	run := <-started
	<-second.Entered()
	aborting := make(chan AbortResult, 1)
	go func() {
		result, err := jobs.Imports.Abort(run.ID(), background)
		must(t, err)
		aborting <- result
	}()
	jobs.settle()
	failLive(jobs.Self, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "seal after reservation"}, nil))
	second.Release()
	if result := <-aborting; result != AbortMarked {
		t.Fatalf("abort = %s", result)
	}
	jobs.Close()
}

// persisted.test.ts › the clean stop and the close writer › host close writes the stopped runs back: the next start resumes them without counting
func TestPersistedTheCleanStopAndTheCloseWriterHostCloseWritesTheStoppedRunsBackTheNextStartResumesThemWithoutCounting(t *testing.T) {
	open := storeFile(t)
	started := newGate()
	jobs := opened(t, open(), func(_ any, ctx JobCtx) (any, error) {
		started.Open()
		untilDone(ctx)
		return nil, errors.New("stopped")
	}, nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	started.Wait()
	jobs.Close()
	reopened := open()
	expectRecord(t, record(t, reopened, run.ID()), map[string]any{"phase": "stopped", "restarts": float64(0)})
	jobs = opened(t, reopened, func(any, JobCtx) (any, error) { return countResult(7), nil }, nil)
	finished := jobs.expectFinished(1)
	if finished[0].Kind != OutcomeDone {
		t.Fatalf("finished = %+v", finished)
	}
	nexttest.RequireJSONEqual(t, finished[0].Result, countResult(7))
	jobs.Close()
}

// persisted.test.ts › the clean stop and the close writer › a seal (a live failure) stops the run; the retry resumes it once its old invocation settled, uncounted (R3)
func TestPersistedTheCleanStopAndTheCloseWriterASealStopsTheRunTheRetryResumesItOnceItsOldInvocationSettledUncounted(t *testing.T) {
	open := storeFile(t)
	var mu sync.Mutex
	invocations := 0
	old := newGate()
	started := newGate()
	jobs := opened(t, open(), func(any, JobCtx) (any, error) {
		mu.Lock()
		invocations++
		n := invocations
		mu.Unlock()
		if n == 1 {
			started.Open()
			// Ignores its signal until released.
			old.Wait()
			return countResult(-1), nil
		}
		return countResult(n), nil
	}, nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	started.Wait()
	failLive(jobs.Self, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	jobs.Retry()
	jobs.settle()
	// The retry waits for the old invocation (the drain, bounded by closeTimeout): no second run beside it.
	mu.Lock()
	if invocations != 1 {
		t.Fatalf("invocations = %d, want 1", invocations)
	}
	mu.Unlock()
	old.Open()
	outcome := runOutcome(t, run)
	if outcome.Kind != OutcomeDone {
		t.Fatalf("outcome = %+v", outcome)
	}
	nexttest.RequireJSONEqual(t, outcome.Result, countResult(2))
	if kinds := jobs.expectFinished(1); len(kinds) != 1 || kinds[0].Kind != OutcomeDone {
		t.Fatalf("finished = %v", jobs.finishedKinds())
	}
	jobs.Close()
}

// persisted.test.ts › the clean stop and the close writer › a close writer that cannot commit leaves the records running: the next start counts a restart
func TestPersistedTheCleanStopAndTheCloseWriterACloseWriterThatCannotCommitLeavesTheRecordsRunningTheNextStartCountsARestart(t *testing.T) {
	open := storeFile(t)
	jobs := opened(t, open(), hang(t), nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running"})
	jobs.Crash()
	// The host closes over a store that is closed already: its close writer cannot commit.
	_ = jobs.Host.Close(background)
	_ = jobs.Store.Close(background)
	jobs = opened(t, open(), hang(t), nil)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running", "restarts": float64(1)})
	jobs.Crash()
}

// sealedRun is `sealed()` of the invocations-after-a-seal suite: a store object whose first invocation ignores its signal
// until old resolves, the next ones run next.
type sealedRun struct {
	Jobs        *jobsOpened
	Run         Run
	Old         *gate
	mu          sync.Mutex
	signals     []bool
	invocations int
}

func (s *sealedRun) Invocations() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.invocations
}

func sealedInvocations(t *testing.T, next func(ctx JobCtx) (any, error), declared ...*JobDefinition) *sealedRun {
	t.Helper()
	open := storeFile(t)
	s := &sealedRun{Old: newGate()}
	started := newGate()
	s.Jobs = opened(t, open(), func(_ any, ctx JobCtx) (any, error) {
		s.mu.Lock()
		s.signals = append(s.signals, ctx.Err() != nil)
		s.invocations++
		n := s.invocations
		s.mu.Unlock()
		if n > 1 {
			return next(ctx)
		}
		started.Open()
		s.Old.Wait()
		_, err := ctx.State().Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("done", float64(99)) }, ctx, ChangeOptions{})
		if err == nil {
			return countResult(-2), nil
		}
		return countResult(-1), nil
	}, nil, declared...)
	s.Run = jobStart(t, s.Jobs.Imports, pathArgs("a"), background)
	started.Wait()
	failLive(s.Jobs.Self, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	return s
}

// persisted.test.ts › invocations after a seal (R3, review regressions) › an invocation replaced after the bounded drain writes nothing and records nothing
func TestPersistedInvocationsAfterASealAnInvocationReplacedAfterTheBoundedDrainWritesNothingAndRecordsNothing(t *testing.T) {
	current := newGate()
	s := sealedInvocations(t, func(JobCtx) (any, error) {
		current.Wait()
		return countResult(2), nil
	})
	s.Jobs.Retry()
	// The drain (closeTimeout 200 ms) ends without the old invocation: the retry invokes the run again.
	eventually(t, "the run to be invoked again", func() bool { return s.Invocations() == 2 })
	s.Old.Open()
	s.Jobs.settle()
	if finished := s.Jobs.Finished(); len(finished) != 0 {
		t.Fatalf("finished = %v", finished)
	}
	entries := jobRunning(t, s.Jobs.Imports)
	if len(entries) != 1 || entries[0].(*delta.JsonObject).Value("id") != s.Run.ID() || outputDone(entries[0].(*delta.JsonObject).Value("state")) != 0 {
		t.Fatalf("running = %v", entries)
	}
	current.Open()
	outcome := runOutcome(t, s.Run)
	if outcome.Kind != OutcomeDone {
		t.Fatalf("outcome = %+v", outcome)
	}
	nexttest.RequireJSONEqual(t, outcome.Result, countResult(2))
	s.Jobs.Close()
}

// persisted.test.ts › invocations after a seal (R3, review regressions) › a resumed invocation gets a fresh abort signal
func TestPersistedInvocationsAfterASealAResumedInvocationGetsAFreshAbortSignal(t *testing.T) {
	s := sealedInvocations(t, func(JobCtx) (any, error) { return countResult(1), nil })
	s.Jobs.Retry()
	s.Old.Open()
	runOutcome(t, s.Run)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.signals) != 2 || s.signals[0] || s.signals[1] {
		t.Fatalf("signals = %v", s.signals)
	}
	s.Jobs.Close()
}

// persisted.test.ts › invocations after a seal (R3, review regressions) › abort joins the resumed invocation, not the first
func TestPersistedInvocationsAfterASealAbortJoinsTheResumedInvocationNotTheFirst(t *testing.T) {
	second := newGate()
	s := sealedInvocations(t, func(JobCtx) (any, error) {
		second.Wait()
		return countResult(2), nil
	})
	s.Old.Open()
	s.Jobs.Retry()
	eventually(t, "the resumed invocation", func() bool { return s.Invocations() == 2 })
	aborted := make(chan AbortResult, 1)
	go func() {
		result, err := s.Jobs.Imports.Abort(s.Run.ID(), background)
		must(t, err)
		aborted <- result
	}()
	// The abort must not settle while the resumed invocation runs (a wait that must not settle: real time).
	select {
	case result := <-aborted:
		t.Fatalf("abort settled (%s) while the resumed invocation ran", result)
	case <-time.After(50 * time.Millisecond):
	}
	second.Open()
	if result := <-aborted; result != AbortMarked {
		t.Fatalf("abort = %s", result)
	}
	s.Jobs.Close()
}

// persisted.test.ts › invocations after a seal (R3, review regressions) › resume abandon: a run the seal stopped finishes failed { aborted, restart } without a second run
func TestPersistedInvocationsAfterASealResumeAbandonARunTheSealStoppedFinishesFailedAbortedRestartWithoutASecondRun(t *testing.T) {
	open := storeFile(t)
	old := newGate()
	started := newGate()
	var mu sync.Mutex
	calls := 0
	options := importOptions()
	options.Persist = &JobPersistence{Resume: ResumeAbandon}
	declared := mustDefineJob(DefineJob("notes.import", options))
	jobs := opened(t, open(), func(any, JobCtx) (any, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			started.Open()
			old.Wait()
		}
		return countResult(n), nil
	}, nil, declared)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	started.Wait()
	failLive(jobs.Self, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	jobs.Retry()
	old.Open()
	outcome := runOutcome(t, run)
	if outcome.Kind != OutcomeFailed || outcome.Error.Kind != JobAborted || outcome.Error.Reason != AbortedByRestart {
		t.Fatalf("outcome = %+v", outcome)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	jobs.Close()
}

// persisted.test.ts › loading (review regressions) › a refused recovery commit loses nothing: the retry loads the runs again and counts the crash once
//
// Pi makes the recovery commit fail by replacing `store.commit`; the Go store has no hook for one commit, so the load
// fails the way the code's own failure does (a state document that cannot be migrated, the case below): the activation
// fails, nothing of the load stays, and the retry loads the run again and counts the crash once.
func TestPersistedLoadingALoadThatFailsLosesNothingTheRetryLoadsTheRunsAgainAndCountsTheCrashOnce(t *testing.T) {
	open := storeFile(t)
	jobs := opened(t, open(), hang(t), nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running"})
	jobs.Crash()
	var mu sync.Mutex
	failing := true
	progress := mustDefineState(documents.DefineState("import.progress", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("done", contracts.Int())),
		Version: 2,
		Migrate: func(old *delta.JsonObject, _ int) (*delta.JsonObject, error) {
			mu.Lock()
			defer mu.Unlock()
			if failing {
				return nil, errors.New("migrate failed")
			}
			return old, nil
		},
		Initial:        func() *delta.JsonObject { return jsonObject("done", float64(0)) },
		Persist:        true,
		CheckpointWhen: func(*documents.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	}))
	options := importOptions()
	options.State = progress
	declared := mustDefineJob(DefineJob("notes.import", options))
	st := open()
	var invocations int
	jobs = opened(t, st, func(any, JobCtx) (any, error) {
		mu.Lock()
		invocations++
		mu.Unlock()
		<-t.Context().Done()
		return nil, errors.New("the test ended")
	}, nil, declared)
	var activationFailed bool
	for _, err := range jobs.Errors.All() {
		activationFailed = activationFailed || err.Kind == ErrActivationFailed
	}
	if !activationFailed {
		t.Fatalf("errors = %v", kindsOf(jobs.Errors.All()))
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	jobs.Retry()
	jobs.Live()
	waitRecord(t, st, run.ID(), map[string]any{"phase": "running", "restarts": float64(1)})
	eventually(t, "the run to be invoked once", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return invocations == 1
	})
	if entries := jobRunning(t, jobs.Imports); len(entries) != 1 || entries[0].(*delta.JsonObject).Value("id") != run.ID() {
		t.Fatalf("running = %v", entries)
	}
	jobs.Crash()
}

// persisted.test.ts › loading (review regressions) › a state document that fails to load fails the activation; the retry with fixed code loads the run
func TestPersistedLoadingAStateDocumentThatFailsToLoadFailsTheActivationTheRetryWithFixedCodeLoadsTheRun(t *testing.T) {
	open := storeFile(t)
	jobs := opened(t, open(), hang(t), nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running"})
	jobs.Crash()
	var mu sync.Mutex
	failing := true
	progressV2 := mustDefineState(documents.DefineState("import.progress", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("done", contracts.Int())),
		Version: 2,
		Migrate: func(old *delta.JsonObject, _ int) (*delta.JsonObject, error) {
			mu.Lock()
			defer mu.Unlock()
			if failing {
				return nil, errors.New("migrate failed")
			}
			return old, nil
		},
		Initial:        func() *delta.JsonObject { return jsonObject("done", float64(0)) },
		Persist:        true,
		CheckpointWhen: func(*documents.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	}))
	options := importOptions()
	options.State = progressV2
	jobs = opened(t, open(), hang(t), nil, mustDefineJob(DefineJob("notes.import", options)))
	var activationFailed bool
	for _, err := range jobs.Errors.All() {
		activationFailed = activationFailed || err.Kind == ErrActivationFailed
	}
	if !activationFailed {
		t.Fatalf("errors = %v", kindsOf(jobs.Errors.All()))
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	jobs.Retry()
	jobs.Live()
	jobs.settle()
	entries := jobRunning(t, jobs.Imports)
	if len(entries) != 1 || entries[0].(*delta.JsonObject).Value("id") != run.ID() || outputDone(entries[0].(*delta.JsonObject).Value("state")) != 0 {
		t.Fatalf("running = %v", entries)
	}
	jobs.Close()
}

// persisted.test.ts › finish failures › are counted on the record, across a crash
func TestPersistedFinishFailuresAreCountedOnTheRecordAcrossACrash(t *testing.T) {
	open := storeFile(t)
	failing := func(Outcome) error { return errors.New("finish failed") }
	jobs := opened(t, open(), func(any, JobCtx) (any, error) { return countResult(1), nil }, failing)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "finishing", "finishFailures": float64(1)})
	jobs.Crash()
	jobs = opened(t, open(), func(any, JobCtx) (any, error) { return countResult(1), nil }, failing)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "finishing", "finishFailures": float64(2)})
	jobs.Crash()
	jobs = opened(t, open(), func(any, JobCtx) (any, error) { return countResult(1), nil }, nil)
	finished := jobs.expectFinished(1)
	if finished[0].Kind != OutcomeDone {
		t.Fatalf("finished = %+v", finished)
	}
	nexttest.RequireJSONEqual(t, finished[0].Result, countResult(1))
	jobs.Close()
}

// persisted.test.ts › abort (D7) › is stored and joined: marked once run settled, terminal for an unknown id
func TestPersistedAbortIsStoredAndJoinedMarkedOnceRunSettledTerminalForAnUnknownID(t *testing.T) {
	open := storeFile(t)
	started := newGate()
	jobs := opened(t, open(), func(_ any, ctx JobCtx) (any, error) {
		started.Open()
		untilDone(ctx)
		return countResult(-1), nil
	}, nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	started.Wait()
	if result, err := jobs.Imports.Abort(run.ID(), background); err != nil || result != AbortMarked {
		t.Fatalf("abort = %s, %v", result, err)
	}
	outcome := runOutcome(t, run)
	nexttest.RequireJSONEqual(t, outcome.Result, countResult(-1))
	if result, err := jobs.Imports.Abort(run.ID(), background); err != nil || result != AbortTerminal {
		t.Fatalf("abort of a settled run = %s, %v", result, err)
	}
	jobs.Close()
}

// persisted.test.ts › abort (D7) › an abort-marked run not invoked yet finishes aborted without run after a crash
func TestPersistedAbortAnAbortMarkedRunNotInvokedYetFinishesAbortedWithoutRunAfterACrash(t *testing.T) {
	open := storeFile(t)
	jobs := opened(t, open(), hang(t), nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "running"})
	go func() { _, _ = jobs.Imports.Abort(run.ID(), background) }()
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"abort": true})
	jobs.Crash()
	var mu sync.Mutex
	ran := false
	jobs = opened(t, open(), func(any, JobCtx) (any, error) {
		mu.Lock()
		ran = true
		mu.Unlock()
		return countResult(1), nil
	}, nil)
	finished := jobs.expectFinished(1)
	mu.Lock()
	defer mu.Unlock()
	if ran {
		t.Fatal("run ran")
	}
	if finished[0].Kind != OutcomeFailed || finished[0].Error.Kind != JobAborted || finished[0].Error.Reason != AbortedByAbort {
		t.Fatalf("finished = %+v", finished)
	}
	jobs.Close()
}

// persisted.test.ts › abort of a finishing run (review regression) › stores the mark and keeps the recorded outcome
func TestPersistedAbortOfAFinishingRunStoresTheMarkAndKeepsTheRecordedOutcome(t *testing.T) {
	open := storeFile(t)
	jobs := opened(t, open(), func(any, JobCtx) (any, error) { return countResult(1), nil },
		func(Outcome) error { return errors.New("retry finish") })
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	waitRecord(t, jobs.Store, run.ID(), map[string]any{"phase": "finishing"})
	if result, err := jobs.Imports.Abort(run.ID(), background); err != nil || result != AbortMarked {
		t.Fatalf("abort = %s, %v", result, err)
	}
	expectRecord(t, record(t, jobs.Store, run.ID()), map[string]any{
		"phase": "finishing", "abort": true, "outcome": map[string]any{"kind": "done", "result": map[string]any{"count": float64(1)}},
	})
	jobs.Crash()
}

// persisted.test.ts › abort racing an outcome › an abort that comes as run returns keeps its result: marked or terminal, the outcome done
func TestPersistedAbortRacingAnOutcomeAnAbortThatComesAsRunReturnsKeepsItsResultMarkedOrTerminalTheOutcomeDone(t *testing.T) {
	open := storeFile(t)
	jobs := opened(t, open(), func(any, JobCtx) (any, error) { return countResult(5), nil }, nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	result, err := jobs.Imports.Abort(run.ID(), background)
	must(t, err)
	if result != AbortMarked && result != AbortTerminal {
		t.Fatalf("abort = %s", result)
	}
	outcome := runOutcome(t, run)
	if outcome.Kind != OutcomeDone {
		t.Fatalf("outcome = %+v", outcome)
	}
	nexttest.RequireJSONEqual(t, outcome.Result, countResult(5))
	jobs.Close()
}

// persisted.test.ts › pinning › an unsettled run's state never idles: past the residency retention it is in memory, read synchronously
//
// Pi spies `Residency.prototype.touch` and advances the clock past RETENTION_MS. The Go form advances the host's virtual
// clock by twice documents.RetentionMs, which runs the residency's eviction timer, and checks that the run's state is
// still in memory and read synchronously (its slot is never cold, so it is never evicted).
func TestPersistedPinningAnUnsettledRunsStateNeverIdlesPastTheResidencyRetentionItIsInMemoryReadSynchronously(t *testing.T) {
	open := storeFile(t)
	st := open()
	loop := newVirtualLoop()
	g, changed := newGate(), newGate()
	var mu sync.Mutex
	var values []int
	host := newJobHost(t, HostOptions{Store: st, OnError: func(HostError) {}, loop: loop})
	var imports JobHandle
	must(t, host.Load([]HostFacet{{Name: "notes", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(jobNotesStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			imports = self.Job(jobImport, JobImplementation{Run: func(_ any, ctx JobCtx) (any, error) {
				if _, err := ctx.State().Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("done", float64(3)) }, ctx, ChangeOptions{}); err != nil {
					return nil, err
				}
				changed.Open()
				g.Wait()
				value, err := ctx.State().Value()
				if err != nil {
					return nil, err
				}
				mu.Lock()
				values = append(values, outputDone(value))
				mu.Unlock()
				return countResult(outputDone(value)), nil
			}})
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	})}}, background))
	run := jobStart(t, imports, pathArgs("a"), background)
	changed.Wait()
	loop.fake.Advance(2 * documents.RetentionMs)
	nexttest.Flushed()
	entries := jobRunning(t, imports)
	if len(entries) != 1 || entries[0].(*delta.JsonObject).Value("id") != run.ID() || outputDone(entries[0].(*delta.JsonObject).Value("state")) != 3 {
		t.Fatalf("running = %v", entries)
	}
	var cold bool
	recordsOf(imports).loop.Run(func() { cold = recordsOf(imports).states.Cold(run.ID()) })
	if cold {
		t.Fatal("a run's state document is cold")
	}
	g.Open()
	outcome := runOutcome(t, run)
	nexttest.RequireJSONEqual(t, outcome.Result, countResult(3))
	mu.Lock()
	defer mu.Unlock()
	if len(values) != 1 || values[0] != 3 {
		t.Fatalf("values = %v", values)
	}
	must(t, host.Close(background))
	must(t, st.Close(background))
}

func setDone(ctx JobCtx, done float64, options ChangeOptions) error {
	_, err := ctx.State().Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("done", done) }, ctx, options)
	return err
}

func deferredWithin(within float64) ChangeOptions {
	return ChangeOptions{Persistence: &documents.CommitPersistence{Kind: documents.CommitDeferred, WithinMs: &within}}
}

var infinity = math.Inf(1)

// persisted.test.ts › deferred state (D8) › is adopted at once, held, and stored with the outcome
func TestPersistedDeferredStateIsAdoptedAtOnceHeldAndStoredWithTheOutcome(t *testing.T) {
	open := storeFile(t)
	g := newGate()
	seen := make(chan int, 1)
	jobs := opened(t, open(), func(_ any, ctx JobCtx) (any, error) {
		if err := setDone(ctx, 1, deferredWithin(infinity)); err != nil {
			return nil, err
		}
		value, err := ctx.State().Value()
		if err != nil {
			return nil, err
		}
		seen <- outputDone(value)
		g.Wait()
		return countResult(1), nil
	}, nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	if got := <-seen; got != 1 {
		t.Fatalf("seen = %d", got)
	}
	revisions := stateRevisions(t, jobs.Store)
	if len(revisions) != 1 || revisions[0].Kind != "base" {
		t.Fatalf("revisions = %v", revisions)
	}
	g.Open()
	outcome := runOutcome(t, run)
	if outputDone(outcome.State) != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	jobs.Close()
}

// persisted.test.ts › deferred state (D8) › merges held ops into the next immediate change: one delta on the stored revision
func TestPersistedDeferredStateMergesHeldOpsIntoTheNextImmediateChangeOneDeltaOnTheStoredRevision(t *testing.T) {
	open := storeFile(t)
	g, done := newGate(), newGate()
	jobs := opened(t, open(), func(_ any, ctx JobCtx) (any, error) {
		for _, n := range []float64{1, 2} {
			if err := setDone(ctx, n, deferredWithin(infinity)); err != nil {
				return nil, err
			}
		}
		if err := setDone(ctx, 3, ChangeOptions{}); err != nil {
			return nil, err
		}
		done.Open()
		g.Wait()
		return countResult(3), nil
	}, nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	done.Wait()
	revisions := stateRevisions(t, jobs.Store)
	// The held sets of one leaf folded (PendingOps), then the change's own.
	want := []revision{
		{Kind: "base", Content: jsonObject("done", float64(0))},
		{Kind: "delta", Content: []any{
			[]any{"s", []any{"done"}, float64(2)},
			[]any{"s", []any{"done"}, float64(3)},
		}},
	}
	if len(revisions) != 2 {
		t.Fatalf("revisions = %v", revisions)
	}
	for i := range want {
		if revisions[i].Kind != want[i].Kind {
			t.Fatalf("revision %d kind = %s", i, revisions[i].Kind)
		}
		nexttest.RequireJSONEqual(t, revisions[i].Content, want[i].Content)
	}
	g.Open()
	runOutcome(t, run)
	jobs.Close()
}

// persisted.test.ts › deferred state (D8) › folds held writes: 100 sets of one leaf keep one op; past the pending bound the next change stores a base
func TestPersistedDeferredStateFoldsHeldWrites100SetsOfOneLeafKeepOneOp(t *testing.T) {
	open := storeFile(t)
	st := open()
	changed := newGate()
	jobs := opened(t, st, func(_ any, ctx JobCtx) (any, error) {
		for n := 1; n <= 100; n++ {
			if err := setDone(ctx, float64(n), deferredWithin(infinity)); err != nil {
				return nil, err
			}
		}
		if err := setDone(ctx, 101, ChangeOptions{}); err != nil {
			return nil, err
		}
		changed.Open()
		<-t.Context().Done()
		return nil, errors.New("the test ended")
	}, nil)
	jobStart(t, jobs.Imports, pathArgs("a"), background)
	changed.Wait()
	revisions := stateRevisions(t, st)
	last := revisions[len(revisions)-1]
	if last.Kind != "delta" {
		t.Fatalf("last revision = %v", last)
	}
	nexttest.RequireJSONEqual(t, last.Content, []any{
		[]any{"s", []any{"done"}, float64(100)},
		[]any{"s", []any{"done"}, float64(101)},
	})
	jobs.Close()
}

// persisted.test.ts › deferred state (D8) › an overflow of held ops stores the next change as a base
func TestPersistedDeferredStateAnOverflowOfHeldOpsStoresTheNextChangeAsABase(t *testing.T) {
	open := storeFile(t)
	st := open()
	items := make([]any, delta.MaxPendingOps+2)
	for i := range items {
		items[i] = float64(0)
	}
	itemsState := mustDefineState(documents.DefineState("import.progress", documents.StateOptions{
		Schema:         contracts.Obj(contracts.Prop("done", contracts.Int()), contracts.Prop("items", contracts.Arr(contracts.Int()))),
		Version:        1,
		Initial:        func() *delta.JsonObject { return jsonObject("done", float64(0), "items", append([]any(nil), items...)) },
		Persist:        true,
		CheckpointWhen: func(*documents.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	}))
	options := importOptions()
	options.State = itemsState
	declared := mustDefineJob(DefineJob("notes.import", options))
	changed := newGate()
	jobs := opened(t, st, func(_ any, ctx JobCtx) (any, error) {
		// One leaf each: nothing folds, and the queue passes its bound.
		for n := 0; n <= delta.MaxPendingOps; n++ {
			_, err := ctx.State().Change(func(draft *delta.Object) (any, error) { return nil, draft.Array("items").Set(n, float64(1)) }, ctx, deferredWithin(infinity))
			if err != nil {
				return nil, err
			}
		}
		if err := setDone(ctx, 1, ChangeOptions{}); err != nil {
			return nil, err
		}
		changed.Open()
		<-t.Context().Done()
		return nil, errors.New("the test ended")
	}, nil, declared)
	jobStart(t, jobs.Imports, pathArgs("a"), background)
	changed.Wait()
	// A new base prunes the older revisions (`history: none`): the start's base is gone.
	revisions := stateRevisions(t, st)
	if len(revisions) != 1 || revisions[0].Kind != "base" || revisions[0].Content.(*delta.JsonObject).Value("done") != float64(1) {
		t.Fatalf("revisions = %v", revisions)
	}
	jobs.Close()
}

// persisted.test.ts › deferred state (D8) › is stored by the close writer at a clean stop, and after withinMs by the object's timer
func TestPersistedDeferredStateIsStoredByTheCloseWriterAtACleanStopAndAfterWithinMsByTheObjectsTimer(t *testing.T) {
	open := storeFile(t)
	held := newGate()
	jobs := opened(t, open(), func(_ any, ctx JobCtx) (any, error) {
		if err := setDone(ctx, 4, deferredWithin(infinity)); err != nil {
			return nil, err
		}
		held.Open()
		<-t.Context().Done()
		return nil, errors.New("the test ended")
	}, nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	held.Wait()
	jobs.Close()
	reopened := open()
	timed := newGate()
	var mu sync.Mutex
	var problems []string
	jobs = opened(t, reopened, func(_ any, ctx JobCtx) (any, error) {
		value, err := ctx.State().Value()
		mu.Lock()
		if err != nil || outputDone(value) != 4 {
			problems = append(problems, "the close writer's state is missing")
		}
		mu.Unlock()
		if err := setDone(ctx, 5, deferredWithin(10)); err != nil {
			return nil, err
		}
		timed.Open()
		<-t.Context().Done()
		return nil, errors.New("the test ended")
	}, nil)
	timed.Wait()
	// The object's timer stores it after withinMs (10 ms: real time).
	eventually(t, "the held change to be stored", func() bool {
		revisions := stateRevisions(t, reopened)
		if len(revisions) == 0 {
			return false
		}
		last := revisions[len(revisions)-1].Content
		if object, ok := last.(*delta.JsonObject); ok {
			return object.Value("done") == float64(5)
		}
		ops := last.([]any)
		return len(ops) > 0 && ops[len(ops)-1].([]any)[2] == float64(5)
	})
	jobs.Crash()
	third := newGate()
	jobs = opened(t, open(), func(_ any, ctx JobCtx) (any, error) {
		value, err := ctx.State().Value()
		mu.Lock()
		if err != nil || outputDone(value) != 5 {
			problems = append(problems, "the timer's state is missing")
		}
		mu.Unlock()
		third.Open()
		return countResult(0), nil
	}, nil)
	third.Wait()
	finished := jobs.expectFinished(1)
	if finished[0].Kind != OutcomeDone || outputDone(finished[0].State) != 5 {
		t.Fatalf("finished = %+v (run %s)", finished, run.ID())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	jobs.Close()
}

// persisted.test.ts › deferred state (D8) › refuses a negative withinMs
func TestPersistedDeferredStateRefusesANegativeWithinMs(t *testing.T) {
	open := storeFile(t)
	refused := make(chan error, 1)
	jobs := opened(t, open(), func(_ any, ctx JobCtx) (any, error) {
		_, err := ctx.State().Change(func(*delta.Object) (any, error) { return nil, nil }, ctx, deferredWithin(-1))
		refused <- err
		return countResult(0), nil
	}, nil)
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	runOutcome(t, run)
	expectChordError(t, <-refused, data.CodeInvalid, data.ReasonArgs)
	jobs.Close()
}

// persisted.test.ts › GC › settled runs, cancelled abort waits, and an ended object with a held change and its timer are collected
func TestPersistedGCSettledRunsCancelledAbortWaitsAndAnEndedObjectWithAHeldChangeAndItsTimerAreCollected(t *testing.T) {
	open := storeFile(t)
	// Runs one settled run and leaves one held change behind a closed host: what must be collected.
	var runRefs []weak.Pointer[jobRun]
	var objectRefs []weak.Pointer[jobRecords]
	func() {
		held := newGate()
		jobs := openedWith(t, open(), func(_ any, ctx JobCtx) (any, error) {
			if err := setDone(ctx, 1, deferredWithin(60_000)); err != nil {
				return nil, err
			}
			held.Open()
			<-ctx.Done()
			return nil, ctx.Err()
		}, nil, nil)
		settled := jobStart(t, jobs.Abandoned, pathArgs("a"), background)
		runOutcome(t, settled)
		jobStart(t, jobs.Imports, pathArgs("b"), background)
		held.Wait()
		jobs.settle()
		for _, handle := range []JobHandle{jobs.Imports, jobs.Abandoned} {
			records := recordsOf(handle)
			objectRefs = append(objectRefs, weak.Make(records))
			records.loop.Run(func() {
				for _, run := range records.order.snapshot() {
					runRefs = append(runRefs, weak.Make(run))
				}
			})
		}
		jobs.Close()
	}()
	nexttest.ExpectCollected(t, runRefs...)
	nexttest.ExpectCollected(t, objectRefs...)
	hanging := opened(t, open(), hang(t), nil)
	run := jobStart(t, hanging.Imports, pathArgs("c"), background)
	waitRecord(t, hanging.Store, run.ID(), map[string]any{"phase": "running"})
	cancelCtx, cancel := chordctx.WithCancel(background)
	waiting := make(chan error, 1)
	go func() {
		_, err := hanging.Imports.Abort(run.ID(), cancelCtx)
		waiting <- err
	}()
	records := recordsOf(hanging.Imports)
	eventually(t, "the abort to join the invocation", func() bool {
		var joined int
		records.loop.Run(func() {
			if current := records.runs[run.ID()].invocation; current != nil {
				joined = len(current.joins.waiting)
			}
		})
		return joined == 1
	})
	cancel(nil)
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled abort wait = %v", err)
	}
	var left int
	records.loop.Run(func() { left = len(records.runs[run.ID()].invocation.joins.waiting) })
	if left != 0 {
		t.Fatalf("%d cancelled abort waits left registered", left)
	}
	hanging.Crash()
}
