# Perf basket, BEFORE the 0.7.1 pig-bolt changes (2026-11-06) (PROVISIONAL: do not publish)

Tip measured: `741f27c59b` (pig-bolt rename; no pin bump, no `import.meta` rewrite, no digest cache). Harness: `bench/bolt-aot/basket.py` at the commit named in the generated header below; raw JSONL (`runs.jsonl`, 2 MB, 224 records) stays under `~/pigaot-scratch/basket/before/` on xd670 and is not committed. This file is the generated report (`basket.py report`) with a method note, headline rows and a reading of it in front.

**One run on a loaded shared host is not a publishable number.** The 1-minute load average during the run was median 25 (min 17, max 41) on 192 visible CPUs, the container is capped at 16 CPUs (cgroup `cpu.max 1600000 100000`), and the earlier results in this directory were taken at load 140-186. The noise floor is the A/A columns (`node` against `node2`, `aot` against `aot2`, the same arm twice, interleaved with the others): 0-3% on nearly every launch, wall and memory row, up to 7% on a few memory and ratio rows, and 18-40% only on the 10 ms-tick first-turn CPU rows (a 10 ms tick on a 10-30 ms quantity). A first full run, kept as `before-run1/` next to the raw JSONL (not committed), agrees with this one within the same floor (five-set launch to ready: Node 1180/1175 ms, Bun 1066/1047, aot 645/638; Go host CPU to ready 190/190, 290/280, 470/470).

## What "BEFORE" means for the aot arm

- **aot 0.7.4 (the pin of this tip) cannot be measured: its image does not build on this host.** The compiler segfaults ("Segmentation fault at address 0x30", Bun Canary v1.4.3-canary.1) in the final AOT step for the ten-extension set, in the CI container and natively (PigAotClose bisected it: any non-empty `--bytecode-order` file). The arm `aot074` (slot `slot-before`, 0.7.4 manifest, no image) is in the tables as `n/a` with PiG's own answer: the extension host that started was Bun, not `pig-ts-host` (`arm aot074 ran ['bun'], not pig-ts-host`), and the compiler's crash record is quoted in the header. The 2026-10-10 and 2026-10-11 aot numbers are the only 0.7.4 numbers there are.
- **The `aot` and `aot2` arms are Pi-Bolt 1.1.2 on the old code**: the same pig binary (built from `741f27c59b`) and the images built by the Piglet worker of `0a0d27d74` (= `741f27c59b` + the two pin commits `a921aa335`, `0a0d27d74`; the Piglet-only diff), compiler downloaded from the Pi-Bolt release with the pinned key and digests, images 240.1 MB (ten) and 245.1 MB (five). So the AFTER comparison for the pin bump alone is `aot` here against the same arm later; the digest cache and the `import.meta` rewrite are what differs after.
- Every aot cell is backed by the extension host's `/proc/<pid>/exe`: a run whose host is not `<slot>/aot/images/<key>/pig-ts-host` is an error, not a number (the same check for Bun: the slot's `bun/bun`, whose SHA-256 equals the lock's `a83d2637...`; for Node: a `node` binary). The executables seen are listed under each set. `pig extensions runtime status --json` per arm is in the header (it names the runtime selection; it has no per-set view, so the exe path is the proof).

## Arms

