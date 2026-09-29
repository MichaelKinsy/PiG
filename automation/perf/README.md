# Startup performance checks

## Blocking proxies

Run `make startup-proxies`. The Linux CI `startup` shard runs the same target from a fresh checkout before generating runtime assets.

`startup-proxies.json` names the required tests. The runner verifies that every named test actually executes and passes; a missing or skipped test fails. It also reads Go's production embed inventory and rejects a second, uncompressed Node runtime tree.

The guards cover:

- provider-filtered backing arrays sized to their results;
- scalar compatibility copies without a JSON round trip;
- unchanged catalog encoding reuse, no per-model normalization allocations on scalar cache hits, and one eliminated projection during initial binding;
- exact callbacks, registration/model ordering, frame bytes and queue ownership;
- signed zero, nil/empty containers, mutable metadata, opaque marshalers, cycles, and shared subgraphs;
- zero archive opens or decompression on a warm runtime hit;
- content/version identity, atomic concurrent publication, and lease-aware cache pruning;
- archive/source equality and the embedded runtime size ceiling;
- deferred presentation imports, complete shared library exports and identities, and exact bundle regeneration;
- Jiti and native bytecode cache reuse with same-size/mtime source edits, compiler identity changes, explicit disabling, and ready-time publication;
- unused private Unicode initialization and linear frame-copy volume, including malformed-frame termination;
- package initializers of this module bounded by the runtime's init accounting (bytes and allocations, not time), no package-level `regexp.MustCompile` in any package linked into `pig`, and every lazy pattern compiling;
- catalog materialization bounded by allocations per model, and `MaxThinking` selection equal to `GetSupportedThinkingLevels` for every catalog entry and level-map combination;
- SDK staging that swaps a complete tree, leaves no staging directory, and skips SDK hashing when the extension cache has nothing to classify;
- a streamed embedded-SDK marker digest equal to the whole-file digest, so a warm start never restages;
- a first-start docs tree equal, byte for byte and mode for mode, to the tree the per-file path writes, with no staging directory left.

No blocking assertion compares wall-clock time. Allocation assertions use an independently measured original projection/serialization path or the known owned storage nodes of the test input.

`startup-budgets.json` sets the runtime archive ceiling to 12 MiB. The reviewed compressed baseline is approximately 9.3 MB. The allowance permits bounded upstream/runtime growth without accepting a return to the approximately 25 MB uncompressed embed. Change the ceiling only with retained artifact-size, cold extraction, allocation, and startup evidence. The source-identification and byte-integrity tests remain mandatory even when the archive fits.

## Advisory timing

`.github/workflows/startup-performance.yml` records startup timing for each pull-request revision and main commit, plus scheduled and manual runs. The timing job is non-blocking. It builds the current source and `v0.2.0` with the same Go toolchain and flags, then compares them with the pinned real Pi CLI.

Run it locally:

```bash
automation/ci/with-isolated-pig-home.sh python3 automation/perf/startup_timing.py \
  --pig bin/pig \
  --baseline /path/to/pig-0.2.0 \
  --pi extensions/sdk-ts/node_modules/.bin/pi \
  --runs 25 --out tmp/startup-timing
```

The fixtures are frozen copies of the stack-check TS extension and local package. An untimed RPC preflight verifies that each fixture really registers its command. Timing uses the first JSON assistant `message_start`, followed by complete successful output and shutdown. Cold trials use new HOME, agent directories, application caches, and TMPDIR. Warm trials start a new process after three unmeasured cache-seeding launches. OS page caches are not dropped. The cwd is empty and outside the checkout; no worker credentials, settings, or ancestor project context enter a trial.

The harness records every trial without retries, the source and binary identities, raw stdout/stderr, medians, p90 values, and budget ratios. Invalid output, an oracle-version mismatch, a timeout, or a registration failure remains a visible job failure. Timing misses also return a nonzero status, but the job does not block CI. Artifact upload and the job summary run even after a failure.

The relative budgets are 1.05 times the same-run 0.2.0 no-extension control and 1.15 times the same-run Pi extension/package control, for both cold and warm application caches. They are not absolute timing claims across different machines. The original 760/980 ms cold targets and each experiment's results remain in the performance findings.

Pi loads the faux-provider extension to avoid network credentials; PiG uses its built-in faux provider. All other fixture inputs are the same. Request-size, memory, long-session and steady-state budgets remain owned by `make evals` and `make perf-check`; this startup job does not replace them.

## Harness tests

```bash
python3 -m unittest discover -s automation/perf -p 'test_*.py'
```

These tests exercise the measurement logic and isolation, not workflow YAML.
