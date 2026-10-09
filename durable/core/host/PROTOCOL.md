# Host protocol: what the hosts send and expect beyond ABI.md

Status: host proposal, 2026-10-06, lane dcore-host (first proposed as dcore-cfhost). ABI.md fixes the event, effect and notice kinds, the binary layouts and the `api` table (section 8.1). It leaves the JSON payload bodies of most events to the core. This file is the bodies the hosts in this repository (`host/cf/src`, `sqlhost`) send and read. The core owners ratify a row or replace it; until then a hosts-versus-core disagreement is a bug in whichever side departs from this file. `abi_id` does not cover these bodies yet.

Code of record: `host/cf/src/protocol.ts` (shapes), `host/cf/src/effects.ts` (effects), `host/cf/src/facade/` (events and transactions).

## Events

| event | body | notes |
|---|---|---|
| `open` | `{mode: "harness" or "session", settings, models, registry, sidecar, delta, hasEnv, hasConversationCreated}` | `settings` is `HarnessSettings` over its defaults, `extensions` as names. `models` is the catalogue entries the host lets the core name (default: every model the `models` object lists). `registry` is the snapshot below. `mode: "session"` opens the tables-and-documents Session of `createSession`: no built-in documents, no scheduler |
| `registry` | `{registry, resume?: true}` | a registry publication after open, or the first call that asks for progress (`resume()`, `submit`, `compact`, `abort`, the waits): scheduling stays paused until the host says `resume` |
| `submit` | `{conversationId, type: "input" or "write", content or entry, requestId, whenBusy?}` | the host always sends a `requestId` (a minted UUID when the caller gave none) and matches the `published` notice on it |
| `abort` | `{requestId, conversationId?, background?}` or `{requestId, taskId}` or `{requestId, submissionId, conversationId?}` | the core answers with an `api_result` notice carrying `requestId`: `"marked" or "terminal"` for a task, `"aborted", "already_placed", "settled" or "not_found"` for a submission, `null` for a conversation |
| `inspect` | `{query: "harness", requestId}` | `HarnessInspection`: scheduling state, live tasks with their scheduler state, unsettled submissions. Answer: `api_result` with `requestId` |
| `tx` | `u32 txId` then `{op, ...}` | section 8. `begin` carries `{invocation?, conversationId?}` |

### Registry snapshot (`open.registry`, `registry.registry`)

`{extensions: [{name, tools, sections, hooks, wraps, tasks}]}` in installation order. Every `handler` is an index into the host's append-only function table: an index never changes meaning, so an effect issued under an older snapshot still resolves after a reload.

- tool: `{name, description, parameters, replay, handler, executionMode?, outputLimits?, prepare?}`
- section: `{key, tag, handler}`
- hook: `{task, handlers: {hookName: handler}}`
- wrap: `{tool, handler}` or `{section, handler}`
- task: `{name, version, phases: [names], abort: bool, handler}`

## Transactions (ABI section 8)

Operations carry the `Tx` method name and a JSON object of its arguments. An absent record is `null` on the wire and `undefined` in the host.

`conversation{id}`, `entry{id | kind,id}`, `task{id}`, `scanConversations`, `scanEntries`, `latestHeadMarker`, `scanTasks`, `submissionByRequest`, `createRootConversation`, `createConversation{ownership}`, `forkConversation{parentConversationId, at, ownership}`, `appendEntry{conversationId, kind?, value}`, `createTask{task, version, state, input, options}` (the host computes `initial(input)`), `createSubmission`, `settleSubmission`, `placeSubmission`, `doc{address, version, seed?}` (answer `{value}`), `doc.write{address, ops, baseRevision}` (Chord `Op[]` from `@earendil-works/chord/delta`, sent for every changed draft before `end`), `retireDoc{address}`, `end{state?}`, `abort{error}`.

The core runs the built-in conversation-creation hook inside `createRootConversation`, `createConversation` and `forkConversation`, and calls the host through a `callback` effect named `conversationCreated` when `hasConversationCreated` is set.

