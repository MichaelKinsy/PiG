package host

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/jobs/persisted.ts
//
// A persisted job's run records in its object's store (slice 6b §4.1, D5): the record document of each unsettled run
// (namespace `chord.jobs`, kind the job name, key the run id, `history: none`), written whole in the transactions that
// change the run, and read once per object when it first activates; for a run of another job version, also what is
// stored of its state document (`chord.job-states`), never decoded, so its drop retires it whatever the code declares.
//
// Async contract: `load` and `findBlockedState` block on the store's reads outside the host's sections and enter the
// loop (`sectionLoop.Run`) where they change what is known of the store; the rest runs inside a section.

// loadedRecord is a run record as a load found it: its run id, the record and what is stored of it.
type loadedRecord struct {
	id     string
	record StoredRun
	stored documents.Stored
}

// keysPage is the keys a load reads per page.
const keysPage = 100

// persistedRuns are the record documents of one persisted job's runs, and what is stored of each.
type persistedRuns struct {
	loop    sectionLoop
	records *documents.Persistence
	states  *documents.Persistence
	stored  map[string]documents.Stored
	// blockedStates is what is stored of the state documents of runs of another job version, by run id.
	blockedStates map[string]documents.Stored
}

// newPersistedRuns are the runs of a job whose `records` and `states` are the job's record and state documents' store
// sides (`chord.jobs`, `chord.job-states`).
func newPersistedRuns(loop sectionLoop, records, states *documents.Persistence) *persistedRuns {
	return &persistedRuns{
		loop: loop, records: records, states: states,
		stored: map[string]documents.Stored{}, blockedStates: map[string]documents.Stored{},
	}
}

// stage stages run `id`'s record `record` into writes (rule 8: serialized now): its creation, or a new base of what is
// stored; it returns the adoption, which takes what the commit stored.
func (runs *persistedRuns) stage(id string, record *delta.JsonObject, writes *[]store.StagedWrite) (func(), error) {
	text, err := jsstring.MarshalJSON(record)
	if err != nil {
		return nil, err
	}
	stored, found := runs.stored[id]
	version, fingerprint := runRecord.version, runRecord.fingerprint
	if !found {
		created := &documents.CreatedID{}
		base := store.DocumentBase{Version: version, Fingerprint: fingerprint, Value: record}
		*writes = append(*writes, store.CreateDocument(runs.records.At(id), base, string(text), func(made int64) { created.ID = made }))
		return func() {
			runs.stored[id] = documents.Stored{ID: created.ID, Revision: 1, Version: version}
		}, nil
	}
	content := store.DocumentContent{Kind: store.ContentBase, Version: version, Fingerprint: fingerprint, Value: record}
	*writes = append(*writes, store.ChangeDocument(stored.ID, stored.Revision, content, string(text)))
	return func() {
		stored.Revision++
		runs.stored[id] = stored
	}, nil
}

// stageRetire stages the retirement of run `id`'s record into writes (the run settled or was dropped), and of its state
// document when the run is of another job version; the adoption forgets them.
func (runs *persistedRuns) stageRetire(id string, writes *[]store.StagedWrite) func() {
	if stored, found := runs.stored[id]; found {
		*writes = append(*writes, store.RetireDocument(stored.ID, stored.Revision))
	}
	if state, found := runs.blockedStates[id]; found {
		*writes = append(*writes, store.RetireDocument(state.ID, state.Revision))
	}
	return func() {
		delete(runs.stored, id)
		delete(runs.blockedStates, id)
	}
}

// findBlockedState finds what is stored of run `id`'s state document (a run of another job version), keeping it for the
// drop.
func (runs *persistedRuns) findBlockedState(id string, ctx context.Context) error {
	found, err := runs.states.Find(id, ctx)
	if err != nil {
		return err
	}
	if found != nil {
		runs.loop.Run(func() { runs.blockedStates[id] = found.Stored })
	}
	return nil
}

// load is every stored record of the job, in run id order, page by page with plain reads (activation holds the line).
func (runs *persistedRuns) load(ctx context.Context) ([]loadedRecord, error) {
	var loaded []loadedRecord
	var after *string
	for {
		keys, err := runs.records.Keys(after, keysPage, ctx)
		if err != nil {
			return nil, err
		}
		for _, id := range keys {
			document, err := runs.records.Read(id, ctx)
			if err != nil {
				return nil, err
			}
			if document == nil {
				continue
			}
			value, _ := document.Value.(*delta.JsonObject)
			record, err := recordOf(value)
			if err != nil {
				return nil, err
			}
			found := documents.Stored{ID: document.Record.ID, Revision: document.Record.Revision, Version: document.Record.Version, DeltasSinceBase: document.DeltasSinceBase}
			runs.loop.Run(func() { runs.stored[id] = found })
			loaded = append(loaded, loadedRecord{id: id, record: record, stored: found})
		}
		if len(keys) < keysPage {
			return loaded, nil
		}
		after = &keys[len(keys)-1]
	}
}

// drop is: the object's documents are dropped: nothing is known of the store any more.
func (runs *persistedRuns) drop() {
	clear(runs.stored)
	clear(runs.blockedStates)
}
