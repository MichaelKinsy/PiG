# TypeScript Extension Runtimes

PiG runs TypeScript and JavaScript extensions in a subprocess. By default, Node runs that subprocess: the `node` on your `PATH`, version 22.13 or newer. Pi runs extensions inside its own Node process; the runtime choice described here is PiG-only (D19).

You can ask PiG to run the extension subprocess on another runtime that an extension or Piglet installed. PiG itself downloads nothing. When the requested runtime cannot run, PiG tries the runtime its manifest names as `fallback`, then Node, and records why.

Compatible TypeScript and JavaScript extensions share one Node process, a packed cell. The process runs one IO worker for all of its extensions and decodes each model catalog update once. Each additional extension costs about 4 MiB and 5 ms. A Piglet Binary that carries Node extensions runs them with the installed Node through the same path (see [Piglet Binaries](/docs/latest/piglet-binaries)).

## Choose a runtime

Set `extensionsTypescriptRuntime` in the global `settings.json`:

```json
{ "extensionsTypescriptRuntime": "bun" }
```

The value is `node` (the default) or the name of an installed runtime. A project `settings.json` cannot set it. The environment variable `PIG_TS_RUNTIME` overrides the setting for one process, for example in an evaluation arm:

```bash
PIG_TS_RUNTIME=bun pig
```

### Why Node is the default

Node is the default in Stock PiG and in PiG Standard. Stock PiG keeps Pi's runtime: Pi runs extensions on Node, so this needs no numbered divergence. PiG Standard selects the same default because the measurements do not favor another runtime, and Node needs no download.

The ext10 overhead workload (ten TypeScript extensions, RPC chat, openai-completions, 12 interleaved rounds after one warm-up, an A/A copy of each arm, a quiet check before each sample, dedicated bench slot, run `mA-rpc2`, provisional hardware: shared host, clock policy not exposed) measured these medians:

| Runtime | Per-turn CPU | Total CPU | Ready | Peak RSS |
|---|---:|---:|---:|---:|
| Node (`node`) | 383 ms | 8141 ms | 434 ms | 309 MiB |
| Bun 1.4.2 (`bun`) | 382 ms | 8101 ms | 500 ms | 221 MiB |
| Pi-Bolt compiled host (`aot`) | 373 ms | 7907 ms | 501 ms | 200 MiB |

The three runtimes differ by less than 3% in per-turn CPU, which is inside the A/A floor: the extension host, not the runtime, sets the per-turn cost. Node starts the extensions about 65 ms sooner; Bun and the compiled host use 90 to 110 MiB less memory. Bun also needs a 3 to 4 s download, and the compiled host needs a first-run compile of about 30 s. Select `bun` when memory matters more than start time.

## Check which runtime runs

```bash
pig extensions runtime status
pig extensions runtime status --json
```

The command prints the requested runtime and where the request came from (`default`, `setting` or `env`), the runtime a launch uses, each runtime it skipped with the reason, the platform, the slot directory and the installed runtimes. A skipped runtime has one of these reasons:

| Reason | Meaning |
|---|---|
| `invalid_request` | The requested name is not a runtime name. |
| `not_installed` | The slot has no `runtime.json` for the runtime, or PiG cannot resolve the slot directory. |
| `invalid_manifest` | `runtime.json` cannot be read or does not have the shape below. |
| `unsupported_platform` | The runtime was installed for another platform. |
| `no_image` | An executable runtime has no image for this extension set. |
| `stale` | A file an executable image was built from changed after the build. |
| `verify_failed` | The executable's SHA-256 differs from `runtime.json`, or a Bun executable is not in PiG's runtime lock. |
| `node_only` | A launch of this set failed to start on a slot runtime and started on Node, and no file of the set changed since. |
| `start_failed` | The executable did not answer `--version`, a Bun executable reported another version, or the started process exited, an extension failed to register in it, or an extension's module failed to evaluate or exported no factory, before its factory was called. |

