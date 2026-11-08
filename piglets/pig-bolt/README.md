# PiG Bolt (experimental)

`pig-bolt` compiles the TypeScript extensions of a session into one executable and runs that executable from the next start. It changes speed and memory and nothing else: extensions start sooner and use less memory (measurements below and in `bench/bolt-aot/`). The rest of this file says what runs when, what it needs and what it costs.

**It is built on Pi-Bolt and is not an official Pi-Bolt project.** The compiler is [Pi-Bolt](https://github.com/opensec-git/Pi-Bolt) (MIT, © 2025 Mario Zechner, © 2026 OpenSec): Pi compiled ahead of time with a patched Bun and JavaScriptCore. PiG's authors wrote this Piglet; Pi-Bolt's maintainers did not write, review or endorse it. Report a problem with this Piglet to PiG, and a problem in the compiler to Pi-Bolt. The Piglet uses the compiler's runtime release and its own build recipe (`extensions/boltruntime/build.go`); it does not run `pi-bolt` itself.

It was called `pig-aot` in 0.5.0 and 0.7.0. `piglets/pig-bolt/pig-aot.yaml` is a one-file Piglet named `pig-aot` that extends `pig-bolt` (a compatibility alias the owner approved for 0.7.1): `pig --piglet piglets/pig-bolt/pig-aot.yaml` and `pig piglet build piglets/pig-bolt/pig-aot.yaml` select and build what pig-bolt does under the old name. That is all it does. The bare name `pig --piglet pig-aot` does not resolve to it (a name searches only the Piglet directories, and `pig piglet add` refuses a Piglet with relative origins or an extends, as it refused pig-aot in 0.7.0), the 0.7.0 path `piglets/aot/pig-aot.yaml` is gone, and `pig piglet pull` and `update` have nothing to alias because no pig-aot Piglet Binary release exists. The command that was called `aot-runtime` is now `/bolt-runtime`. The TypeScript runtime slot is still called `aot`: the setting, `PIG_TS_RUNTIME=aot`, the slot directory and the `PIG_AOT_*` variables below did not change.

It is a Piglet with one Go extension, `boltruntime` (its command is `/bolt-runtime`). It selects the `aot` TypeScript runtime in PiG's runtime slot (`docs/site/docs/typescript-runtimes.md`). The compiler is the Pi-Bolt 1.1.2 release of its Bun runtime. This Piglet does not redistribute the compiler or any compiled executable: the first build downloads it from the Pi-Bolt release on your machine, and every executable is compiled on your machine.

## Use

```bash
pig --piglet piglets/pig-bolt/pig-bolt.yaml
```

Select the runtime with `"extensionsTypescriptRuntime": "aot"` in `settings.json` or `PIG_TS_RUNTIME=aot`. Install PiG Standard's `bun-runtime` extension (`/bun-runtime install`) to give the first builds a Bun to run on.

Nothing blocks or asks. Each start:

| Start | The extension set runs on |
| --- | --- |
| The runtime has no compiled image for this set | Bun (Node when Bun is not installed). PiG records a build request. |
| The background build finished | The compiled executable. |
| An extension source changed (also after `/reload`) | Bun at once; the set is rebuilt in the background and the next start uses the new build. |
| An extension's module fails to evaluate on a tier (an import or top-level code that tier lacks) | The next tier, ending on Node. PiG remembers extensions that only Node loads until their source or `package.json` changes. An extension whose factory throws has run, so its error is final on every tier. |

`pig extensions runtime status` shows the tier in use, the reason for each tier it skipped and the Node-only extensions. The first build is silent. `PIG_AOT_VERBOSE=1` prints each build phase.

Platforms: linux-x64 (AVX2 or baseline) and darwin-arm64. Elsewhere the Piglet reports the runtime unsupported and PiG runs on Bun or Node. (Pi-Bolt 1.1.2 also publishes linux-arm64 and win32-x64 runtimes; this Piglet does not use them yet.)

## Commands

`/bolt-runtime [status|install|verify|remove|prune|budget [size]]`

- `status` shows the compiler, the images, the cache size and the last failures.
- `install` downloads and verifies the compiler without waiting for a request. `verify` re-checks it; `remove` deletes it and every image.
- `prune` deletes the least recently used images down to the budget.
- `budget 4GB` sets the total cache budget. The default is 2 GB, which holds about eight images (one image is 240 to 270 MB); 0.7.0 kept 1 GB. `PIG_AOT_CACHE_MAX` overrides it.

`PIG_AOT_RELEASE_URL` points downloads at a mirror. Every download is still checked against the signature key and digests pinned in this Piglet.

A new Piglet release that pins another compiler installs it on the first start, removes the earlier compiler and drops the images that compiler built: their extension sets run on Bun once and are compiled again in the background.

## Costs and gains (PROVISIONAL: one shared host, read the ratios, not the milliseconds)

Measured on one shared linux-x64 host (Xeon Platinum 8468, 16-CPU container quota, load median 25), Node 26.12.1, stock Bun 1.4.2, Pi-Bolt 1.1.2, 12 interleaved rounds, 20 turns per run against a fake model, after the 0.7.1 changes (`bench/bolt-aot/results/2026-11-06-basket-after.md`, with the before/after deltas and the A/A noise floor; `...-before.md` for the earlier code). A shared host is not a quiet one: read the ratios, not the milliseconds. The `five` set is five extensions with a hot loop, `ten` is ten trivial extensions, `rewrite` is `@juicesharp/rpiv-todo`, `pi-mcp-adapter` and a fixture that reads files beside its own module.

| Median | Node | Bun | Compiled host |
| --- | ---: | ---: | ---: |
| Launch to ready, `five` (ms) | 1183 | 960 | 362 |
| Launch to ready, `ten` (ms) | 440 | 406 | 331 |
| Launch to ready, `rewrite` (ms) | 922 | 889 | 346 |
| Go host CPU to ready, `five` or `ten` (ms) | 190 to 195 | 190 to 195 | 190 to 200 |
| Extension host CPU to ready, `five` (ms) | 1320 | 1140 | 130 |
| Extension host CPU per turn, `five` (ms) | 10.0 | 7.9 | 4.7 |
| Turn 1 / steady turn, wall, `five` | 2.9 | 3.5 | 2.7 |
| Extension host RSS after 20 turns, `five` (MB) | 183 | 165 | 112 |
| Extension host RSS after 20 turns, `ten` (MB) | 194 | 146 | 128 |
| Extension host RSS after 20 turns, `rewrite` (MB) | 202 | 172 | 126 |
| Private dirty after 20 turns, `five` (MB) | 127 | 102 | 45 |
| Word-count hot loop on a 16.8 MB file (ms) | 145 | 104 | 114 |
| JS heap allocated per turn, `ten` (MB; Node's and JSC's counters differ) | 7.2 | 11.2 | 9.5 |
| Executable (MB) | 150 | 80 | 245 |

- The compiled host reaches ready 3.3 times sooner than Node on `five`, 2.7 times on `rewrite` and 1.3 times on `ten`, with a tenth (`five`) to a quarter (`ten`) of the extension host CPU to ready, and uses 34 to 39% less memory after 20 turns. Its anonymous memory is small (45 MB private dirty against Node's 127 on `five`) and its file-backed pages are large (the mapped 245 MB image). It reserves much more address space (about 36 GiB at start against Node's 2.6 GiB and Bun's 11.5 GiB), which a limit on address space (`RLIMIT_AS`) would count.
- A start no longer hashes the 245 MB image: before the digest cache the Go host spent 460 to 480 ms of CPU to reach ready with this runtime and 190 ms with Node; now it spends 190 to 200 ms with both (Bun's slot executable gains the same cache: about 55 to 80 ms less Go host CPU and about 70 to 120 ms sooner to ready, a change larger than the A/A floor of the Bun arm; `bench/bolt-aot/results/2026-11-06-basket-bun-aa.md`). The first start after a build (or after an image is copied or touched) still hashes once and records the result; a cold start measured 514 to 639 ms to ready and 370 to 470 ms of Go host CPU.
- The compiled host runs a tight JavaScript loop slower than Bun's JIT (114 ms against 104 ms for the word-count loop, about 1.1x; it was 135 ms against 96 ms with Pi-Bolt 0.7.4) and faster than Node (145 ms). A JIT-on build and a build with frozen built-ins measured the same with 0.7.4.
- Per-turn CPU of the Go host (11 to 18 ms) is the same on every runtime: the runtime does not set the cost of a turn.
- The compiled host starts a `node -p process.execPath` of about 30 ms CPU so that `process.execPath` names a Node.
- Building an image takes about 20 to 30 s on this host (about 5 s more for a set that needs the rewrite below). It runs in the background; until it finishes the set runs on Bun or Node.
- Numbers from the same harness for Pi-Bolt 0.7.4 cannot be given: its compiler crashed on the current runtime tree (below).

## What is verified

The release's `SHA256SUMS` must carry a valid Ed25519 signature from the key pinned in `lock.go`. The key is not fetched from the repository it verifies (Pi-Bolt 1.1.2 signs with the same key as 0.7.4; the PEM file at both release commits has SHA-256 `ca62e435...`). The signed digest of the archive must equal the pin, and the compiler's digest must equal the pin. A failed check leaves nothing installed and the session on its current tier.

When PiG starts the compiled executable it compares the executable's SHA-256 with the digest in `runtime.json`, as for every slot executable. The comparison is skipped only while the executable is the file whose digest PiG already checked: the check is remembered beside it, keyed by device, inode, size, modification time and status-change time, so a changed, replaced or copied executable is hashed again (`docs/additive-features.md`, D19). A writer who can write beside the executable can already rewrite `runtime.json`; the cache adds no way in for another user.

## What is compiled

Each extension's sources and its packages are bundled into the executable. A file that names its own location (`import.meta.url`, `import.meta.dirname`, `import.meta.dir`, `__dirname`, `__filename`, `require.resolve`, `import.meta.resolve`, a computed `require()` or `import()`) is compiled from a copy in which those expressions say where the installed file is, as Pi-Bolt's own `pi-bolt compile` does (the scanner is a port of its `rewriteCopy`; see `THIRD_PARTY_NOTICES.md`). The extension therefore reads its skills, locales and data files from where it is installed, which is why a compiled-in extension must stay installed in place: the image is stale, and the set runs on Bun, as soon as one of its files changes. A set that needs this takes one more bytecode build to compile.

A set is refused, and runs on Bun or Node, when a file that reads files or starts processes uses `import.meta` as a value (`const m = import.meta`, `import.meta["url"]`, `{...import.meta}`), or its path or source is not valid UTF-8, because the build cannot say what location it means. A set that fails to compile or to register is recorded as failed and retried only after its sources change. A function over the compiler's bytecode limit is built with the bytecode kept in the heap (a larger image) when the compiler allows it.

## Known limits

- In a compiled CommonJS module `module.filename`, `module.paths`, `module.id`, `require.main` and `process.argv[1]` do not name the installed file; only `__dirname`, `__filename`, `import.meta.*`, `require.resolve` and computed `require()`/`import()` are pinned to it.
- The rewrite has no scope analysis, as in Pi-Bolt: a module that declares `__dirname`, `__filename` or `require` anywhere keeps its own in every use, and a class head whose generics contain `{`, `;` or `:` is not recognised as a class body.
- The compiled host runs a tight loop slower than Bun's JIT (about 1.1x in the measurement above).
- The image is 240 to 270 MB, so the default 2 GB budget keeps about eight sets; the least recently used image goes first and its set is built again at its next request.
- Pi-Bolt 0.7.4's compiler segfaulted ("Segmentation fault at address 0x30") in the ahead-of-time step on the current runtime tree, on the CI image and on the host, whenever the build's function order held any record; 1.1.2 builds the same input. A Piglet release that is pinned to 0.7.4 cannot build an image on this tree.
- Pi-Bolt 1.x publishes other platforms (linux-arm64, win32-x64) that this Piglet does not build for yet.

## Differences an extension can see

A compiled host does not re-read edited source: `/reload` after an edit moves the session to Bun (see the table). `process.execPath` is the `node` found on `PATH`; with no `node` on `PATH` the set runs on Bun.

Each build runs every extension's factory once more, outside any session, to record the order in which the compiled host runs its functions. These training runs use a scratch `HOME`, a scratch working directory and, of your environment, only `PATH`. A factory's side effects, such as files it writes, requests it sends or processes it starts, therefore happen once per build in addition to each session start.

## Notices

The compiled executable contains Bun (MIT), WebKit's JavaScriptCore (LGPL-2.0 and BSD-2-Clause) and ICU (Unicode-3.0) code. Each image directory holds `licenses/` with the notices from the release archive and the location of the corresponding source. See `THIRD_PARTY_NOTICES.md`.
