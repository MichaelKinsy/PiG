# Durable benchmark report

This directory renders the Durable benchmark deck from one results file. The style follows Mario Zechner's pi-vs-tardigrade deck ([badlogic/durable-bench](https://github.com/badlogic/durable-bench), `pi-vs-tardigrade/slides/`): dark slides, four stat cards at 3,500 turns, and warm and cold latency as conversations grow at 50, 250, 1,000 and 3,500 turns. durable-bench has no license, so the deck borrows only that visual style; no code is taken from it. The deck also shows database size, the time to build the history, realistic workloads, the PiG builds, Cloudflare numbers when they exist, and the method.

Contestants: pi-durable main `da866ada` (Pi's own Durable Object SQLite adapter), Tardigrade 0.44.0, and PiG Durable (TinyGo build as the headline; the Go and native builds of the same gated core on a detail slide). The gate's TypeScript core appears on the builds slide only, as a separately labelled design control (`kind`/`role` `control`), never as PiG Durable. Cloudflare numbers appear only if the owner's Cloudflare account is ready; otherwise the deck is Miniflare-only and says so.

Status: the pipeline works and has rendered a dry run with placeholder data (`draft/png/`, watermarked DRAFT). The final deck waits for the production core to pass the CONTRACT gate and for the session in [RUNNER-SPEC.md](RUNNER-SPEC.md).

## Commands

```sh
npm ci --ignore-scripts      # playwright-core and the Inter and JetBrains Mono fonts, pinned in package-lock.json
npm test                     # gate and metric tests
./render.sh --draft          # data/placeholder.jsonl -> draft/png/*.png (2000x1125), draft/tables.md
./render.sh                  # data/results.jsonl -> final/png/*.png; refuses unless every gate passes
node assemble.mjs --meta <meta.json> --out data/results.jsonl --samples <durable-bench results.jsonl>... --seeds <fixture .json>... [--cloudflare <file>...]
node placeholder.mjs > data/placeholder.jsonl
```

`render.sh` runs the tests, then `build.mjs`. `build.mjs` validates the file, writes one HTML page per slide (laid out at 1600x900), renders each page with headless Chromium through Playwright at device scale 1.25 (2000x1125 PNG), and fails if any element overflows the slide, a panel or the footer, if two chart labels overlap, or if a font did not load. Chromium comes from `$CHROME`, else the newest browser in `~/.cache/ms-playwright`, else `chromium` or `google-chrome` on `PATH`.

## Files

| path | content |
|---|---|
| `build.mjs` | results file → HTML → PNG, plus `tables.md` and `SOURCE.txt` |
| `render.sh` | regenerates every slide from the committed results file |
| `assemble.mjs`, `lib/assemble.mjs` | merges a session's raw durable-bench outputs into one results file and validates it |
| `contract-record.mjs`, `lib/contract.mjs` | derives `meta.contract` from the conformance gate's `report.json` and `durable/contract/corpus.toml`; refuses unless the reference and every listed build passed every ready row |
| `lib/results.mjs` | parser, gates, metrics, review tables |
| `lib/slides.mjs` | slide HTML and SVG bar charts |
| `lib/shoot.mjs` | Playwright rendering and layout checks |
| `placeholder.mjs`, `data/placeholder.jsonl` | the synthetic dry-run data |
| `data/session-meta.template.json` | the meta line for the final session; fill every TODO |
| `data/results.jsonl` | the final results file; absent until the final session |
| `draft/` | the dry run: `png/`, `tables.md`, `SOURCE.txt` |
| `final/` | the final deck; only a final results file renders here |
| `RUNNER-SPEC.md` | the final session, agreed with lane durable-repro |

## Results file

The file is JSON Lines.

- Line 1 is the meta line (`"t": "meta"`). It holds `status` (`placeholder`, `draft` or `final`), `session`, `machine`, `runner`, `protocol`, `model`, `contract`, `cloudflare`, `targets`, `bench_targets`, `workloads`, `text`, `publish` and, after `assemble.mjs`, `sources`. `data/session-meta.template.json` shows every field.
- Sample lines (`"t": "sample"`, or no `t`) are durable-bench `bench/run.ts` lines unchanged: `target`, `version`, `turns`, `open`, `turn[]`, `bytes`, `rss`, `cpu`, `load`, and so on. `assemble.mjs` adds `workload` (default `standard`), `host` (`miniflare`, `cloudflare`, or `native` for the native build, which runs as a native process) and `bench_target` (the durable-bench name).
- Seed lines (`"t": "seed"`) are durable-bench `bench/seed.ts` fixture JSON files: `target`, `version`, `turns`, `seedMs`, `fingerprint`, `bytes`. A `from` field names where the run started (`"empty"`, `"250"` or `"from250"`). Runs from an empty store (`from` absent or `"empty"`) need a fingerprint. Several seed runs of one cell are allowed; the deck shows their median.
- `meta.workloads.<w>.fixtures` (optional) maps a size to the facts of the history pi-durable main wrote: `entries`, `entry_bytes` (transcript bytes), `compactions`, `active_entries`. The workload slides print entries, transcript MB and compactions under each size (durable-repro step 4). `assemble.mjs` fills it from the `fixture.meta` of the reference target's `run-session.ts` lines only, because those lines copy pi-head's fixture metadata into other targets' lines (RUNNER-SPEC section 7, point 4); two different facts for one cell stop it.
- Probe lines (`"t": "probe"`) are where-it-runs.patch `bench/probe.ts` lines (RUNNER-SPEC section 4a): `mode` (`count` or `time`), `target`, `version`, `turns`, and per request (`open`, `turn[]`) `ms`, `cpuMs`, `crossings`, `hopSteps`, `coreMs`, `instantiateMs`, `rowsRead`, `rowsWritten`, `wasmBytes`, `heapUsed`, `backing`. Count lines also carry `retained` (`heapUsed`, `backing`, `wasmBytes` after a full collection after the last turn); the memory row judges it, and the final gate requires it for the headline and the reference. Size lines (`"t": "size"`) are `bench/size.ts` lines: `bytes` (upload, uncompressed), `wasm`, `gzip`. Both come only from Durable Object targets (pi-durable main and the Wasm builds of PiG). `assemble.mjs --probes/--sizes` reads them. Cloudflare probe lines (cloudflare.patch `bench/cloud.ts`, RUNNER-SPEC section 5) carry `host` `cloudflare`, run in `count` mode only, take `cpuMs` from Cloudflare's invocation records, and hold `heapUsed`, `heapTotal` and `backing` as `null`, because Cloudflare does not expose the isolate heap; they render on a second where-it-runs slide, with Wasm memory shown but not judged.
- `assemble.mjs` also reads durable-repro's `run-session.ts` lines (one per target and size, raw samples inside) and `seed-time.ts` lines, and converts them to the lines above. `meta.bench_targets` maps a runner target to `null` to drop it: a prototype never reaches the results file. durable-bench's `empty` target maps to the floor target of the same key.

Metrics use the definitions from durable-bench's own slides:

- cold = `open + turn[0]`, one per sample (runtime startup excluded); warm = every `turn[1..]` of every sample, pooled. Both are reported as p50, p95 and max net of the floor: the p50 of the floor target's (`kind` and `role` `"floor"`, durable-bench's `empty`) same latency at the same size. The native build (Rule B) has no floor. A final file needs the floor target and at least 100 warm turns per headline, reference, contender and floor cell (RUNNER-SPEC 2a);
- rule: every number is tagged Rule A (durable-bench's JS model in a Durable Object, the same model path as pi-durable) or Rule B (the native build's own scripted model, a harness floor);
- rows written per warm turn and the Wasm high-water mark (largest `wasmBytes` after any request) come from count probes at every size;
- database = median `bytes` after the measured turns, decimal MB, PiG's index tables included;
- where it runs (probe lines, medians over samples): CPU per warm turn = per-sample median of `turn[1..].cpuMs`; cold-start CPU = `open.cpuMs + turn[0].cpuMs`; rows read and written and crossings per warm turn likewise; peak isolate memory = largest `heapUsed + backing + wasmBytes` after any request of the sample, decimal MB, against the 128 MB isolate limit; core time from `time` runs only. A verdict is a loss when PiG is more than 3% worse than pi-durable main, and a win only from 1.3× (smaller leads read "slightly better").
- building the history = median over from-zero times. A from-scratch run to the size counts as it is. Without one, each pass (`pass`, else `sample`) whose fingerprinted segments chain empty → … → n without a gap counts as the sum of its segments (`meta.protocol.seed` `"chain"`): durable-bench's `seed.ts` restarts its timer at each size of one invocation, and each segment's fingerprint proves the history the next segment starts from. A gap or an unfingerprinted segment drops that pass; two segments of one pass from the same size stop the build.

