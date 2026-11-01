# Multi-session RPC server implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `pig --mode rpc --listen <socket>` serves one Pi RPC session per connection in one process, and sessions that load the same extensions share the extension processes.

**Architecture:** The RPC mode gets a transport (reader, writer, exit) so one `runRPCMode` serves a connection as it serves stdin today; a server loop accepts connections and builds each one through the replacement path of `cliRuntimeBuilder`. In the Extension Host a process-wide `CellRegistry`, joined only by the server's Hosts, lets a Host admit its members into cell processes another Host already runs, through the generation admission reload uses. The Node runtime scopes its process-wide bookkeeping by a `group` the host puts in `ready`.

**Tech Stack:** Go 1.26 module floor (toolchain from `.go-version`), Node runtime `runtime.mjs`, the Go/Rust/Python SDK packed runners, `test/extension-conformance`, Python 3 for the bench script.

**Spec:** `docs/specs/multi-session-rpc-server.md`

## Global Constraints

- Every production decision point carries `// pig additive (D201): ...` (a `//` comment in `runtime.mjs`); the record is `## D201` in `docs/additive-features.md` with `Approval: pending owner review`.
- Nothing changes for a process without `--listen`: existing tests are the regression guard for every refactor task, and `make parity-family rpc` stays green.
- Extension processes keep one socket and one registration per extension instance; no multi-register, no session id on request, state or call frames. The only wire addition is the optional `ready.group` string.
- The Go, Rust and Python SDKs ignore `ready.group`; the conformance row runs every SDK in isolated, packed and fused Go realizations.
- New duration or cap literals carry an upstream citation or a D201 marker (`make divergence-guard`).
- Windows is a first-class target: `net.Listen("unix", path)`, no named pipes; the integration tests run in the Windows CI job.
- Commits: Conventional Commits, `git commit --signoff -S`, stage only listed files. Changelog fragment `changelog.d/rpc-listen-<sha>.md`, `### Added`.
- Before the handoff: `make generate`, `go vet ./coding/... ./test/...`, `make lint-changed`, `make test-changed`, `make ci-drift ci-contracts`, `make parity-family rpc`.
- Docs: direct technical English, one instruction per sentence, no hard wrap.

## Review Focus

1. A client connects and disconnects before its build finished: the build is discarded, no Host or process leaks, the next connection works. Test in Task 4.
2. A socket path longer than the platform limit (104 bytes on macOS, 108 on Linux): `pig --listen: listen <path>: <os error>` on stderr, exit 1, no hang. Test in Task 4.
3. SIGTERM while a connection's build is in progress: the server exits with `128 + 15` within the test budget. Test in Task 4.
4. Two Hosts start the same cell at the same time and both registry lookups miss: exactly one process; the second Host admits. Test in Task 3.
5. The shared extension process crashes while one session is inside a tool call: that session gets the tool error, the other session's next tool call works, the process count returns to one. Test in Task 6.

---

### Task 1: `--listen` flag

**Files:**
- Modify: `coding/cli/args.go` (the `Args` struct near line 128, the parse loop near `case arg == "--no-stdin"` at line 353, the post-loop validation, `pigCommands` at line 417)
- Test: `coding/cli/args_listen_test.go`

**Interfaces:**
- Produces: `Args.Listen string` (the socket path; empty when absent).
- Produces: the diagnostics `--listen requires a socket path`, `--listen requires --mode rpc`, `--listen cannot be combined with --continue, --resume, --session, --session-id or --fork`, as `argDiagnostic{Type: "error"}`.

- [ ] **Step 1: Write the failing test**

