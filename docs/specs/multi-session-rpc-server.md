# Multi-session RPC server with shared extension cells

Status: implemented on `feat/rpc-listen`, commits `205cfa483..bd6f9cad3` and the Task 7 record. Additive feature record: D232 in `docs/additive-features.md`.

## Intent

PiG runs one Session per process. A client that needs many concurrent sessions (a swarm runner, an IDE, an evaluation harness) starts one `pig --mode rpc` process per session, and every process pays the whole runtime again: about 14 MB of private memory on macOS for the Go runtime and the relocated data segments of the binary, plus every extension process the session loads. jcode and OpenCode v2 host every session in one daemon and pay those costs once.

Measured on an Apple M3 Max against a scripted local model, five turns per session, phys_footprint after the last turn:

| Sessions | 1 | 10 | 20 | 100 |
|---|---|---|---|---|
| `pig --mode rpc`, one process per session | 17.5 MB | 167 MB | 331 MB | – |
| one PiG process hosting every session (Go SDK spike) | 14.7 MB | 22.8 MB | 27.8 MB | 64.1 MB |
| jcode daemon | 16.8 MB | 26.2 MB | 49.0 MB | 146.5 MB |
| `pig --mode rpc` per session, one Node extension loaded | 174 MB | 984 MB | – | – |
| one PiG process, one Node extension loaded per session | 108 MB | 505 MB | – | – |

The last two rows are the problem this design solves: a session's extension processes cost 45 to 90 MB each, so hosting sessions in one process wins nothing while every session starts its own extension processes.

## Goals

1. One `pig` process serves many concurrent RPC sessions, each speaking Pi's RPC protocol byte for byte.
2. Sessions that load the same extensions share the extension processes. Memory per additional session, extensions included, is a small fraction of today's.
3. Every extension sees exactly what it sees in a `pig --mode rpc` process of its own: its own registration, its own replicated state, its own events, its own session lifecycle.
4. Nothing changes for a process that does not opt in. The feature is an inert capability of Stock PiG.

## Non-goals

- A session configuration per connection. Every session of a server has the server's cwd, agent directory, flags, model and tool selection. A different cwd is a different server.
- Interactive, print and JSON modes over the socket.
- Pi's shared-services extension semantics (one factory call serving every session). This design keeps one extension instance per session.
- Reconnecting to a session after its connection closed.
- Authentication on the socket beyond file permissions.
- A change to Pi's RPC protocol. No session id, no multiplexing, no new commands.

## Anti-goals

- Any observable change to an extension's behavior between isolated, packed, shared and fused realizations. A difference is a bug, not a divergence to record.
- A new wire contract between the host and the extension runtimes, or a change the four SDKs must each implement differently.
- A shared process that outlives its last session, or one that dies while a session still uses it.

## Constraints

- `AGENTS.md`: the capability is classified before implementation (inert capability, selected only by `--listen`); it gets a `D232` record in `docs/additive-features.md`, one `// pig additive (D232)` marker at each production decision point, a `changelog.d` fragment, and `make generate` output committed. Extension Host changes are proven across Go, Rust, Python and Node, in isolated, packed and fused realizations, through `test/extension-conformance/`. New duration or cap literals need an upstream citation or a D232 marker for `make divergence-guard`. Windows is a first-class target: the server uses `AF_UNIX`, which Go supports on Windows 10 1803 and later, and the integration tests run in the Windows CI job.
- The RPC protocol per connection stays Pi's. The existing `rpc` parity family proves `pig --mode rpc` against Pi; the server is proven equal to `pig --mode rpc`.
- Extension processes keep one socket and one registration per extension instance (D20). Multi-register and session-tagged frames are out.

## Approaches considered

1. **Share the processes, one extension instance per session (chosen).** A session's host admits its extension members into the cell processes another session already started, as a further factory generation on new sockets, through the admission path reload and session replacement already use. The wire, the SDKs and D20 stay as they are. New: a process-wide cell registry, generations that coexist, and per-session scoping of the Node runtime's process-wide bookkeeping.
2. **One extension instance for all sessions, session-scoped wire.** Pi's `createAgentSessionFromServices` semantics. Every request, state push and call frame carries a session id; each runtime keeps state per session; the host routes calls per session. It contains all of approach 1's runtime work plus a wire change that reaches every extension, in or out of the server. Rejected for the first version; it can be added later on top of approach 1's registry.
3. **One state per cell, context switch before each dispatch.** The host pushes a session's state into the cell before dispatching its request and serializes the cell per session. Rejected: a suspended handler blocks every other session's extensions, and a timer reads another session's state.

## Decisions

### Surface

