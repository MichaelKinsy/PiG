// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package hopcost

// The ticks of the group commit Session line (packages/durable/src/session/session.ts #enqueue, #onLine and writer.ts) that no
// Storage adapter decides: they follow from the promise structure of the Session itself, so they are the same for every profile.
// An `await p` of a native promise resumes one tick after p settles; an async function that returns a promise settles two ticks
// after it (the resolve-thenable job, then its reaction), never before tick 2 after the return; `then` runs its callback one tick
// after the promise it is attached to settles. Each constant cites the structure it counts and is measured on the pinned Pi by
// testdata/gc_ticks.mjs, whose output session's TestLineTicksMatchPi compares to this model.
//
// A line job's `work` (#runCommit) returns at tick r from the job's start: for a commit with writes at its admission to the
// writer (the writer's call to Storage.commit starts in that tick, when no call is in flight), for any other job where it
// returns its value.

const (
	// RunResolved is the ticks from the return of `work` (r) to the settlement of `ran`, the promise #enqueue chains the job's
	// outcome and the line's tail on: `#onLine` awaits `work` (r+1) and returns; the async callback of `this.#tail.then(...)`
	// returns that promise, which it adopts (r+3, after the resolve-thenable job and its reaction); the promise `then` returned
	// for the callback adopts it again (r+3 as measured: the two adoptions overlap).
	RunResolved = 3

	// LineRelease is the ticks from the return of `work` to the start of the next queued job's callback: `this.#tail = ran.then(ignore,
	// ignore)` settles one tick after ran, and the next job's `this.#tail.then(...)` callback runs one tick after that.
	LineRelease = RunResolved + 2

	// OutcomeSettled is the ticks from the return of `work` to the settlement of a job's `outcome` when it admitted no part:
	// `ran.then(...)` runs one tick after ran.
	OutcomeSettled = RunResolved + 1

	// OutcomeSettledAfterPart is OutcomeSettled for a job that admitted a part whose publication came first: `ran.then(...)` returns
	// `result.after.then(...)`, which runs a tick after that callback, and `outcome` adopts it two ticks after the callback returned.
	OutcomeSettledAfterPart = OutcomeSettled + 2

	// OutcomeAfterPublication is the ticks from a part's publication to the settlement of its job's `outcome`: the part's
	// outcome promise resolves in the publication's tick; `result.after.then(...)` returns the job's value one tick later, and
	// `outcome`, which adopted that promise, follows one tick after it.
	OutcomeAfterPublication = 2

	// FenceStep is the ticks from the settlement of the fence of the job before (its `fence.then(() => outcome)` result, `x`) to
	// the earliest settlement of the next job's: `.then(ignore, ignore)` settles the fence a tick after x, the next job's
	// `fence.then(() => outcome)` callback runs one tick after that and returns its outcome, whose adoption takes two more.
	FenceStep = 4

	// FenceAfterOutcome is the ticks from the settlement of a job's outcome to the settlement of its own fence promise (x): the
	// adoption of the outcome by the `fence.then(...)` result is one reaction once the outcome has settled.
	FenceAfterOutcome = 1

	// Delivery is the ticks from the settlement of a job's fenced result (x) to the resumption of its caller: the `.then` that
	// notes the delivery runs one tick after x, and the caller's reaction one tick after that.
	Delivery = 2

	// ExemptDelivery is the ticks from the settlement of the outcome of an exempt job that admitted nothing and waited at no gate
	// to the resumption of its caller (`ran.then((result) => deliver(false))`, whose promise the exempt wrapper adopts): the
	// outcome's own reaction (1), the wrapper's adoption (2) and the caller's reaction (1) less the tick the outcome's
	// settlement shares with the first.
	ExemptDelivery = 3

	// WriterRepump is the ticks from the end of a call's publications to the writer's next call: `#publish` returns, `#send`
	// resumes from `await this.#publish(...)` one tick later and returns, and `#flush` resumes from `await this.#send(...)` one
	// tick after that, clears `#flight` and pumps.
	WriterRepump = 2

	// WriterRepumpUnsent is WriterRepump of a call that returned from `#send` without publishing (the Session failed first):
	// `#flush` resumes one tick after the return.
	WriterRepumpUnsent = 1

	// PublishResume is the ticks from the macrotask that ends the hop between two parts to the next part's publication: the
	// macrotask resolves the hop promise and `#publish` resumes from `await hop()` one tick later.
	PublishResume = 1

	// FailureSettle is the ticks from the last part's publication to the writer's report of a failure (`await
	// Promise.resolve()`): after the host dispatch of the last part published, which is one microtask away.
	FailureSettle = 1

	// FailedCallbackReturn is the ticks from the check of the Session's health after the change callback (#runCommit :1092) to the return of
	// the work when it throws: the catch's `await tx.settleFailure()` (two ticks, see Job.ReturnFailed) precedes the rethrow.
	FailedCallbackReturn = 2

	// GateResume is the ticks from the publication of the part a job's touch waits for to the continuation of the job: `touch` returns
	// `gate.then(() => this.#assertHealthy())`, which settles a tick after the gate opens, and the callback's await of it resumes one
	// tick later.
	GateResume = 2

	// GatedReadAdoption is the ticks a read through the Session's gated Storage takes beyond its own after its gate opened: `gate.then(read)`
	// returns a promise that adopts the read's, so the caller resumes one tick after the read's own resumption (keys.ts #after).
	GatedReadAdoption = 1

	// BackpressureResume is the ticks from the delivery of a job's outcome (the tick `#delivered` runs, one before the caller
	// resumes) to the start of a job that waited for room: `room.resolve()` runs in `#delivered`, and the waiting callback's
	// `await this.#room.promise` resumes one tick after it.
	BackpressureResume = 1
)

// MaxOutstanding is session.ts MAX_OUTSTANDING: the most jobs that started on the line and whose outcome is not delivered yet.
const MaxOutstanding = 256

// StoreCallResume is the ticks from the writer's call of Storage.commit to its resumption from `await this.#host.commit(...)`
// when the backend's returned promise settles the given number of ticks after the call: the Session's GuardedStorage.commit
// adopts it through `(async () => backend.commit(...))()` and awaits that, and the writer awaits the guard. Same arithmetic as
// guarded: the backend promise's settlement, awaited by a caller one tick later (backend), becomes max(d, 2) + 2.
func StoreCallResume(settles int) int { return guarded(settles + 1) }

// MemoryCommitResume is StoreCallResume for MemoryStorage.commit (storage/memory.ts), an async function without awaits: its promise
// settles in the tick of the call, and `receive` runs in that tick too.
const MemoryCommitResume = 4