```go
func TestParseArgsListen(t *testing.T) {
	cases := []struct {
		args       []string
		listen     string
		diagnostic string
	}{
		{[]string{"--mode", "rpc", "--listen", "/tmp/x.sock"}, "/tmp/x.sock", ""},
		{[]string{"--mode", "rpc", "--listen"}, "", "--listen requires a socket path"},
		{[]string{"--mode", "rpc", "--listen", "--no-session"}, "", "--listen requires a socket path"},
		{[]string{"--listen", "/tmp/x.sock"}, "/tmp/x.sock", "--listen requires --mode rpc"},
		{[]string{"--mode", "rpc", "--listen", "/tmp/x.sock", "--continue"}, "/tmp/x.sock", "--listen cannot be combined with --continue, --resume, --session, --session-id or --fork"},
		{[]string{"--mode", "rpc", "--listen", "/tmp/x.sock", "--session", "s"}, "/tmp/x.sock", "--listen cannot be combined with --continue, --resume, --session, --session-id or --fork"},
	}
	for _, tc := range cases {
		flags := parseArgs(tc.args)
		if flags.Listen != tc.listen { t.Errorf("%v: Listen = %q, want %q", tc.args, flags.Listen, tc.listen) }
		got := ""
		for _, d := range flags.Diagnostics { if d.Type == "error" { got = d.Message } }
		if got != tc.diagnostic { t.Errorf("%v: diagnostic = %q, want %q", tc.args, got, tc.diagnostic) }
	}
}
```

Also assert `pigCommands` contains the line `  --listen <path>                Serve one RPC session per connection on the Unix socket at path (with --mode rpc)`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./coding/cli -run TestParseArgsListen -v`
Expected: FAIL, `flags.Listen undefined`.

- [ ] **Step 3: Implement**

Add `Listen string` to `Args` beside `NoStdin` with the comment `// Listen is the Unix socket path --listen serves one RPC session per connection on; pig additive (D201).` Parse `--listen <path>` like `--use-theme` (a missing value or a value starting with `-` is the first diagnostic). After the loop, when `Listen != ""`: emit the mode diagnostic when `Mode != "rpc"`, and the combination diagnostic when any of `Continue`, `Resume`, `Session != ""`, `SessionID != ""`, `Fork != ""` holds. Add the help line to `pigCommands` under `PiG Options`.

- [ ] **Step 4: Run tests**

Run: `go test ./coding/cli -run 'TestParseArgs|TestArgs|Help' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add coding/cli/args.go coding/cli/args_listen_test.go
git commit --signoff -S -m "feat(cli): parse --listen for a multi-session rpc server"
```

### Task 2: RPC transport

**Files:**
- Create: `coding/cli/rpc_transport.go`
- Modify: `coding/cli/rpc_mode.go` (`rpcModeResources` at line 74, `runRPCMode` at line 214, `serveRPCMode` at line 386: `exitRPC`, `forceSignal`, the `setTerminationPrelude`/`setTerminationShutdownHook` calls at lines 442-448 and 549-550, the stdin reader at line 844)

**Interfaces:**
- Produces:

```go
// rpcTransport is where an RPC session reads its commands, writes its records and ends. The process transport is stdin, the raw
// stdout writer and os.Exit; a --listen connection's is the connection. pig additive (D201).
type rpcTransport struct {
	in  io.Reader
	out io.Writer
	// exit ends the session with code once the pending records are written: os.Exit for the process; for a connection, closing it.
	exit func(code int)
	// processOwned marks the process transport: it installs the termination hooks, terminates extension processes at exit and
	// writes profiles. A connection registers its controls with the server instead.
	processOwned bool
	// register hands a connection's controls to the server for the signal fan-out and returns the unregister; nil when processOwned.
	register func(rpcSessionControls) (unregister func())
}

// rpcSessionControls is what one rpc process installs through setTerminationPrelude and setTerminationShutdownHook, per session.
type rpcSessionControls struct {
	prelude  func()
	shutdown func(sig syscall.Signal)
}

func processRPCTransport() rpcTransport // stdin, codingagent.RawStdoutWriter(), exit through the existing exitRPC path
```

- Produces: `rpcModeResources.Transport *rpcTransport` and `func (r rpcModeResources) transport() rpcTransport`, which returns `*r.Transport` or `processRPCTransport()` when nil.

- [ ] **Step 1: Baseline**

Run: `go test ./coding/cli -run 'RPC|Rpc' -count=1 2>&1 | tail -3`
Expected: PASS. This task changes no behavior; these tests are its guard.

- [ ] **Step 2: Implement the transport**

