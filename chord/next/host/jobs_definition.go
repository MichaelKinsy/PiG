package host

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/jobs/definition.ts
//
// Job definitions (§15): `DefineJob(name, ...)`, the checks of a declaration, and the types of what a run ends with.
//
// Forced Go differences. `JobOptions<A, R, D>`, `Static<A>`, `StateOf<D>` and the `~types` member are type-level only
// (owner class a: TypeScript type-level checks): args, results and states are JSON values (`any`), and a job's state
// definition is a *documents.StateDefinition. `checkKeys` (unknown keys of a JavaScript options object) is designed out
// because a Go struct has no unknown keys. A JSON schema is a `contracts.TSchema` or any value `contracts.SnapshotSchema`
// takes. The accepts table is a map by older version; its keys are checked in ascending order, as an object's integer
// keys iterate.

// JobOptions is what DefineJob takes besides the name.
type JobOptions struct {
	// Version is the job's version; every change of its args, result or state bumps it.
	Version int
	// Args is the run's arguments: closed objects, JSON only (`running` carries them), checked at every start.
	Args any
	// Result is what `run` returns; not validated.
	Result any
	// State is a singleton document definition: one document per run, `ctx.State()` in `run`, `outcome.State` in
	// `finish`; a persisted job's is persisted (`Persist: true`, its `CheckpointWhen`). nil is no state.
	State *documents.StateDefinition
	// Concurrency is at most this many runs running or finishing per object; a start beyond it returns
	// `unavailable/busy`. nil is no limit.
	Concurrency *int
	// Accepts maps, per older version, its args and result forwards (used from slice 7 on; checked now).
	Accepts JobAccepts
	// Persist persists the runs in the object's store (slice 6b; only in `local` types with empty params, P3): how a run
	// cut off by a crash or a stop resumes, `rerun` (its `run` again: idempotent work) or `abandon` (`finish` gets
	// `failed { aborted, reason: "restart" }`). Its result is JSON only (no `bytes()`). nil is a job in memory.
	Persist *JobPersistence
}

// JobResume is how a persisted job's runs resume after a crash or a stop.
type JobResume string

// The resume rules.
const (
	// ResumeRerun runs a cut-off run's `run` again.
	ResumeRerun JobResume = "rerun"
	// ResumeAbandon ends a cut-off run `failed { aborted, reason: "restart" }`.
	ResumeAbandon JobResume = "abandon"
)

// JobPersistence is how a persisted job's runs resume after a crash or a stop.
type JobPersistence struct {
	Resume JobResume
}

// JobAdapter maps one older version's args to the current args and its result to the current result.
type JobAdapter struct {
	Args   func(old any) (any, error)
	Result func(old any) (any, error)
}

// JobAccepts is, per older version, its mappings forwards.
type JobAccepts map[int]JobAdapter

// OutcomeKind is how a run ended.
type OutcomeKind string

// The kinds of Outcome.
const (
	// OutcomeDone: `run` returned a result.
	OutcomeDone OutcomeKind = "done"
	// OutcomeFailed: the run failed (Outcome.Error).
	OutcomeFailed OutcomeKind = "failed"
)

// JobErrorKind is why a run failed.
type JobErrorKind string

// The kinds of JobError.
const (
	// JobThrew: `run` returned an error.
	JobThrew JobErrorKind = "threw"
	// JobAborted: the run was aborted.
	JobAborted JobErrorKind = "aborted"
	// JobRestartLimit: a persisted run was cut off five times in a row.
	JobRestartLimit JobErrorKind = "restart_limit"
)

// AbortReason is why an aborted run was aborted.
type AbortReason string

// The abort reasons.
const (
	// AbortedByAbort: `abort(id)`.
	AbortedByAbort AbortReason = "abort"
	// AbortedByReload: comes with slice 7.
	AbortedByReload AbortReason = "reload"
	// AbortedByRestart: cut off by its attempt's seal while the object continues, or a persisted run abandoned after a
	// crash.
	AbortedByRestart AbortReason = "restart"
)

// JobError is why a run failed: `run` returned an error (reported as `job_failed`; anything but a *ChordError or
// *PluginError becomes a PluginError `internal` with its message), it was aborted (`abort`: `abort(id)`; `restart`: cut
// off by its attempt's seal while the object continues, or a persisted run abandoned after a crash; `reload` comes with
// slice 7), or a persisted run was cut off five times in a row (`restart_limit`, D6). Only the fields of its Kind are
// set.
type JobError struct {
	Kind JobErrorKind
	// Error is the *data.ChordError or *data.PluginError `run` ended with (JobThrew).
	Error error
	// Reason is why it was aborted (JobAborted).
	Reason AbortReason
	// Restarts is the number of crashes in a row (JobRestartLimit).
	Restarts int
}