## Effects: fields beyond ABI.md section 6

- `tool` adds `handler` (registry index). Without it the host takes the first installed tool of that name.
- `section` adds `handler`. Without it the host asks the `agent` api and indexes `sections`.
- `hook` names: `beforeRequest {messages}`, `afterResponse {message}`, `onYield {answer}`, `afterTools {assistant, results}`, `beforeTool {call}`, `afterTool {call, result}`, `beforeCompact {compaction}`, `prepareArguments {arguments}`, each with `handler`. Host-answered hooks take no handler: `resolveAgent {state}` returns the resolved agent (wraps are host code), `conversationCreated`.
- Resolved agent JSON: `{model?, thinkingLevel, extensions: [names], tools: [{name, description, parameters, replay, handler, ...}], sections: [{key, tag, handler}], instructions?, cwd?}`. The core caches it per registry publication and stored `pi.agent` state.
- `env` adds `taskId`: the host keeps the built environment under it and gives it to the task's `section` and `tool` effects.
- `model_http` payload: `u32 headLength`, the head JSON, then the body recipe of section 10.2. The host answers with `model_bytes` phase 0 `{status, statusText, headers}`, chunks, end, or a transport error.
- `callback` names: `conversationCreated {conversation}`; document `initial`, `migrate` and `checkpointWhen` of extension definitions, and a conversation's `init` (names to be fixed with the document family owner).

## Hook results and section input (both hosts)

`hook_done` result by hook: `beforeRequest` `{messages}` or `null`; `onYield` `{continue: UserInput}` or `null`; `beforeTool` `{arguments}` or `{block}` or `null`; `afterTool` a tool result object `{content, isError?, details?, diagnostics?, usage?, control?}` or `null`; `beforeCompact` `{decline: true}` or `{summary}` or `null`; `prepareArguments` the repaired arguments; `afterResponse` and `afterTools` `null`. A hook that throws completes with outcome 1 `{name, message}`. A handler of the wrong kind for the hook name, an unknown hook name, or an undecodable payload is a protocol failure: the Go host discards the handle (the TS host reports the same case as a thrown outcome until the core ratifies this).

`section` input is `{shown: {key: text}}`. The host adds the conversation, the agent it resolved, the task's environment and a document reader. The `section` index selects `agent.sections[index]`; an index out of range is a protocol failure. The result is the rendered text or `null`. `tool` results use the same object as `afterTool`.

## Events the host sends for effects

- `model_event`: a JSON array of pi-ai events, batched until the next macrotask or 64 events. pi-ai mutates one partial message in place, so only the last event of a batch carries its `partial`; an earlier event carries `partial: null`, unless the content was still empty when it arrived (the partial throttle skips empty partials, CONTRACT section 3.9). The core must not read `partial` of a non-final event except to test for empty content.
- `tool_progress` with `skipped` non-zero: the field holds `skipped.bytes`; the payload is `u32 metaLength`, `{bytes, newlines, endsWithNewline}`, then the chunk. `skipped` zero: the payload is the chunk.
- `tool_done` outcome 1 and `hook_done` outcome 1 carry `{name, message, stack?}`.

## Notices

`published` body: `{submissions: [{id, conversationId, requestId, status}], entries: [ids], tasks: [{id, kind, status}], documents?, watchId?, frame?}`. A submission appears with its `id` when admitted and again on every status change. The host reads the settled record itself from `submissions`.

## Host-served reads (CONTRACT section 3.10)

`getTask` and `submission` read `tasks.record` and `submissions.record` from Pi's tables on the host connection. They need no core state.

## Not supported by the CF host yet

`Session.documentState`, `Conversation.viewState` and `watch`, `Harness.taskGraph` and `watchTaskGraph`, `watchEvents`, document watches (`watch.open` is in the api table, the host does not use it). They throw `... is not supported by the Durable core host yet`.
