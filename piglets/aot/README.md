# PiG aot runtime (experimental)

`pig-aot` compiles the TypeScript extensions of a session into one executable and runs that executable from the next start. It changes speed and nothing else: extensions start sooner and use less memory (measurements: `bench/bolt-aot/`). The rest of this file says what runs when, and what it needs.

It is a Piglet with one Go extension, `aotruntime` (its command is `/aot-runtime`). It selects the `aot` TypeScript runtime in PiG's runtime slot (`docs/site/docs/typescript-runtimes.md`). The compiler is Pi-Bolt's ahead-of-time build of Bun, version 0.7.4. This Piglet does not redistribute it or any compiled executable: the first build downloads it from the Pi-Bolt release on your machine, and every executable is compiled on your machine.

## Use

```bash
pig --piglet piglets/aot/pig-aot.yaml
```

Select the runtime with `"extensionsTypescriptRuntime": "aot"` in `settings.json` or `PIG_TS_RUNTIME=aot`. Install PiG Standard's `bun-runtime` extension (`/bun-runtime install`) to give the first builds a Bun to run on.

Nothing blocks or asks. Each start:

| Start | The extension set runs on |
| --- | --- |
| The runtime has no compiled image for this set | Bun (Node when Bun is not installed). PiG records a build request. |
| The background build finished | The compiled executable. |
| An extension source changed (also after `/reload`) | Bun at once; the set is rebuilt in the background and the next start uses the new build. |
| An extension fails to load on a tier | The next tier, ending on Node. PiG remembers extensions that only Node loads until their source or `package.json` changes. |

`pig extensions runtime status` shows the tier in use, the reason for each tier it skipped and the Node-only extensions. The first build is silent. `PIG_AOT_VERBOSE=1` prints each build phase.

Platforms: linux-x64 (AVX2 or baseline) and darwin-arm64. Elsewhere the Piglet reports the runtime unsupported and PiG runs on Bun or Node.

## Commands

`/aot-runtime [status|install|verify|remove|prune|budget [size]]`

- `status` shows the compiler, the images, the cache size and the last failures.
- `install` downloads and verifies the compiler without waiting for a request. `verify` re-checks it; `remove` deletes it and every image.
- `prune` deletes the least recently used images down to the budget.
- `budget 2GB` sets the total cache budget. The default is 1 GB. `PIG_AOT_CACHE_MAX` overrides it.

`PIG_AOT_RELEASE_URL` points downloads at a mirror. Every download is still checked against the signature key and digests pinned in this Piglet.

## Known limits

- The compiled host runs a tight JavaScript loop slower than Bun's JIT: the word-count example loop measured 135 ms compiled, 96 ms on Bun (about 1.4x) and 123 ms on Node. A JIT-on build and a build with frozen built-ins measured the same. The gain is at start and in memory: five extensions reached ready in 534 ms against 1671 ms on Node and 1814 ms on Bun, and used 111 MB after five prompts against 144 MB on Node and 283 MB on Bun (`bench/bolt-aot/results/2026-10-10.md`, one shared host, provisional).
- A set is refused, and runs on Bun, when a file in it reads files next to its own location (see "What is not compiled"). Compiling such a set faithfully needs a per-file rewrite of `import.meta.url`; that is planned after 0.5.0.

## What is verified

The release's `SHA256SUMS` must carry a valid Ed25519 signature from the key pinned in `lock.go`. The key is not fetched from the repository it verifies. The signed digest of the archive must equal the pin, and the compiler's digest must equal the pin. A failed check leaves nothing installed and the session on its current tier.

## What is not compiled

A set is refused, and runs on Bun or Node, when a file in it both names its own location (`__dirname`, `import.meta.url`) and reads files or starts processes, because the compiled executable has no such directory. A set that fails to compile or to register is recorded as failed and retried only after its sources change.

## Differences an extension can see

A compiled host does not re-read edited source: `/reload` after an edit moves the session to Bun (see the table). `process.execPath` is the `node` found on `PATH`; with no `node` on `PATH` the set runs on Bun.

Each build runs every extension's factory once more, outside any session, to record the order in which the compiled host runs its functions. These training runs use a scratch `HOME`, a scratch working directory and, of your environment, only `PATH`. A factory's side effects, such as files it writes, requests it sends or processes it starts, therefore happen once per build in addition to each session start.

## Notices

The compiled executable contains Bun (MIT), WebKit's JavaScriptCore (LGPL-2.0 and BSD-2-Clause) and ICU (Unicode-3.0) code. Each image directory holds `licenses/` with the notices from the release archive and the location of the corresponding source. See `THIRD_PARTY_NOTICES.md`.
