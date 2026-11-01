# Chord v2 plugins: the build in Go, the evaluation in a Node subprocess

Status: owner decision 2026-11-01 for `chord/nextnode/plugins` (Pi `2c8d1feb`, `packages/chord/src/next-node/plugins`).
Replaces the `Builder` and `Evaluator` seams of the first port, whose only implementations were test fakes.

## 1. Split

| Pi file | Go form |
|---|---|
| `discover.ts`, `install.ts`, `watcher.ts`, `plugins.ts` | Go, as ported (`plugins_*.go`). |
| `build.ts` | Go, 1:1 on esbuild's Go API (`github.com/evanw/esbuild/pkg/api`, Pi's pin `0.28.2`): every option `buildFacet` passes, `ImportRules` as an `api.Plugin`, `freeGlobals` over the inline source map (VLQ decoder with Node `SourceMap.findEntry` semantics), `describe`. No `Builder` interface: `BuildFacet` is the build. |
| `evaluate.ts`, `stack.ts`, `runtime.ts` | Pi's own TypeScript, vendored verbatim and run by Node (type stripping, no transpile) in a subprocess. Go has no `vm`, and AGENTS.md forbids an embedded JavaScript runtime. |
| (none) | `node/host.mjs`, the thin driver of that subprocess, and `plugins_node*.go`, its Go client (`PluginRuntime`, `NodeHost`). No Pi counterpart: Pi's workers (`next-node/workers/main.ts`) fork the same shape over Node IPC. |

The in-process `PluginRuntime`/`Evaluator` seams, `Plugins.Load/Reload` on a Go `host.Host` and `HostPackages map[string]any` are deleted: a facet is JavaScript, so it can only be loaded into a JavaScript host. That host is Pi's own chord host (`next/host`), running in the subprocess and linked to the Go host.

## 2. Process model

One Node process per `NodeHost` (Pi: one worker per host). It runs, from one extracted tree (section 7):

- `node/host.mjs`: parses its one argument (the socket address), connects, makes `createHost`, `createPluginRuntime` and the two channels below, `connect`s the link to its parent as Pi's forked worker does (`next-node/workers/main.ts`), and serves the control requests.
- Pi's `next/host` (the chord host), `next-node/plugins/runtime.ts` (per-host loaded facets, complete-set load and reload, the `adopt` rule after an aborted commit), `evaluate.ts` (`vm.Script` under `/chord-plugins/<plugin>/<file>?<digest>`, `require` for host packages, `pluginFile`, `chord.external` packages) and `stack.ts` (`mapStack`, source maps kept while a facet lives). Nothing of these is translated.

