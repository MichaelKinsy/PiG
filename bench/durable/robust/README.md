# Durable robustness rig additions (robust-durable)

Additions to a durable-bench checkout (Rule A: durable-bench's JS model in a Durable Object under Miniflare) used for the scale and several-objects items of the Durable robustness proof. The proofs that run without the rig (memory induction, exhaustive crash points) live in `durable/contract` (`cli/robust.mjs`).

- `isolate.ts`: several Durable Objects of one target in one Miniflare isolate. Copy it into the checkout's `bench/` and run `BENCH_PROBE=count node bench/isolate.ts <target> <size> [objects=4] [turns=10]`. It reports the isolate's peak (no collection) and retained (after a forced collection) memory: V8 heap, ArrayBuffer backing store and every Wasm core's linear memory. Cloudflare's limit is 128 MB per isolate.
- `scale.sh`: one run and one probe (time and count modes) per target and size on one CPU.
- `summarize.mjs`: a line per probe record.

The 10,000- and 25,000-turn fixtures are durable-bench's 3,500-turn fixture extended with `durable/contract/tools/extend-fixture.mjs` (same W1 history; fingerprints in `durable/contract/lib/workload.mjs` `EXTENDED_FINGERPRINTS`).
