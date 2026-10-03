# d-harness-a progress

Lane: port of Pi 1.0.0 `@earendil-works/pi-durable` `src/harness` core to `durable/harness`.

## Scope and state

| Upstream | Go | Test port |
|---|---|---|
| `src/harness/types.ts` (harness half) | `types.go` | — |
| `agent.ts`, `define.ts`, `registry.ts`, `util.ts`, `json.ts` | `agent.go`, `define.go`, `registry.go`, `util.go`, `json.go`, `ordered_map.go` | `harness-registry` (9/9) |
| `output.ts` | `output.go` | `harness-output` (17/17) |
| `prompt.ts` | `prompt.go` | `harness-prompt` (9/9) |
| `context.ts` | `context.go` | `harness-context` (6/6) |
| `harness.ts` | `harness.go` | `harness-conversations` (12/12), `harness-inspect` (2/2), `harness-lifecycle` (14/14) |
| `view.ts` | `view.go` | `harness-view` (14/14) |
| `task-graph.ts` (unowned; taken by this lane) | `task_graph.go` | `harness-task-graph` (5/5) |
| `events.ts` | `events.go` | `harness-events` (14/14) |
| test support | `harness_support_test.go`, `harness_open_support_test.go`, `task_support_test.go`, `session_support_test.go`, `chat_support_test.go`, `future_support_test.go` | — |

d-harness-b owns generation, compaction, scheduler, live, inbox, submissions, usage and tool.

## Decisions

- Root harness types (Agent, ToolRegistration, Extension, Settings, ContextView, PromptSection, HookRegistration) live in foundation's `durable/harness_types.go` to break the Go import cycle; `durable/harness/types.go` declares the rest.
- Hooks are structs of optional funcs registered as pointers so handlers compare by identity. A wrapper that throws upstream panics in Go; `callWrapper` recovers, converts with `env.ToError`, drops the target and reports.
- Extensions and tools compare by pointer identity of the defined value, as upstream compares object identity.
- `OrderedMap[T]` mirrors JS `Map` insertion order where upstream iterates a Map.
- `AssignJson` assigns keys in sorted order: Go maps carry no insertion order.
- Drafts are `*delta.Object` handles (d-session's chord/delta); `CreateAgent` copies the owner by `Keys()` order.
- `AgentEvent` is a sealed interface with one struct per event; each marshals to the upstream discriminated union (`type` first). Event identity checks compare raw JSON revisions (map/slice identity), as upstream's `===` over immutable revisions.
- View and task-graph mount bookkeeping is locked: a `CommittedWatch` releases from its delivery goroutine when its listener fails, which is off the Session line (found by -race).
- Tests: `drained()` is a no-op Session line job plus `WaitDeliveries()` (upstream `setTimeout(0)`); an acquisition queued behind a held commit is detected with `SessionImpl.LineJobs()`; close beginning (synchronous upstream) is awaited through a close listener. No sleeps decide ordering.
- SQLite: this lane adds no storage; tests use d-storage's pure-Go `durable/storage/sqlite/node` (modernc.org/sqlite, CGO_ENABLED=0).

## Stubs

None remain. `stubs.go` (ToolTask, TaskGraph, ConversationViews) and `stubs_scheduler.go` were deleted as tool.go, task_graph.go, view.go and scheduler.go landed.

## Findings outside this lane

- `ai.SystemMessage.Sections` has `omitempty`, so an empty baseline sections object is dropped when persisted; replay is unaffected.
- `ai.Usage.MarshalJSON` fills a zero `totalTokens` with the sum, so a JSON comparison of a raw usage with `totalTokens: 0` differs from upstream; `harness-events` compares that update as the typed value.
- chord/delta (d-session): a Splice/Pop/Shift that emptied an array recorded no op, so removing the last inbox item never committed and a queued follow-up was placed twice. Reported; fixed in d-session 27355498c.
- scheduler (d-harness-b): `Join` waits on the background WaitGroup while an ending invocation's `kick` adds to it; flaky `-race` panic. Reported.

## Red → green

- output, registry, prompt, context, conversations: red against the missing source, green after the port; compiling mutations killed (bound arithmetic, decoder lead ranges, tool dedupe, wrapper drop, section order, OrderedMap position, entries scope, head rebaseline, CreateAgent skip, init order, closed flag).
- inspect, lifecycle: red against the scheduler/view/task-graph stubs ("not ported" errors, a hung tool case), green after scheduler.go, tool.go, view.go and task_graph.go.
- view: red against the stub; -race then found the off-line release race, fixed with a lock; mutations of head cut, mount drop and closed check killed.
- task-graph: red against the stub; mutations of the no-change skip, conversation sort, mount drop, outcome status killed.
- events: 11/14 green on first run; 3 red (:213, :360, :447) on the chord/delta empty-splice bug, green after d-session 27355498c.
