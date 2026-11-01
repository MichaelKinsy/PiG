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

- `node/host.mjs`: parses its one argument (the socket address), connects, makes `createHost`, `createPluginRuntime` and the two channels below, and serves the control requests.
- Pi's `next/host` (the chord host), `next-node/plugins/runtime.ts` (per-host loaded facets, complete-set load and reload, the `adopt` rule after an aborted commit), `evaluate.ts` (`vm.Script` under `/chord-plugins/<plugin>/<file>?<digest>`, `require` for host packages, `pluginFile`, `chord.external` packages) and `stack.ts` (`mapStack`, source maps kept while a facet lives). Nothing of these is translated.

Host packages (Pi: `HostPackages`, this process's module instances) are modules imported in that process. `@earendil-works/chord/next` is always Pi's vendored `next/index.ts`. Any other specifier of `RuntimeLoad.Modules` names a file the app ships (`specifier -> absolute path`); the driver `import()`s it once and the plugins share that one instance. The `core` facets (the app's own) are files too (`CoreFacet{Name, Module}`, default export a facet).

## 3. Wire

One local socket (a unix socket in a private 0700 directory; a named pipe on Windows), accepted once. Frames are what Pi's IPC link uses (`next/link/ipc.ts`, `link.IpcLink`): a 4-byte big-endian length, then one kind byte and the payload. Kind `0`: one chord link message (`link.BinaryCodec` bytes), carried by `link.IpcLink` on the Go side and Pi's `ipcLink` on the Node side, so the two hosts speak the pinned link wire byte for byte. Kind `1`: a control message, one JSON object (Pi's worker `envelope` shape).

The Go host is the child of the Node host: `NodeHost.Attach(h)` is `h.Connect(link)`, so a remote type the Go host does not serve is held over the link from the Node host, which serves what the plugins' facets serve. The Node host `accept`s the link as the plugin host's child with the principal `Peer` names.

Control (JSON, request ids chosen by Go, one reply per request, replies in any order):

| request | reply |
|---|---|
| `load {kind, core, scripts, modules, principal, peer}` (once) | `{failed: [{plugin, error}]}`: runtime.ts `load`'s report. |
| `reload {scripts}` | `{plugins, failed, objects}`: the host's `ReloadReport`; or an error. |
| `abort {target}` | none: aborts the context of request `target` (Go's context cancel). |
| `close` | `{}`, then the process exits: `host.close`, then exit. |

Node to Go, unrequested: `started` (connected), `fatal {error}` (an uncaught exception or rejection; the process exits).

`scripts` are `PluginScript`s as plain data (`{plugin, built}`: the `BuiltFacet` the Go build made, `source` and `digest` included), exactly what Pi ships to a worker. The digest rule of `runtime.ts` (same digest: same facet object) therefore holds across reloads of one process.

## 4. Lifecycle

- **Start** (`Load`): discover Node (section 6), extract the tree, listen, spawn, wait for `started` (`ReadyTimeout`, 30 s, as `chord/host`'s loader), send `load`, return the report. A failed start kills the process and returns the cause with the stderr tail.
- **Reload**: `Plugins.Reload` applies the build to the kind's current set and sends `reload` in that same section, so requests reach Node in the order the sets were decided; Node's `runtime.reload` queues per host in arrival order.
- **Cancel**: a cancelled Go context sends `abort`; the Node side aborts that request's `Context.abortSignal`. After a commit `runtime.ts` adopts what the host committed, as upstream.
- **Crash**: process exit or a closed socket fails every pending request with `*NodeExit` (exit code or signal, stderr tail), closes the link (the Go host's parent link drops; its holds go `loading`), and the `NodeHost` stays dead: nothing restarts it (Pi: "never restarted by itself"). `NodeHost.Exited()` closes.
- **Close**: `close` request, then the process gets 3 s to exit and is killed (the worker stop of `workers.ts`). `Close` after exit is a no-op and returns the exit if it was a crash.
- **Output**: the child's stdout and stderr go to `NodeOptions.OnOutput`, else to this process's, as workers do; the last 8 KiB of stderr is kept for errors.

## 5. Errors

A failure inside Node is data on the wire: `{name, message, stack, code?, details?}`. `stack` is `mapStack(error)` computed in Node while the script's map lives, so a plugin's import-time throw or a failed `setup` names `notes/helper.ts:3:8`, not `/chord-plugins/...`. `code` and `details` are a `ChordError`'s (`aborted` carries `details.committed`). Go returns `*NodeError` (its `Error()` is `message`; `Stack` is the mapped stack), or `*data.ChordError` when `code` is one of chord's. Only plugin errors are plugin failures: a Go panic anywhere (the client, the decoder) propagates, it is never turned into a `PluginFailure`.

## 6. Node

Pi's `packages/chord` `engines.node` is `>=22.19.0`, the first release that strips types by default. Discovery is the runtime-node way (`ensureNodeRuntime`): `node` on `PATH`, `node --version` parsed as semver, an error naming the requirement when missing or older. The process runs `node --disable-warning=ExperimentalWarning host.mjs <address>`; no other flag. A missing Node is an environment failure of `Load`, not of the build: `Build`, `Discover`, the watcher and `Plugins.Scripts` need none.

## 7. Packaging

`node/` is embedded (`go:embed all:node`) and extracted once per content hash under the user cache directory (`os.UserCacheDir()/pig/chord-plugins-node-<hash>`), published by an atomic rename; concurrent processes share it.

- `node/host.mjs`: the driver, written here.
- `node/pi/chord/src/**`: the import closure of `next/index.ts`, `next-node/plugins/{evaluate,stack,runtime}.ts` and `context/index.ts` at the pinned Pi commit, copied byte for byte, at Pi's own relative layout (so `../../next/host/index.ts` resolves).
- `node/pi/node_modules/{typebox,@earendil-works/pi-telemetry}`: Pi's runtime dependencies at Pi's pins (`typebox@1.3.27`, integrity-checked, as `vendor-typebox.sh`; pi-telemetry from the `extensions/sdk-ts` lock).
- `node/manifest.json`: the Pi commit and the sha256 of every vendored file; a Go test fails when a file differs from it.

`automation/gen/vendor-chord-plugins-node.sh` (`make vendor-chord-plugins-node`, next to `vendor-pi-dist`) regenerates `node/pi` from the mirror of the pin (`PI_CHORD_SRC` overrides the Pi tree for a pin leap); it writes nothing else.

## 8. Designed out

- **The store.** Pi's `createHost` takes the app's `store`, a JavaScript object. The Node host has none: a facet that declares persisted documents or tables fails at declaration, as on a Pi host without a store. Rationale: the store is an app object, and the Go form of persistence is the Go host's own store.
- **`vm`-level API of `evaluate.ts` on the Go side** (`EvaluateFacet`, `PluginFile`, `ScriptFilename`, `mapStack` as Go functions): they exist only in the subprocess; Go sees their effects (reports, mapped stacks).
- **V8's compilation cache flag** (`runtime.ts`): runs unchanged inside the subprocess.
- **Other runtimes** (Bun, a runtime-slot executable) for this subprocess: Node is the only runtime (AGENTS.md: no embedded JavaScript).

## 9. Evidence

The tests run the real esbuild build and the real Node subprocess with Pi's inputs: `build.test.ts` and `discover.test.ts`, `install.test.ts` and `watcher.test.ts` (already Go), `plugins.test.ts`, `runtime.test.ts`, `evaluate.test.ts` and `stack.test.ts` with their expected results, and examples 35 to 39. A case that reads a JavaScript object of the evaluated facet (`facet.made instanceof ChordError`, `facet.values`) reads it through a probe core facet (a fixture) that calls Pi's `evaluateFacet` and `mapStack` in the subprocess and serves the answers over a remote contract held by a Go host: the same expectations, the process boundary in between, each such case marked in its test comment.