In `runRPCMode`: `transport := resources.transport()` (the nil default); `newStdoutQueue(transport.out, ...)`; the reader at line 844 reads `transport.in`. In `serveRPCMode`: `exitRPC` keeps its body when `transport.processOwned`; otherwise it waits for the queued tail (`rawStdout.Wait`) and calls `transport.exit(code)` only, no `terminateProcesses`, `stopProfiles` or `os.Exit`. `forceSignal` and the `dieBySignal` path stay process-owned; a connection's `die` is a no-op. The prelude and shutdown hooks: when `processOwned`, the existing `setTerminationPrelude`/`setTerminationShutdownHook` calls; otherwise `transport.register(rpcSessionControls{prelude: <the prelude closure>, shutdown: shutdown.signal})` with its unregister deferred. The `receivedTerminationSignal` exit-code override at line 216 applies only to the process transport.

- [ ] **Step 3: Run the guard**

Run: `go test ./coding/cli -run 'RPC|Rpc' -count=1 2>&1 | tail -3` and `go vet ./coding/cli`
Expected: PASS, no vet findings.

- [ ] **Step 4: Commit**

```bash
git add coding/cli/rpc_transport.go coding/cli/rpc_mode.go
git commit --signoff -S -m "refactor(rpc): read, write and exit an rpc session through a transport"
```

### Task 3: Cell registry

**Files:**
- Create: `coding/extension/host/subprocess/cell_registry.go`
- Modify: `coding/extension/host/subprocess/host.go` (`Host` fields, the `ReadyPayload{}` at line 3434, the `retention().register` at line 3298), `coding/extension/host/subprocess/packed_go.go` (the claim at line 427, the register at line 686, the `ReadyPayload{}` at line 915), `coding/extension/host/subprocess/reload_cells.go` (the claim at line 186), `coding/extension/host/subprocess/with_session.go` (line 237), `coding/extension/host/subprocess/protocol.go` (`ReadyPayload`)
- Test: `coding/extension/host/subprocess/cell_registry_test.go`

**Interfaces:**
- Produces:

```go
// CellRegistry shares extension cell processes between the Hosts of one process that joined it (Host.ShareCells): a Host that would
// spawn a cell process another Host runs admits its members to that process as a further factory generation. Hosts that did not join
// keep one process per Host. pig additive (D201).
type CellRegistry struct { /* mu, procs map[string]*processShare, seq atomic.Uint64 */ }
func NewCellRegistry() *CellRegistry
// lookup returns a live, unparked, undead process registered under key, one reference held for the caller, or nil. It probes with the
// noop control line as RuntimeRetention.claim does.
func (r *CellRegistry) lookup(key string) *processShare
// register records share under key.
func (r *CellRegistry) register(key string, share *processShare)
// group returns a new id unique in the registry: "g<seq>".
func (r *CellRegistry) group() string

// ShareCells joins r: this Host admits into the cell processes r knows and registers the ones it spawns. Call it before the first load.
func (h *Host) ShareCells(r *CellRegistry)
// Group is the ready.group this Host's members receive; "" for a Host that shares no cells.
func (h *Host) Group() string
```

- Produces: `ReadyPayload.Group string \`json:"group,omitempty"\`` with the comment `// Group names the Host whose Session this member serves, unique in the process; a runtime scopes its per-process bookkeeping by it. Absent for a Host that shares no cells. pig additive (D201).`
- Registry key: `h.retentionKey(ctx, artifact, node) + "\x00" + h.cwd + "\x00" + h.builder.configRoot`; for the isolated Node path, `"node-extension:" + name + "\x00" + entry` plus the same suffix.

- [ ] **Step 1: Write the failing tests**

Use the package's existing Node and Go factory fixtures (`testdata/fixture-ext`, the `.mjs` fixtures, and the packed Go helpers of `packed_go_test.go`).