The host's plain-value options are the app's (`RuntimeLoad`): `heartbeat`, `closeTimeout`, `readyTimeout` and `compression` (`createHost({ compression: nodeCompression })`, Pi's `next-node/compression.ts` vendored; the Go host the link is attached to compresses its own parts when it is created with `host.HostOptions.Compression: nextnode.NodeCompression`, a compressor being a host's, not a link's). `telemetry`, `principalKey` and `revokedBy` are functions or adapters of the app and cannot cross.

Host packages (Pi: `HostPackages`, this process's module instances) are modules imported in that process. `@earendil-works/chord/next` is always Pi's vendored `next/index.ts`. Any other specifier of `RuntimeLoad.Modules` names a file the app ships (`specifier -> absolute path`); the driver `import()`s it once and the plugins share that one instance. The `core` facets (the app's own) are files too (`CoreFacet{Name, Module}`, default export a facet). The driver answers `@earendil-works/chord/next` and `@earendil-works/chord/context` for every module it imports (`module.registerHooks`), so an app's modules import Pi's chord by its package name and share the host's instances.

## 3. Wire

One local socket (a unix socket in a private 0700 directory; a named pipe on Windows), accepted once. Frames are what Pi's IPC link uses (`next/link/ipc.ts`, `link.IpcLink`): a 4-byte big-endian length, then one kind byte and the payload. Kind `0`: one chord link message (`link.BinaryCodec` bytes), carried by `link.IpcLink` on the Go side and Pi's `ipcLink` on the Node side, so the two hosts speak the pinned link wire byte for byte. Kind `1`: a control message, one JSON object (Pi's worker `envelope` shape).

The Node host is the child of the Go side, as Pi's forked worker is of its server. `host.mjs` makes its host at `load` and `connect`s the link as its parent link with `{ principal: peer, trusted: true }` (`RuntimeLoad.Peer`: who the Go side is, as the Node host stamps it, `workers/main.ts:114`). The Go end of that link is `NodeHost.Link()`, an `IpcLink` over the channel made when the process connected, before the first frame is read: whatever the Node host sent before the app attached it (its `hello`) waits in the link. The app attaches it to a Go host as Pi's app does, and nothing in this package touches a Go host:

- `Host.Route(kind, RouteOptions{Authorize, Resolve})` for an address-bound kind: `Resolve` returns `host.ResolvedLink{Link: node.Link(), Principal: <who the Node host is>, Trusted: true}` (Pi: `workers.ts:98`, and `plugins.test.ts`'s end to end: `server.route(Worker, { authorize, resolve: async () => ({ link, principal: { kind: "worker" } }) })`). A hold of a remote type whose `hostAddress` is that kind then goes over the link to the Node host, which serves what the plugins' facets serve. The app calls `Load` inside `Resolve` when it wants the process started on demand, as `workers.ts` forks inside `resolve`.
- `Host.Accept(node.Link(), LinkOptions{Principal, Trusted: true})` for a Node host nothing is held from (a child of the host).

`RuntimeLoad` has no Go host: a NodeHost needs none to load, so one Go host owns any number of NodeHosts (one `Route` per kind, one `Accept` per child), and a Go host that has a parent of its own can load plugins (the worker host of `next-node/workers`: it `Connect`s to its server, and routes its plugins' kind to a NodeHost; give it no address of its own, which its kind's route would not reach). The first port made the Node host the *parent* of the Go host. That had no room for either: a Go host has one parent, and a hold relayed up to a Node host that accepted the relaying host as its child fails with `hold: an origin on a child link` (a child relays nothing), which only the Node host's `onError` saw: the hold waited until its ready timeout.

The heartbeat is the host's: a Node host closes a link silent for 30 s (`RuntimeLoad.Heartbeat`), its handshake included, and the process exits with the link, so the app attaches the link in time (right after `Load` returns, in the same step, in `Resolve` or `Accept`). A link attached after `Load` and other requests still carries the host; a link not attached in time ends the process with a clean exit nobody asked for (`NodeHost.Err()` is a `*NodeExit` with no exit error, `Exited` closes, the link closes), tested with a shortened heartbeat. Closing the link, from either end, ends the channel, so the process exits (Pi: a worker whose link closed is stopped).

Control (JSON, request ids chosen by Go, one reply per request, replies in any order):

| request | reply |
|---|---|
| `load {kind, core, scripts, modules, address, principal, peer, heartbeat, closeTimeout, readyTimeout, compression}` (once) | `{failed: [{plugin, error}]}`: runtime.ts `load`'s report. |
| `reload {scripts}` | `{plugins, failed, objects}`: the host's `ReloadReport`; or an error. |
| `abort {target}` | none: aborts the context of request `target` (Go's context cancel). |
| `close` | none: `host.close`, which ends the link and with it the channel, then the process exits. `NodeHost.Close` waits for the exit. |

Node to Go, unrequested: `started` (connected), `activity {counts}` (the host's keep-alive counts `host.activity`, once at load and at each change: `NodeOptions.OnActivity`, `NodeHost.Activity`; a Go host that only routes to the NodeHost counts none of its objects' work, so a supervisor that stops idle workers reads them here), `error {error}` (the host's `onError`: every field of the `HostError` of its kind, so `NodeHostError` carries them typed), `fatal {error}` (an uncaught exception or rejection; the process exits).

`scripts` are `PluginScript`s as plain data (`{plugin, built}`: the `BuiltFacet` the Go build made, `source` and `digest` included), exactly what Pi ships to a worker. The digest rule of `runtime.ts` (same digest: same facet object) therefore holds across reloads of one process.

## 4. Lifecycle

- **Start** (`Load`): discover Node (section 6), extract the tree, listen, spawn, wait for `started` (`ReadyTimeout`, 30 s, as `chord/host`'s loader), send `load`, return the report. A failed start (aborted context, `ReadyTimeout`, a process that ended) kills the process, waits until it is gone and returns the cause with the stderr tail; the socket's directory is removed on every path.
- **Reload**: `Plugins.Reload` applies the build to the kind's current set and sends `reload` in that same section, so requests reach Node in the order the sets were decided; Node's `runtime.reload` queues per host in arrival order.
- **Cancel**: a cancelled Go context sends `abort`; the Node side aborts that request's `Context.abortSignal`. After a commit `runtime.ts` adopts what the host committed, as upstream.
- **Crash**: process exit or a closed socket fails every pending request with `*NodeExit` (exit code or signal, the `fatal` the process sent, stderr tail: the exit is built once the channel was read to its end, bounded by 3 s, so a `fatal` read after the process was waited for is in it). A request sent after the channel ended and before the process exited returns that exit once it is known (bounded by 6 s), not that the connection is closed. The crash closes the link (the Go host's route or child link drops; its holds go `loading`), and the `NodeHost` stays dead: nothing restarts it (Pi: "never restarted by itself"). `NodeHost.Exited()` closes.
- **Close**: `close` request, then the process gets 3 s to exit and is killed (the worker stop of `workers.ts`). `Close` after exit is a no-op and returns the exit if it was a crash.
- **Output**: the child's stdout and stderr go to `NodeOptions.OnOutput`, else to this process's, as workers do; the last 8 KiB of stderr is kept for errors.

## 5. Errors

A failure inside Node is data on the wire: `{name, message, stack, chord?, cause?}`; `cause` is the error's `cause`, itself a wire error (a cause that is no Error is an error of its string, a cause already on the chain ends it). `stack` is `mapStack(error)` computed in Node while the script's map lives, so a plugin's import-time throw or a failed `setup` names `notes/helper.ts:3:8`, not `/chord-plugins/...`. `code` and `details` are a `ChordError`'s (`aborted` carries `details.committed`). Go returns `*NodeError` (its `Error()` is `message`; `Stack` is the mapped stack; `Cause` the cause's `*NodeError`; `Unwrap` gives the `*data.ChordError` when it is one and the cause, so `errors.As` finds a ChordError anywhere in the chain). Only plugin errors are plugin failures: a Go panic anywhere (the client, the decoder) propagates, it is never turned into a `PluginFailure`.

## 6. Node

Pi's `packages/chord` `engines.node` is `>=22.19.0`, the first release that strips types by default. Discovery is the runtime-node way (`ensureNodeRuntime`): `node` on `PATH`, `node --version` parsed as semver, an error naming the requirement when missing or older. The process runs `node --disable-warning=ExperimentalWarning <NodeOptions.ExecArgv...> host.mjs <address>`. A missing Node is an environment failure of `Load`, not of the build: `Build`, `Discover`, the watcher and `Plugins.Scripts` need none.

## 7. Packaging

`node/` is embedded (`go:embed all:node`) and extracted once per content hash under the user cache directory (`os.UserCacheDir()/pig/chord-plugins-node-<hash>`), published by an atomic rename; concurrent processes share it. A directory is trusted only when its `.complete` marker (written last, holding the hash) names this content and every file has its embedded size; a partial or older one is repaired in place, file by file (each a rename of a complete file), the marker last.

- `node/host.mjs`: the driver, written here.
- `node/pi/chord/src/**`: the import closure of `next/index.ts`, `next-node/plugins/{evaluate,stack,runtime}.ts`, `next-node/compression.ts` and `context/index.ts` at the pinned Pi commit, copied byte for byte, at Pi's own relative layout (so `../../next/host/index.ts` resolves).
- `node/pi/node_modules/{typebox,@earendil-works/pi-telemetry}`: Pi's runtime dependencies at Pi's pins (`typebox@1.3.27`, integrity-checked, as `vendor-typebox.sh`; pi-telemetry from the `extensions/sdk-ts` lock).
- `node/manifest.json`: the Pi commit, the pin (`.upstream/pin`) it was made against, the sha256 of every vendored file, and, when the commit is not the pin's, `catchup` naming it. A Go test fails when a file differs from the manifest, when the pin moved since, and when the commit differs from the pin without being the named catch-up (the manifest is ahead of the pin during the 0.7.0 catch-up: `2c8d1feb`, the pin `e7a0df27`).

`automation/gen/vendor-chord-plugins-node.sh` (`make vendor-chord-plugins-node`, run by `make pin-bump` next to `vendor-pi-dist`) regenerates `node/pi` from the mirror of the pin (`PI_TREE`/`PI_COMMIT` override the Pi tree and its commit for a catch-up); it checks typebox and pi-telemetry against the versions Pi's chord depends on, and writes nothing else.

## 8. Designed out, and what differs

- **The store.** Pi's `createHost` takes the app's `store`, a JavaScript object. The Node host has none: a facet that declares persisted documents or tables fails at activation (`persist needs a store`, the error of a Pi host created without one; `TestRuntimeAFacetWithAPersistedDocumentFailsToActivateTheHostHasNoStore`). Rationale: the store is an app object, and the Go form of persistence is the Go host's own store.
- **`vm`-level API of `evaluate.ts` on the Go side** (`EvaluateFacet`, `PluginFile`, `ScriptFilename`, `mapStack` as Go functions): they exist only in the subprocess; Go sees their effects (reports, mapped stacks).
- **V8's compilation cache flag** (`runtime.ts`): runs unchanged inside the subprocess.
- **Other runtimes** (Bun, a runtime-slot executable) for this subprocess: Node is the only runtime (AGENTS.md: no embedded JavaScript).

Every remaining difference a Pi user or plugin can observe, with whether and at what cost it can be closed, is in `handoffs/next/rows/plugnode-differences.md`.

## 9. Evidence

The tests run the real esbuild build and the real Node subprocess with Pi's inputs (Node 24.22.1 in `tern-ci:v6`): `build.test.ts` and `discover.test.ts`, `install.test.ts` and `watcher.test.ts` (already Go), `plugins.test.ts`, `runtime.test.ts`, `evaluate.test.ts` and `stack.test.ts` with their expected results, and examples 35 to 39. The end to end case is Pi's: one observing server Go host routes `Worker` to the worker's NodeHost (`server.get(Notes)`, `notes.add`, the label frames), and routes the server kind's own NodeHost for the probe that reads `Index`; a second case is a Go host with a parent of its own (a worker's shape) that routes its kind to a NodeHost. A case that reads a JavaScript object of the evaluated facet (`facet.made instanceof ChordError`, `facet.values`) reads it through a probe core facet (a fixture) that calls Pi's `evaluateFacet` and `mapStack` in the subprocess and serves the answers over a remote contract held by a Go host: the same expectations, the process boundary in between, each such case marked in its test comment.