// Outcome is what a run ended with, as `finish` and `outcome` get it: its args and state, and its result or its error.
// The type parameters of Pi's Outcome<A, R, S> are type-level only: Args, Result and State are JSON values (State nil
// without a state document).
type Outcome struct {
	ID    string
	Args  any
	State any
	Kind  OutcomeKind
	// Result is what `run` returned (OutcomeDone).
	Result any
	// Error is why it failed (OutcomeFailed).
	Error JobError
}

// DefineJob declares a job: owned work a call starts that outlives it (§15).
func DefineJob(name string, options JobOptions) (*JobDefinition, error) {
	// Declaration policy: callers reach every check below.
	if err := contracts.CheckName(name, "A job name"); err != nil {
		return nil, err
	}
	version, state := options.Version, options.State
	if version < 1 {
		return nil, fmt.Errorf("%s.version: expected a positive integer", name)
	}
	args, err := contracts.SnapshotArguments(options.Args, name+".args")
	if err != nil {
		return nil, err
	}
	if args.Bytes {
		return nil, fmt.Errorf("%s.args: job arguments are JSON (running carries them); no bytes()", name)
	}
	result, err := contracts.SnapshotSchema(options.Result, name+".result")
	if err != nil {
		return nil, err
	}
	if state != nil && state.Keyed {
		return nil, fmt.Errorf("%s.state: expected a singleton document definition", name)
	}
	if options.Persist != nil {
		if err := checkPersistence(options.Persist, state, result, name); err != nil {
			return nil, err
		}
	}
	if options.Concurrency != nil && *options.Concurrency <= 0 {
		return nil, fmt.Errorf("%s.concurrency: expected a positive integer", name)
	}
	if options.Accepts != nil {
		if err := checkJobAccepts(options.Accepts, version, name+".accepts"); err != nil {
			return nil, err
		}
	}
	var stateFingerprint any
	if state != nil {
		stateFingerprint = state.Fingerprint
	}
	text, err := jsstring.MarshalJSON(delta.JsonObjectOf("args", args.Normal, "result", result.Normal, "state", stateFingerprint))
	if err != nil {
		return nil, fmt.Errorf("%s: fingerprint: %w", name, err)
	}
	accepts := options.Accepts
	if accepts == nil {
		accepts = JobAccepts{}
	}
	return &JobDefinition{
		Name: name, Version: version, Args: args, Result: result, State: state, Concurrency: options.Concurrency,
		Accepts: accepts, Persist: options.Persist, Fingerprint: contracts.Hash64(string(text)),
	}, nil
}

// checkPersistence checks a persisted job's declaration: a resume rule, a persisted state document (if any), a
// JSON-only result.
func checkPersistence(persist *JobPersistence, state *documents.StateDefinition, result *contracts.Snapshot, name string) error {
	if persist.Resume != ResumeRerun && persist.Resume != ResumeAbandon {
		return fmt.Errorf(`%s.persist.resume: expected "rerun" or "abandon"`, name)
	}
	if state != nil && !state.Persist {
		return fmt.Errorf("%s.state: a persisted job's state is a persisted document (persist: true)", name)
	}
	if result.Bytes {
		return fmt.Errorf("%s.result: a persisted job's result is JSON (it is stored); no bytes()", name)
	}
	return nil
}

// checkJobAccepts checks an accepts table's shape: older versions only, each with an `args` and a `result` function.
func checkJobAccepts(accepts JobAccepts, version int, path string) error {
	older := make([]int, 0, len(accepts))
	for key := range accepts {
		older = append(older, key)
	}
	slices.Sort(older)
	for _, key := range older {
		if key < 1 || key >= version {
			return fmt.Errorf("%s[%d]: not an older version", path, key)
		}
		adapter := accepts[key]
		if adapter.Args == nil {
			return fmt.Errorf("%s[%d].args: expected a function", path, key)
		}
		if adapter.Result == nil {
			return fmt.Errorf("%s[%d].result: expected a function", path, key)
		}
	}
	return nil
}

// JobDefinition is a declared job. Its args, result and state are JSON (Pi's `A`, `R` and `S` are type-level only).
type JobDefinition struct {
	Name    string
	Version int
	// Args is the args schema, Chord's copy; its compiled check is cached by this snapshot.
	Args   *contracts.Snapshot
	Result *contracts.Snapshot
	// State is the run state document's definition, nil without one.
	State *documents.StateDefinition
	// Concurrency is the limit of running and finishing runs, nil without one.
	Concurrency *int
	// Accepts is the older versions' mappings, checked at declaration; slice 7 maps older runs with them.
	Accepts JobAccepts
	// Persist is how its runs persist; nil for a job in memory.
	Persist *JobPersistence
	// Fingerprint is `hash64` of its args', result's and state's normal forms: a stored run of another fingerprint is
	// blocked.
	Fingerprint string
}