PiG chooses before the extension process starts: the requested runtime, then the one its `runtime.json` names in `fallback` (that runtime's own `fallback` is not followed), then Node.

A process on a slot runtime can also fail after it started: it exits, or an extension fails to register. PiG then records `start_failed` for that runtime and starts the extension again on the next runtime in the same order, ending on Node. When the process failed after the extension's factory ran, the factory runs again on the next runtime, as it does on `/reload`. A packed cell starts again as a whole when its first member failed. When a later member of a packed cell fails after earlier members registered, that member's failure is this launch's load error and the cell stays on its runtime, because the earlier members' factories already ran. PiG then records the cell's set as Node-only (below), so the next launch runs every member on Node, where the same extension isolated or first in its cell would have loaded. On Node, a failure is a load error, as it always was.

An extension whose module fails to evaluate or exports no factory on a slot runtime, before its factory is called, starts again on the next runtime too, because it may import an API that runtime lacks. Its module's top-level code then runs again on the next runtime. An extension whose factory throws reports that error on any runtime, as Pi does: the factory ran, and Pi runs it once. A later member of a packed cell is the exception described above: its failure is a load error and the record moves the set to Node. If it then starts on Node, PiG records the set in `<slot>/.node-only/<set key>.json` and later launches run the set on Node without trying the slot runtimes again, with the reason `node_only` in `pig extensions runtime status`. The record lasts until the entry file or the nearest `package.json` of a member changes, or seven days. An extension whose module fails on Node too reports its own error and no record is written.

A reload does not reuse a retained process that runs an executable runtime's host when the set no longer resolves to that host. A compiled host does not re-read edited sources, so a changed source makes the image `stale` and `/reload` starts the set on the fallback runtime.

## The runtime slot

Each runtime is a directory under the slot root: `$PIG_TS_RUNTIMES_DIR` when set, else `<agent dir>/runtimes/typescript`. The directory name is the runtime name: lowercase letters, digits, `.`, `_` and `-`, at most 64 characters, not `node`. The directory holds `runtime.json`:

```json
{
  "id": "bun",
  "kind": "bun",
  "version": "1.4.2",
  "platform": "linux-x64",
  "provider": "pig-bun",
  "image": { "executable": "bun", "sha256": "a83d2637…" }
}
```

| Field | Meaning |
|---|---|
| `id` | The runtime name. It must equal the directory name. |
| `kind` | `bun` or `executable` (see below). |
| `version` | The runtime's version. For `bun`, the version `--version` prints. |
| `platform` | `<os>-<arch>`: `linux`, `darwin`, `windows` or `android` with `x64` or `arm64`. PiG uses the runtime only on that platform. |
| `provider` | The extension or Piglet that installed the runtime. |
| `image` | The executable for any extension set: `executable` (a path inside the runtime directory), `sha256` (lowercase hex) and, for an executable runtime, optional `inputs`. |
| `sets` | Executable runtimes only: images per extension set, keyed by set key. |
| `fallback` | Optional. The name of another slot runtime PiG tries when this one cannot run a set, before Node. |

The shape is strict: PiG rejects unknown fields. A provider writes `runtime.json` last, after the executable is in place, so PiG never reads a manifest for a partial install.

PiG hashes each executable once per process and runs `<executable> --version` before the first launch. For `kind: "bun"`, the executable's SHA-256 must also be in PiG's runtime lock (`coding/extension/host/tsruntime/lock.go`): the `bun` executables of Bun 1.4.2 for PiG's eight release targets. Other Bun builds do not run.

### Kind `bun`

PiG starts stock Bun with the extension runtime tree PiG materializes for each extension or packed cell:

```text
<bun> --no-install --no-env-file --config=<tree>/shims/bunfig.toml --preload <tree>/register-loader.mjs <tree>/cli.mjs <entry>
<bun> --no-install --no-env-file --config=<tree>/shims/bunfig.toml --preload <tree>/register-loader.mjs <tree>/cell.mjs <manifest.json> <entries…>
```

`--no-install` keeps Bun from installing missing packages from a registry. `--no-env-file` keeps Bun from loading the project's `.env` files into the extension process's environment: the process runs in the project directory, and Node loads no such file. `--config` names an empty file, so Bun reads neither a project `bunfig.toml` nor yours: either could preload code into the extension process. Pi's own Bun binaries disable the same two autoloads.

On Bun, an extension runs on JavaScriptCore instead of V8. Observable differences:

- The engine writes its own error messages. Assigning to a frozen object's property throws `TypeError: Attempted to assign to readonly property.` instead of V8's `Cannot assign to read only property 'k' of object '#<Object>'`.
- `util.inspect` and `console.log` format some values differently. Bun prints a repeated reference to an object as `[Circular *1]` where Node prints the object again, and with `depth: 0` Bun prints a `URL` as `URL [Object]` where Node prints its fields.
- `Function.prototype.toString` returns the source as Bun's transpiler printed it, so whitespace and line breaks can differ from the file.
- `node:v8` `Serializer` and `Deserializer` throw `NotImplementedError`. `v8.serialize`, `structuredClone` and `pi.events` work, and a value that cannot be cloned fails with Bun's `DataCloneError` text.
- `/reload` evaluates an edited ES module extension (`.mjs`, or `.js` under `"type": "module"`) and its importers again, as on Node ([D93](../../parity/DIVERGENCES.md)). On Bun the module evaluated again keeps its URL: `import.meta.url` carries no `pig-reload` query.

An extension that depends on V8 behavior should run on Node: set `PIG_TS_RUNTIME=node` or remove the `extensionsTypescriptRuntime` setting.

### Kind `executable`

An executable runtime is a host built from PiG's runtime tree, such as an ahead-of-time compiled host. PiG starts it with the script's mode as the first argument and the runtime tree in `PIG_TS_RUNTIME_TREE`:

```text
<executable> cli <entry>
<executable> cell <manifest.json> <entries…>
```

An executable runtime may be installed with no image and no sets: a provider writes that manifest first, so launches record build requests. A launch that selects an image touches `.used` in the image's directory, at most once a minute, so a provider can evict the least recently used images.

The executable must answer `--version` and exit 0. It must not load `.env` or `bunfig.toml` files from its working directory, which is the project: a host compiled with `bun build --compile` needs `--no-compile-autoload-dotenv` and `--no-compile-autoload-bunfig`, as Pi's binaries use. PiG verifies its SHA-256 against `runtime.json`; PiG does not pin locally built executables in the lock.

A bundling host cannot follow the runtime tree's computed `require` calls or URLs. Before the runtime loads, it sets `globalThis[Symbol.for("pig.runtimeModules")]` to an object with `require(specifier)`, `source(specifier)` and `worker(specifier)` for the specifiers that `runtime-modules.mjs` lists, relative to the tree's root. Without that object the tree resolves each specifier itself.

### Extension sets

PiG launches each isolated extension and each packed cell as one extension set. The set key is the lowercase hex SHA-256 of the runtime tree's digest, then for each member in admission order a NUL byte, its name, a NUL byte and its entry path. PiG looks for an executable runtime's image in `sets[<key>]`, then in `image`.

Each image may list `inputs`: the absolute `path`, `size` and `mtimeNs` of each file the image was built from. When one differs, the image is stale.

When an executable runtime has no current image for a set, PiG writes `<runtime dir>/requests/<key>.json`, at most once a minute for each set, and runs the set on the next runtime:

```json
{
  "key": "…",
  "members": [{ "name": "todo", "entry": "/home/me/.pig/agent/extensions/todo/index.ts" }],
  "runtimeTree": "/home/me/.pig/cache/cells/node/…/runtime"
}
```

The runtime's provider reads the requests, builds images, and adds them to `sets` in `runtime.json`. The next launch of the set uses the image.

### Host with the extensions compiled in

A host that carries the extension factories sets `globalThis[Symbol.for("pig.compiledFactories")]` to a `Map` from entry path to the module's default export before the runtime loads. The loader serves a listed entry from the map and loads any other entry from disk. The runtime tree's jiti loads on first use, so a host that compiles every extension in never loads it. `PIG_TS_RUNTIME_TREE` names the materialized tree, so the host reads `harness/` from disk.

## Platforms

Node runs extensions on every platform. Whether another runtime exists for a platform depends on its provider. PiG's runtime lock pins Bun 1.4.2 for `linux-x64` and `linux-arm64` (glibc and musl), `darwin-x64`, `darwin-arm64`, `android-x64`, `android-arm64`, `windows-x64` and `windows-arm64`.
