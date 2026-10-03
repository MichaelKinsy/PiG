# durable-final-100

Closes R20: the four partial durable hot-path rows of `test-mapping-v1.0.0.json` are `ported`. Base `agg-100` `a96c8df7f` plus `rev-durable-partials-100` (`024420026`: the earlier lane's ported `spec-usage`, the facet-host `chord-guide`, `harness.Hook` generic over its task, `internal/testenv/typecheck.go`). `harness-tools.test.ts` was already ported on agg-100; the "fourth" partial row was `spec-usage.test.ts`, which that lane closed.

| Row | Before | After | How |
|---|---|---|---|
| `chord-guide.test.ts` | partial (canvas ordering) | ported | Source fix: `CommitWith` runs the Session state attachments' queued drains on the committing goroutine after the line is free, before Commit returns (`pending.queueDrain`/`runDrains`; `WaitDeliveries` also runs them). The guide's canvas case no longer waits before `host.Dispose`, as upstream. |
| `session-definitions.test.ts` | partial (scope-typed tokens) | ported | Equivalence test: each negative that only the token's type rejects runs on Pi's runtime inputs (resolveAddress ignores extra owner and seed arguments; Go's resolver gives the same results). Undefined seed, mistyped seed and missing entry data are the JS-only forms, pinned to Go's zero value, rejection with no document, and zero data. |
| `types.test.ts` | partial (TaskId result, record unions, missingPhase) | ported | Equivalence tests: 15 member-exclusivity literals encode as Pi's objects; the SubmissionDraft literals are ignored by Submit as in submissions.ts; typed results decode from the settled outcome; a missing phase handler faults the task as a throwing handler does. |
| `spec-usage.test.ts` | partial | ported | already by rev-durable-partials-100 |

## Red to green

| Test | Red |
|---|---|
| `TestChordUsageGuide` (canvas, reviews) | with `runDrains` disabled: canvas logs only the hydration; reviews time out |
| `TestSessionStateSubscribersRunBeforeTheCommitReturns` | same mutation (commit-return ordering and reentrant subscriber) |
| `TestDocumentDefinitions` equivalence case | a Session document that consumes an owner argument |
| `TestTypesUpstreamExclusivityLiteralsEncodeAsPiObjects` | `SubmissionRecord.Answer` tagged `json:"-"` |
| `TestSubmissionDraftIgnoresTheFieldsOfTheOtherType` | the busy-reject check without the input type guard |
| `TestMissingPhaseHandlerFaultsTheTaskAsAThrowingHandlerDoes` | the missing-handler failure removed |

Not red-proven first: the equivalence tests assert existing behavior against Pi's source (no Pi runtime was run; Pi's node_modules are not installed, expected values come from reading `documents.ts`, `transaction.ts`, `submissions.ts`, `scheduler.ts`).

## Known limits

- Equivalence tests pin Go's form of JS-only inputs (zero seed for `undefined`); that is not Pi's output byte for byte.
- A subscriber that commits from inside a Session state delivery now runs its own commit after the outer line job, and its frame reaches the same subscriber before the outer Commit returns. Pi's microtask order is the same shape; a second goroutine's commit can interleave, as any two Session callers can.

## Commands

`go test ./durable/... ./chord/... ./internal/chord/... ./internal/testenv/` (green), `-race -count=20` of the new session test, `-count=50 -cpu 1,4` of the guide, `go tool golangci-lint run ./durable/...` (0), `make test-inventory`, `make test-porting-release` (603 ported, 0 partial), `make known-gaps` (regenerated), `source-hygiene`, `interface-go-drift`, `port-map-drift`, `known-gaps-drift`.
