package host

import (
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/src/next/jobs/stored.ts
//
// A persisted run's record (slice 6b §4.1, D5): the document each unsettled run of a persisted job keeps in its
// object's store (namespace `chord.jobs`, kind the job name, key the run id), and the conversion of an outcome to and
// from it. Errors are stored in the wire error format, so a `PluginError`'s code, message and details survive.
//
// Forced Go differences. The record is a JSON document, `*delta.JsonObject`, as the documents engine stores it:
// `storedRun` builds it (keys in Pi's order) and `recordOf` reads it back into StoredRun, the Go form of Pi's
// `StoredRun` type (a TypeScript cast that Go has to write out).

// storedJobError is how a run failed, as stored: a thrown error in the wire format, an abort, the restart limit.
type storedJobError struct {
	kind     JobErrorKind
	err      link.WireError
	reason   AbortReason
	restarts int
}

// storedOutcome is a run's outcome as stored: its result, or how it failed.
type storedOutcome struct {
	kind   OutcomeKind
	result delta.JsonValue
	err    storedJobError
}

// StoredRun is the record document of one unsettled persisted run.
type StoredRun struct {
	Version        int
	Fingerprint    string
	Args           delta.JsonValue
	Key            *string
	Trace          map[string]string
	Phase          Phase
	Restarts       int
	FinishFailures int
	Abort          bool
	Blocked        *BlockedReason
	outcome        *storedOutcome
}

// runRecord is the record documents' version and fingerprint (Chord's own documents: no definition, no `migrate`; a
// new record shape raises the version).
var runRecord = struct {
	version     int64
	fingerprint string
}{version: 1, fingerprint: "chord.job-run.1"}

// storedFields is what a change sets on a run's record; the rest is the run's own. A nil pointer is a field the change
// does not set.
type storedFields struct {
	phase          *Phase
	restarts       *int
	finishFailures *int
	abortMarked    bool
	blocked        *BlockedReason
	outcome        *AnyOutcome
}

// storedRun is run's record as stored with fields changed, with the job's fingerprint.
func storedRun(run *jobRun, fingerprint string, fields storedFields) *delta.JsonObject {
	phase, restarts, finishFailures := run.phase, run.restarts, run.finishFailures
	abortMarked, blocked, outcome := run.abortMarked, run.blocked, run.outcome
	if fields.phase != nil {
		phase = *fields.phase
	}
	if fields.restarts != nil {
		restarts = *fields.restarts
	}
	if fields.finishFailures != nil {
		finishFailures = *fields.finishFailures
	}
	if fields.abortMarked {
		abortMarked = true
	}
	if fields.blocked != nil {
		blocked = fields.blocked
	}
	if fields.outcome != nil {
		outcome = fields.outcome
	}
	record := delta.NewJsonObject(11)
	record.Set("version", float64(run.version))
	record.Set("fingerprint", fingerprint)
	record.Set("args", run.args)
	if run.key != nil {
		record.Set("key", *run.key)
	}
	if run.trace != nil {
		trace := delta.NewJsonObject(len(run.trace))
		for key, value := range run.trace {
			trace.Set(key, value)
		}
		record.Set("trace", trace)
	}
	record.Set("phase", string(phase))
	record.Set("restarts", float64(restarts))
	record.Set("finishFailures", float64(finishFailures))
	if abortMarked {
		record.Set("abort", true)
	}
	if blocked != nil {
		record.Set("blocked", string(*blocked))
	}
	if outcome != nil {
		record.Set("outcome", storedOutcomeOf(*outcome))
	}
	return record
}

// storedOutcomeOf is an outcome as stored: a result as it is (JSON: persisted jobs refuse `bytes()`), an error in the
// wire format.
func storedOutcomeOf(outcome AnyOutcome) *delta.JsonObject {
	if outcome.Kind == OutcomeDone {
		return delta.JsonObjectOf("kind", "done", "result", outcome.Result)
	}
	return delta.JsonObjectOf("kind", "failed", "error", storedErrorOf(outcome.Error))
}

func storedErrorOf(failure JobError) *delta.JsonObject {
	switch failure.Kind {
	case JobThrew:
		return delta.JsonObjectOf("kind", "threw", "error", link.WireErrorOf(failure.Error))
	case JobAborted:
		return delta.JsonObjectOf("kind", "aborted", "reason", string(failure.Reason))
	default:
		return delta.JsonObjectOf("kind", "restart_limit", "restarts", float64(failure.Restarts))
	}
}

// outcomeOf is the outcome a stored one stands for, rebuilt as the in-memory one (`id`, `args` and `state` from the
// run).
func outcomeOf(stored storedOutcome, id string, args delta.JsonValue, state delta.JsonValue) (AnyOutcome, error) {
	if stored.kind == OutcomeDone {
		return AnyOutcome{ID: id, Args: args, State: state, Kind: OutcomeDone, Result: stored.result}, nil
	}
	failure := stored.err
	rebuilt := JobError{Kind: failure.kind, Reason: failure.reason, Restarts: failure.restarts}
	if failure.kind == JobThrew {
		received, err := link.ErrorFromWire(failure.err)
		if err != nil {
			return AnyOutcome{}, err
		}
		rebuilt.Error = received
	}
	return AnyOutcome{ID: id, Args: args, State: state, Kind: OutcomeFailed, Error: rebuilt}, nil
}

// recordOf is a stored record as Chord wrote it (the record definition's value, a JSON object).
func recordOf(value *delta.JsonObject) (StoredRun, error) {
	// Chord's own document: `storedRun` wrote it.
	record := StoredRun{Args: value.Value("args")}
	version, ok := value.Value("version").(float64)
	if !ok {
		return StoredRun{}, errors.New("a stored run: version is not a number")
	}
	record.Version = int(version)
	record.Fingerprint, _ = value.Value("fingerprint").(string)
	if key, ok := value.Value("key").(string); ok {
		record.Key = &key
	}
	if trace, ok := value.Value("trace").(*delta.JsonObject); ok {
		record.Trace = make(map[string]string, trace.Len())
		for key, entry := range trace.All() {
			record.Trace[key], _ = entry.(string)
		}
	}
	phase, _ := value.Value("phase").(string)
	record.Phase = Phase(phase)
	restarts, _ := value.Value("restarts").(float64)
	record.Restarts = int(restarts)
	finishFailures, _ := value.Value("finishFailures").(float64)
	record.FinishFailures = int(finishFailures)
	record.Abort, _ = value.Value("abort").(bool)
	if blocked, ok := value.Value("blocked").(string); ok {
		reason := BlockedReason(blocked)
		record.Blocked = &reason
	}
	if outcome, ok := value.Value("outcome").(*delta.JsonObject); ok {
		stored, err := storedOutcomeFrom(outcome)
		if err != nil {
			return StoredRun{}, err
		}
		record.outcome = &stored
	}
	return record, nil
}

func storedOutcomeFrom(object *delta.JsonObject) (storedOutcome, error) {
	if kind, _ := object.Value("kind").(string); kind == string(OutcomeDone) {
		return storedOutcome{kind: OutcomeDone, result: object.Value("result")}, nil
	}
	failure, ok := object.Value("error").(*delta.JsonObject)
	if !ok {
		return storedOutcome{}, errors.New("a stored run: its outcome has no error")
	}
	kind, _ := failure.Value("kind").(string)
	stored := storedJobError{kind: JobErrorKind(kind)}
	switch stored.kind {
	case JobThrew:
		wire, ok := failure.Value("error").(*delta.JsonObject)
		if !ok {
			return storedOutcome{}, errors.New("a stored run: its thrown error is not an object")
		}
		stored.err = wire
	case JobAborted:
		reason, _ := failure.Value("reason").(string)
		stored.reason = AbortReason(reason)
	case JobRestartLimit:
		restarts, _ := failure.Value("restarts").(float64)
		stored.restarts = int(restarts)
	default:
		return storedOutcome{}, fmt.Errorf("a stored run: unknown failure %q", kind)
	}
	return storedOutcome{kind: OutcomeFailed, err: stored}, nil
}