`pig --mode rpc --listen <path>` listens on the Unix domain socket `<path>` and serves one RPC session per accepted connection. The connection's bytes are the session's stdin and stdout. PiG writes nothing else on the connection.

- `--listen` requires `--mode rpc`. Any other mode fails with an argument diagnostic, as `--tui-mode` with a wrong value does.
- `--listen` rejects `--continue`, `--resume`, `--session`, `--session-id` and `--fork` with an argument diagnostic: every connection creates a new Session, persisted under the session directory unless `--no-session` is given, as each `pig --mode rpc` process does.
- The socket path must not exist. PiG creates the socket with mode 0600 and removes it at exit. A path that exists is an error; PiG does not remove it.
- The process ignores its own stdin. The server runs until a termination signal.
- A closed connection is that session's stdin end: PiG runs the session's shutdown (`session_shutdown` to its extensions, dispose of the runtime), closes the connection and keeps serving. The exit code a `pig --mode rpc` process would have returned is logged at debug level and otherwise dropped.
- A termination signal runs every live session's shutdown concurrently, then exits with `128 + signal`, as `pig --mode rpc` does for one session. Extension processes are terminated after the shutdowns, as today's forced exit does.
- `--listen` keeps D174's general GOMAXPROCS cap of 2 and does not apply the rpc cap of 1, because the process runs many sessions. It does not apply D175's GOGC 400 start, because resident memory is the server's objective. Both are recorded in D232.

### Per-connection runtime

- The startup build (project trust, services, resources, extension load) serves the first connection, as it serves the only session of `pig --mode rpc` today.
- Every later connection builds through the same cwd-bound factory a session replacement uses (`cliRuntimeBuilder.rebuild`): its own `SettingsManager`, resource loader, extension host, `ModelRuntime` and Session. Builds run one at a time under one mutex, the replacement builds a connection's `new_session` or `switch_session` command requests included; sessions run concurrently.
- Connections do not share a `ModelRuntime`. Pi's `createRuntime` builds a fresh one per replacement (`main.ts:733-775`, `createRuntime`; `agent-session-services.ts:140-147`, `ModelRuntime.create` when the options carry no runtime), and an extension's `registerProvider` and `registerVirtualModel` land in the runtime (`coding/model_runtime_native.go`, `model_runtime_members.go`): two sessions' instances of one extension would register the same provider twice, and the first session to close would unregister it from the other. A per-connection runtime costs its own catalog state; the bench below records that cost.
- `serveRPCMode` takes its reader, its writer and its exit through one transport value instead of `os.Stdin`, the raw stdout writer and `os.Exit`. For `pig --mode rpc` the transport is the process's; for a connection it is the connection, and exit closes the connection. Process-wide signal hooks (`setTerminationPrelude`, `setTerminationShutdownHook`) are installed once by the server and fan out to every live session.
- The RPC command loop, admission, UI bridge, shutdown ordering and event forwarding are per session and unchanged.

### Shared extension cells

- A process-wide `CellRegistry` records every running cell process under a key: the planner's cell hash, the cwd, the config root and the runtime tier. A Host joins it only when the server builds it (`Host.ShareCells(registry)`); every other Host in PiG keeps today's behavior.
- A Host with a registry, before it spawns a cell process, first claims a parked process from its own `RuntimeRetention` (reload and replacement, unchanged), then asks the registry for a live, unparked, unquarantined process under the key. Found: it acquires a reference and admits its members through the existing admission path (`startManagedIn` over a retained share for an isolated Node extension, the retained-share paths of packed Go, Rust, Python and Node cells), one new socket per member. Not found: it spawns the process and registers it.
- A process ends with its last generation, through the existing reference count. A Host's shutdown releases its generations and never kills a process another Host still uses. `TerminateProcesses` runs only at server exit.
- A crash of a shared process ends every generation in it. Each Host's supervisor handles its own members as today; the first Host to respawn registers the new process and the others join it. A quarantined cell starts isolated and is never shared. The registry key includes the cwd and config root so two servers in one process could never share a cell; one server has one cwd.
- The admission line format (`admit`, `park`, `noop`) and the registration protocol do not change.

### Scoping the Node runtime per session

The Node runtime keeps bookkeeping per process that assumes every member belongs to one session. The host identifies the session group of a member with a string unique per Host in the process. The payload carries it as a new optional `ready` member `group`, which the Go, Rust and Python SDKs ignore. The Node runtime does not read it from `ready`: its factory subscribes to the bus before `ready` arrives, so the group travels on the cell's admission line (a trailing field, present only for a Host that shares cells) and is set before the factory runs. `ready.group` stays an informational wire member. A runtime that receives no group behaves as today.

