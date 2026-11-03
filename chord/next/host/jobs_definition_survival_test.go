package host

// Field survival of JobDefinition: every option DefineJob takes comes back from the registered definition and shows in
// what the host does with the job (a declaration is the options object Pi's defineJob keeps; self.job registers it).

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

func TestJobDefinitionRegistrationKeepsEveryOptionThroughSelfJob(t *testing.T) {
	adapted := errors.New("adapter ran")
	definition := mustDefineJob(DefineJob("survive.import", JobOptions{
		Version:     2,
		Args:        contracts.Obj(contracts.Prop("path", contracts.Str())),
		Result:      contracts.Obj(contracts.Prop("count", contracts.Int())),
		State:       jobImportProgress,
		Concurrency: new(1),
		Accepts: JobAccepts{1: JobAdapter{
			Args:   func(any) (any, error) { return nil, adapted },
			Result: func(any) (any, error) { return countResult(9), nil },
		}},
		Persist: &JobPersistence{Resume: ResumeAbandon},
	}))
	open := storeFile(t)
	gate := newJobGate()
	jobs := opened(t, open(), func(_ any, _ JobCtx) (any, error) {
		gate.Wait()
		return countResult(3), nil
	}, nil, definition)

	// The registered records hold the declared definition: its Accepts adapter is the declared function, and its fields are
	// the declared ones.
	var registered *JobDefinition
	jobs.Host.(*chordHost).enter(func() {
		registered = jobs.Self.(*attempt).object.jobs["survive.import"].definition
	})
	if registered != definition {
		t.Fatalf("the object registered %p, not the declared definition %p", registered, definition)
	}
	if registered.Name != "survive.import" || registered.Version != 2 || registered.State != jobImportProgress ||
		registered.Concurrency == nil || *registered.Concurrency != 1 ||
		registered.Persist == nil || registered.Persist.Resume != ResumeAbandon || len(registered.Accepts) != 1 || registered.Fingerprint == "" {
		t.Fatalf("registered definition = %+v", registered)
	}
	adapter, err := adapterOf(&jobRun{version: 1}, registered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Args(nil); !errors.Is(err, adapted) {
		t.Fatalf("the registered Accepts[1].Args is not the declared adapter: %v", err)
	}
	mapped, err := adapter.Result(nil)
	if err != nil {
		t.Fatalf("the registered Accepts[1].Result is not the declared adapter: %v", err)
	}
	nexttest.RequireJSONEqual(t, mapped, countResult(9))

	// Args: a start whose args the declared schema refuses is refused.
	_, err = jobs.Imports.Start(jsonObject("nope", float64(1)), background, StartOptions{})
	expectChordError(t, err, data.CodeInvalid, data.ReasonArgs)

	// Persist and State: the run's record and its state document are in the store while it runs.
	run := jobStart(t, jobs.Imports, pathArgs("a"), background)
	if got := stored(t, jobs.Store, "chord.jobs"); !equalStrings(got, "survive.import/"+run.ID()) {
		t.Fatalf("records = %v, want the persisted run's", got)
	}
	if got := stored(t, jobs.Store, "chord.job-states"); !equalStrings(got, "survive.import/"+run.ID()) {
		t.Fatalf("state documents = %v, want the persisted state's", got)
	}
	expectRecord(t, record(t, jobs.Store, run.ID()), map[string]any{"version": float64(2), "restarts": float64(0), "args": map[string]any{"path": "a"}})

	// Concurrency: one run at a time.
	_, err = jobs.Imports.Start(pathArgs("b"), background, StartOptions{})
	expectChordError(t, err, data.CodeUnavailable, data.ReasonBusy)

	// Result: what run returned is the outcome's.
	gate.Open()
	outcome := runOutcome(t, run)
	if outcome.Kind != OutcomeDone {
		t.Fatalf("outcome = %+v", outcome)
	}
	nexttest.RequireJSONEqual(t, outcome.Result, countResult(3))
	jobs.Close()
}