## Gates

`lib/results.mjs` enforces these rules for `build.mjs` and `assemble.mjs`. `test/results.test.mjs` covers each one. Disabling any one of the placeholder, contract status, crash matrix, build, module sha256, prototype, fingerprint, sample count, interleaving, from-zero seed and seed-increment checks fails at least one test (checked by hand at commit time).

1. A `placeholder` file holds only lines marked `"placeholder": true`, and a measured file holds none. Placeholder and draft decks carry a DRAFT watermark and a red banner, and neither renders into `final/`.
2. Real PiG numbers need a green CONTRACT gate in every status, draft included: `contract.status` `pass`, `corpus` `full`, `crash_matrix` `pass`, `reference` equal to the pi-durable main version, an https `report_url`, every PiG target's version equal to `contract.core`, its build listed in `contract.builds`, and its module sha256 equal to `contract.artifacts[build]`. `contract.identity` names the gate row behind each history size (`W1-50` … `W1-3500` for the standard workload; a final file needs all four). Do not type `contract` by hand: `contract-record.mjs` writes it from the gate's `report.json`. The gate's realistic-workload rows (`W4a`, `W4c`) use other fixtures than the deck's variants (different fingerprints), so the realistic slides rest on equal context fingerprints across targets, not on per-size gate rows. The slides state the ready and skipped corpus row counts from `contract.rows`; the gate skips some rows for every implementation, each with a recorded reason in `corpus.toml`.
3. Benchmark-slice prototype cores (`872c81bf5`, `407c624b7`) never render, in any status, not even as a design control. A design control is not PiG Durable, needs no gated module, and appears only on the builds slide.
4. Like-for-like: every target's seeded history has durable-bench's published fingerprint where one is known (standard and the `compact`, `big` and `compact-big` variants), and all targets agree for every other workload.
5. A `final` file holds no `TODO` left over from the template, names the machine (`host`, `cpu`, `pinning`, `relative`) and the model, comes from one interleaved session, times the history from zero (`protocol.seed` `scratch` or `chain`), has at least `protocol.samples` samples and a seed line for each contestant at every size, labels any Cloudflare numbers, and, when it holds PiG lines, answers the where-it-runs question: count-mode probe samples of the headline and pi-durable main and time-mode samples of the headline at 50 and 3,500 turns, and both upload sizes.

Every slide has the same footer. It names the machine (smc1, one pinned shared core, with the note that absolute times run about 2× Mario's i9 numbers, so only same-session comparisons hold), links the row-identity verification, and names the like-for-like model. The Cloudflare slide names Cloudflare instead of smc1.

## Writing rules for the final deck

Keep the tone friendly and factual. Credit Mario Zechner and the Tardigrade team for the benchmark. Describe PiG as a Go implementation of Pi, created by Michael Kinsy and originally developed at Hewlett Packard Enterprise, and not an official Pi project. Ratios on the cover are against pi-durable main, and a ratio within 3% reads "about the same". Do not describe within-session differences below about 1.3× as wins (repro step 2 drift). Never post anything: publication is the owner's decision.