In `runtime.mjs`, these become per group:

- the in-process event bus (`sharedBus`): one bus per group, so `pi.events` emitted in one session never reach another session's instance in the same process (the routed bus mode is already host-scoped);
- the virtual-model write hold (`holdVirtualWrite`), which today writes into every live runtime of the process: only the runtimes of the emitting group hold the write;
- the quit drain (`quitDrain`): a connection end drains only its group;
- the xref transport selection (`xrefRealm`): a payload crosses to a connection of its own group.

The sibling-tool check that marks a timer call as a tick (`siblingToolPending`) counts only the group's tools: another session's running tool is no sibling. These stay per process, because the group shares one event loop: `turnHold`, `turnWait`, `shareDecodedFrames`. The print and JSON prompt-end waits (`hostStep`) are not engaged by rpc-mode sessions and are not scoped; a multi-session host for print or JSON mode is out of scope.

The Go, Rust and Python runtimes route their event bus through the host and key listeners by process and handler id, with process-unique ids, so no change is expected. The conformance row below proves it.

### Evidence

Integration tests in `coding/cli`, faux provider, hermetic:

1. **Equality with `pig --mode rpc`.** One connection runs the script of an existing rpc scenario; its event stream equals the stdout of `pig --mode rpc` for the same script, under the comparator the rpc family uses (`compareJSONResults` in `test/parity/runner/asserts.go`, or its normalization copied if the import does not fit).
2. **Concurrency.** Three connections prompt at once; each receives its own events only; disconnecting one leaves the others running; the server exits on SIGTERM with `128 + 15` after `session_shutdown` reached every session's extension.
3. **Shared cells.** A Go factory and a Node factory extension are configured. With one, two and three connections the process tree holds exactly one Go cell process and one Node cell process. A tool that returns a per-instance counter returns independent values per connection. `pi.events` emitted in one connection do not reach another. A virtual model registered in one connection is not listed in another. `session_shutdown` reaches an instance only when its connection closes. The cell processes exit after the last connection closes.
4. **Crash recovery.** The shared Node process is killed while two connections are live; both sessions' instances restart and answer a tool call; the process count returns to one.
5. **Flags.** `--listen` without `--mode rpc`, with a resume flag, or with an existing path fails with the specified diagnostic.

Conformance row in `test/extension-conformance/`: two Hosts share one registry; their instances of one extension are admitted into one process for Go, Rust, Python and Node, isolated and packed, and the fused Go realization; each instance holds its own state and bus; the behavior equals two Hosts without a registry.

Memory evidence: a bench script under `bench/` starts `pig --mode rpc --listen` against the scripted model, connects N = 1, 10, 50, 100 sessions, runs the five-turn workload, and records RSS and phys_footprint (PSS on Linux) with and without the two extensions, beside `pig --mode rpc` per session. The numbers go into the D232 record.

### Records and documentation

- `docs/additive-features.md`: `## D232` with the Stock disposition (inert capability), what, current behavior, the Pi source note (Pi's RPC serves one session per process; Pi's SDK shares one resource loader across sessions from one services object (`agent-session-services.ts:214-233`)), the policy decisions above, the markers, the tests, the measurements, and the remove-when condition (Pi gains a multi-session RPC mode).
- `// pig additive (D232)` markers: `coding/cli/args.go` (`--listen`), `coding/cli/rpc_mode.go` (transport, server loop), `coding/extension/host/subprocess` (registry opt-in and lookup), `coding/extension/host/subprocess/protocol.go` (`ready.group`), `runtime.mjs` (group scoping).
- `docs/site/docs/cli.md` (PiG Options), `docs/site/docs/rpc.md` (a "Serve many sessions" section with a minimal client), `pigCommands` help text, `docs/extension-api-parity.md` (the cell-sharing invariant, the `ready.group` member and its async contract), `changelog.d/<lane>-<sha>.md` under `### Added`.
- `make generate`, `make ci-drift ci-contracts`, `make lint-changed`, the affected `make parity-family rpc` and `make test` groups before the handoff.

## Left to the builder

- The transport value's exact shape and where `rpcModeResources` grows a `Listen` member.
- Whether the registry lives in `subprocess` or a sibling package, and its lock discipline against `RuntimeRetention`.
- The group id's format and how `runtime.mjs` carries it from `ready` to the bus, the write hold, the quit drain and the xref transport.
- Test fixtures: the counter extension in Go and Node, and the bench script's location and name.
- Debug logging of per-connection exit codes.

## Approval

The `--listen` proposal was posted on the PiG Discord and approved there on 2026-11-03, before PR #404 was opened. The D232 record carries that approval; the review of the PR is the owner's sign-off.