| arm | what |
|---|---|
| `node` | PiG's default TypeScript runtime, Node v26.12.1 (the container's `--node current`) |
| `node2` | A/A repeat of `node` |
| `node24` | Node v24.22.1 (the container's qualified Node; the 2026-10 results used 24.19.0) |
| `bun` | stock Bun 1.4.2 in the slot |
| `aot`, `aot2` | the compiled host, Pi-Bolt 1.1.2 on the old code (above); `aot2` is the A/A repeat |
| `aot074` | Pi-Bolt 0.7.4: cannot build, `n/a` |

Sets: `five` = the five extensions of `bench.py` (pi-subagents, `todo.ts`, `permission-gate.ts`, `heavy-toolcall.ts`, `word-count.ts` with its hot loop); `ten` = the ten trivial `bench-ext-NN.ts` of `ext10_bench.py` (compiled in). Fake model (`Model` in `basket.py`, derived from `fake_model.py`): a turn is a prompt, one model tool call, the tool, a final answer; 20 turns per run.

## Method (what each number is)

- 12 interleaved rounds, arm order rotated each round, one recorded warm-up run per arm and set before them. Cells are median [p10-p90] over the 12 runs (linear interpolation; with 12 values p10/p90 are close to min/max). Allocation passes have 3 rounds (median [min-max]) and no A/A arm.
- **Launch to ready**: `pig --mode rpc` spawned; `get_commands` answered with the set's commands (checked by name).
- **CPU**: `/proc/<pid>/stat` utime+stime, 100 ticks/s (10 ms resolution), of the Go host (the pig process) and of the extension host (every descendant of pig, summed). At ready it is read immediately; memory is read 0.5 s later. Turn 1 and turns 2-20 use snapshots 0.5 s after ready, 0.2 s after turn 1 and 0.2 s after turn 20 (so turn-1 CPU includes up to 0.2 s of idle, which is ~0). "Reaped children" is `cutime+cstime`: CPU of children that exited and were waited for (the Go host's `--version` start checks; for aot, the compiled host's own `node -p process.execPath`, ~30 ms CPU). Tick sampling makes short children read 0 or 10 ms, so those rows are lower bounds.
- **First turn against steady state**: turn 1 is timed separately; steady is the median of turns 6-20 of each run (turns 2-5 are warm-up of the JIT/caches and shown by turn 2).
- **Memory**: `/proc/<pid>/smaps_rollup` summed over the extension host's process tree: RSS, PSS, Pss_Anon/Pss_File/Pss_Shmem, Private_Dirty/Private_Clean, Shared_Clean+Shared_Dirty, at 0.5 s after ready, after turn 1 and after turn 20; Go host RSS/PSS beside them. Minor/major faults are the process counters.
- **Allocation, three different things, each defined where it is shown**: (1) page faults (above, every timing run); (2) memory syscalls from `strace -f --seccomp-bpf -e trace=memory,process` in a separate pass (never mixed into a timing number): counts of mmap/munmap/brk/mprotect/madvise per process class (Go host; "ext" = every process the Go host started: the `--version` start checks plus the extension host) and phase, and the anonymous-mmap bytes requested (address space, reservations count in full, so Bun's 11.8 GB and aot's 36.8 GB are reservations, not use); (3) JS heap allocation in a third pass on the set plus `exts/basket-probe.ts` with a forced GC at each probe point: Node 26 from `v8.getHeapStatistics().total_allocated_bytes` (cumulative since start), Bun and aot from JSC's own GC log (`BUN_JSC_logGC`; allocated = heap before a collection minus heap after the previous one, kb resolution, cut into phases by marker lines the probe writes to stderr), live heap after the forced GC from `process.memoryUsage().heapUsed`, JSC `heapStats().objectCount`. These are not the same quantity: V8 counts every allocation since the isolate started, the JSC log counts heap growth between collections and the aot host's pre-built static heap is not "allocated" at all. Compare within a runtime across BEFORE/AFTER; do not read Node's 354 MB against aot's 33 MB at ready (five set) as a ratio. **n/a**: Node 24 (its `getHeapStatistics()` has no `total_allocated_bytes`; no GC log is read for Node, a `--trace-gc` shim produced no lines on the host's stderr in a trial and I did not chase it), JSC live-object counts on Node (V8 exposes none), and everything for `aot074`.
- **The Go host's per-start hash of the runtime executable** is read as "Go host CPU to ready": Node has no hashed image, Bun's 79.5 MB binary is hashed, the aot image (240-245 MB) is hashed. One Python SHA-256 of an image here takes 277-291 ms (header). This is the row the digest cache moves.

## Reading it

Headline numbers (full tables follow in the generated report).

- **Go host CPU to ready is the hash cost.** Node 190-200 ms, Bun 280 ms, aot 470-480 ms on both sets, A/A 0%. aot minus Node is 270-280 ms, which equals one SHA-256 of the image (277-291 ms), Bun minus Node is 80-90 ms (79.5 MB). That is what the digest cache should remove; the AFTER run should show aot's Go host CPU to ready at about Node's 190-200 ms.
- **Total CPU to ready (Go host + extension host + reaped children)**: five set: Node 1505 ms, Node 24 1865, Bun 1400, aot 630. Ten set: Node 560, Node 24 620, Bun 620, aot 620 (480 + 110 + 30). On the five set aot is 2.4x cheaper to ready than Node because the compiled host has the five extensions (pi-subagents included) compiled in, so nothing is resolved, loaded or transpiled at start; on the ten set the extension host saves ~220-250 ms CPU and the Go host hash spends ~280 ms of it back. Without the hash aot would be ~340 ms there.
- **Launch to ready (wall)**: five: Node 1175, Node 24 1504, Bun 1047, aot 638. Ten: Node 431, Node 24 465, Bun 500, aot 615. So aot starts the five set 1.8x sooner than Node and the ten set 1.4x later: the ten set has nothing heavy to skip, and the 280 ms of hash is on the critical path.
- **First turn against steady state**: five set: turn 1 is 41 ms on Node, 43 Bun, 36 aot against steady 14.5 / 12.9 / 13.2 (ratio 2.7-3.3: the first turn costs ~23-30 ms more on every runtime, so the first-turn penalty is mostly not the runtime); ten set: 46 / 43 / 39 ms against steady 39.2 / 34.0 / 33.9 (ratio 1.16-1.29). aot's first turn is 5-7 ms quicker than Node's and its steady turn equals Bun's. Turns are 13-40 ms wall on all runtimes; at these sizes the runtime hardly moves the turn wall, the extension set does.
- **CPU per turn**: Go host 11.1-11.8 ms (five) and 17.9-18.4 ms (ten) on every arm: the runtime does not set it. Extension host per turn: five: Node 9.2, Bun 7.9, aot 4.7; ten: Node 40.3, Bun 35.5, aot 25.3 ms (aot -37% to -49% against Node, 10 ms-tick averages over 19 turns, A/A 0-1%).
- **Memory of the extension host (RSS / PSS, MB)**: five at ready+0.5 s: Node 167 / 162, Bun 162 / 160, aot 101 / 99; after 20 turns Node 182 / 176, Bun 165 / 163, aot 108 / 106. Ten: at ready Node 111 / 106, Bun 97 / 95, aot 96 / 94; after 20 turns Node 196 / 191, Bun 151 / 149, aot 135 / 133. The kinds differ more than the totals: the aot host's anonymous (dirty) memory after 20 turns is 41 MB (five) and 83 MB (ten) against Node's 126 and 143, but its file-backed pages are 65 and 49 MB (the mapped 240 MB image) against Node's 50 and 47; Bun's are 62 and 41. Pss_Shmem is 0 everywhere. The Go host is 87.7-88.2 MB RSS at ready and 109-113 MB after 20 turns on every arm.
- **Node 24's PSS is not comparable**: Pss_File 10-16 MB against 50 MB for Node 26 and Shared_Clean 52-54 MB against 8, because other processes on this shared host map the same Node 24 binary and PSS divides shared pages by sharers. Use RSS or Private_Dirty for `node24`; the other binaries (Node 26, Bun 1.4.2, the images) were not mapped by anyone else.
- **Page faults** (extension host): minor faults to 0.5 s after ready: five: Node 30.9 k, Node 24 45.4 k, Bun 39.0 k, aot 12.4 k; ten: 16.6 k / 17.8 k / 16.6 k / 12.6 k. Per steady turn: five 286 / 376 / 88 / 95, ten 1030 / 1336 / 1459 / 1080. No major faults anywhere.
- **/words hot loop** (five set, in the extension): Node 145 ms, Node 24 140, Bun 103, aot 120 (A/A: 0-1%). aot is 1.17x Bun here (the README's accepted figure was 1.4x with 0.7.4: 135 against 96).
- **Syscalls and allocation** (3 rounds): at startup the extension host (with its start checks) does 454 mmap / 342 munmap / 50 brk / 628 mprotect on Node, 522 / 8 / 6 / 50 on Bun and 191 / 85 / 24 / 75 on aot (five set); per steady turn on the ten set the aot host does ~5.7 madvise and no mmap/mprotect, Node ~16 madvise and ~7 mprotect, Node 24 ~27 and ~17, Bun ~24 madvise. JS allocation per steady turn on the ten set: Node 6.9 MB, Bun 14.0, aot 9.4; GC cycles over the 20 turns Node 18, Bun 28, aot 25; live heap after 20 turns Node 15.5 MB heapUsed, Bun 10.6, aot 6.6 (JSC objects: Bun 126 k, aot 117 k). Single numbers from 3 rounds with different accounting: use them across BEFORE/AFTER, not across runtimes.
- **The compiled host starts a Node.** strace shows `node -p process.execPath` (a child of the aot host, ~30 ms CPU, counted as reaped children) in every aot run: the host asks a Node on PATH for its `process.execPath` at start. (The 0.7.4 host, run natively with no Node on PATH during my failed build, exited 87 with "no node executable on PATH, so process.execPath cannot name a Node"; I did not run the 1.1.2 host without Node.) So the compiled host depends on a Node being on PATH and pays one Node start per extension-host start; that is an observation for the Piglet README, not a measurement of a feature.

## Against the 2026-10-10 and 2026-10-11 results (where the new ones disagree, and why I think so)

Different code (those runs: integrate-042 `3352c45971` and `2558498298`), Node 24.19.0, load 140-186, Pi-Bolt 0.7.4. Reasons below are [INFERENCE] unless stated; nothing was bisected.

- **Per-turn Go host CPU 285-293 ms then, 11-18 ms now** (ten set: 282/286/293 against 17.9/18.4/18.2 ms), and wall per turn 255-259 ms against 33-39 ms steady. The per-turn cost was in the Go host's boundary work, as the 2026-10-11 note said, and it has been fixed in the Go host between those tips and this one; this harness cannot attribute it further.
- **Bun's start and memory are much lower**: ten set launch to ready 2114 ms then, 500 now; extension host CPU to ready 2035 ms then, 340 now; RSS at ready 321 MB then, 97 now; five set ready 1814 ms then, 1047 now, PSS 279 MB then, 160 now. CPU, not wall, dropped 6x, so load (140-186 then, 25 now) alone does not explain it. Consistent with the Bun path having changed in PiG or Pi between the tips; not confirmed.
- **Node start**: five set 1671 ms (Node 24.19) then, 1504 ms (Node 24.22.1) / 1175 ms (Node 26.12.1) now; ten set 520 then, 465 / 431 now; Go host CPU to ready 330 then, 190-200 now. Lower load and a newer Node; the Node 26 gain over Node 24 on the five set (1.28x) is real within the A/A floor.
- **aot start is not better than before** despite the lower load: five 534 ms then, 638 now; ten 605 then, 615 now; Go host CPU to ready 555 then, 480 now (the hash is unchanged, ~280 ms, the images are 7-12 MB bigger: 233 MB then, 240.1 / 245.1 MB now). The ready gap to Node on the ten set therefore grew (85 ms then, 184 ms now) because Node got faster and aot did not. Extension host CPU to ready fell: 215 ms then, 110 + 30 now (ten set).
- **aot memory after turns changed direction**: ten set, extension host RSS after 20 turns 92 MB then (99 at ready), 135 MB now (96 at ready, +39 MB); Node grew +87 then and +85 now (129 to 216, 111 to 196), so the harness agrees on Node. The 1.1.2 engine on the old code keeps more after turns than the 0.7.4 image did (Pss_Anon 45 to 83 MB); I did not look for why. The five-set private dirty memory at ready is 34 MB now against 48 MB then.
- **/words**: aot 135 ms then, 120 now (compiler 1.1.2 against 0.7.4, -11%); Bun 96 then, 103 now; Node 123 then, 145 (Node 26) / 140 (Node 24.22.1) now (the A/A and run-to-run agreement is within 1-2%, so a +14-18% against the Node 24.19 number is the host or the Node build, not noise; unexplained).

## What is not measured, and why

- Node and Bun JS allocation counters for Node 24; GC logs for Node (above).
- Per-turn CPU of the Go host at better than 10 ms: tick accounting only; /proc has no finer thread-group counter without summing per-thread schedstat (exited threads are lost).
- aot 0.7.4: cannot build (above).
- Windows/macOS: Linux x64 only.
- The strace and heap passes have 3 rounds and no A/A arm; read them as one-shot counts.

## Re-running for AFTER

All inside the CI container (node, go and strace on PATH; strace is bind-mounted from the host because the image has none). From xd670, with the lead's tree `T` (the AFTER tip: pin 1.1.2, `import.meta` rewrite, digest cache) mounted:

```sh
S=~/pigaot-scratch/basket
nice -n 10 ~/cf3-work/run-mem.sh --tree ~/pigaot-basket --image tern-ci:v7 --node current --prepare \
  --env TMPDIR=$S/tmp --mount $S/..:$S/.. --mount /usr/bin/strace:/usr/local/bin/strace:ro --mount $T:$T -- '
  (cd bench/bolt-aot/exts && npm ci --ignore-scripts)
  (cd '$T' && go build -o '$S'/pig-after ./cmd/pig)           # the AFTER Go host: the digest cache is in core
  python3 bench/bolt-aot/basket.py run --out '$S'/after --pig '$S'/pig --slot '$S'/slot-after --bun '$S'/dl/bun-linux-x64/bun --build \
    --piglet-dir '$T'/piglets/pig-bolt --rounds 12 --turns 20 --alloc-rounds 3 \
    --arm node:node --arm node-after:node:pig='$S'/pig-after --arm node2:node \
    --arm bun:bun --arm bun-after:bun:pig='$S'/pig-after:slot='$S'/slot-after \
    --arm aot-pin:aot:slot='$S'/slot-pin --arm aot:aot:pig='$S'/pig-after:slot='$S'/slot-after --arm aot2:aot:pig='$S'/pig-after:slot='$S'/slot-after \
    --label tree=<AFTER commit> --label aot=<pin and commit>'
```

`--pig` is the BEFORE Go host (every arm without `pig=` uses it), and `pig=` per arm points an arm at the AFTER Go host, so one session interleaves BEFORE and AFTER arms (`aot-pin` keeps the BEFORE slot's images: it needs the images in `slot-pin` to be fresh, and a slot's images are keyed by the set's member paths, so run from the same checkout path `~/pigaot-basket` that built them). `--build` installs stock Bun into the slot (SHA-256 checked against `lock.go`), runs the worker of `--piglet-dir` (its pin decides the Pi-Bolt version; `PIG_AOT_RELEASE_URL` is honoured as in `build_set.py`) and builds the four images (`five`, `ten` and their `+probe` variants). Arm names ending in `2` are A/A repeats of the arm without the `2`. `basket.py report <out>/runs.jsonl` regenerates the markdown.

## Headline rows

### Set `five` (headline rows; the complete tables are in the generated report below)

| Metric | node | node2 | node24 | bun | aot | aot2 | aot074 | A/A node-node2 (%) | A/A aot-aot2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 0/1 | | |
| Launch to ready (ms) | 1175 [1144-1206] | 1176 [1167-1272] | 1504 [1166-1513] | 1047 [1035-1057] | 638 [595-644] | 632 [613-645] | n/a | 0 | 1 |
| Go host CPU to ready, user+sys (ms) | 190 [180-199] | 190 [180-200] | 185 [180-190] | 280 [270-280] | 470 [424-470] | 470 [460-470] | n/a | 0 | 0 |
| Extension host CPU to ready, user+sys (ms) | 1315 [1281-1365] | 1320 [1271-1408] | 1680 [1309-1699] | 1120 [1101-1130] | 130 [112-140] | 130 [111-130] | n/a | 0 | 0 |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 30 [30-39] | 30 [30-30] | n/a | - | 0 |
| Turn 1 wall (ms) | 41 [41-44] | 42 [41-46] | 41 [40-43] | 43 [42-45] | 36 [34-37] | 37 [34-40] | n/a | 1 | 3 |
| Turn 2 wall (ms) | 19 [17-20] | 19 [18-23] | 18 [18-21] | 18 [17-21] | 17 [15-19] | 17 [16-18] | n/a | 0 | 3 |
| Steady wall, median of turns 6-20 (ms) | 14.5 [13.5-15.4] | 14.4 [12.9-15.1] | 14.5 [14.1-15.4] | 12.9 [12.4-13.4] | 13.2 [11.9-13.9] | 13.3 [12.4-14.3] | n/a | 1 | 1 |
| Turn 1 / steady wall (ratio) | 3.01 [2.66-3.08] | 3.04 [2.74-3.27] | 2.87 [2.64-2.98] | 3.34 [3.19-3.98] | 2.70 [2.58-2.99] | 2.69 [2.46-3.13] | n/a | 1 | 0 |
| Go host CPU, turn 1 (ms) | 30 [30-39] | 30 [20-40] | 30 [20-30] | 30 [20-39] | 30 [20-30] | 25 [20-30] | n/a | 0 | 18 |
| Extension host CPU, turn 1 (ms) | 20 [20-30] | 25 [20-30] | 30 [20-30] | 50 [30-50] | 10 [10-20] | 10 [10-20] | n/a | 22 | 0 |
| Go host CPU per turn, turns 2-20 (ms) | 11.3 [10.5-12.1] | 11.6 [10.6-12.6] | 11.8 [11.1-12.1] | 11.1 [10.6-13.1] | 11.6 [10.5-12.1] | 11.6 [10.1-12.1] | n/a | 2 | 0 |
| Extension host CPU per turn, turns 2-20 (ms) | 9.2 [8.5-9.9] | 8.9 [8.5-9.5] | 9.2 [8.9-9.9] | 7.9 [7.4-8.4] | 4.7 [4.2-4.7] | 4.7 [3.7-4.7] | n/a | 3 | 0 |
| Extension host minor faults to 0.5 s after ready | 30871 [30701-31248] | 30850 [30608-36228] | 45419 [34425-45955] | 39006 [38255-39235] | 12427 [12364-12464] | 12431 [12061-12450] | n/a | 0 | 0 |
| Extension host minor faults, turn 1 | 745 [436-755] | 743 [725-1426] | 186 [52-218] | 654 [594-687] | 610 [433-652] | 608 [493-648] | n/a | 0 | 0 |
| Extension host minor faults per turn, turns 2-20 | 286.2 [162.0-387.5] | 269.8 [182.0-295.8] | 375.6 [351.7-396.1] | 88.1 [82.5-93.3] | 94.8 [84.1-97.7] | 95.8 [94.3-96.9] | n/a | 6 | 1 |
| Extension host RSS @ready+0.5s | 167.1 [165.8-168.9] | 166.9 [165.0-168.0] | 171.1 [170.1-172.0] | 161.7 [160.4-163.6] | 100.9 [100.0-109.6] | 100.9 [100.6-102.4] | n/a | 0 | 0 |
| Extension host PSS @ready+0.5s | 161.7 [160.6-163.2] | 161.6 [159.9-162.6] | 131.6 [125.6-148.9] | 159.5 [158.1-161.4] | 98.6 [97.7-107.3] | 98.6 [98.4-100.1] | n/a | 0 | 0 |
|   Pss_Anon @ready+0.5s | 111.4 [110.0-113.2] | 111.2 [109.4-112.3] | 116.1 [115.2-117.0] | 97.8 [96.4-99.7] | 34.2 [33.4-42.9] | 34.2 [34.0-35.7] | n/a | 0 | 0 |
|   Pss_File @ready+0.5s | 50.2 [50.0-50.6] | 50.2 [50.0-50.6] | 15.7 [9.3-29.2] | 61.7 [61.6-61.8] | 64.4 [64.3-64.4] | 64.5 [64.4-64.5] | n/a | 0 | 0 |
|   Private_Dirty @ready+0.5s | 111.4 [110.0-113.2] | 111.2 [109.4-112.3] | 116.1 [115.2-117.0] | 97.8 [96.4-99.7] | 34.2 [33.4-42.9] | 34.2 [34.0-35.7] | n/a | 0 | 0 |
| Extension host RSS @after turn 1 | 170.1 [167.5-171.8] | 169.8 [167.9-171.1] | 171.3 [170.3-172.1] | 160.1 [159.4-161.6] | 101.1 [100.8-106.8] | 101.7 [100.7-104.4] | n/a | 0 | 1 |
| Extension host PSS @after turn 1 | 164.8 [161.9-166.2] | 164.6 [162.5-165.6] | 131.0 [126.0-142.2] | 157.9 [157.2-159.4] | 98.8 [98.6-104.4] | 99.4 [98.5-102.1] | n/a | 0 | 1 |
| Extension host RSS @after 20 turns | 182.0 [179.8-182.9] | 182.3 [179.0-184.4] | 194.6 [193.2-197.4] | 164.9 [162.0-165.3] | 107.9 [107.0-112.3] | 107.4 [107.0-110.7] | n/a | 0 | 0 |
| Extension host PSS @after 20 turns | 176.3 [174.5-177.6] | 177.0 [173.9-178.9] | 154.8 [145.7-169.1] | 162.6 [159.8-163.1] | 105.6 [104.7-109.9] | 105.0 [104.8-108.3] | n/a | 0 | 1 |
|   Pss_Anon @after 20 turns | 126.3 [124.1-127.1] | 126.5 [123.3-128.7] | 139.6 [138.3-142.6] | 100.9 [98.2-101.4] | 40.8 [39.9-45.3] | 40.3 [40.0-43.6] | n/a | 0 | 1 |
|   Pss_File @after 20 turns | 50.2 [50.0-50.6] | 50.3 [50.1-51.4] | 14.4 [7.0-26.6] | 61.7 [61.7-61.8] | 64.7 [64.7-64.8] | 64.8 [64.7-64.8] | n/a | 0 | 0 |
|   Private_Dirty @after 20 turns | 126.3 [124.1-127.1] | 126.5 [123.3-128.7] | 139.6 [138.3-142.6] | 100.9 [98.2-101.4] | 40.8 [39.9-45.3] | 40.3 [40.0-43.6] | n/a | 0 | 1 |
|   Private_Clean @after 20 turns | 47.8 [47.8-47.9] | 47.8 [47.8-49.4] | 0.9 [0.1-3.4] | 61.6 [61.6-61.6] | 64.6 [64.5-64.6] | 64.6 [64.6-64.6] | n/a | 0 | 0 |
|   Shared_Clean+Shared_Dirty @after 20 turns | 7.9 [7.8-8.0] | 7.8 [6.3-7.9] | 54.1 [51.5-54.8] | 2.3 [2.3-2.4] | 2.5 [2.4-2.6] | 2.5 [2.4-2.6] | n/a | 1 | 0 |
| Go host RSS @after 20 turns | 108.9 [107.6-112.1] | 111.5 [109.2-119.1] | 109.4 [108.7-112.6] | 109.5 [108.6-113.7] | 109.7 [107.9-112.3] | 110.1 [108.6-112.5] | n/a | 2 | 0 |
| Extension host threads after the last turn | 12 [12-12] | 12 [12-12] | 12 [12-12] | 19 [18-19] | 7 [7-7] | 7 [7-7] | n/a | 0 | 0 |
| Extension host executable size (MB) | 150.3 [150.3-150.3] | 150.3 [150.3-150.3] | 126.9 [126.9-126.9] | 79.5 [79.5-79.5] | 245.1 [245.1-245.1] | 245.1 [245.1-245.1] | n/a | 0 | 0 |

### Set `ten` (headline rows; the complete tables are in the generated report below)

| Metric | node | node2 | node24 | bun | aot | aot2 | aot074 | A/A node-node2 (%) | A/A aot-aot2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 0/1 | | |
| Launch to ready (ms) | 431 [404-436] | 432 [384-449] | 465 [414-471] | 500 [461-507] | 615 [606-628] | 615 [598-622] | n/a | 0 | 0 |
| Go host CPU to ready, user+sys (ms) | 200 [190-200] | 200 [190-200] | 200 [181-200] | 280 [262-290] | 480 [471-499] | 480 [470-489] | n/a | 0 | 0 |
| Extension host CPU to ready, user+sys (ms) | 360 [340-370] | 355 [304-370] | 420 [373-439] | 340 [302-350] | 110 [100-110] | 100 [91-110] | n/a | 1 | 10 |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 30 [21-39] | 30 [30-40] | n/a | - | 0 |
| Turn 1 wall (ms) | 46 [45-47] | 47 [45-53] | 48 [45-50] | 43 [40-47] | 39 [38-42] | 38 [38-41] | n/a | 4 | 2 |
| Turn 2 wall (ms) | 36 [35-39] | 37 [33-44] | 38 [37-42] | 30 [28-35] | 30 [29-31] | 30 [27-33] | n/a | 2 | 3 |
| Steady wall, median of turns 6-20 (ms) | 39.2 [37.9-40.6] | 39.2 [37.2-39.8] | 40.8 [39.6-43.2] | 34.0 [31.9-37.3] | 33.9 [31.9-35.9] | 33.3 [31.4-34.3] | n/a | 0 | 2 |
| Turn 1 / steady wall (ratio) | 1.16 [1.12-1.22] | 1.23 [1.14-1.44] | 1.18 [1.06-1.21] | 1.29 [1.11-1.37] | 1.16 [1.09-1.23] | 1.16 [1.11-1.28] | n/a | 6 | 0 |
| Go host CPU, turn 1 (ms) | 30 [20-30] | 25 [20-30] | 30 [20-30] | 25 [20-30] | 30 [20-30] | 25 [20-30] | n/a | 18 | 18 |
| Extension host CPU, turn 1 (ms) | 45 [40-50] | 50 [40-50] | 50 [40-50] | 70 [60-88] | 30 [20-30] | 20 [20-30] | n/a | 11 | 40 |
| Go host CPU per turn, turns 2-20 (ms) | 17.9 [17.4-18.9] | 18.2 [15.8-19.5] | 18.2 [17.4-19.4] | 18.4 [15.9-20.4] | 18.2 [16.4-18.9] | 18.2 [17.4-18.9] | n/a | 1 | 0 |
| Extension host CPU per turn, turns 2-20 (ms) | 40.3 [39.0-44.0] | 39.7 [38.4-42.4] | 43.9 [40.3-48.7] | 35.5 [33.7-37.9] | 25.3 [24.7-28.8] | 25.0 [23.2-25.7] | n/a | 1 | 1 |
| Extension host minor faults to 0.5 s after ready | 16589 [15744-16950] | 16704 [16150-17073] | 17818 [17348-18125] | 16610 [16460-16680] | 12619 [12581-13041] | 12781 [12581-13318] | n/a | 1 | 1 |
| Extension host minor faults, turn 1 | 2035 [1979-2120] | 2027 [1905-2080] | 2067 [1850-2114] | 1544 [1334-2086] | 1826 [1773-1855] | 1800 [1412-1857] | n/a | 0 | 1 |
| Extension host minor faults per turn, turns 2-20 | 1029.5 [912.1-1087.8] | 989.8 [938.8-1087.9] | 1335.6 [1147.7-1384.5] | 1458.6 [1356.4-1512.5] | 1080.0 [1041.6-1136.0] | 1086.8 [1051.6-1190.4] | n/a | 4 | 1 |
| Extension host RSS @ready+0.5s | 111.0 [107.2-112.4] | 111.2 [108.9-112.5] | 113.7 [112.1-115.1] | 97.3 [96.1-97.6] | 96.4 [96.3-98.0] | 97.1 [96.3-99.2] | n/a | 0 | 1 |
| Extension host PSS @ready+0.5s | 106.3 [102.2-107.1] | 106.1 [103.7-107.5] | 74.9 [70.0-106.7] | 95.1 [93.9-95.5] | 94.1 [94.0-95.8] | 94.8 [94.0-96.8] | n/a | 0 | 1 |
|   Pss_Anon @ready+0.5s | 58.5 [54.7-59.9] | 58.8 [56.4-60.0] | 61.0 [59.5-62.4] | 57.5 [56.3-57.8] | 45.1 [45.0-46.8] | 45.7 [45.0-47.9] | n/a | 0 | 1 |
|   Pss_File @ready+0.5s | 47.3 [47.1-47.6] | 47.2 [47.0-47.6] | 12.6 [8.7-46.0] | 37.6 [37.6-37.7] | 49.0 [49.0-49.0] | 49.0 [49.0-49.0] | n/a | 0 | 0 |
|   Private_Dirty @ready+0.5s | 58.5 [54.7-59.9] | 58.8 [56.4-60.0] | 61.0 [59.5-62.4] | 57.5 [56.3-57.8] | 45.1 [45.0-46.8] | 45.7 [45.0-47.9] | n/a | 0 | 1 |
| Extension host RSS @after turn 1 | 118.8 [112.8-120.2] | 119.4 [116.2-120.4] | 121.8 [120.2-123.2] | 96.9 [93.0-99.2] | 103.8 [103.6-105.5] | 103.8 [96.6-105.4] | n/a | 1 | 0 |
| Extension host PSS @after turn 1 | 113.6 [107.4-115.0] | 114.0 [110.9-115.2] | 85.7 [78.8-95.2] | 94.7 [90.9-96.9] | 101.5 [101.3-103.2] | 101.5 [94.2-103.0] | n/a | 0 | 0 |
| Extension host RSS @after 20 turns | 196.0 [180.8-197.3] | 191.6 [187.8-197.0] | 219.3 [204.9-220.0] | 151.2 [144.8-157.8] | 135.0 [117.3-142.8] | 136.5 [124.5-141.1] | n/a | 2 | 1 |
| Extension host PSS @after 20 turns | 190.6 [175.5-192.6] | 186.4 [182.6-191.7] | 177.9 [170.6-191.7] | 149.0 [142.6-155.7] | 132.7 [114.9-140.4] | 134.2 [122.3-138.8] | n/a | 2 | 1 |
|   Pss_Anon @after 20 turns | 143.3 [128.1-144.6] | 139.0 [135.1-144.3] | 166.6 [152.3-167.3] | 108.3 [102.1-115.1] | 83.4 [65.5-91.0] | 84.8 [72.8-89.4] | n/a | 3 | 2 |
|   Pss_File @after 20 turns | 47.5 [47.3-47.8] | 47.5 [47.3-47.8] | 16.3 [7.1-24.5] | 40.6 [40.6-40.7] | 49.4 [49.3-49.4] | 49.4 [49.3-49.5] | n/a | 0 | 0 |
|   Private_Dirty @after 20 turns | 143.3 [128.1-144.6] | 139.0 [135.1-144.3] | 166.6 [152.3-167.3] | 108.3 [102.1-115.1] | 83.4 [65.5-91.0] | 84.8 [72.8-89.4] | n/a | 3 | 2 |
|   Private_Clean @after 20 turns | 45.0 [45.0-45.0] | 45.0 [45.0-45.0] | 0.1 [0.0-0.3] | 40.5 [40.5-40.5] | 49.1 [49.1-49.2] | 49.1 [49.1-49.3] | n/a | 0 | 0 |
|   Shared_Clean+Shared_Dirty @after 20 turns | 7.7 [7.6-7.7] | 7.7 [7.6-7.8] | 52.5 [52.3-52.6] | 2.3 [2.3-2.4] | 2.5 [2.5-2.6] | 2.5 [2.4-2.6] | n/a | 0 | 0 |
| Go host RSS @after 20 turns | 111.7 [109.5-117.1] | 110.9 [109.7-116.8] | 110.7 [110.0-116.4] | 111.1 [110.3-112.6] | 112.2 [111.3-116.3] | 112.6 [109.4-116.5] | n/a | 1 | 0 |
| Extension host threads after the last turn | 12 [12-12] | 12 [12-12] | 12 [12-12] | 18 [18-19] | 7 [7-7] | 7 [7-7] | n/a | 0 | 0 |
| Extension host executable size (MB) | 150.3 [150.3-150.3] | 150.3 [150.3-150.3] | 126.9 [126.9-126.9] | 79.5 [79.5-79.5] | 240.1 [240.1-240.1] | 240.1 [240.1-240.1] | n/a | 0 | 0 |

## Generated report (`basket.py report`)

Generated by `bench/bolt-aot/basket.py report` from `runs.jsonl`. **PROVISIONAL: one run on a loaded shared host; do not publish a number from it.** Read the A/A columns first.

## Header

- date (UTC): 2026-11-07T00:10:36Z; host `d6007ad32345`, kernel 7.0.0-38-generic
- CPU: Intel(R) Xeon(R) Platinum 8468; 192 CPUs visible; cgroup cpu.max `1600000 100000`, memory.max `17179869184`
- bench commit 0e437962b8fc64e2b9e1c7d536c9dba1dcf44c81 (dirty tracked files: False); labels: tree=741f27c59b, pig=built-from-741f27c59b, aot=pin-1.1.2-on-old-code-a921aa335-0a0d27d74, aot074=0.7.4-pin-cannot-build
- host load average (1 min): before 23.02, during runs median 25 (min 17, max 41), after 32.21
- strace: strace -- version 6.19; python 3.12.3; clock tick 100/s (CPU resolution 10 ms)

| arm | kind | pig (sha256, version) | runtime |
|---|---|---|---|
| node | node | 1b320811e19c, 0.7.0+1.5.0 | Node v26.12.1 (/opt/node/26.12.1/bin/node) |
| node2 | node | 1b320811e19c, 0.7.0+1.5.0 | Node v26.12.1 (/opt/node/26.12.1/bin/node) |
| node24 | node | 1b320811e19c, 0.7.0+1.5.0 | Node v24.22.1 (/opt/node/24.22.1/bin/node) |
| bun | bun | 1b320811e19c, 0.7.0+1.5.0 | Bun 1.4.2 (sha256 a83d263767d8, lock match) |
| aot | aot | 1b320811e19c, 0.7.0+1.5.0 | Pi-Bolt manifest `1.1.2`, toolchain ['pi-bolt-1.1.2-linux-x64'], 4 image(s) |
| aot2 | aot | 1b320811e19c, 0.7.0+1.5.0 | Pi-Bolt manifest `1.1.2`, toolchain ['pi-bolt-1.1.2-linux-x64'], 4 image(s) |
| aot074 | aot | 1b320811e19c, 0.7.0+1.5.0 | Pi-Bolt manifest `0.7.4`, toolchain ['pi-bolt-0.7.4-linux-x64'], 0 image(s) |

aot slot of `aot` (`/home/kinsy/pigaot-scratch/basket/slot-pin`): images `aot/images/17e7f9041a2e2e79-1794009340118344629/pig-ts-host` 245.1 MB sha256 c8876a78d418 (one Python SHA-256 of it: 291 ms); `aot/images/a7cd851ad062fb1d-1794009310918408967/pig-ts-host` 240.1 MB sha256 f2d265110d27 (one Python SHA-256 of it: 278 ms); `aot/images/cae5b43fa55d7699-1794009575719067886/pig-ts-host` 240.1 MB sha256 ee7a825358e7 (one Python SHA-256 of it: 277 ms); `aot/images/f41d924fcdb7cb91-1794009553772984900/pig-ts-host` 245.1 MB sha256 94c9578e1db1 (one Python SHA-256 of it: 277 ms)

aot slot of `aot074` (`/home/kinsy/pigaot-scratch/basket/slot-before`): NO image built
  - build failure `a7cd851ad062` (2026-11-06T18:43:02.632444596-05:00): build_failed: aot build: signal: segmentation fault: oh no: Bun has crashed. This indicates a bug in Bun, not your code. To send a redacted crash report to Bun's team, please file a GitHub issue using the link below: https://bun.report/1.4.3/lb232a5cd5gGgoggCm/6upFm6844F+9+09Cws+09Cy/+oB286tCA2AgD (members ['bench-ext-01.ts', 'bench-ext-02.ts', 'bench-ext-03.ts', 'bench-ext-04.ts', 'bench-ext-05.ts', 'bench-ext-06.ts', 'bench-ext-07.ts', 'bench-ext-08.ts', 'bench-ext-09.ts', 'bench-ext-10.ts'])

`pig extensions runtime status --json` for `node` (abridged to selection and installed manifests):

```json
{
 "selection": {
  "request": {
   "id": "node",
   "source": "env"
  },
  "runtime": "node"
 },
 "installed": []
}
```

`pig extensions runtime status --json` for `node24` (abridged to selection and installed manifests):

```json
{
 "selection": {
  "request": {
   "id": "node",
   "source": "env"
  },
  "runtime": "node"
 },
 "installed": []
}
```

`pig extensions runtime status --json` for `bun` (abridged to selection and installed manifests):

```json
{
 "selection": {
  "request": {
   "id": "bun",
   "source": "env"
  },
  "runtime": "bun",
  "kind": "bun",
  "version": "1.4.2",
  "executable": "/home/kinsy/pigaot-scratch/basket/slot-pin/bun/bun"
 },
 "installed": [
  {
   "id": "aot",
   "version": "1.1.2",
   "provider": "pig-bolt/bolt-runtime",
   "sets": 4
  },
  {
   "id": "bun",
   "version": "1.4.2",
   "provider": "basket",
   "sets": 0
  }
 ]
}
```

`pig extensions runtime status --json` for `aot` (abridged to selection and installed manifests):

```json
{
 "selection": {
  "request": {
   "id": "aot",
   "source": "env"
  },
  "runtime": "bun",
  "kind": "bun",
  "version": "1.4.2",
  "executable": "/home/kinsy/pigaot-scratch/basket/slot-pin/bun/bun",
  "fallback": {
   "reason": "no_image",
   "detail": "aot has no image for extension set "
  },
  "skipped": [
   {
    "runtime": "aot",
    "reason": "no_image",
    "detail": "aot has no image for extension set "
   }
  ]
 },
 "installed": [
  {
   "id": "aot",
   "version": "1.1.2",
   "provider": "pig-bolt/bolt-runtime",
   "sets": 4
  },
  {
   "id": "bun",
   "version": "1.4.2",
   "provider": "basket",
   "sets": 0
  }
 ]
}
```

`pig extensions runtime status --json` for `aot074` (abridged to selection and installed manifests):

```json
{
 "selection": {
  "request": {
   "id": "aot",
   "source": "env"
  },
  "runtime": "bun",
  "kind": "bun",
  "version": "1.4.2",
  "executable": "/home/kinsy/pigaot-scratch/basket/slot-before/bun/bun",
  "fallback": {
   "reason": "no_image",
   "detail": "aot has no image for extension set "
  },
  "skipped": [
   {
    "runtime": "aot",
    "reason": "no_image",
    "detail": "aot has no image for extension set "
   }
  ]
 },
 "installed": [
  {
   "id": "aot",
   "version": "0.7.4",
   "provider": "pig-bolt/bolt-runtime",
   "sets": 0
  },
  {
   "id": "bun",
   "version": "1.4.2",
   "provider": "basket",
   "sets": 0
  }
 ]
}
```

## Set `five`

Timing pass: 12 interleaved rounds (order rotated each round, one recorded warm-up run per arm before them), cells are median [p10-p90] over rounds (fewer than 7 runs: median [min-max]), A/A columns are |median(a)-median(b)| / their mean.

Proof each arm ran its tier: every run's extension-host `/proc/<pid>/exe` is checked (node: a node binary; bun: the slot's bun; aot: `<slot>/aot/images/<key>/pig-ts-host`) and a run that differs is an error, not a number. Executables seen: node: /opt/node/26.12.1/bin/node; node2: /opt/node/26.12.1/bin/node; node24: /opt/node/24.22.1/bin/node; bun: /home/kinsy/pigaot-scratch/basket/slot-pin/bun/bun; aot: /home/kinsy/pigaot-scratch/basket/slot-pin/aot/images/17e7f9041a2e2e79-1794009340118344629/pig-ts-host; aot2: /home/kinsy/pigaot-scratch/basket/slot-pin/aot/images/17e7f9041a2e2e79-1794009340118344629/pig-ts-host; aot074: 

| Metric | node | node2 | node24 | bun | aot | aot2 | aot074 | A/A node-node2 (%) | A/A aot-aot2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 0/1 | | |
| **Startup (launch to ready: get_commands answered with the set's commands)** | | | | | | | | | |
| Launch to ready (ms) | 1175 [1144-1206] | 1176 [1167-1272] | 1504 [1166-1513] | 1047 [1035-1057] | 638 [595-644] | 632 [613-645] | n/a | 0 | 1 |
| Go host CPU to ready, user+sys (ms) | 190 [180-199] | 190 [180-200] | 185 [180-190] | 280 [270-280] | 470 [424-470] | 470 [460-470] | n/a | 0 | 0 |
|   Go host user (ms) | 150 [141-160] | 150 [141-169] | 145 [140-150] | 235 [220-240] | 390 [353-400] | 390 [380-400] | n/a | 0 | 0 |
|   Go host sys (ms) | 40 [30-40] | 40 [30-40] | 40 [31-49] | 40 [40-59] | 70 [70-89] | 75 [70-80] | n/a | 0 | 7 |
| Go host: CPU of reaped children by ready, the `--version` start checks (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-10] | 5 [0-10] | n/a | - | 200 |
| Extension host CPU to ready, user+sys (ms) | 1315 [1281-1365] | 1320 [1271-1408] | 1680 [1309-1699] | 1120 [1101-1130] | 130 [112-140] | 130 [111-130] | n/a | 0 | 0 |
|   extension host user (ms) | 1165 [1123-1216] | 1170 [1140-1230] | 1505 [1184-1530] | 945 [930-978] | 100 [81-109] | 100 [81-100] | n/a | 0 | 0 |
|   extension host sys (ms) | 150 [131-159] | 155 [130-179] | 170 [124-179] | 170 [151-199] | 30 [30-40] | 30 [30-39] | n/a | 3 | 0 |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 30 [30-39] | 30 [30-30] | n/a | - | 0 |
| Extension host CPU, ready to 0.5 s later (idle after ready) (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 20 [11-30] | 0 [0-10] | 10 [0-19] | n/a | - | 200 |
| **Turns (a prompt, one model tool call, the tool, a final answer; 20 per run)** | | | | | | | | | |
| Turn 1 wall (ms) | 41 [41-44] | 42 [41-46] | 41 [40-43] | 43 [42-45] | 36 [34-37] | 37 [34-40] | n/a | 1 | 3 |
| Turn 2 wall (ms) | 19 [17-20] | 19 [18-23] | 18 [18-21] | 18 [17-21] | 17 [15-19] | 17 [16-18] | n/a | 0 | 3 |
| Steady wall, median of turns 6-20 (ms) | 14.5 [13.5-15.4] | 14.4 [12.9-15.1] | 14.5 [14.1-15.4] | 12.9 [12.4-13.4] | 13.2 [11.9-13.9] | 13.3 [12.4-14.3] | n/a | 1 | 1 |
| Turn 1 / steady wall (ratio) | 3.01 [2.66-3.08] | 3.04 [2.74-3.27] | 2.87 [2.64-2.98] | 3.34 [3.19-3.98] | 2.70 [2.58-2.99] | 2.69 [2.46-3.13] | n/a | 1 | 0 |
| **CPU per phase (user+sys, 10 ms ticks; the first turn is measured with 0.5 s settled before and 0.2 s after)** | | | | | | | | | |
| Go host CPU, turn 1 (ms) | 30 [30-39] | 30 [20-40] | 30 [20-30] | 30 [20-39] | 30 [20-30] | 25 [20-30] | n/a | 0 | 18 |
| Extension host CPU, turn 1 (ms) | 20 [20-30] | 25 [20-30] | 30 [20-30] | 50 [30-50] | 10 [10-20] | 10 [10-20] | n/a | 22 | 0 |
| Go host CPU per turn, turns 2-20 (ms) | 11.3 [10.5-12.1] | 11.6 [10.6-12.6] | 11.8 [11.1-12.1] | 11.1 [10.6-13.1] | 11.6 [10.5-12.1] | 11.6 [10.1-12.1] | n/a | 2 | 0 |
| Extension host CPU per turn, turns 2-20 (ms) | 9.2 [8.5-9.9] | 8.9 [8.5-9.5] | 9.2 [8.9-9.9] | 7.9 [7.4-8.4] | 4.7 [4.2-4.7] | 4.7 [3.7-4.7] | n/a | 3 | 0 |
|   extension host sys per turn, turns 2-20 (ms) | 1.6 [1.1-2.1] | 1.6 [1.1-2.1] | 1.6 [1.6-2.1] | 1.6 [1.1-1.6] | 1.1 [0.5-1.1] | 1.1 [0.5-1.1] | n/a | 0 | 0 |
| **Page faults (cumulative counters of the process; extension host = its process tree)** | | | | | | | | | |
| Extension host minor faults to 0.5 s after ready | 30871 [30701-31248] | 30850 [30608-36228] | 45419 [34425-45955] | 39006 [38255-39235] | 12427 [12364-12464] | 12431 [12061-12450] | n/a | 0 | 0 |
| Extension host major faults to 0.5 s after ready | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | n/a | - | - |
| Extension host minor faults, turn 1 | 745 [436-755] | 743 [725-1426] | 186 [52-218] | 654 [594-687] | 610 [433-652] | 608 [493-648] | n/a | 0 | 0 |
| Extension host minor faults per turn, turns 2-20 | 286.2 [162.0-387.5] | 269.8 [182.0-295.8] | 375.6 [351.7-396.1] | 88.1 [82.5-93.3] | 94.8 [84.1-97.7] | 95.8 [94.3-96.9] | n/a | 6 | 1 |
| Extension host major faults, whole run to the last turn | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | n/a | - | - |
| Go host minor faults to 0.5 s after ready | 13182 [13101-13235] | 13226 [13170-13263] | 13214 [13106-13252] | 13268 [13220-13316] | 13324 [13266-13375] | 13319 [13264-13366] | n/a | 0 | 0 |
| Go host minor faults per turn, turns 2-20 | 175.3 [170.2-221.6] | 211.5 [172.0-324.3] | 183.9 [173.2-222.8] | 179.5 [168.0-232.5] | 180.6 [164.4-219.0] | 185.9 [161.8-219.7] | n/a | 19 | 3 |
| **Memory 0.5 s after ready (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | |
| Extension host RSS | 167.1 [165.8-168.9] | 166.9 [165.0-168.0] | 171.1 [170.1-172.0] | 161.7 [160.4-163.6] | 100.9 [100.0-109.6] | 100.9 [100.6-102.4] | n/a | 0 | 0 |
| Extension host PSS | 161.7 [160.6-163.2] | 161.6 [159.9-162.6] | 131.6 [125.6-148.9] | 159.5 [158.1-161.4] | 98.6 [97.7-107.3] | 98.6 [98.4-100.1] | n/a | 0 | 0 |
|   Pss_Anon | 111.4 [110.0-113.2] | 111.2 [109.4-112.3] | 116.1 [115.2-117.0] | 97.8 [96.4-99.7] | 34.2 [33.4-42.9] | 34.2 [34.0-35.7] | n/a | 0 | 0 |
|   Pss_File | 50.2 [50.0-50.6] | 50.2 [50.0-50.6] | 15.7 [9.3-29.2] | 61.7 [61.6-61.8] | 64.4 [64.3-64.4] | 64.5 [64.4-64.5] | n/a | 0 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | n/a | - | - |
|   Private_Dirty | 111.4 [110.0-113.2] | 111.2 [109.4-112.3] | 116.1 [115.2-117.0] | 97.8 [96.4-99.7] | 34.2 [33.4-42.9] | 34.2 [34.0-35.7] | n/a | 0 | 0 |
|   Private_Clean | 47.8 [47.8-47.8] | 47.8 [47.8-47.8] | 1.8 [0.0-6.9] | 61.6 [61.6-61.6] | 64.2 [64.2-64.2] | 64.3 [64.2-64.3] | n/a | 0 | 0 |
|   Shared_Clean+Shared_Dirty | 7.9 [7.8-8.0] | 7.9 [7.8-7.9] | 53.3 [47.9-54.9] | 2.3 [2.3-2.4] | 2.5 [2.4-2.6] | 2.5 [2.4-2.6] | n/a | 0 | 0 |
| Go host RSS | 87.6 [87.3-87.8] | 87.8 [87.6-88.1] | 87.8 [87.6-88.3] | 87.9 [87.6-88.4] | 88.3 [88.0-88.5] | 88.1 [87.7-88.4] | n/a | 0 | 0 |
| Go host PSS | 86.1 [85.8-86.3] | 86.3 [86.0-86.6] | 86.3 [86.0-86.7] | 86.4 [86.1-86.9] | 86.8 [86.4-86.9] | 86.6 [86.2-86.9] | n/a | 0 | 0 |
| **Memory after turn 1 (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | |
| Extension host RSS | 170.1 [167.5-171.8] | 169.8 [167.9-171.1] | 171.3 [170.3-172.1] | 160.1 [159.4-161.6] | 101.1 [100.8-106.8] | 101.7 [100.7-104.4] | n/a | 0 | 1 |
| Extension host PSS | 164.8 [161.9-166.2] | 164.6 [162.5-165.6] | 131.0 [126.0-142.2] | 157.9 [157.2-159.4] | 98.8 [98.6-104.4] | 99.4 [98.5-102.1] | n/a | 0 | 1 |
|   Pss_Anon | 114.3 [111.6-116.1] | 114.1 [112.2-115.3] | 116.2 [115.4-117.2] | 96.2 [95.4-97.8] | 34.1 [33.8-39.7] | 34.6 [33.8-37.5] | n/a | 0 | 2 |
|   Pss_File | 50.3 [50.1-50.6] | 50.2 [50.1-50.7] | 15.3 [9.4-26.9] | 61.7 [61.7-61.8] | 64.7 [64.6-64.8] | 64.8 [64.6-64.8] | n/a | 0 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | n/a | - | - |
|   Private_Dirty | 114.3 [111.6-116.1] | 114.1 [112.2-115.3] | 116.2 [115.4-117.2] | 96.2 [95.4-97.8] | 34.1 [33.8-39.7] | 34.6 [33.8-37.5] | n/a | 0 | 2 |
|   Private_Clean | 47.8 [47.8-47.9] | 47.8 [47.8-47.9] | 1.4 [0.1-3.7] | 61.6 [61.6-61.6] | 64.6 [64.5-64.6] | 64.6 [64.5-64.6] | n/a | 0 | 0 |
|   Shared_Clean+Shared_Dirty | 7.9 [7.8-8.0] | 7.9 [7.8-7.9] | 53.7 [51.2-54.8] | 2.3 [2.3-2.4] | 2.5 [2.4-2.6] | 2.5 [2.4-2.6] | n/a | 0 | 0 |
| Go host RSS | 96.1 [95.8-96.4] | 96.3 [96.1-96.7] | 96.3 [96.1-96.7] | 96.5 [96.2-96.9] | 96.8 [96.6-97.0] | 96.7 [96.2-97.0] | n/a | 0 | 0 |
| Go host PSS | 94.6 [94.3-94.9] | 94.8 [94.6-95.2] | 94.8 [94.5-95.2] | 95.0 [94.7-95.4] | 95.2 [95.1-95.4] | 95.2 [94.7-95.4] | n/a | 0 | 0 |
| **Memory after 20 turns (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | |
| Extension host RSS | 182.0 [179.8-182.9] | 182.3 [179.0-184.4] | 194.6 [193.2-197.4] | 164.9 [162.0-165.3] | 107.9 [107.0-112.3] | 107.4 [107.0-110.7] | n/a | 0 | 0 |
| Extension host PSS | 176.3 [174.5-177.6] | 177.0 [173.9-178.9] | 154.8 [145.7-169.1] | 162.6 [159.8-163.1] | 105.6 [104.7-109.9] | 105.0 [104.8-108.3] | n/a | 0 | 1 |
|   Pss_Anon | 126.3 [124.1-127.1] | 126.5 [123.3-128.7] | 139.6 [138.3-142.6] | 100.9 [98.2-101.4] | 40.8 [39.9-45.3] | 40.3 [40.0-43.6] | n/a | 0 | 1 |
|   Pss_File | 50.2 [50.0-50.6] | 50.3 [50.1-51.4] | 14.4 [7.0-26.6] | 61.7 [61.7-61.8] | 64.7 [64.7-64.8] | 64.8 [64.7-64.8] | n/a | 0 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | n/a | - | - |
|   Private_Dirty | 126.3 [124.1-127.1] | 126.5 [123.3-128.7] | 139.6 [138.3-142.6] | 100.9 [98.2-101.4] | 40.8 [39.9-45.3] | 40.3 [40.0-43.6] | n/a | 0 | 1 |
|   Private_Clean | 47.8 [47.8-47.9] | 47.8 [47.8-49.4] | 0.9 [0.1-3.4] | 61.6 [61.6-61.6] | 64.6 [64.5-64.6] | 64.6 [64.6-64.6] | n/a | 0 | 0 |
|   Shared_Clean+Shared_Dirty | 7.9 [7.8-8.0] | 7.8 [6.3-7.9] | 54.1 [51.5-54.8] | 2.3 [2.3-2.4] | 2.5 [2.4-2.6] | 2.5 [2.4-2.6] | n/a | 1 | 0 |
| Go host RSS | 108.9 [107.6-112.1] | 111.5 [109.2-119.1] | 109.4 [108.7-112.6] | 109.5 [108.6-113.7] | 109.7 [107.9-112.3] | 110.1 [108.6-112.5] | n/a | 2 | 0 |
| Go host PSS | 107.4 [106.1-110.7] | 109.9 [107.6-117.5] | 107.9 [107.1-111.0] | 108.0 [107.1-112.2] | 108.2 [106.3-110.7] | 108.6 [107.1-111.0] | n/a | 2 | 0 |
| **Process** | | | | | | | | | |
| Extension host threads after the last turn | 12 [12-12] | 12 [12-12] | 12 [12-12] | 19 [18-19] | 7 [7-7] | 7 [7-7] | n/a | 0 | 0 |
| Extension host executable size (MB) | 150.3 [150.3-150.3] | 150.3 [150.3-150.3] | 126.9 [126.9-126.9] | 79.5 [79.5-79.5] | 245.1 [245.1-245.1] | 245.1 [245.1-245.1] | n/a | 0 | 0 |

`aot074`: 1 failed run(s); first error: `arm aot074 ran ['bun'], not pig-ts-host`

**/words hot loop (the word-count extension, 16.8 MB file, once per run after the turns)**

| Metric | node | node2 | node24 | bun | aot | aot2 | aot074 | A/A node-node2 (%) | A/A aot-aot2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 0/0 | | |
| /words in the extension (ms) | 145 [137-146] | 145 [144-149] | 140 [138-150] | 103 [103-105] | 120 [120-123] | 120 [97-123] | n/a | 0 | 0 |
| /words wall from prompt (ms) | 180 [175-185] | 179 [174-185] | 173 [171-184] | 126 [123-131] | 139 [138-144] | 140 [115-142] | n/a | 0 | 1 |

Allocation, strace pass (3 rounds; the same run under `strace -f --seccomp-bpf -e trace=memory,process`; counts per process class, `ext` = every process the Go host started: the `--version` start checks and the extension host). Cells median [min-max].

Processes seen by strace (executable: count of processes, first run per arm): aot: {'pig': 1, 'pig-ts-host': 2, 'node': 1, 'git': 1}; bun: {'pig': 1, 'bun': 2, 'git': 1}; node: {'pig': 1, 'node': 2, 'git': 1}; node24: {'pig': 1, 'node': 2, 'git': 1}

| Metric | node | node24 | bun | aot |
|---|---:|---:|---:|---:|
| Runs ok / total | 3/3 | 3/3 | 3/3 | 3/3 |
| **strace: memory syscalls, Extension host (and its start-check children), launch to ready** | | | | |
| mmap | 454 [452-455] | 440 [435-440] | 522 [521-523] | 191 [191-191] |
| munmap | 342 [338-342] | 371 [363-371] | 8 [6-9] | 85 [85-85] |
| brk | 50 [50-51] | 67 [66-83] | 6 [6-6] | 24 [24-24] |
| mprotect | 628 [608-671] | 708 [689-877] | 50 [49-50] | 75 [74-75] |
| madvise | 325 [322-325] | 351 [346-353] | 1271 [1104-1815] | 182 [166-183] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 2685.8 [2621.2-2688.4] | 2312.6 [2310.1-2313.8] | 11751.5 [11687.0-11751.5] | 36899.6 [36835.6-36899.6] |
| **strace: memory syscalls, Extension host (and its start-check children), turn 1** | | | | |
| mmap | 3 [3-3] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| munmap | 2 [2-3] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| brk | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| mprotect | 36 [35-37] | 0 [0-0] | 0 [0-0] | 1 [1-1] |
| madvise | 2 [2-2] | 0 [0-0] | 21 [21-25] | 0 [0-2] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 65.0 [65.0-129.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| **strace: memory syscalls, Extension host (and its start-check children), per turn, turns 2-20** | | | | |
| mmap | 0.3 [0.2-0.3] | 0.1 [0.1-0.2] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| munmap | 0.2 [0.2-0.2] | 0.1 [0.1-0.2] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| brk | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| mprotect | 11.2 [9.8-11.4] | 21.9 [18.2-21.9] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| madvise | 0.2 [0.1-0.2] | 0.1 [0.1-0.3] | 6.6 [6.5-15.2] | 2.2 [0.5-2.7] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 10.2 [10.2-10.2] | 0.0 [0.0-0.1] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| **strace: memory syscalls, Go host, launch to ready** | | | | |
| mmap | 81 [81-82] | 81 [79-83] | 82 [79-82] | 82 [76-82] |
| munmap | 9 [9-10] | 9 [8-11] | 9 [9-10] | 9 [8-9] |
| brk | 3 [3-3] | 3 [3-3] | 3 [3-3] | 3 [3-3] |
| mprotect | 22 [22-24] | 22 [22-24] | 22 [22-22] | 22 [20-22] |
| madvise | 2 [2-2] | 2 [2-2] | 2 [2-2] | 2 [2-2] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 2416.2 [2416.2-2488.4] | 2416.2 [2416.1-2552.2] | 2416.4 [2352.1-2420.2] | 2416.4 [2279.9-2420.2] |
| **strace: memory syscalls, Go host, turn 1** | | | | |
| mmap | 3 [3-3] | 2 [2-5] | 3 [2-5] | 2 [2-3] |
| munmap | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| brk | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| mprotect | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| madvise | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 8.2 [8.2-8.2] | 4.2 [4.2-72.3] | 8.2 [4.2-72.3] | 4.2 [4.2-8.2] |
| **strace: memory syscalls, Go host, per turn, turns 2-20** | | | | |
| mmap | 0.5 [0.4-0.7] | 0.4 [0.4-0.4] | 0.4 [0.3-0.6] | 0.4 [0.4-0.5] |
| munmap | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| brk | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| mprotect | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| madvise | 0.0 [0.0-0.0] | 0.9 [0.1-1.1] | 0.9 [0.4-0.9] | 0.0 [0.0-0.6] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 4.0 [0.7-4.7] | 0.7 [0.7-0.9] | 0.7 [0.5-4.5] | 0.7 [0.7-4.0] |

Allocation, heap pass (3 rounds; the set plus `basket-probe.ts`, which reads the runtime's own heap numbers). Cells median [min-max].

| Metric | node | node24 | bun | aot |
|---|---:|---:|---:|---:|
| Runs ok / total | 3/3 | 3/3 | 3/3 | 3/3 |
| **JS heap allocation (probe pass; Node: v8 total_allocated_bytes, Bun and aot: JSC GC log; a forced GC at each probe point)** | | | | |
| Allocated, launch to ready (MB) | 354.3 [352.1-356.1] | n/a | 126.8 [124.8-132.4] | 32.7 [31.6-33.0] |
| Allocated, turn 1 incl. the probe (MB) | 3.3 [3.2-3.7] | n/a | 11.1 [10.8-32.7] | 4.7 [4.7-4.7] |
| Allocated per turn, turns 2-20 (MB) | 0.58 [0.56-0.59] | n/a | 1.05 [1.03-1.08] | 0.32 [0.32-0.32] |
| GC cycles over the run (JSC log; Node: PerformanceObserver gc entries) | 3 [3-3] | 2 [2-3] | 16 [16-16] | 5 [5-5] |
| **JS heap live after a forced GC, at ready** | | | | |
| process.memoryUsage().heapUsed (MB) | 38.0 [38.0-38.0] | 38.2 [38.2-38.2] | 24.7 [24.7-24.8] | 9.6 [9.6-9.6] |
| process.memoryUsage().external + arrayBuffers (MB) | 3.9 [3.9-3.9] | 3.7 [3.7-3.7] | 10.5 [10.5-10.5] | 1.2 [1.2-1.2] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | n/a | 236615 [236587-236622] | 158372 [158320-158376] |
| JSC heapSize (MB; Node: n/a) | n/a | n/a | 24.8 [24.7-24.8] | 9.6 [9.6-9.6] |
| **JS heap live after a forced GC, after turn 1** | | | | |
| process.memoryUsage().heapUsed (MB) | 38.3 [38.3-38.3] | 38.6 [38.6-38.6] | 25.4 [25.4-25.4] | 10.1 [10.1-10.1] |
| process.memoryUsage().external + arrayBuffers (MB) | 3.9 [3.9-3.9] | 3.7 [3.7-3.7] | 11.1 [11.1-11.1] | 1.6 [1.6-1.6] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | n/a | 238620 [238603-238640] | 159847 [159839-159853] |
| JSC heapSize (MB; Node: n/a) | n/a | n/a | 25.4 [25.4-25.4] | 10.1 [10.1-10.1] |
| **JS heap live after a forced GC, after 20 turns** | | | | |
| process.memoryUsage().heapUsed (MB) | 38.8 [38.8-38.8] | 39.1 [39.1-39.1] | 26.1 [26.1-26.1] | 10.3 [10.3-10.3] |
| process.memoryUsage().external + arrayBuffers (MB) | 4.1 [4.1-4.1] | 3.9 [3.9-3.9] | 11.5 [11.5-11.5] | 1.6 [1.6-1.6] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | n/a | 242957 [242879-243036] | 164087 [164026-164091] |
| JSC heapSize (MB; Node: n/a) | n/a | n/a | 26.1 [26.1-26.1] | 10.3 [10.3-10.3] |

`node24`: allocation n/a: v24.22.1's v8.getHeapStatistics() has no total_allocated_bytes field, and no GC log is read for Node (in a trial a `--trace-gc` shim put no GC lines on the host's stderr; not investigated further).

## Set `ten`

Timing pass: 12 interleaved rounds (order rotated each round, one recorded warm-up run per arm before them), cells are median [p10-p90] over rounds (fewer than 7 runs: median [min-max]), A/A columns are |median(a)-median(b)| / their mean.

Proof each arm ran its tier: every run's extension-host `/proc/<pid>/exe` is checked (node: a node binary; bun: the slot's bun; aot: `<slot>/aot/images/<key>/pig-ts-host`) and a run that differs is an error, not a number. Executables seen: node: /opt/node/26.12.1/bin/node; node2: /opt/node/26.12.1/bin/node; node24: /opt/node/24.22.1/bin/node; bun: /home/kinsy/pigaot-scratch/basket/slot-pin/bun/bun; aot: /home/kinsy/pigaot-scratch/basket/slot-pin/aot/images/a7cd851ad062fb1d-1794009310918408967/pig-ts-host; aot2: /home/kinsy/pigaot-scratch/basket/slot-pin/aot/images/a7cd851ad062fb1d-1794009310918408967/pig-ts-host; aot074: 

| Metric | node | node2 | node24 | bun | aot | aot2 | aot074 | A/A node-node2 (%) | A/A aot-aot2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 0/1 | | |
| **Startup (launch to ready: get_commands answered with the set's commands)** | | | | | | | | | |
| Launch to ready (ms) | 431 [404-436] | 432 [384-449] | 465 [414-471] | 500 [461-507] | 615 [606-628] | 615 [598-622] | n/a | 0 | 0 |
| Go host CPU to ready, user+sys (ms) | 200 [190-200] | 200 [190-200] | 200 [181-200] | 280 [262-290] | 480 [471-499] | 480 [470-489] | n/a | 0 | 0 |
|   Go host user (ms) | 155 [150-160] | 160 [150-160] | 155 [150-160] | 230 [211-230] | 400 [370-420] | 390 [372-400] | n/a | 3 | 3 |
|   Go host sys (ms) | 40 [31-49] | 40 [40-40] | 40 [30-50] | 60 [41-69] | 80 [71-109] | 90 [80-90] | n/a | 0 | 12 |
| Go host: CPU of reaped children by ready, the `--version` start checks (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-10] | 0 [0-10] | n/a | - | - |
| Extension host CPU to ready, user+sys (ms) | 360 [340-370] | 355 [304-370] | 420 [373-439] | 340 [302-350] | 110 [100-110] | 100 [91-110] | n/a | 1 | 10 |
|   extension host user (ms) | 300 [290-310] | 300 [262-310] | 360 [315-370] | 270 [250-290] | 70 [61-70] | 70 [60-80] | n/a | 0 | 0 |
|   extension host sys (ms) | 55 [50-60] | 60 [50-60] | 60 [50-69] | 60 [51-79] | 40 [30-40] | 30 [30-40] | n/a | 9 | 29 |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 30 [21-39] | 30 [30-40] | n/a | - | 0 |
| Extension host CPU, ready to 0.5 s later (idle after ready) (ms) | 0 [0-20] | 0 [0-10] | 0 [0-0] | 5 [0-19] | 0 [0-0] | 0 [0-9] | n/a | - | - |
| **Turns (a prompt, one model tool call, the tool, a final answer; 20 per run)** | | | | | | | | | |
| Turn 1 wall (ms) | 46 [45-47] | 47 [45-53] | 48 [45-50] | 43 [40-47] | 39 [38-42] | 38 [38-41] | n/a | 4 | 2 |
| Turn 2 wall (ms) | 36 [35-39] | 37 [33-44] | 38 [37-42] | 30 [28-35] | 30 [29-31] | 30 [27-33] | n/a | 2 | 3 |
| Steady wall, median of turns 6-20 (ms) | 39.2 [37.9-40.6] | 39.2 [37.2-39.8] | 40.8 [39.6-43.2] | 34.0 [31.9-37.3] | 33.9 [31.9-35.9] | 33.3 [31.4-34.3] | n/a | 0 | 2 |
| Turn 1 / steady wall (ratio) | 1.16 [1.12-1.22] | 1.23 [1.14-1.44] | 1.18 [1.06-1.21] | 1.29 [1.11-1.37] | 1.16 [1.09-1.23] | 1.16 [1.11-1.28] | n/a | 6 | 0 |
| **CPU per phase (user+sys, 10 ms ticks; the first turn is measured with 0.5 s settled before and 0.2 s after)** | | | | | | | | | |
| Go host CPU, turn 1 (ms) | 30 [20-30] | 25 [20-30] | 30 [20-30] | 25 [20-30] | 30 [20-30] | 25 [20-30] | n/a | 18 | 18 |
| Extension host CPU, turn 1 (ms) | 45 [40-50] | 50 [40-50] | 50 [40-50] | 70 [60-88] | 30 [20-30] | 20 [20-30] | n/a | 11 | 40 |
| Go host CPU per turn, turns 2-20 (ms) | 17.9 [17.4-18.9] | 18.2 [15.8-19.5] | 18.2 [17.4-19.4] | 18.4 [15.9-20.4] | 18.2 [16.4-18.9] | 18.2 [17.4-18.9] | n/a | 1 | 0 |
| Extension host CPU per turn, turns 2-20 (ms) | 40.3 [39.0-44.0] | 39.7 [38.4-42.4] | 43.9 [40.3-48.7] | 35.5 [33.7-37.9] | 25.3 [24.7-28.8] | 25.0 [23.2-25.7] | n/a | 1 | 1 |
|   extension host sys per turn, turns 2-20 (ms) | 6.1 [5.3-6.8] | 5.8 [5.3-6.3] | 6.8 [5.8-7.3] | 8.9 [7.4-10.0] | 5.8 [5.3-6.3] | 5.5 [5.3-6.3] | n/a | 4 | 5 |
| **Page faults (cumulative counters of the process; extension host = its process tree)** | | | | | | | | | |
| Extension host minor faults to 0.5 s after ready | 16589 [15744-16950] | 16704 [16150-17073] | 17818 [17348-18125] | 16610 [16460-16680] | 12619 [12581-13041] | 12781 [12581-13318] | n/a | 1 | 1 |
| Extension host major faults to 0.5 s after ready | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | n/a | - | - |
| Extension host minor faults, turn 1 | 2035 [1979-2120] | 2027 [1905-2080] | 2067 [1850-2114] | 1544 [1334-2086] | 1826 [1773-1855] | 1800 [1412-1857] | n/a | 0 | 1 |
| Extension host minor faults per turn, turns 2-20 | 1029.5 [912.1-1087.8] | 989.8 [938.8-1087.9] | 1335.6 [1147.7-1384.5] | 1458.6 [1356.4-1512.5] | 1080.0 [1041.6-1136.0] | 1086.8 [1051.6-1190.4] | n/a | 4 | 1 |
| Extension host major faults, whole run to the last turn | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | n/a | - | - |
| Go host minor faults to 0.5 s after ready | 13323 [13210-13372] | 13244 [13189-13338] | 13247 [13199-13285] | 13300 [13257-13338] | 13331 [13286-13397] | 13309 [13278-13420] | n/a | 1 | 0 |
| Go host minor faults per turn, turns 2-20 | 285.9 [247.0-385.9] | 258.4 [244.2-388.7] | 256.9 [233.3-381.6] | 252.3 [240.8-261.1] | 257.7 [239.4-385.7] | 249.7 [242.8-370.0] | n/a | 10 | 3 |
| **Memory 0.5 s after ready (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | |
| Extension host RSS | 111.0 [107.2-112.4] | 111.2 [108.9-112.5] | 113.7 [112.1-115.1] | 97.3 [96.1-97.6] | 96.4 [96.3-98.0] | 97.1 [96.3-99.2] | n/a | 0 | 1 |
| Extension host PSS | 106.3 [102.2-107.1] | 106.1 [103.7-107.5] | 74.9 [70.0-106.7] | 95.1 [93.9-95.5] | 94.1 [94.0-95.8] | 94.8 [94.0-96.8] | n/a | 0 | 1 |
|   Pss_Anon | 58.5 [54.7-59.9] | 58.8 [56.4-60.0] | 61.0 [59.5-62.4] | 57.5 [56.3-57.8] | 45.1 [45.0-46.8] | 45.7 [45.0-47.9] | n/a | 0 | 1 |
|   Pss_File | 47.3 [47.1-47.6] | 47.2 [47.0-47.6] | 12.6 [8.7-46.0] | 37.6 [37.6-37.7] | 49.0 [49.0-49.0] | 49.0 [49.0-49.0] | n/a | 0 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | n/a | - | - |
|   Private_Dirty | 58.5 [54.7-59.9] | 58.8 [56.4-60.0] | 61.0 [59.5-62.4] | 57.5 [56.3-57.8] | 45.1 [45.0-46.8] | 45.7 [45.0-47.9] | n/a | 0 | 1 |
|   Private_Clean | 44.8 [44.8-44.8] | 44.8 [44.8-44.8] | 0.2 [0.0-41.4] | 37.5 [37.5-37.5] | 48.8 [48.8-48.8] | 48.8 [48.8-48.8] | n/a | 0 | 0 |
|   Shared_Clean+Shared_Dirty | 7.7 [7.7-7.7] | 7.7 [7.6-7.8] | 52.5 [11.4-52.6] | 2.3 [2.2-2.4] | 2.5 [2.5-2.6] | 2.5 [2.4-2.6] | n/a | 0 | 0 |
| Go host RSS | 87.7 [87.5-87.8] | 87.5 [87.3-87.9] | 87.5 [87.3-87.6] | 87.9 [87.7-88.2] | 88.0 [87.7-88.4] | 88.1 [87.8-88.2] | n/a | 0 | 0 |
| Go host PSS | 86.1 [86.0-86.3] | 86.0 [85.7-86.4] | 85.9 [85.8-86.1] | 86.4 [86.2-86.6] | 86.5 [86.1-86.8] | 86.6 [86.3-86.7] | n/a | 0 | 0 |
| **Memory after turn 1 (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | |
| Extension host RSS | 118.8 [112.8-120.2] | 119.4 [116.2-120.4] | 121.8 [120.2-123.2] | 96.9 [93.0-99.2] | 103.8 [103.6-105.5] | 103.8 [96.6-105.4] | n/a | 1 | 0 |
| Extension host PSS | 113.6 [107.4-115.0] | 114.0 [110.9-115.2] | 85.7 [78.8-95.2] | 94.7 [90.9-96.9] | 101.5 [101.3-103.2] | 101.5 [94.2-103.0] | n/a | 0 | 0 |
|   Pss_Anon | 66.2 [60.1-67.6] | 66.8 [63.6-67.8] | 69.2 [67.6-70.5] | 57.0 [53.2-59.2] | 52.2 [52.0-53.9] | 52.2 [45.0-53.7] | n/a | 1 | 0 |
|   Pss_File | 47.4 [47.1-47.7] | 47.3 [47.1-47.7] | 16.3 [8.0-25.4] | 37.7 [37.7-37.8] | 49.3 [49.3-49.4] | 49.3 [49.3-49.4] | n/a | 0 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | n/a | - | - |
|   Private_Dirty | 66.2 [60.1-67.6] | 66.8 [63.6-67.8] | 69.2 [67.6-70.5] | 57.0 [53.2-59.2] | 52.2 [52.0-53.9] | 52.2 [45.0-53.7] | n/a | 1 | 0 |
|   Private_Clean | 44.9 [44.9-45.0] | 44.9 [44.9-45.0] | 0.1 [0.0-1.6] | 37.6 [37.6-37.6] | 49.1 [49.1-49.1] | 49.1 [49.1-49.1] | n/a | 0 | 0 |
|   Shared_Clean+Shared_Dirty | 7.7 [7.7-7.7] | 7.7 [7.6-7.8] | 52.5 [51.2-52.6] | 2.3 [2.2-2.4] | 2.5 [2.5-2.6] | 2.5 [2.4-2.6] | n/a | 0 | 0 |
| Go host RSS | 94.4 [94.3-94.6] | 94.1 [94.0-94.5] | 94.2 [94.0-94.4] | 94.4 [94.3-95.0] | 94.7 [94.3-94.9] | 94.7 [94.5-94.8] | n/a | 0 | 0 |
| Go host PSS | 92.8 [92.7-93.1] | 92.6 [92.5-93.0] | 92.6 [92.5-92.9] | 92.9 [92.7-93.4] | 93.1 [92.7-93.4] | 93.2 [92.9-93.4] | n/a | 0 | 0 |
| **Memory after 20 turns (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | |
| Extension host RSS | 196.0 [180.8-197.3] | 191.6 [187.8-197.0] | 219.3 [204.9-220.0] | 151.2 [144.8-157.8] | 135.0 [117.3-142.8] | 136.5 [124.5-141.1] | n/a | 2 | 1 |
| Extension host PSS | 190.6 [175.5-192.6] | 186.4 [182.6-191.7] | 177.9 [170.6-191.7] | 149.0 [142.6-155.7] | 132.7 [114.9-140.4] | 134.2 [122.3-138.8] | n/a | 2 | 1 |
|   Pss_Anon | 143.3 [128.1-144.6] | 139.0 [135.1-144.3] | 166.6 [152.3-167.3] | 108.3 [102.1-115.1] | 83.4 [65.5-91.0] | 84.8 [72.8-89.4] | n/a | 3 | 2 |
|   Pss_File | 47.5 [47.3-47.8] | 47.5 [47.3-47.8] | 16.3 [7.1-24.5] | 40.6 [40.6-40.7] | 49.4 [49.3-49.4] | 49.4 [49.3-49.5] | n/a | 0 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | n/a | - | - |
|   Private_Dirty | 143.3 [128.1-144.6] | 139.0 [135.1-144.3] | 166.6 [152.3-167.3] | 108.3 [102.1-115.1] | 83.4 [65.5-91.0] | 84.8 [72.8-89.4] | n/a | 3 | 2 |
|   Private_Clean | 45.0 [45.0-45.0] | 45.0 [45.0-45.0] | 0.1 [0.0-0.3] | 40.5 [40.5-40.5] | 49.1 [49.1-49.2] | 49.1 [49.1-49.3] | n/a | 0 | 0 |
|   Shared_Clean+Shared_Dirty | 7.7 [7.6-7.7] | 7.7 [7.6-7.8] | 52.5 [52.3-52.6] | 2.3 [2.3-2.4] | 2.5 [2.5-2.6] | 2.5 [2.4-2.6] | n/a | 0 | 0 |
| Go host RSS | 111.7 [109.5-117.1] | 110.9 [109.7-116.8] | 110.7 [110.0-116.4] | 111.1 [110.3-112.6] | 112.2 [111.3-116.3] | 112.6 [109.4-116.5] | n/a | 1 | 0 |
| Go host PSS | 110.1 [108.0-115.6] | 109.4 [108.2-115.3] | 109.2 [108.5-114.9] | 109.6 [108.7-111.1] | 110.6 [109.8-114.7] | 111.1 [107.9-115.0] | n/a | 1 | 0 |
| **Process** | | | | | | | | | |
| Extension host threads after the last turn | 12 [12-12] | 12 [12-12] | 12 [12-12] | 18 [18-19] | 7 [7-7] | 7 [7-7] | n/a | 0 | 0 |
| Extension host executable size (MB) | 150.3 [150.3-150.3] | 150.3 [150.3-150.3] | 126.9 [126.9-126.9] | 79.5 [79.5-79.5] | 240.1 [240.1-240.1] | 240.1 [240.1-240.1] | n/a | 0 | 0 |

`aot074`: 1 failed run(s); first error: `arm aot074 ran ['bun'], not pig-ts-host`

Allocation, strace pass (3 rounds; the same run under `strace -f --seccomp-bpf -e trace=memory,process`; counts per process class, `ext` = every process the Go host started: the `--version` start checks and the extension host). Cells median [min-max].

Processes seen by strace (executable: count of processes, first run per arm): aot: {'pig': 1, 'pig-ts-host': 2, 'node': 1}; bun: {'pig': 1, 'bun': 2}; node: {'pig': 1, 'node': 2}; node24: {'pig': 1, 'node': 2}

| Metric | node | node24 | bun | aot |
|---|---:|---:|---:|---:|
| Runs ok / total | 3/3 | 3/3 | 3/3 | 3/3 |
| **strace: memory syscalls, Extension host (and its start-check children), launch to ready** | | | | |
| mmap | 278 [276-279] | 277 [277-292] | 77 [77-78] | 190 [189-190] |
| munmap | 163 [160-165] | 205 [204-222] | 8 [8-8] | 85 [85-87] |
| brk | 40 [40-40] | 41 [41-42] | 6 [6-6] | 24 [24-24] |
| mprotect | 382 [322-406] | 434 [415-451] | 47 [46-47] | 72 [72-73] |
| madvise | 151 [148-151] | 190 [188-203] | 416 [410-421] | 151 [151-153] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 2587.8 [2523.7-2589.2] | 2221.2 [2221.2-2228.6] | 11751.5 [11751.0-11751.5] | 36835.6 [36835.6-36899.6] |
| **strace: memory syscalls, Extension host (and its start-check children), turn 1** | | | | |
| mmap | 3 [2-7] | 1 [0-1] | 0 [0-0] | 0 [0-0] |
| munmap | 3 [2-7] | 1 [0-1] | 0 [0-0] | 0 [0-0] |
| brk | 0 [0-0] | 0 [0-3] | 0 [0-0] | 0 [0-0] |
| mprotect | 3 [2-161] | 145 [144-154] | 0 [0-0] | 0 [0-0] |
| madvise | 3 [2-7] | 1 [0-1] | 30 [27-31] | 0 [0-0] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 1.5 [1.0-3.5] | 0.5 [0.0-0.5] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| **strace: memory syscalls, Extension host (and its start-check children), per turn, turns 2-20** | | | | |
| mmap | 16.2 [15.4-16.2] | 27.4 [26.9-27.4] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| munmap | 16.2 [15.5-16.4] | 27.4 [26.9-27.7] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| brk | 0.0 [0.0-0.0] | 0.0 [0.0-0.4] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| mprotect | 7.0 [5.6-7.1] | 16.7 [15.5-16.9] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| madvise | 16.2 [15.4-16.2] | 27.4 [27.0-27.5] | 23.6 [21.6-25.7] | 5.7 [4.0-13.3] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 7.9 [7.4-7.9] | 13.4 [13.2-13.4] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| **strace: memory syscalls, Go host, launch to ready** | | | | |
| mmap | 82 [81-84] | 81 [79-81] | 81 [79-83] | 81 [81-85] |
| munmap | 9 [9-10] | 9 [8-9] | 9 [9-11] | 9 [8-9] |
| brk | 3 [3-3] | 3 [3-3] | 3 [3-3] | 3 [3-3] |
| mprotect | 22 [22-24] | 22 [22-22] | 22 [22-24] | 22 [22-24] |
| madvise | 2 [2-2] | 2 [2-2] | 2 [2-2] | 2 [2-2] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 2416.4 [2416.2-2552.4] | 2416.2 [2352.1-2416.2] | 2416.2 [2352.1-2488.1] | 2416.2 [2416.2-2492.4] |
| **strace: memory syscalls, Go host, turn 1** | | | | |
| mmap | 2 [2-2] | 2 [2-4] | 4 [2-4] | 2 [2-2] |
| munmap | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| brk | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| mprotect | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| madvise | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 4.2 [4.2-4.2] | 4.2 [4.2-68.3] | 68.3 [4.2-68.3] | 4.2 [4.2-4.2] |
| **strace: memory syscalls, Go host, per turn, turns 2-20** | | | | |
| mmap | 0.4 [0.4-0.6] | 0.5 [0.5-0.6] | 0.4 [0.4-0.5] | 0.5 [0.4-0.6] |
| munmap | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| brk | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| mprotect | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| madvise | 5.3 [1.2-6.9] | 1.1 [0.8-1.4] | 3.0 [0.9-4.5] | 3.7 [0.6-4.9] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 0.9 [0.7-1.3] | 1.1 [1.1-1.3] | 0.9 [0.9-1.1] | 1.1 [0.9-1.3] |

Allocation, heap pass (3 rounds; the set plus `basket-probe.ts`, which reads the runtime's own heap numbers). Cells median [min-max].

| Metric | node | node24 | bun | aot |
|---|---:|---:|---:|---:|
| Runs ok / total | 3/3 | 3/3 | 3/3 | 3/3 |
| **JS heap allocation (probe pass; Node: v8 total_allocated_bytes, Bun and aot: JSC GC log; a forced GC at each probe point)** | | | | |
| Allocated, launch to ready (MB) | 94.5 [90.2-95.8] | n/a | 74.8 [44.7-74.9] | 39.2 [39.2-39.2] |
| Allocated, turn 1 incl. the probe (MB) | 5.9 [5.4-6.7] | n/a | 23.7 [11.6-24.1] | 3.8 [3.8-3.8] |
| Allocated per turn, turns 2-20 (MB) | 6.86 [6.75-6.94] | n/a | 14.04 [10.35-15.27] | 9.40 [9.06-9.87] |
| GC cycles over the run (JSC log; Node: PerformanceObserver gc entries) | 18 [18-18] | 20 [20-20] | 28 [25-29] | 25 [24-25] |
| **JS heap live after a forced GC, at ready** | | | | |
| process.memoryUsage().heapUsed (MB) | 14.6 [14.5-14.6] | 14.6 [14.6-14.6] | 9.3 [9.3-9.3] | 6.1 [6.1-6.1] |
| process.memoryUsage().external + arrayBuffers (MB) | 3.0 [3.0-3.0] | 2.9 [2.9-2.9] | 3.2 [3.2-3.2] | 0.9 [0.9-0.9] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | n/a | 113775 [113774-113846] | 105800 [105800-105800] |
| JSC heapSize (MB; Node: n/a) | n/a | n/a | 9.3 [9.3-9.3] | 6.1 [6.1-6.1] |
| **JS heap live after a forced GC, after turn 1** | | | | |
| process.memoryUsage().heapUsed (MB) | 14.9 [14.8-14.9] | 14.9 [14.9-14.9] | 9.9 [9.9-9.9] | 6.3 [6.3-6.3] |
| process.memoryUsage().external + arrayBuffers (MB) | 3.1 [3.1-3.1] | 3.0 [3.0-3.0] | 3.7 [3.7-3.7] | 0.9 [0.9-0.9] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | n/a | 116189 [116177-116223] | 108380 [108365-108386] |
| JSC heapSize (MB; Node: n/a) | n/a | n/a | 9.9 [9.9-9.9] | 6.3 [6.3-6.3] |
| **JS heap live after a forced GC, after 20 turns** | | | | |
| process.memoryUsage().heapUsed (MB) | 15.5 [15.4-15.5] | 15.5 [15.5-15.5] | 10.6 [10.6-10.6] | 6.6 [6.6-6.6] |
| process.memoryUsage().external + arrayBuffers (MB) | 3.1 [3.1-3.1] | 2.9 [2.9-2.9] | 4.1 [4.1-4.1] | 1.0 [1.0-1.0] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | n/a | 125992 [125974-126230] | 117203 [117142-117453] |
| JSC heapSize (MB; Node: n/a) | n/a | n/a | 10.6 [10.6-10.6] | 6.6 [6.6-6.6] |

`node24`: allocation n/a: v24.22.1's v8.getHeapStatistics() has no total_allocated_bytes field, and no GC log is read for Node (in a trial a `--trace-gc` shim put no GC lines on the host's stderr; not investigated further).