```go
func TestSharedCellsAdmitASecondHostIntoTheRunningProcess(t *testing.T)   // two Hosts, one registry, one Node factory and one packed Go factory: the pids behind both Hosts' members are equal per cell; each Host's tool call answers; closing Host A leaves B's tool answering; closing B ends the process (exitedChan)
func TestHostsWithoutARegistryKeepOneProcessEach(t *testing.T)            // the same two Hosts, no ShareCells: distinct pids
func TestSharedCellsSkipADeadProcess(t *testing.T)                        // kill the shared process; both Hosts reload their cell; one new pid shared by both
func TestSharedCellsStartTheSameCellOnceUnderARace(t *testing.T)          // two Hosts LoadAll concurrently (sync.WaitGroup, no sleeps); exactly one process spawned (count processShare registrations)
func TestReadyCarriesTheHostGroup(t *testing.T)                           // a Host with ShareCells sends ready.group == h.Group(); without, the member is absent from the frame
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./coding/extension/host/subprocess -run 'SharedCells|ReadyCarriesTheHostGroup' -v`
Expected: FAIL, `NewCellRegistry undefined`.

- [ ] **Step 3: Implement**

`lookup` reuses the liveness rules of `RuntimeRetention.claim` (`alive`, `exited`, `dead`, the `noop` probe) and `processShare.acquire`. In `startGoPackedCellOnce` (packed_go.go:427): after `claimRetainedProcess` returns nil, `if h.cells != nil { share = h.cells.lookup(h.cellRegistryKey(ctx, cell.BinaryPath, nodeRuntime)) }`; the tier check and `processState.adopt(retained)` stay. After the spawn registers with retention (line 686), also `h.cells.register(key, processState.share)`. In `stageIsolated` (reload_cells.go:186): the same fallback over the isolated Node key, and the registration beside host.go:3298. The three `ReadyPayload` literals get `Group: h.group` (with_session.go: the target Host's group). A registry entry whose process died is dropped on the next `lookup`.

- [ ] **Step 4: Run tests**

Run: `go test ./coding/extension/host/subprocess -run 'SharedCells|ReadyCarriesTheHostGroup|Retention|Reload|Packed' -count=1 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add coding/extension/host/subprocess/cell_registry.go coding/extension/host/subprocess/cell_registry_test.go coding/extension/host/subprocess/host.go coding/extension/host/subprocess/packed_go.go coding/extension/host/subprocess/reload_cells.go coding/extension/host/subprocess/with_session.go coding/extension/host/subprocess/protocol.go
git commit --signoff -S -m "feat(extension-host): share cell processes between hosts that join a cell registry"
```

### Task 4: Server loop

**Files:**
- Create: `coding/cli/rpc_listen.go`
- Modify: `coding/cli/main.go` (policy at lines 750-757, builder construction at line 1029, rpc dispatch at line 1215), `coding/cli/print_mode_build.go` (beside `rebuild` at line 79), `coding/cli/cli_runtime_build.go` (`cliRuntimeBuilder` at line 48)
- Test: `coding/cli/rpc_listen_test.go`, and generalize the client in `coding/cli/rpc_ui_integration_test.go`

**Interfaces:**
- Consumes: `rpcTransport`, `rpcSessionControls` (Task 2), `Args.Listen` (Task 1).
- Produces:

```go
// runRPCListen serves one RPC session per connection accepted on the Unix socket at flags.Listen until a termination signal, and
// returns the process exit code. pig additive (D201).
func runRPCListen(ctx context.Context, flags Args, activePiglet *piglet.Piglet, resources rpcModeResources) (exitCode int)

// listenBuild builds the resources of one --listen connection: rebuild's body over cliBuildInput{CWD: cwd}, a fresh Session log. pig additive (D201).
func (b *cliRuntimeBuilder) listenBuild(ctx context.Context, cwd string) (*cliBuild, error)
```

- Consumes: `subprocess.CellRegistry`, `NewCellRegistry` (Task 3).
- Produces for Task 6: `cliRuntimeBuilder.cellRegistry *subprocess.CellRegistry`, set in `main.go` before the startup build when `flags.Listen != ""`; Task 6 passes it to `newSubprocessExtensionHost`.
- Test helper: `newRPCClient(t *testing.T, in io.WriteCloser, out io.Reader) *rpcProcess` factored from `startRPCCommandAt` so a connection and a child process share `sendJSON`, `await`, `outputError`, `closeInput`; `startListenServer(t, env []string, args ...string) *listenServer` with `dial() *rpcProcess`, `signal(os.Signal)`, `waitForExit() int`, `stderr() string`.

- [ ] **Step 1: Write the failing tests**

```go
func TestListenServesOneRPCSessionPerConnection(t *testing.T) // equality: the same command list against startRPCProcess and against one connection; records compared after the parity runner's JSON normalization (compareJSONResults, or its rules copied: volatile ids, timestamps and paths)
func TestListenSessionsRunConcurrently(t *testing.T)          // three connections prompt "alpha", "beta", "gamma" at once (faux provider); each sees exactly one agent_end and get_messages returns its own prompt; closing the first leaves the others answering get_state
func TestListenExitsOnSIGTERMWithEveryConnectionOpen(t *testing.T) // two open connections; SIGTERM; exit code 128+15; the socket file is gone (skipped on windows for the signal, the socket removal checked on quit)
func TestListenRefusesAnExistingPath(t *testing.T)            // stderr "pig --listen: <path> exists", exit 1
func TestListenRefusesAPathTheOSRejects(t *testing.T)         // a 200-byte path: stderr "pig --listen: listen <path>:", exit 1
func TestListenDiscardsABuildWhoseConnectionClosed(t *testing.T) // connect and close at once, then one normal connection answers get_state; SIGTERM exits clean
func TestListenExitsOnSIGTERMDuringABuild(t *testing.T)       // open 5 connections and signal immediately; exit within the test budget
```

Command list for the equality test: `get_state`, `get_available_models`, `prompt` `"hello"` then await `agent_end`, `get_messages`, `get_session_stats`. Server args for every test: `--mode rpc --listen <tempdir>/pig.sock --model test-faux/echo --no-session`, env `PIG_TEST_FAUX=1`, `PIG_HOME=<tempdir>/home`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./coding/cli -run TestListen -v`
Expected: FAIL (the binary rejects `--listen` or ignores it).

- [ ] **Step 3: Implement `runRPCListen`**

Refuse a path that exists (`os.Lstat`). `net.Listen("unix", path)`; on error print `pig --listen: listen <path>: <err>` and return 1. `os.Chmod(path, 0o600)`. `defer os.Remove(path)`. Accept in a goroutine; for each connection start a goroutine that takes `buildMu`, selects `resources.Build` for the first connection or `builder.listenBuild(ctx, initial.CWD)` for later ones, releases `buildMu`, and calls `runRPCMode(connCtx, flags, activePiglet, rpcModeResources{Builder: builder, Build: build, Transport: &rpcTransport{in: conn, out: conn, exit: closeOnce(conn), register: controls.add}})`. A connection whose build fails gets nothing and is closed; the error goes to stderr. The server installs `setTerminationPrelude` and `setTerminationShutdownHook` once: each runs every registered control concurrently and waits. On a termination signal: stop accepting, run the hooks, wait for every connection goroutine, terminate extension processes through the live Hosts as `exitRPC` does, return `128 + signal`.

Builds that a connection's `new_session` or `switch_session` command requests go through the same factory; make the factory's rebuild take `buildMu` too.

- [ ] **Step 4: Implement `listenBuild` and wire `main.go`**

`listenBuild` is `rebuild` with `cliBuildInput{CWD: cwd}` and no session manager (confirm `buildResources` reads `in.Manager` only for `Continuing`). In `main.go`: after `builder := &cliRuntimeBuilder{...}`, when `flags.Listen != ""`, set `builder.cellRegistry = subprocess.NewCellRegistry()` with a D201 marker. Policy: `gcpolicy.Apply()` and `procpolicy.ApplyRPC()` run only when `flags.Listen == ""`, each with a D201 marker naming the reason from the spec. Dispatch: inside `if flags.Mode == "rpc"`, `if flags.Listen != "" { exitProcess(runRPCListen(ctx, flags, activePiglet, resources)) }`.

- [ ] **Step 5: Run tests**

Run: `go test ./coding/cli -run 'TestListen|RPC' -count=1 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add coding/cli/rpc_listen.go coding/cli/rpc_listen_test.go coding/cli/rpc_ui_integration_test.go coding/cli/main.go coding/cli/print_mode_build.go coding/cli/cli_runtime_build.go
git commit --signoff -S -m "feat(rpc): serve one rpc session per connection with --listen"
```

### Task 5: Node runtime group scoping and the conformance row

**Files:**
- Modify: `coding/extension/host/subprocess/runtime-node/runtime.mjs` (`ready` handling at line 3083, `sharedBus` sites at lines 79, 1288, 2382, 2385, 2880, 2883, 2916, 3125; `holdVirtualWrite` at line 4118; `quitDrain` at lines 940-1095; `xrefRealm` at line 69), `coding/extension/host/subprocess/runtime-node/event-bus.mjs` if `SharedBus` needs a per-group constructor
- Create: `coding/extension/host/subprocess/testdata/shared-cells.mjs` (a factory: tool `counter` returns an instance counter; tool `ping` emits `pi.events.emit("shared-cells", {from: name})`; `pi.events.on("shared-cells", ...)` records what it received into tool `received`; command `virtual` registers a virtual model `shared/<name>`)
- Test: `coding/extension/host/subprocess/node_cell_shared_test.go`, `test/extension-conformance/shared_cells_test.go` (and the harness makers of `conformance_test.go` take an optional `*subprocess.CellRegistry` through a package variable beside `conformanceHostInfo`)

**Interfaces:**
- Consumes: `CellRegistry`, `Host.ShareCells`, `ready.group` (Task 3).
- Produces in `runtime.mjs`: `runtime.group` (string, `""` when absent); `busFor(group)` returning the process-wide `sharedBus` for `""` and one `SharedBus` per group otherwise; `drainFor(group)` returning the quit-drain state for the group; `holdVirtualWrite` iterating only runtimes whose `group === this.group`; the xref transport chooser preferring a connection of the requesting runtime's group.

- [ ] **Step 1: Write the failing tests**

```go
// coding/extension/host/subprocess/node_cell_shared_test.go
func TestSharedNodeCellKeepsEachHostsEventBusApart(t *testing.T)       // two Hosts, one registry, shared-cells.mjs: ping in A; received in A lists A's own emit; received in B is empty
func TestSharedNodeCellKeepsVirtualWritesPerHost(t *testing.T)         // virtual in A; A's registry lists shared/A; B's registry does not
func TestSharedNodeCellDrainsOnlyTheClosingHost(t *testing.T)          // A's input ends (EndInput) and A shuts down; B's counter tool still answers and the process lives
func TestSharedNodeCellCountersAreInstanceLocal(t *testing.T)          // counter in A twice, in B once: 2 and 1

// test/extension-conformance/shared_cells_test.go
func TestConformance_SharedCellsIsolatePerHostInstances(t *testing.T)  // for each case of allHarnessCases() that has a host: two harnesses over one registry; counter and event-bus assertions as above; for subprocess cases the two Hosts' members share one pid, for fused-go there is no process
```

The conformance fixtures gain `counter`, `ping` and `received` in the Go, Rust, Python and Node SDK fixtures (`sdkscenarios`), the same names and results in every SDK.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./coding/extension/host/subprocess -run SharedNodeCell -v` and `go test ./test/extension-conformance -run SharedCells -v`
Expected: FAIL: B receives A's event, or the fixtures lack the tools.

- [ ] **Step 3: Implement the runtime scoping**

On `ready`, `this.group = env.ready?.group ?? ""`. Replace each `sharedBus.<method>(...)` site with `busFor(<runtime>.group).<method>(...)`; host-driven `dispatch`, `release` and `migrate` use the connection's runtime group. `holdVirtualWrite` filters `liveRuntimes` by group. Factor the `quitDrain` object into `drainFor(group)` with the same fields, and make every `quitDrain.` site use the group of the runtime it handles. In the `xrefRealm` chooser, prefer `currentRuntime()` and then runtimes of its group before the rest. Each change carries a one-line `// pig additive (D201)` comment stating the per-group rule.

- [ ] **Step 4: Run tests**

Run: `go test ./coding/extension/host/subprocess -run 'SharedNodeCell|Node' -count=1 2>&1 | tail -5` and `go test ./test/extension-conformance -run 'SharedCells|Conformance_ExtensionAPIThinkingLevel' -count=1 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add coding/extension/host/subprocess/runtime-node/runtime.mjs coding/extension/host/subprocess/runtime-node/event-bus.mjs coding/extension/host/subprocess/testdata/shared-cells.mjs coding/extension/host/subprocess/node_cell_shared_test.go test/extension-conformance/shared_cells_test.go test/extension-conformance/conformance_test.go test/extension-conformance/sdkscenarios
git commit --signoff -S -m "feat(extension-host): scope the Node runtime's bus, virtual writes and quit drain per host group"
```

### Task 6: Server with extensions

**Files:**
- Modify: `coding/cli/extensions.go` (`newSubprocessExtensionHost` at line 108 gains a `cells *subprocess.CellRegistry` parameter and calls `host.ShareCells(cells)` when non-nil), `coding/cli/cli_runtime_build.go` (the call at line 272 passes `b.cellRegistry`), `coding/cli/main.go` (the registry assignment from Task 3 now compiles)
- Test: `coding/cli/rpc_listen_ext_test.go`, fixtures under `coding/cli/testdata/listen-counter-go/` (a Go factory with `go.mod` replacing the SDK as `examples/extensions/go-factory` does) and `coding/cli/testdata/listen-counter.mjs` (the Node factory of Task 5's fixture)

**Interfaces:**
- Consumes: `CellRegistry` (Task 3), `cliRuntimeBuilder.cellRegistry` (Task 4), the fixtures' tools `counter`, `ping`, `received`.
- Produces test helper: `cellProcessCount(t *testing.T, serverPID int) (nodeCells, goCells int)` over the platform process list (unix `ps -axo pid=,ppid=,command=`; Windows as `test/extension-conformance/soak_test.go` lists processes), counting descendants whose command names the Node runtime or the Go cell binary.

- [ ] **Step 1: Write the failing tests**

```go
func TestListenSharesExtensionProcessesAcrossSessions(t *testing.T) // agent dir with both fixtures; connections 1, 2, 3 each call counter once: (nodeCells, goCells) == (1, 1) after each; counters answer 1 in every session; ping in session 1 is received only in session 1; session_shutdown reaches an instance only when its connection closes (the fixture appends a line to a file per instance on session_shutdown); after the last close the counts reach (0, 0)
func TestListenRecoversASharedExtensionCrash(t *testing.T)          // two sessions; kill the Node cell pid while session 1 awaits a slow tool call (the fixture's `slow` tool sleeps on a timer the host cancels); session 1 gets the tool error, session 2's next counter call answers, (nodeCells) == 1
func TestListenSessionShutdownReachesEverySessionOnSIGTERM(t *testing.T) // two sessions; SIGTERM; both instances' shutdown lines exist; exit 128+15
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./coding/cli -run 'TestListenShares|TestListenRecovers|TestListenSessionShutdown' -v`
Expected: FAIL: `(nodeCells, goCells) == (2, 2)` at the second connection.

- [ ] **Step 3: Wire the registry**

Thread `b.cellRegistry` to `newSubprocessExtensionHost`; the server's `main.go` assignment from Task 4 is now live. No other production change is expected; a failing assertion here is a bug in Task 3 or 5, fixed there.

- [ ] **Step 4: Run tests**

Run: `go test ./coding/cli -run 'TestListen' -count=1 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add coding/cli/extensions.go coding/cli/cli_runtime_build.go coding/cli/main.go coding/cli/rpc_listen_ext_test.go coding/cli/testdata/listen-counter-go coding/cli/testdata/listen-counter.mjs
git commit --signoff -S -m "feat(rpc): --listen sessions share their extension processes"
```

### Task 7: Bench, record, docs

**Files:**
- Create: `bench/sessionhost/sessionhost.py`, `bench/sessionhost/README.md`
- Modify: `docs/additive-features.md` (append `## D201`), `docs/site/docs/cli.md` (the `## Options` table at line 75), `docs/site/docs/rpc.md` (a `## Serve many sessions` section after `## Start RPC mode`), `docs/extension-api-parity.md` (`## Retained runner invariants`: the shared-cell row; `## Context APIs`: the `ready.group` member and its contract), `docs/specs/multi-session-rpc-server.md` (status line: implemented)
- Create: `changelog.d/rpc-listen-<sha>.md`

**Interfaces:**
- Consumes: the `--listen` binary from Tasks 4 and 6.

- [ ] **Step 1: Write the bench**

`sessionhost.py --pig <binary> --sessions 1 10 50 100 --turns 5 [--extensions] --json-out <file>`: starts a scripted OpenAI-compatible model (the one in `bench/compare/fake_server.py`), an isolated agent directory with `models.json` and `settings.json` for it (and the two Task 6 fixtures with `--extensions`), then for each N: arm `listen` (one `pig --mode rpc --listen` process, N connections) and arm `rpc` (N `pig --mode rpc` processes), the five `TURNS` of `scripts/bench_headless_memory.py` of jcode (listed in the README), and records RSS and phys_footprint (macOS) or PSS (Linux) of the process tree after each turn, the extension process count, and the per-additional-session slope. Self-check: `--sessions 1 --turns 1` asserts both arms made exactly 2 model requests per session.

- [ ] **Step 2: Run the bench**

Run: `python3 bench/sessionhost/sessionhost.py --pig ./pig --sessions 1 10 50 100 --json-out /tmp/sessionhost.json` and again with `--extensions --sessions 1 10 20`
Expected: both arms report 0 errors; the `listen` arm's slope is below the `rpc` arm's; with `--extensions` the `listen` arm's extension process count is 2 at every N.

- [ ] **Step 3: Write the D201 record**

`## D201 Multi-session RPC server (--listen) and shared extension cells`, in the shape of D174: `Stock disposition: inert capability` (selected only by `--listen`); What; Current behavior (the surface, lifecycle and policy decisions of the spec, each in one sentence); Pi source: none (`rpc-mode.ts` serves one session per process; `agent-session-services.ts:214-233` shares one resource loader across sessions from one services object); Why (the measurements of the spec and the bench); Call-site markers (every `pig additive (D201)` site); Locked by (every test of Tasks 1-6 by name); Remove when (Pi gains a multi-session RPC mode); `Approval: pending owner review`.

- [ ] **Step 4: Documentation**

`cli.md`: a row `| \`--listen <path>\` | Serve one RPC session per connection on the Unix socket at path; requires \`--mode rpc\` (D201). |`. `rpc.md`: the section with the command line, the per-connection contract (one session per connection, the connection's close is the session's stdin end, the server runs until a signal, every session has the server's cwd and flags), the socket permission note, and a 15-line Python client that connects twice and prompts both. `extension-api-parity.md`: the two rows. The changelog fragment: one `### Added` bullet naming `--listen` and shared extension processes.

- [ ] **Step 5: Gates**

Run: `make generate && make lint-changed && make test-changed && make ci-drift ci-contracts && make parity-family rpc`
Expected: each exits 0; `git status --short` shows only intended files plus generated outputs.

- [ ] **Step 6: Commit**

```bash
git add bench/sessionhost docs/additive-features.md docs/site/docs/cli.md docs/site/docs/rpc.md docs/extension-api-parity.md docs/specs/multi-session-rpc-server.md changelog.d/rpc-listen-<sha>.md <generated files make generate changed>
git commit --signoff -S -m "docs(rpc): record D201, the --listen server and shared extension cells"
```

Rename the fragment after the final commit SHA of the range.

### Task 8: Handoff

- [ ] **Step 1: Draft the Discord message**

Before any push or pull request, write a message for the user to post on PiG's Discord: what `--listen` and shared cells do, the memory numbers from the bench, the D201 record awaiting owner approval, and a link to the branch. Use the davids-voice skill.

- [ ] **Step 2: Wait**

Push and PR only after the user has posted and says to proceed.
