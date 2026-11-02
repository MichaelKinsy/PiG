package documents

import "context"

// The types Pi's `transaction.ts` imports from `jobs/` (`JobHandle`, `Outcome`, `Run`, `StartOptions` and the
// internal `Job`), as the interfaces `Tx.Start` needs. The jobs (chord/next/host) satisfy them structurally, so this
// package imports nothing of them: Go has no import cycles, and documents is below jobs.

// StartOptions is what `start` and `tx.start` take besides the args.
type StartOptions struct {
	// Key names one run: a start with the key of an unsettled run returns that run (args not compared); nil is no key.
	Key *string
}

// Run is a started run as its caller holds it: its id and a wait for its outcome.
type Run interface {
	ID() string
	// Outcome waits for the run's outcome once `finish` committed (also without a `finish`); ctx ends only this wait.
	Outcome(ctx context.Context) (any, error)
}

// JobHandle is a job as a transaction stages a run of it (Pi's internal `Job.stage`): the job checks that tx is its
// object's, that its attempt is live, and stages the run's records in tx. A start that only dedupes stages nothing.
type JobHandle interface {
	Stage(tx *Transaction, args any, ctx context.Context, options StartOptions) (Run, error)
}
