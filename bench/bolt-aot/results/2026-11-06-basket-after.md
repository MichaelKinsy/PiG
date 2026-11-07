# Perf basket, AFTER the 0.7.1 pig-bolt changes (2026-11-06) (PROVISIONAL: do not publish)

AFTER tree `5f6145dc3` (pin 1.1.2, `import.meta` rewrite, cross-process digest cache in `tsruntime`, the Piglet rename) against the BEFORE of `2026-11-06-basket-before.md` (tip `741f27c59b`), **in one interleaved session**: BEFORE arms and AFTER arms take turns inside every round, so the load of the host cancels as far as it can. Harness `bench/bolt-aot/basket.py` at `7063ec23d` (adds the `rewrite` set, the cold pass and the `--pair` delta table to the BEFORE harness). Raw JSONL (`runs.jsonl`, 5.7 MB) stays on xd670 and is not committed. As in BEFORE: **one run on a loaded shared host is not a publishable number**; the A/A columns are the noise floor.

Host load (1-minute average) before the run 20, during the runs median 25 (min 16, max 130: one spike, which the medians and p10-p90 absorb), after 38; 16-CPU container quota on 192 visible CPUs, Xeon Platinum 8468, kernel 7.0.0-38.

## What differs between the two eras (and what does not)

- **Go host**: `pig` built from `741f27c59b` (BEFORE) and from `5f6145dc3` (AFTER), same flags (`-trimpath`) in the same container. The only production-code difference between the two commits outside the Piglet is the digest cache (`coding/extension/host/tsruntime/digestcache.go`, `identity_*.go`, `verify.go`; the rest of the outside-Piglet diff is tests and docs; checked with `git diff --stat 741f27c59b 5f6145dc3`). So a change in a **Go host** row is the cache (or noise).
- **aot extension host**: BEFORE arms (`aot`, `aot2`) run the images of `slot-pin` (Pi-Bolt 1.1.2 on the old code, built by `0a0d27d74`'s worker); AFTER arms (`aot-a`, `aot-a2`) run images built by the worker of `5f6145dc3` into a fresh slot (same compiler pin 1.1.2; the `five` and `ten` images are the same size, 245.1 and 240.1 MB, with different bytes). The `rewrite` set (270 MB image) exists only AFTER.
- **Node and Bun**: the same Node 26.12.1 and stock Bun 1.4.2 (SHA-256 = the lock's) on both sides, only the Go host differs (`node` / `node-a`, `bun` / `bun-a`, `node2`, `node-a2` and `aot2`, `aot-a2` are the A/A repeats).
- Each aot cell is proven by the extension host's `/proc/<pid>/exe` (`<slot>/aot/images/<key>/pig-ts-host` of that arm's slot); a run on another tier is an error, not a number, and the executables seen are listed under each set in the generated report. The BEFORE `aot` arm on the `rewrite` set is `n/a` for that reason: `slot-pin` has no image of it, so PiG ran Bun (`arm aot ran ['bun'], not pig-ts-host`); the old worker was not run on that set here (the Piglet README says the old code refuses such sets).

## New in this harness

- **Cold pass** (5 rounds per Bun and aot arm and set): the digest cache's record (`.verified-<executable>` beside the executable) is removed before each start, the start is measured to ready, and the record's presence after the start is read. So the first start of an image, which hashes it, is its own cell; the timing pass below it is the warm start (record present, read in every run: row "Digest record present"). BEFORE arms never write records (0 in every run), AFTER arms always have one after the start. Not tested: a start within 2 s of the image being written (the cache records a file only once it is 2 s old); every image here was older.
- **Set `rewrite`**: `rpiv-todo`, `pi-mcp-adapter` and the `locating` fixture (`bench/bolt-aot/rewrite-exts`, pinned) on Node, Bun and the compiled host; a turn is a scripted tool call (`todo list`, `locating_read`, `locating_read legacy.txt`, `mcp`), 20 per run.
- **`--pair BEFORE=AFTER`**: the delta tables below (medians, delta = after against before).
- Per-arm `pig=` and `slot=`: one session interleaves both eras.

## Reading it

- **The ~280 ms hash is gone from the warm path.** Go host CPU to ready (warm, 2nd and later start), BEFORE to AFTER: aot `five` 480 to 190 ms (-60%), `ten` 460 to 200 (-57%); Bun 280 to 190 (-32%) and 290 to 195 (-33%), `rewrite` 260 to 140 (-46%); Node unchanged (190 to 190, 190 to 195). aot's Go host CPU to ready now equals Node's (190 / 200 against 190 / 195; BEFORE it was +290 / +270 ms). A/A of the AFTER arms: 0-5%.
- **Launch to ready follows it.** aot `five` 664 to 362 ms (-45%), `ten` 598 to 331 (-45%); Bun 1052 to 960 (-9%) and 506 to 406 (-20%); Node unchanged (1183 to 1183, 434 to 440). On the `ten` set aot, which was 160 ms behind Node BEFORE, is now 109 ms ahead (331 against `node-a` 440 ms); on `five` it starts 3.3x sooner than Node (362 against 1183). Total CPU to ready (Go host + extension host + reaped children) for aot: `five` 650 to 350 ms, `ten` 590 to 330 ms; Node-a: 1510 and 570.
- **A cold start still pays the hash, once.** With the record removed, aot-a: ready 639 ms (`five`), 514 (`ten`), 533 (`rewrite`) and Go host CPU to ready 470, 390, 370 ms; the same start with the record present: 362, 331, 346 ms and 190, 200, 180 ms. The record is there after every cold start (5 of 5), so the second start is warm. Bun-a cold: Go host CPU 280 / 260 / 210 ms against 190 / 195 / 140 warm (the 79.5 MB binary). The cold cells of the BEFORE arms equal their warm cells (they hash every time): `aot` 650 ms, 480 ms of Go host CPU on `five`. Cold-start spread is wider (A/A up to 10-13% on `ten` and `rewrite` with 5 rounds).
- **Nothing else moved beyond the noise floor** in the Go host or the turns: turn 1, steady turn, per-turn CPU of both hosts, faults and the memory kinds agree within A/A on Node and Bun. Two small exceptions, reported as observed, not explained: the Go host's RSS is 2-3 MB higher on every AFTER arm including Node (87.8 to 90.0 MB at ready on `five`, `node` against `node-a`; +2% to +4% later), and the aot host's anonymous memory on `five` is higher AFTER (Pss_Anon 34.7 to 39.2 MB at ready, RSS 101.5 to 106.0, +4%; after 20 turns 41.7 to 45.0), while on `ten` it is lower after 20 turns (82.5 to 76.5 MB, RSS -5%). The AFTER images are rebuilt bytes of the same sizes; I did not look for the cause.
- **Memory of the extension host, AFTER (RSS MB, ready / after 20 turns)**: `five` Node 168 / 183, Bun 163 / 165, aot 106 / 112; `ten` Node 111 / 194, Bun 97 / 146, aot 98 / 128; `rewrite` Node 188 / 202, Bun 168 / 172, aot 120 / 126. Kinds (`rewrite`, after 20 turns, MB): aot Pss_Anon 48 and Pss_File 76 (the 270 MB image is mapped), Node 145 and 52, Bun 106 and 64; Pss_Shmem 0 everywhere. On the `ten` set the extension host grows under 20 turns by 83 MB (Node), 49 MB (Bun) and 31 MB (aot); aot's 128 MB end value is the lowest.
- **The `rewrite` set compiles in and is the cheapest cell**: aot-a ready 346 ms against Node 922 and Bun 889 (2.7x, 2.6x); extension host CPU to ready 135 ms (+30 reaped) against 1035 and 1025; per-turn extension host CPU 3.7 ms against 8.4 and 8.2; turn 1 22 ms against 29 and 36; steady turn the same (11.4 against 11.4 and 11.7). The image is 269.5 MB. The turns include `locating_read` calls (data and skills read next to the extension); this harness does not inspect the tool results, `equivalence_pkgs.py` is the check that the right files are read, and every run reached ready with the `locating` command and completed its 20 turns.
- **Allocation** (3 rounds, passes without A/A): the memory-syscall counts per phase and the JS-heap allocation agree BEFORE and AFTER within the spread on Node, Bun and aot, as they should (nothing in the extension path changed): e.g. `five` startup extension host mmap 456 / 454 (Node), 523 / 522 (Bun), 190 / 191 (aot); aot JS allocation at ready 32.9 / 32.9 MB, per turn 0.32 / 0.32 MB. The one cell that differs is aot `ten` allocated to ready (39.2 to 52.3 MB; per turn 11.9 to 9.5), with AFTER ranges 40.6-52.3 and 7.8-11.8 MB; the BEFORE session's first run measured 9.4 MB per turn on the same arm (`before-run1`), so three rounds cannot separate it from noise. `rewrite` (AFTER only): aot allocates 33.6 MB to ready and 0.49 MB per turn against Node 284 MB and 0.96, Bun 76 and 0.79 (different accounting per runtime, see the BEFORE file), live heap after 20 turns 12.9 MB against Node 52.7 and Bun 33.5. Node 24 was not run in this session.

## A finding about the Piglet that this run hit

The Piglet's image store keeps **1 GB** by default (`defaultCacheBudget`, `PIG_AOT_CACHE_MAX`). The compiled images here are 240-270 MB, so the store holds three or four. My first AFTER session built six images (`five`, `ten`, `rewrite` and their probe variants) into one slot with the default budget; the slot was left with four images (the two `rewrite` ones and two others) and the `five` and `ten` aot-a arms ran on Bun [inferred: eviction by the 1 GB budget; I did not read the worker log], and the tier check rejected every aot-a run of those two sets (the failed session, `after-failed1`, is not used). The session reported here sets `PIG_AOT_CACHE_MAX=6GB` for the build. A user with three or four extension sets compiled in will see images evicted and rebuilt at the default; the BEFORE slot fitted four 240-245 MB images (970 MB); the `rewrite` images are 270 MB.

## Caveats (as in the BEFORE file, plus)

- Cold cells have 5 rounds, median [min-max]; the A/A of the cold cells is 0-13%.
- CPU is 10 ms ticks; reaped-child CPU is a lower bound; PSS of Node 24 is not in this session.
- Go host differences between eras are one commit's worth (the digest cache), but the compiled host's image was rebuilt: a change in an extension-host row of aot is the rebuild (or noise), a change in a Go host row is the cache (or noise).
- The BEFORE arms use the old Go host with a slot that holds 1.1.2 images, not 0.7.4 (it cannot build, see the BEFORE file).

## Before / after and cold tables (from the generated report below)

### Set `five`

**Cold starts** (5 rounds per arm): the digest cache's record beside the executable is removed before the start, so the Go host hashes the Bun or aot executable (and, with the cache, records it). The timing table above is the warm start (record present). An arm whose Go host has no digest cache hashes every time, so its cold and warm cells agree.

| Metric | bun | aot | aot2 | bun-a | aot-a | aot-a2 | A/A aot-aot2 (%) | A/A aot-a-aot-a2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 5/5 | 5/5 | 5/5 | 5/5 | 5/5 | 5/5 | | |
| Launch to ready, cold (ms) | 1055 [1043-1134] | 650 [611-674] | 645 [612-670] | 1045 [1000-1051] | 639 [613-651] | 641 [613-650] | 1 | 0 |
| Go host CPU to ready, cold, user+sys (ms) | 280 [270-360] | 480 [440-490] | 480 [420-510] | 280 [250-290] | 470 [460-470] | 470 [460-470] | 0 | 0 |
| Digest records removed before the start | 0 [0-0] | 0 [0-0] | 0 [0-0] | 1 [1-1] | 1 [1-1] | 1 [1-1] | - | 0 |
| Digest record present after the start (1 = yes) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 1 [1-1] | 1 [1-1] | 1 [1-1] | - | 0 |


**Before / after** (medians of the timing pass; delta = after against before)

| Metric | node | node-a | delta % | bun | bun-a | delta % | aot | aot-a | delta % |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Launch to ready (ms) | 1183 | 1183 | +0 | 1052 | 960 | -9 | 664 | 362 | -45 |
| Go host CPU to ready, user+sys (ms) | 190 | 190 | +0 | 280 | 190 | -32 | 480 | 190 | -60 |
| Extension host CPU to ready, user+sys (ms) | 1340 | 1320 | -1 | 1150 | 1140 | -1 | 140 | 130 | -7 |
| Digest record present beside the executable after ready (1 = yes; Node: n/a) | n/a | n/a | - | 0 | 1 | - | 0 | 1 | - |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 | 0 | - | 0 | 0 | - | 30 | 30 | +0 |
| Turn 1 wall (ms) | 43 | 44 | +2 | 46 | 46 | -0 | 37 | 37 | +1 |
| Turn 2 wall (ms) | 19 | 19 | -1 | 17 | 18 | +5 | 19 | 17 | -6 |
| Steady wall, median of turns 6-20 (ms) | 15.6 | 14.6 | -6 | 13.0 | 13.1 | +1 | 14.1 | 14.2 | +1 |
| Turn 1 / steady wall (ratio) | 2.86 | 2.92 | +2 | 3.49 | 3.51 | +1 | 2.70 | 2.71 | +1 |
| Go host CPU, turn 1 (ms) | 30 | 30 | +0 | 30 | 30 | +0 | 30 | 25 | -17 |
| Extension host CPU, turn 1 (ms) | 30 | 30 | +0 | 40 | 40 | +0 | 10 | 15 | +50 |
| Go host CPU per turn, turns 2-20 (ms) | 12.1 | 12.1 | +0 | 11.6 | 11.6 | +0 | 12.1 | 12.1 | +0 |
| Extension host CPU per turn, turns 2-20 (ms) | 10.0 | 10.0 | +0 | 7.4 | 7.9 | +7 | 5.5 | 4.7 | -14 |
| Extension host minor faults to 0.5 s after ready | 30823 | 30906 | +0 | 39384 | 38895 | -1 | 12405 | 12404 | -0 |
| Extension host minor faults, turn 1 | 750 | 726 | -3 | 643 | 658 | +2 | 537 | 512 | -5 |
| Extension host minor faults per turn, turns 2-20 | 295.3 | 277.3 | -6 | 86.2 | 93.6 | +9 | 94.0 | 91.9 | -2 |
| Extension host RSS @0.5 s after ready | 167.1 | 167.7 | +0 | 161.4 | 162.7 | +1 | 101.5 | 106.0 | +4 |
| Extension host PSS @0.5 s after ready | 162.7 | 162.8 | +0 | 159.3 | 160.6 | +1 | 99.3 | 103.8 | +5 |
| Pss_Anon @0.5 s after ready | 111.4 | 112.1 | +1 | 97.5 | 98.9 | +1 | 34.7 | 39.2 | +13 |
| Pss_File @0.5 s after ready | 51.0 | 51.4 | +1 | 61.8 | 61.7 | -0 | 64.5 | 64.5 | +0 |
| Private_Dirty @0.5 s after ready | 111.4 | 112.1 | +1 | 97.5 | 98.9 | +1 | 34.7 | 39.2 | +13 |
| Private_Clean @0.5 s after ready | 48.6 | 49.3 | +1 | 61.6 | 61.6 | +0 | 64.2 | 64.3 | +0 |
| Go host RSS @0.5 s after ready | 87.8 | 90.0 | +2 | 88.0 | 90.2 | +2 | 88.4 | 91.0 | +3 |
| Extension host RSS @after turn 1 | 169.9 | 170.3 | +0 | 160.1 | 160.5 | +0 | 102.6 | 106.5 | +4 |
| Extension host PSS @after turn 1 | 165.5 | 165.5 | +0 | 157.9 | 158.3 | +0 | 100.4 | 104.3 | +4 |
| Pss_Anon @after turn 1 | 114.2 | 114.6 | +0 | 96.2 | 96.5 | +0 | 35.7 | 39.5 | +11 |
| Pss_File @after turn 1 | 50.7 | 51.4 | +1 | 61.8 | 61.7 | -0 | 64.8 | 64.8 | -0 |
| Private_Dirty @after turn 1 | 114.2 | 114.6 | +0 | 96.2 | 96.5 | +0 | 35.7 | 39.5 | +11 |
| Private_Clean @after turn 1 | 47.9 | 49.3 | +3 | 61.6 | 61.6 | +0 | 64.6 | 64.6 | -0 |
| Go host RSS @after turn 1 | 96.3 | 98.2 | +2 | 96.6 | 98.4 | +2 | 96.8 | 99.2 | +2 |
| Extension host RSS @after 20 turns | 182.3 | 182.8 | +0 | 164.6 | 165.4 | +0 | 108.8 | 112.1 | +3 |
| Extension host PSS @after 20 turns | 177.2 | 178.3 | +1 | 162.5 | 163.3 | +0 | 106.5 | 109.8 | +3 |
| Pss_Anon @after 20 turns | 126.5 | 127.1 | +0 | 100.8 | 101.6 | +1 | 41.7 | 45.0 | +8 |
| Pss_File @after 20 turns | 51.1 | 51.4 | +1 | 61.8 | 61.8 | -0 | 64.8 | 64.8 | -0 |
| Private_Dirty @after 20 turns | 126.5 | 127.1 | +0 | 100.8 | 101.6 | +1 | 41.7 | 45.0 | +8 |
| Private_Clean @after 20 turns | 48.6 | 49.3 | +1 | 61.6 | 61.6 | +0 | 64.6 | 64.6 | -0 |
| Go host RSS @after 20 turns | 109.6 | 114.0 | +4 | 111.6 | 114.2 | +2 | 110.5 | 113.1 | +2 |
| Extension host executable size (MB) | 150.3 | 150.3 | +0 | 79.5 | 79.5 | +0 | 245.1 | 245.1 | +0 |


### Set `ten`

**Cold starts** (5 rounds per arm): the digest cache's record beside the executable is removed before the start, so the Go host hashes the Bun or aot executable (and, with the cache, records it). The timing table above is the warm start (record present). An arm whose Go host has no digest cache hashes every time, so its cold and warm cells agree.

| Metric | bun | aot | aot2 | bun-a | aot-a | aot-a2 | A/A aot-aot2 (%) | A/A aot-a-aot-a2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 5/5 | 5/5 | 5/5 | 5/5 | 5/5 | 5/5 | | |
| Launch to ready, cold (ms) | 468 [440-509] | 560 [461-638] | 582 [452-622] | 448 [377-546] | 514 [461-602] | 571 [506-601] | 4 | 10 |
| Go host CPU to ready, cold, user+sys (ms) | 280 [260-290] | 430 [340-520] | 460 [320-490] | 260 [200-350] | 390 [330-470] | 430 [380-470] | 7 | 10 |
| Digest records removed before the start | 0 [0-0] | 0 [0-0] | 0 [0-0] | 1 [1-1] | 1 [1-3] | 1 [1-1] | - | 0 |
| Digest record present after the start (1 = yes) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 1 [1-1] | 1 [1-1] | 1 [1-1] | - | 0 |


**Before / after** (medians of the timing pass; delta = after against before)

| Metric | node | node-a | delta % | bun | bun-a | delta % | aot | aot-a | delta % |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Launch to ready (ms) | 434 | 440 | +1 | 506 | 406 | -20 | 598 | 331 | -45 |
| Go host CPU to ready, user+sys (ms) | 190 | 195 | +3 | 290 | 195 | -33 | 460 | 200 | -57 |
| Extension host CPU to ready, user+sys (ms) | 370 | 375 | +1 | 345 | 345 | +0 | 100 | 100 | +0 |
| Digest record present beside the executable after ready (1 = yes; Node: n/a) | n/a | n/a | - | 0 | 1 | - | 0 | 1 | - |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 | 0 | - | 0 | 0 | - | 30 | 30 | +0 |
| Turn 1 wall (ms) | 47 | 49 | +4 | 42 | 42 | -1 | 41 | 41 | -0 |
| Turn 2 wall (ms) | 40 | 39 | -2 | 31 | 30 | -2 | 31 | 32 | +3 |
| Steady wall, median of turns 6-20 (ms) | 39.6 | 39.3 | -1 | 33.3 | 31.8 | -4 | 33.0 | 33.9 | +3 |
| Turn 1 / steady wall (ratio) | 1.17 | 1.23 | +5 | 1.27 | 1.33 | +4 | 1.25 | 1.20 | -4 |
| Go host CPU, turn 1 (ms) | 20 | 30 | +50 | 30 | 25 | -17 | 25 | 30 | +20 |
| Extension host CPU, turn 1 (ms) | 45 | 50 | +11 | 65 | 70 | +8 | 20 | 30 | +50 |
| Go host CPU per turn, turns 2-20 (ms) | 17.9 | 18.4 | +3 | 17.6 | 17.4 | -1 | 18.2 | 17.9 | -1 |
| Extension host CPU per turn, turns 2-20 (ms) | 41.6 | 42.4 | +2 | 35.8 | 34.5 | -4 | 24.7 | 25.3 | +2 |
| Extension host minor faults to 0.5 s after ready | 16698 | 16833 | +1 | 16556 | 16598 | +0 | 12990 | 12870 | -1 |
| Extension host minor faults, turn 1 | 1982 | 2052 | +4 | 1760 | 1842 | +5 | 1836 | 1778 | -3 |
| Extension host minor faults per turn, turns 2-20 | 1042.7 | 1041.7 | -0 | 1439.7 | 1404.0 | -2 | 1072.6 | 1056.9 | -1 |
| Extension host RSS @0.5 s after ready | 110.8 | 111.4 | +1 | 97.1 | 97.2 | +0 | 97.9 | 97.5 | -0 |
| Extension host PSS @0.5 s after ready | 105.4 | 106.4 | +1 | 94.8 | 95.0 | +0 | 95.6 | 95.2 | -0 |
| Pss_Anon @0.5 s after ready | 58.3 | 58.9 | +1 | 57.1 | 57.3 | +0 | 46.6 | 46.2 | -1 |
| Pss_File @0.5 s after ready | 47.6 | 47.6 | -0 | 37.7 | 37.7 | +0 | 49.0 | 49.0 | +0 |
| Private_Dirty @0.5 s after ready | 58.3 | 58.9 | +1 | 57.1 | 57.3 | +0 | 46.6 | 46.2 | -1 |
| Private_Clean @0.5 s after ready | 44.8 | 44.9 | +0 | 37.5 | 37.5 | +0 | 48.8 | 48.8 | +0 |
| Go host RSS @0.5 s after ready | 87.6 | 89.9 | +3 | 88.1 | 89.9 | +2 | 87.8 | 90.7 | +3 |
| Extension host RSS @after turn 1 | 117.5 | 118.5 | +1 | 94.3 | 94.0 | -0 | 105.0 | 103.7 | -1 |
| Extension host PSS @after turn 1 | 112.5 | 113.5 | +1 | 92.0 | 91.8 | -0 | 102.7 | 101.5 | -1 |
| Pss_Anon @after turn 1 | 64.9 | 65.9 | +2 | 54.3 | 54.0 | -0 | 53.4 | 52.1 | -2 |
| Pss_File @after turn 1 | 47.7 | 47.7 | +0 | 37.8 | 37.7 | -0 | 49.3 | 49.3 | +0 |
| Private_Dirty @after turn 1 | 64.9 | 65.9 | +2 | 54.3 | 54.0 | -0 | 53.4 | 52.1 | -2 |
| Private_Clean @after turn 1 | 44.9 | 44.9 | +0 | 37.6 | 37.6 | -0 | 49.1 | 49.1 | +0 |
| Go host RSS @after turn 1 | 94.4 | 96.3 | +2 | 94.5 | 96.3 | +2 | 94.6 | 97.1 | +3 |
| Extension host RSS @after 20 turns | 196.4 | 194.4 | -1 | 147.2 | 146.0 | -1 | 134.2 | 128.1 | -5 |
| Extension host PSS @after 20 turns | 190.8 | 189.8 | -1 | 145.0 | 143.8 | -1 | 131.9 | 125.8 | -5 |
| Pss_Anon @after 20 turns | 143.7 | 141.8 | -1 | 104.4 | 103.1 | -1 | 82.5 | 76.5 | -7 |
| Pss_File @after 20 turns | 47.6 | 47.6 | +0 | 40.6 | 40.7 | +0 | 49.4 | 49.4 | -0 |
| Private_Dirty @after 20 turns | 143.7 | 141.8 | -1 | 104.4 | 103.1 | -1 | 82.5 | 76.5 | -7 |
| Private_Clean @after 20 turns | 45.0 | 45.0 | -0 | 40.5 | 40.5 | +0 | 49.2 | 49.1 | -0 |
| Go host RSS @after 20 turns | 110.5 | 112.7 | +2 | 112.7 | 116.2 | +3 | 111.0 | 113.7 | +2 |
| Extension host executable size (MB) | 150.3 | 150.3 | +0 | 79.5 | 79.5 | +0 | 240.1 | 240.1 | +0 |


### Set `rewrite`

**Cold starts** (5 rounds per arm): the digest cache's record beside the executable is removed before the start, so the Go host hashes the Bun or aot executable (and, with the cache, records it). The timing table above is the warm start (record present). An arm whose Go host has no digest cache hashes every time, so its cold and warm cells agree.

| Metric | bun | bun-a | aot-a | aot-a2 | A/A aot-a-aot-a2 (%) |
|---|---:|---:|---:|---:|---:|
| Runs ok / total | 5/5 | 5/5 | 5/5 | 5/5 | |
| Launch to ready, cold (ms) | 839 [779-919] | 802 [726-974] | 533 [485-587] | 587 [530-622] | 10 |
| Go host CPU to ready, cold, user+sys (ms) | 230 [200-260] | 210 [190-230] | 370 [350-400] | 420 [360-460] | 13 |
| Digest records removed before the start | 0 [0-0] | 1 [1-1] | 1 [1-3] | 1 [1-1] | 0 |
| Digest record present after the start (1 = yes) | 0 [0-0] | 1 [1-1] | 1 [1-1] | 1 [1-1] | 0 |


**Before / after** (medians of the timing pass; delta = after against before)

| Metric | node | node-a | delta % | bun | bun-a | delta % | aot | aot-a | delta % |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Launch to ready (ms) | 829 | 922 | +11 | 968 | 889 | -8 | n/a | 346 | - |
| Go host CPU to ready, user+sys (ms) | 170 | 180 | +6 | 260 | 140 | -46 | n/a | 180 | - |
| Extension host CPU to ready, user+sys (ms) | 920 | 1035 | +12 | 1030 | 1025 | -0 | n/a | 135 | - |
| Digest record present beside the executable after ready (1 = yes; Node: n/a) | n/a | n/a | - | 0 | 1 | - | n/a | 1 | - |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 | 0 | - | 0 | 0 | - | n/a | 30 | - |
| Turn 1 wall (ms) | 28 | 29 | +2 | 35 | 36 | +5 | n/a | 22 | - |
| Turn 2 wall (ms) | 18 | 18 | +2 | 18 | 17 | -4 | n/a | 16 | - |
| Steady wall, median of turns 6-20 (ms) | 11.8 | 11.4 | -4 | 11.8 | 11.7 | -1 | n/a | 11.4 | - |
| Turn 1 / steady wall (ratio) | 2.34 | 2.53 | +8 | 3.01 | 3.18 | +5 | n/a | 2.17 | - |
| Go host CPU, turn 1 (ms) | 10 | 20 | +100 | 20 | 20 | +0 | n/a | 15 | - |
| Extension host CPU, turn 1 (ms) | 10 | 15 | +50 | 40 | 40 | +0 | n/a | 10 | - |
| Go host CPU per turn, turns 2-20 (ms) | 10.0 | 9.7 | -3 | 10.5 | 10.3 | -3 | n/a | 9.7 | - |
| Extension host CPU per turn, turns 2-20 (ms) | 8.9 | 8.4 | -6 | 8.4 | 8.2 | -3 | n/a | 3.7 | - |
| Extension host minor faults to 0.5 s after ready | 36204 | 36162 | -0 | 31886 | 31860 | -0 | n/a | 12600 | - |
| Extension host minor faults, turn 1 | 608 | 607 | -0 | 588 | 600 | +2 | n/a | 238 | - |
| Extension host minor faults per turn, turns 2-20 | 167.3 | 167.5 | +0 | 126.1 | 123.0 | -2 | n/a | 120.2 | - |
| Extension host RSS @0.5 s after ready | 187.6 | 187.6 | -0 | 167.8 | 167.7 | -0 | n/a | 120.3 | - |
| Extension host PSS @0.5 s after ready | 182.5 | 182.3 | -0 | 165.6 | 165.5 | -0 | n/a | 118.0 | - |
| Pss_Anon @0.5 s after ready | 130.4 | 130.4 | +0 | 101.5 | 101.4 | -0 | n/a | 43.4 | - |
| Pss_File @0.5 s after ready | 51.7 | 51.8 | +0 | 64.0 | 64.0 | -0 | n/a | 74.5 | - |
| Private_Dirty @0.5 s after ready | 130.4 | 130.4 | +0 | 101.5 | 101.4 | -0 | n/a | 43.4 | - |
| Private_Clean @0.5 s after ready | 49.2 | 49.2 | +0 | 63.9 | 63.9 | -0 | n/a | 74.3 | - |
| Go host RSS @0.5 s after ready | 87.3 | 89.5 | +3 | 87.7 | 89.8 | +2 | n/a | 90.8 | - |
| Extension host RSS @after turn 1 | 190.0 | 189.8 | -0 | 165.2 | 165.1 | -0 | n/a | 117.9 | - |
| Extension host PSS @after turn 1 | 184.9 | 184.6 | -0 | 163.1 | 163.0 | -0 | n/a | 115.7 | - |
| Pss_Anon @after turn 1 | 132.8 | 132.5 | -0 | 99.0 | 98.9 | -0 | n/a | 40.9 | - |
| Pss_File @after turn 1 | 51.8 | 51.7 | -0 | 64.0 | 64.0 | +0 | n/a | 74.7 | - |
| Private_Dirty @after turn 1 | 132.8 | 132.5 | -0 | 99.0 | 98.9 | -0 | n/a | 40.9 | - |
| Private_Clean @after turn 1 | 49.2 | 49.2 | -0 | 63.9 | 63.9 | -0 | n/a | 74.5 | - |
| Go host RSS @after turn 1 | 93.0 | 94.6 | +2 | 93.2 | 95.0 | +2 | n/a | 95.9 | - |
| Extension host RSS @after 20 turns | 202.4 | 202.2 | -0 | 172.5 | 172.3 | -0 | n/a | 126.2 | - |
| Extension host PSS @after 20 turns | 197.0 | 196.7 | -0 | 170.4 | 170.1 | -0 | n/a | 123.9 | - |
| Pss_Anon @after 20 turns | 145.2 | 145.0 | -0 | 106.3 | 106.0 | -0 | n/a | 48.4 | - |
| Pss_File @after 20 turns | 51.7 | 51.7 | -0 | 64.0 | 64.0 | +0 | n/a | 75.5 | - |
| Private_Dirty @after 20 turns | 145.2 | 145.0 | -0 | 106.3 | 106.0 | -0 | n/a | 48.4 | - |
| Private_Clean @after 20 turns | 49.2 | 49.2 | -0 | 63.9 | 63.9 | -0 | n/a | 75.3 | - |
| Go host RSS @after 20 turns | 110.7 | 111.8 | +1 | 112.2 | 115.6 | +3 | n/a | 115.8 | - |
| Extension host executable size (MB) | 150.3 | 150.3 | +0 | 79.5 | 79.5 | +0 | n/a | 269.5 | - |



## Re-running

The command line of this session (inside the CI container, from `~/pigaot-basket` at `5f6145dc3`; `$S` = the scratch directory, `pig` = the BEFORE Go host built from `741f27c59b`, `pig-after` from `5f6145dc3`, `slot-pin` = the BEFORE slot, `slot-after` the fresh AFTER slot built by `--build`):

```sh
PIG_AOT_CACHE_MAX=6GB python3 bench/bolt-aot/basket.py run --out $S/after --pig $S/pig-after --slot $S/slot-after --bun $S/dl/bun-linux-x64/bun --build \
  --sets five,ten,rewrite --rounds 12 --turns 20 --alloc-rounds 3 --cold-rounds 5 \
  --arm node:node:pig=$S/pig --arm node2:node:pig=$S/pig --arm bun:bun:pig=$S/pig:slot=$S/slot-pin --arm aot:aot:pig=$S/pig:slot=$S/slot-pin --arm aot2:aot:pig=$S/pig:slot=$S/slot-pin \
  --arm node-a:node --arm node-a2:node --arm bun-a:bun --arm aot-a:aot --arm aot-a2:aot --pair node=node-a --pair bun=bun-a --pair aot=aot-a
```

(`npm ci --ignore-scripts` in `bench/bolt-aot/exts` and `bench/bolt-aot/rewrite-exts` first; strace bind-mounted from the host.)

## Generated report (`basket.py report`)



Generated by `bench/bolt-aot/basket.py report` from `runs.jsonl`. **PROVISIONAL: one run on a loaded shared host; do not publish a number from it.** Read the A/A columns first.

## Header

- date (UTC): 2026-11-07T00:57:57Z; host `da50f1ff79d5`, kernel 7.0.0-38-generic
- CPU: Intel(R) Xeon(R) Platinum 8468; 192 CPUs visible; cgroup cpu.max `1600000 100000`, memory.max `17179869184`
- bench commit 7063ec23d4e360e01dac47f835a4d6d35eecd693 (dirty tracked files: False); labels: before=741f27c59b-pig+pin-1.1.2-slot-pin, after=5f6145dc3, cache=digest-cache-1638f9275
- host load average (1 min): before 20.14, during runs median 25 (min 16, max 130), after 38.01
- strace: strace -- version 6.19; python 3.12.3; clock tick 100/s (CPU resolution 10 ms)

| arm | kind | pig (sha256, version) | runtime |
|---|---|---|---|
| node | node | 1b320811e19c, 0.7.0+1.5.0 | Node v26.12.1 (/opt/node/26.12.1/bin/node) |
| node2 | node | 1b320811e19c, 0.7.0+1.5.0 | Node v26.12.1 (/opt/node/26.12.1/bin/node) |
| bun | bun | 1b320811e19c, 0.7.0+1.5.0 | Bun 1.4.2 (sha256 a83d263767d8, lock match) |
| aot | aot | 1b320811e19c, 0.7.0+1.5.0 | Pi-Bolt manifest `1.1.2`, toolchain ['pi-bolt-1.1.2-linux-x64'], 4 image(s) |
| aot2 | aot | 1b320811e19c, 0.7.0+1.5.0 | Pi-Bolt manifest `1.1.2`, toolchain ['pi-bolt-1.1.2-linux-x64'], 4 image(s) |
| node-a | node | 99b9e6800839, 0.7.0+1.5.0 | Node v26.12.1 (/opt/node/26.12.1/bin/node) |
| node-a2 | node | 99b9e6800839, 0.7.0+1.5.0 | Node v26.12.1 (/opt/node/26.12.1/bin/node) |
| bun-a | bun | 99b9e6800839, 0.7.0+1.5.0 | Bun 1.4.2 (sha256 a83d263767d8, lock match) |
| aot-a | aot | 99b9e6800839, 0.7.0+1.5.0 | Pi-Bolt manifest `1.1.2`, toolchain ['pi-bolt-1.1.2-linux-x64'], 6 image(s) |
| aot-a2 | aot | 99b9e6800839, 0.7.0+1.5.0 | Pi-Bolt manifest `1.1.2`, toolchain ['pi-bolt-1.1.2-linux-x64'], 6 image(s) |

aot slot of `aot` (`<scratch>/basket/slot-pin`): images `aot/images/17e7f9041a2e2e79-1794009340118344629/pig-ts-host` 245.1 MB sha256 c8876a78d418 (one Python SHA-256 of it: 288 ms); `aot/images/a7cd851ad062fb1d-1794009310918408967/pig-ts-host` 240.1 MB sha256 f2d265110d27 (one Python SHA-256 of it: 328 ms); `aot/images/cae5b43fa55d7699-1794009575719067886/pig-ts-host` 240.1 MB sha256 ee7a825358e7 (one Python SHA-256 of it: 282 ms); `aot/images/f41d924fcdb7cb91-1794009553772984900/pig-ts-host` 245.1 MB sha256 94c9578e1db1 (one Python SHA-256 of it: 288 ms)

aot slot of `aot-a` (`<scratch>/basket/slot-after`): images `aot/images/01a358f4a489aeb8-1794012989792337337/pig-ts-host` 269.5 MB sha256 7fefdb29c1c1 (one Python SHA-256 of it: 309 ms); `aot/images/0e2abda90957e4c1-1794013075302704748/pig-ts-host` 269.5 MB sha256 8cec020a9a64 (one Python SHA-256 of it: 315 ms); `aot/images/17e7f9041a2e2e79-1794012938131456291/pig-ts-host` 245.1 MB sha256 3f2a16d54c1b (one Python SHA-256 of it: 287 ms); `aot/images/a7cd851ad062fb1d-1794012959093885020/pig-ts-host` 240.1 MB sha256 ddb089f3f418 (one Python SHA-256 of it: 282 ms); `aot/images/cae5b43fa55d7699-1794013045320438436/pig-ts-host` 240.1 MB sha256 3c143e15b032 (one Python SHA-256 of it: 284 ms); `aot/images/f41d924fcdb7cb91-1794013018307645510/pig-ts-host` 245.1 MB sha256 d338bbce482b (one Python SHA-256 of it: 373 ms)

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
  "executable": "<scratch>/basket/slot-pin/bun/bun"
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
  "executable": "<scratch>/basket/slot-pin/bun/bun",
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

`pig extensions runtime status --json` for `node-a` (abridged to selection and installed manifests):

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

`pig extensions runtime status --json` for `bun-a` (abridged to selection and installed manifests):

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
  "executable": "<scratch>/basket/slot-after/bun/bun"
 },
 "installed": [
  {
   "id": "aot",
   "version": "1.1.2",
   "provider": "pig-bolt/bolt-runtime",
   "sets": 6
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

`pig extensions runtime status --json` for `aot-a` (abridged to selection and installed manifests):

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
  "executable": "<scratch>/basket/slot-after/bun/bun",
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
   "sets": 6
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

Proof each arm ran its tier: every run's extension-host `/proc/<pid>/exe` is checked (node: a node binary; bun: the slot's bun; aot: `<slot>/aot/images/<key>/pig-ts-host`) and a run that differs is an error, not a number. Executables seen: node: /opt/node/26.12.1/bin/node; node2: /opt/node/26.12.1/bin/node; bun: <scratch>/basket/slot-pin/bun/bun; aot: <scratch>/basket/slot-pin/aot/images/17e7f9041a2e2e79-1794009340118344629/pig-ts-host; aot2: <scratch>/basket/slot-pin/aot/images/17e7f9041a2e2e79-1794009340118344629/pig-ts-host; node-a: /opt/node/26.12.1/bin/node; node-a2: /opt/node/26.12.1/bin/node; bun-a: <scratch>/basket/slot-after/bun/bun; aot-a: <scratch>/basket/slot-after/aot/images/17e7f9041a2e2e79-1794012938131456291/pig-ts-host; aot-a2: <scratch>/basket/slot-after/aot/images/17e7f9041a2e2e79-1794012938131456291/pig-ts-host

| Metric | node | node2 | bun | aot | aot2 | node-a | node-a2 | bun-a | aot-a | aot-a2 | A/A node-node2 (%) | A/A aot-aot2 (%) | A/A node-a-node-a2 (%) | A/A aot-a-aot-a2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | | | | |
| **Startup (launch to ready: get_commands answered with the set's commands)** | | | | | | | | | | | | | | |
| Launch to ready (ms) | 1183 [925-1247] | 1176 [862-1200] | 1052 [1020-1077] | 664 [572-688] | 661 [641-674] | 1183 [989-1193] | 1181 [1124-1198] | 960 [950-968] | 362 [330-373] | 368 [311-389] | 1 | 1 | 0 | 1 |
| Go host CPU to ready, user+sys (ms) | 190 [180-190] | 180 [180-190] | 280 [280-290] | 480 [385-490] | 480 [470-499] | 190 [180-190] | 190 [131-190] | 190 [180-190] | 190 [163-200] | 195 [144-218] | 5 | 0 | 0 | 3 |
|   Go host user (ms) | 150 [140-150] | 150 [140-159] | 230 [211-239] | 400 [296-410] | 390 [370-410] | 150 [141-160] | 140 [101-150] | 150 [150-160] | 150 [122-160] | 150 [113-169] | 0 | 3 | 7 | 0 |
|   Go host sys (ms) | 40 [30-49] | 35 [30-40] | 55 [50-60] | 80 [80-90] | 90 [80-109] | 40 [30-40] | 40 [30-49] | 35 [30-40] | 40 [31-49] | 40 [31-50] | 13 | 12 | 0 | 0 |
| Go host: CPU of reaped children by ready, the `--version` start checks (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 10 [0-10] | 10 [0-10] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-10] | 0 [0-10] | - | 0 | - | - |
| Extension host CPU to ready, user+sys (ms) | 1340 [1057-1407] | 1320 [973-1349] | 1150 [1094-1169] | 140 [130-149] | 140 [121-149] | 1320 [1110-1339] | 1330 [1281-1340] | 1140 [1130-1159] | 130 [112-130] | 140 [111-140] | 2 | 0 | 1 | 7 |
|   extension host user (ms) | 1185 [934-1238] | 1175 [863-1210] | 965 [914-990] | 100 [91-110] | 100 [91-110] | 1160 [979-1180] | 1185 [1141-1200] | 965 [941-988] | 100 [81-100] | 100 [81-109] | 1 | 0 | 2 | 0 |
|   extension host sys (ms) | 150 [123-178] | 140 [111-150] | 180 [180-200] | 30 [30-40] | 35 [30-40] | 150 [130-170] | 145 [140-150] | 180 [161-190] | 30 [30-40] | 40 [30-40] | 7 | 15 | 3 | 29 |
| Digest record present beside the executable after ready (1 = yes; Node: n/a) | n/a | n/a | 0 [0-0] | 0 [0-0] | 0 [0-0] | n/a | n/a | 1 [1-1] | 1 [1-1] | 1 [1-1] | - | - | - | 0 |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 30 [30-39] | 30 [30-40] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 30 [21-40] | 30 [21-40] | - | 0 | - | 0 |
| Extension host CPU, ready to 0.5 s later (idle after ready) (ms) | 0 [0-0] | 0 [0-0] | 20 [10-30] | 5 [0-10] | 0 [0-10] | 0 [0-0] | 0 [0-0] | 20 [11-20] | 0 [0-10] | 0 [0-10] | - | 200 | - | - |
| **Turns (a prompt, one model tool call, the tool, a final answer; 20 per run)** | | | | | | | | | | | | | | |
| Turn 1 wall (ms) | 43 [41-55] | 43 [40-55] | 46 [44-48] | 37 [35-39] | 38 [35-40] | 44 [40-48] | 44 [41-49] | 46 [43-48] | 37 [36-41] | 36 [32-38] | 1 | 2 | 0 | 4 |
| Turn 2 wall (ms) | 19 [18-23] | 19 [19-24] | 17 [15-25] | 19 [16-20] | 17 [16-19] | 19 [18-23] | 20 [17-24] | 18 [17-21] | 17 [15-25] | 19 [17-22] | 0 | 8 | 4 | 8 |
| Steady wall, median of turns 6-20 (ms) | 15.6 [13.8-18.6] | 14.7 [14.1-19.0] | 13.0 [11.1-17.6] | 14.1 [12.1-15.4] | 13.5 [12.4-17.4] | 14.6 [12.9-18.4] | 14.5 [13.5-18.8] | 13.1 [12.3-14.2] | 14.2 [11.5-17.1] | 14.0 [11.8-15.6] | 5 | 4 | 0 | 1 |
| Turn 1 / steady wall (ratio) | 2.86 [2.36-3.88] | 2.86 [2.57-3.22] | 3.49 [2.70-3.98] | 2.70 [2.32-3.01] | 2.72 [2.28-3.07] | 2.92 [2.47-3.22] | 3.03 [2.49-3.38] | 3.51 [3.25-3.69] | 2.71 [2.30-3.25] | 2.60 [2.26-3.08] | 0 | 1 | 4 | 4 |
| **CPU per phase (user+sys, 10 ms ticks; the first turn is measured with 0.5 s settled before and 0.2 s after)** | | | | | | | | | | | | | | |
| Go host CPU, turn 1 (ms) | 30 [20-40] | 30 [20-40] | 30 [20-30] | 30 [20-30] | 30 [20-30] | 30 [20-30] | 30 [20-40] | 30 [20-40] | 25 [20-30] | 30 [20-30] | 0 | 0 | 0 | 18 |
| Extension host CPU, turn 1 (ms) | 30 [30-39] | 30 [20-40] | 40 [40-50] | 10 [10-20] | 10 [10-20] | 30 [20-30] | 25 [20-40] | 40 [40-50] | 15 [10-20] | 10 [10-20] | 0 | 0 | 18 | 40 |
| Go host CPU per turn, turns 2-20 (ms) | 12.1 [11.6-13.1] | 12.1 [10.6-12.6] | 11.6 [10.0-13.6] | 12.1 [11.6-14.1] | 11.6 [11.1-12.6] | 12.1 [11.6-12.6] | 11.3 [10.5-12.6] | 11.6 [11.1-12.1] | 12.1 [9.6-12.6] | 11.6 [10.0-13.1] | 0 | 4 | 7 | 4 |
| Extension host CPU per turn, turns 2-20 (ms) | 10.0 [9.1-11.5] | 9.5 [9.0-11.0] | 7.4 [6.9-10.5] | 5.5 [3.8-6.3] | 4.7 [4.2-5.8] | 10.0 [8.5-11.5] | 9.7 [8.9-11.5] | 7.9 [7.4-8.4] | 4.7 [4.3-5.8] | 5.0 [4.2-5.8] | 5 | 15 | 3 | 5 |
|   extension host sys per turn, turns 2-20 (ms) | 1.6 [1.1-2.1] | 1.6 [1.1-2.1] | 1.6 [1.1-1.6] | 1.1 [0.5-1.6] | 1.1 [0.5-1.1] | 2.1 [1.1-2.1] | 1.6 [1.1-2.1] | 1.6 [1.1-1.6] | 1.1 [0.6-1.1] | 1.1 [0.5-1.1] | 0 | 0 | 29 | 0 |
| **Page faults (cumulative counters of the process; extension host = its process tree)** | | | | | | | | | | | | | | |
| Extension host minor faults to 0.5 s after ready | 30823 [30522-32560] | 31000 [30691-31258] | 39384 [38254-39514] | 12405 [12218-12503] | 12388 [12286-12429] | 30906 [30597-31930] | 30929 [30549-31444] | 38895 [38682-39115] | 12404 [11975-12578] | 12327 [12048-12474] | 1 | 0 | 0 | 1 |
| Extension host major faults to 0.5 s after ready | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | - | - | - | - |
| Extension host minor faults, turn 1 | 750 [723-2584] | 746 [738-761] | 643 [571-753] | 537 [467-604] | 556 [493-612] | 726 [332-746] | 740 [714-764] | 658 [583-741] | 512 [459-634] | 516 [473-629] | 0 | 3 | 2 | 1 |
| Extension host minor faults per turn, turns 2-20 | 295.3 [178.8-348.7] | 256.5 [165.4-356.7] | 86.2 [82.7-93.6] | 94.0 [83.9-98.5] | 95.2 [82.7-96.5] | 277.3 [172.4-321.9] | 297.6 [263.4-358.6] | 93.6 [88.9-96.2] | 91.9 [81.2-97.8] | 96.5 [86.0-97.4] | 14 | 1 | 7 | 5 |
| Extension host major faults, whole run to the last turn | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | - | - | - | - |
| Go host minor faults to 0.5 s after ready | 13192 [13171-13254] | 13193 [13142-13276] | 13275 [13238-13389] | 13360 [13271-13426] | 13336 [13243-13359] | 13162 [13068-13220] | 13138 [13084-13206] | 13232 [13205-13258] | 13448 [13377-13514] | 13423 [13308-13519] | 0 | 0 | 0 | 0 |
| Go host minor faults per turn, turns 2-20 | 188.2 [173.2-218.9] | 195.5 [174.3-270.7] | 206.0 [175.2-280.6] | 190.3 [181.7-315.7] | 187.5 [169.1-251.7] | 218.7 [185.2-231.5] | 187.4 [163.0-278.4] | 218.9 [171.7-221.9] | 189.5 [174.4-256.2] | 183.9 [161.6-282.1] | 4 | 2 | 15 | 3 |
| **Memory 0.5 s after ready (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | | | | | | |
| Extension host RSS | 167.1 [165.8-169.3] | 167.5 [166.0-168.9] | 161.4 [160.1-163.6] | 101.5 [98.5-110.2] | 100.9 [97.0-109.9] | 167.7 [165.5-169.2] | 167.7 [166.3-168.5] | 162.7 [160.3-164.5] | 106.0 [100.9-110.3] | 105.0 [97.7-109.5] | 0 | 1 | 0 | 1 |
| Extension host PSS | 162.7 [160.4-164.9] | 163.0 [161.2-163.9] | 159.3 [158.0-161.4] | 99.3 [96.3-108.0] | 98.7 [94.7-107.7] | 162.8 [161.2-164.9] | 163.0 [161.8-164.2] | 160.6 [158.1-162.3] | 103.8 [98.7-108.1] | 102.8 [95.4-107.2] | 0 | 1 | 0 | 1 |
|   Pss_Anon | 111.4 [110.2-113.6] | 111.9 [110.3-113.1] | 97.5 [96.2-99.6] | 34.7 [31.8-43.4] | 34.2 [30.1-43.2] | 112.1 [109.9-113.5] | 112.0 [110.6-112.8] | 98.9 [96.3-100.5] | 39.2 [34.2-43.6] | 38.3 [30.9-42.7] | 0 | 2 | 0 | 2 |
|   Pss_File | 51.0 [50.2-51.4] | 50.6 [50.4-51.5] | 61.8 [61.7-61.8] | 64.5 [64.4-64.6] | 64.5 [64.4-64.6] | 51.4 [50.7-51.4] | 51.0 [50.4-51.5] | 61.7 [61.7-61.8] | 64.5 [64.4-64.6] | 64.5 [64.4-64.6] | 1 | 0 | 1 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | - | - | - | - |
|   Private_Dirty | 111.4 [110.2-113.6] | 111.9 [110.3-113.1] | 97.5 [96.2-99.6] | 34.7 [31.8-43.4] | 34.2 [30.1-43.2] | 112.1 [109.9-113.5] | 112.0 [110.6-112.8] | 98.9 [96.3-100.5] | 39.2 [34.2-43.6] | 38.3 [30.9-42.7] | 0 | 2 | 0 | 2 |
|   Private_Clean | 48.6 [47.8-49.3] | 47.8 [47.8-49.4] | 61.6 [61.6-61.6] | 64.2 [64.2-64.4] | 64.3 [64.2-64.4] | 49.3 [47.8-49.4] | 48.6 [47.8-49.3] | 61.6 [61.6-61.7] | 64.3 [64.2-64.3] | 64.3 [64.2-64.4] | 1 | 0 | 1 | 0 |
|   Shared_Clean+Shared_Dirty | 7.1 [6.4-7.9] | 7.8 [6.4-7.9] | 2.3 [2.2-2.4] | 2.5 [2.4-2.5] | 2.4 [2.4-2.5] | 6.4 [6.3-7.8] | 7.1 [6.3-7.9] | 2.3 [2.3-2.4] | 2.5 [2.4-2.5] | 2.5 [2.4-2.5] | 9 | 0 | 11 | 0 |
| Go host RSS | 87.8 [87.3-88.2] | 87.6 [87.3-88.0] | 88.0 [87.8-88.4] | 88.4 [87.8-88.6] | 88.1 [87.8-88.5] | 90.0 [89.7-90.4] | 90.0 [89.7-90.3] | 90.2 [90.0-90.5] | 91.0 [90.7-91.3] | 91.0 [90.7-91.3] | 0 | 0 | 0 | 0 |
| Go host PSS | 86.2 [85.8-86.7] | 86.0 [85.7-86.4] | 86.5 [86.2-86.8] | 86.8 [86.3-87.1] | 86.6 [86.3-87.0] | 88.5 [88.1-88.9] | 88.6 [88.2-88.8] | 88.7 [88.5-88.9] | 89.5 [89.2-89.8] | 89.5 [89.1-89.8] | 0 | 0 | 0 | 0 |
| **Memory after turn 1 (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | | | | | | |
| Extension host RSS | 169.9 [168.8-172.2] | 170.4 [168.9-172.0] | 160.1 [159.1-163.0] | 102.6 [100.7-109.6] | 100.9 [99.4-107.1] | 170.3 [168.4-171.2] | 170.7 [169.2-171.4] | 160.5 [158.7-163.7] | 106.5 [101.1-108.6] | 105.1 [99.6-107.8] | 0 | 2 | 0 | 1 |
| Extension host PSS | 165.5 [163.6-167.8] | 165.4 [163.9-167.1] | 157.9 [157.0-160.8] | 100.4 [98.5-107.4] | 98.6 [97.2-104.9] | 165.5 [164.1-166.9] | 166.1 [164.8-167.1] | 158.3 [156.5-161.5] | 104.3 [98.8-106.3] | 102.8 [97.3-105.6] | 0 | 2 | 0 | 1 |
|   Pss_Anon | 114.2 [113.1-116.4] | 114.7 [113.2-116.1] | 96.2 [95.3-99.0] | 35.7 [33.6-42.6] | 33.8 [32.4-40.1] | 114.6 [112.7-115.5] | 115.0 [113.5-115.7] | 96.5 [94.7-99.8] | 39.5 [34.1-41.6] | 38.0 [32.5-40.8] | 0 | 5 | 0 | 4 |
|   Pss_File | 50.7 [50.3-51.5] | 50.7 [50.4-51.5] | 61.8 [61.7-61.8] | 64.8 [64.7-64.9] | 64.8 [64.8-65.0] | 51.4 [50.7-51.5] | 51.4 [50.4-51.5] | 61.7 [61.7-61.8] | 64.8 [64.7-64.9] | 64.8 [64.8-64.8] | 0 | 0 | 0 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | - | - | - | - |
|   Private_Dirty | 114.2 [113.1-116.4] | 114.7 [113.2-116.1] | 96.2 [95.3-99.0] | 35.7 [33.6-42.6] | 33.8 [32.4-40.1] | 114.6 [112.7-115.5] | 115.0 [113.5-115.7] | 96.5 [94.7-99.8] | 39.5 [34.1-41.6] | 38.0 [32.5-40.8] | 0 | 5 | 0 | 4 |
|   Private_Clean | 47.9 [47.8-49.4] | 47.9 [47.8-49.4] | 61.6 [61.6-61.6] | 64.6 [64.5-64.6] | 64.6 [64.6-64.7] | 49.3 [47.9-49.4] | 49.3 [47.8-49.4] | 61.6 [61.6-61.7] | 64.6 [64.5-64.6] | 64.6 [64.5-64.6] | 0 | 0 | 0 | 0 |
|   Shared_Clean+Shared_Dirty | 7.8 [6.4-7.9] | 7.8 [6.4-7.9] | 2.3 [2.3-2.4] | 2.5 [2.4-2.5] | 2.5 [2.4-2.5] | 6.4 [6.3-7.8] | 6.4 [6.3-7.9] | 2.3 [2.3-2.4] | 2.5 [2.4-2.5] | 2.5 [2.4-2.5] | 0 | 0 | 1 | 0 |
| Go host RSS | 96.3 [95.9-96.7] | 96.1 [95.9-96.6] | 96.6 [96.3-96.9] | 96.8 [96.3-97.1] | 96.7 [96.4-97.2] | 98.2 [97.8-98.8] | 98.3 [98.1-98.6] | 98.4 [98.2-98.6] | 99.2 [98.9-99.5] | 99.2 [98.9-99.5] | 0 | 0 | 0 | 0 |
| Go host PSS | 94.8 [94.4-95.2] | 94.6 [94.4-95.0] | 95.1 [94.7-95.4] | 95.3 [94.8-95.6] | 95.1 [94.9-95.7] | 96.7 [96.2-97.2] | 96.8 [96.6-97.0] | 96.9 [96.7-97.1] | 97.7 [97.3-98.0] | 97.7 [97.4-98.0] | 0 | 0 | 0 | 0 |
| **Memory after 20 turns (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | | | | | | |
| Extension host RSS | 182.3 [179.1-185.8] | 181.5 [179.3-184.9] | 164.6 [164.2-165.7] | 108.8 [106.9-112.6] | 107.2 [106.3-112.6] | 182.8 [178.5-187.3] | 182.5 [179.9-184.2] | 165.4 [163.1-168.2] | 112.1 [108.1-114.3] | 110.5 [106.7-112.9] | 0 | 1 | 0 | 1 |
| Extension host PSS | 177.2 [174.0-180.9] | 176.7 [174.2-180.5] | 162.5 [162.1-163.6] | 106.5 [104.7-110.3] | 105.0 [104.0-110.4] | 178.3 [174.1-182.9] | 177.3 [174.5-179.3] | 163.3 [161.0-166.0] | 109.8 [105.9-112.0] | 108.3 [104.4-110.7] | 0 | 1 | 1 | 1 |
|   Pss_Anon | 126.5 [123.3-130.0] | 125.7 [123.6-129.3] | 100.8 [100.4-101.7] | 41.7 [39.9-45.5] | 40.2 [39.1-45.6] | 127.1 [122.8-131.5] | 126.8 [124.1-128.6] | 101.6 [99.1-104.2] | 45.0 [41.1-47.3] | 43.5 [39.6-45.8] | 1 | 4 | 0 | 3 |
|   Pss_File | 51.1 [50.2-51.5] | 50.7 [50.4-51.5] | 61.8 [61.7-61.8] | 64.8 [64.8-64.9] | 64.9 [64.8-65.0] | 51.4 [50.7-51.5] | 50.8 [50.4-51.5] | 61.8 [61.7-61.8] | 64.8 [64.8-64.9] | 64.8 [64.7-64.9] | 1 | 0 | 1 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | - | - | - | - |
|   Private_Dirty | 126.5 [123.3-130.0] | 125.7 [123.6-129.3] | 100.8 [100.4-101.7] | 41.7 [39.9-45.5] | 40.2 [39.1-45.6] | 127.1 [122.8-131.5] | 126.8 [124.1-128.6] | 101.6 [99.1-104.2] | 45.0 [41.1-47.3] | 43.5 [39.6-45.8] | 1 | 4 | 0 | 3 |
|   Private_Clean | 48.6 [47.8-49.4] | 47.9 [47.8-49.4] | 61.6 [61.6-61.6] | 64.6 [64.6-64.6] | 64.6 [64.6-64.7] | 49.3 [47.8-49.4] | 47.9 [47.8-49.4] | 61.6 [61.6-61.7] | 64.6 [64.5-64.6] | 64.6 [64.5-64.6] | 1 | 0 | 3 | 0 |
|   Shared_Clean+Shared_Dirty | 7.1 [6.4-7.9] | 7.8 [6.4-7.9] | 2.3 [2.2-2.4] | 2.4 [2.4-2.5] | 2.5 [2.4-2.5] | 6.4 [6.3-7.8] | 7.8 [6.3-7.9] | 2.3 [2.3-2.4] | 2.4 [2.4-2.5] | 2.5 [2.4-2.5] | 9 | 0 | 20 | 1 |
| Go host RSS | 109.6 [108.9-111.2] | 110.1 [108.0-116.4] | 111.6 [109.3-117.2] | 110.5 [109.1-119.6] | 110.1 [109.4-114.7] | 114.0 [111.5-115.8] | 111.9 [109.4-118.6] | 114.2 [111.0-114.8] | 113.1 [111.6-117.7] | 112.2 [110.5-119.3] | 0 | 0 | 2 | 1 |
| Go host PSS | 108.1 [107.5-109.7] | 108.6 [106.6-114.8] | 110.0 [107.8-115.6] | 108.9 [107.6-118.0] | 108.5 [107.9-113.2] | 112.5 [109.9-114.3] | 110.4 [107.9-117.1] | 112.6 [109.5-113.3] | 111.6 [110.1-116.2] | 110.7 [109.0-117.8] | 0 | 0 | 2 | 1 |
| **Process** | | | | | | | | | | | | | | |
| Extension host threads after the last turn | 12 [12-12] | 12 [12-12] | 19 [18-19] | 7 [7-7] | 7 [7-7] | 12 [12-12] | 12 [12-12] | 19 [18-19] | 7 [7-7] | 7 [7-7] | 0 | 0 | 0 | 0 |
| Extension host executable size (MB) | 150.3 [150.3-150.3] | 150.3 [150.3-150.3] | 79.5 [79.5-79.5] | 245.1 [245.1-245.1] | 245.1 [245.1-245.1] | 150.3 [150.3-150.3] | 150.3 [150.3-150.3] | 79.5 [79.5-79.5] | 245.1 [245.1-245.1] | 245.1 [245.1-245.1] | 0 | 0 | 0 | 0 |

**Cold starts** (5 rounds per arm): the digest cache's record beside the executable is removed before the start, so the Go host hashes the Bun or aot executable (and, with the cache, records it). The timing table above is the warm start (record present). An arm whose Go host has no digest cache hashes every time, so its cold and warm cells agree.

| Metric | bun | aot | aot2 | bun-a | aot-a | aot-a2 | A/A aot-aot2 (%) | A/A aot-a-aot-a2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 5/5 | 5/5 | 5/5 | 5/5 | 5/5 | 5/5 | | |
| Launch to ready, cold (ms) | 1055 [1043-1134] | 650 [611-674] | 645 [612-670] | 1045 [1000-1051] | 639 [613-651] | 641 [613-650] | 1 | 0 |
| Go host CPU to ready, cold, user+sys (ms) | 280 [270-360] | 480 [440-490] | 480 [420-510] | 280 [250-290] | 470 [460-470] | 470 [460-470] | 0 | 0 |
| Digest records removed before the start | 0 [0-0] | 0 [0-0] | 0 [0-0] | 1 [1-1] | 1 [1-1] | 1 [1-1] | - | 0 |
| Digest record present after the start (1 = yes) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 1 [1-1] | 1 [1-1] | 1 [1-1] | - | 0 |

**Before / after** (medians of the timing pass; delta = after against before)

| Metric | node | node-a | delta % | bun | bun-a | delta % | aot | aot-a | delta % |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Launch to ready (ms) | 1183 | 1183 | +0 | 1052 | 960 | -9 | 664 | 362 | -45 |
| Go host CPU to ready, user+sys (ms) | 190 | 190 | +0 | 280 | 190 | -32 | 480 | 190 | -60 |
| Extension host CPU to ready, user+sys (ms) | 1340 | 1320 | -1 | 1150 | 1140 | -1 | 140 | 130 | -7 |
| Digest record present beside the executable after ready (1 = yes; Node: n/a) | n/a | n/a | - | 0 | 1 | - | 0 | 1 | - |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 | 0 | - | 0 | 0 | - | 30 | 30 | +0 |
| Turn 1 wall (ms) | 43 | 44 | +2 | 46 | 46 | -0 | 37 | 37 | +1 |
| Turn 2 wall (ms) | 19 | 19 | -1 | 17 | 18 | +5 | 19 | 17 | -6 |
| Steady wall, median of turns 6-20 (ms) | 15.6 | 14.6 | -6 | 13.0 | 13.1 | +1 | 14.1 | 14.2 | +1 |
| Turn 1 / steady wall (ratio) | 2.86 | 2.92 | +2 | 3.49 | 3.51 | +1 | 2.70 | 2.71 | +1 |
| Go host CPU, turn 1 (ms) | 30 | 30 | +0 | 30 | 30 | +0 | 30 | 25 | -17 |
| Extension host CPU, turn 1 (ms) | 30 | 30 | +0 | 40 | 40 | +0 | 10 | 15 | +50 |
| Go host CPU per turn, turns 2-20 (ms) | 12.1 | 12.1 | +0 | 11.6 | 11.6 | +0 | 12.1 | 12.1 | +0 |
| Extension host CPU per turn, turns 2-20 (ms) | 10.0 | 10.0 | +0 | 7.4 | 7.9 | +7 | 5.5 | 4.7 | -14 |
| Extension host minor faults to 0.5 s after ready | 30823 | 30906 | +0 | 39384 | 38895 | -1 | 12405 | 12404 | -0 |
| Extension host minor faults, turn 1 | 750 | 726 | -3 | 643 | 658 | +2 | 537 | 512 | -5 |
| Extension host minor faults per turn, turns 2-20 | 295.3 | 277.3 | -6 | 86.2 | 93.6 | +9 | 94.0 | 91.9 | -2 |
| Extension host RSS @0.5 s after ready | 167.1 | 167.7 | +0 | 161.4 | 162.7 | +1 | 101.5 | 106.0 | +4 |
| Extension host PSS @0.5 s after ready | 162.7 | 162.8 | +0 | 159.3 | 160.6 | +1 | 99.3 | 103.8 | +5 |
| Pss_Anon @0.5 s after ready | 111.4 | 112.1 | +1 | 97.5 | 98.9 | +1 | 34.7 | 39.2 | +13 |
| Pss_File @0.5 s after ready | 51.0 | 51.4 | +1 | 61.8 | 61.7 | -0 | 64.5 | 64.5 | +0 |
| Private_Dirty @0.5 s after ready | 111.4 | 112.1 | +1 | 97.5 | 98.9 | +1 | 34.7 | 39.2 | +13 |
| Private_Clean @0.5 s after ready | 48.6 | 49.3 | +1 | 61.6 | 61.6 | +0 | 64.2 | 64.3 | +0 |
| Go host RSS @0.5 s after ready | 87.8 | 90.0 | +2 | 88.0 | 90.2 | +2 | 88.4 | 91.0 | +3 |
| Extension host RSS @after turn 1 | 169.9 | 170.3 | +0 | 160.1 | 160.5 | +0 | 102.6 | 106.5 | +4 |
| Extension host PSS @after turn 1 | 165.5 | 165.5 | +0 | 157.9 | 158.3 | +0 | 100.4 | 104.3 | +4 |
| Pss_Anon @after turn 1 | 114.2 | 114.6 | +0 | 96.2 | 96.5 | +0 | 35.7 | 39.5 | +11 |
| Pss_File @after turn 1 | 50.7 | 51.4 | +1 | 61.8 | 61.7 | -0 | 64.8 | 64.8 | -0 |
| Private_Dirty @after turn 1 | 114.2 | 114.6 | +0 | 96.2 | 96.5 | +0 | 35.7 | 39.5 | +11 |
| Private_Clean @after turn 1 | 47.9 | 49.3 | +3 | 61.6 | 61.6 | +0 | 64.6 | 64.6 | -0 |
| Go host RSS @after turn 1 | 96.3 | 98.2 | +2 | 96.6 | 98.4 | +2 | 96.8 | 99.2 | +2 |
| Extension host RSS @after 20 turns | 182.3 | 182.8 | +0 | 164.6 | 165.4 | +0 | 108.8 | 112.1 | +3 |
| Extension host PSS @after 20 turns | 177.2 | 178.3 | +1 | 162.5 | 163.3 | +0 | 106.5 | 109.8 | +3 |
| Pss_Anon @after 20 turns | 126.5 | 127.1 | +0 | 100.8 | 101.6 | +1 | 41.7 | 45.0 | +8 |
| Pss_File @after 20 turns | 51.1 | 51.4 | +1 | 61.8 | 61.8 | -0 | 64.8 | 64.8 | -0 |
| Private_Dirty @after 20 turns | 126.5 | 127.1 | +0 | 100.8 | 101.6 | +1 | 41.7 | 45.0 | +8 |
| Private_Clean @after 20 turns | 48.6 | 49.3 | +1 | 61.6 | 61.6 | +0 | 64.6 | 64.6 | -0 |
| Go host RSS @after 20 turns | 109.6 | 114.0 | +4 | 111.6 | 114.2 | +2 | 110.5 | 113.1 | +2 |
| Extension host executable size (MB) | 150.3 | 150.3 | +0 | 79.5 | 79.5 | +0 | 245.1 | 245.1 | +0 |

**/words hot loop (the word-count extension, 16.8 MB file, once per run after the turns)**

| Metric | node | node2 | bun | aot | aot2 | node-a | node-a2 | bun-a | aot-a | aot-a2 | A/A node-node2 (%) | A/A aot-aot2 (%) | A/A node-a-node-a2 (%) | A/A aot-a-aot-a2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | | | | |
| /words in the extension (ms) | 145 [143-147] | 144 [144-146] | 105 [103-106] | 121 [118-127] | 122 [120-152] | 145 [144-148] | 145 [144-147] | 104 [103-113] | 114 [113-116] | 116 [113-122] | 1 | 1 | 0 | 1 |
| /words wall from prompt (ms) | 180 [175-183] | 179 [175-182] | 127 [125-130] | 141 [136-147] | 141 [139-174] | 180 [176-185] | 182 [178-185] | 126 [121-135] | 133 [130-136] | 136 [131-140] | 0 | 1 | 1 | 2 |

Allocation, strace pass (3 rounds; the same run under `strace -f --seccomp-bpf -e trace=memory,process`; counts per process class, `ext` = every process the Go host started: the `--version` start checks and the extension host). Cells median [min-max].

Processes seen by strace (executable: count of processes, first run per arm): aot: {'pig': 1, 'pig-ts-host': 2, 'node': 1, 'git': 1}; aot-a: {'pig-after': 1, 'pig-ts-host': 2, 'node': 1, 'git': 1}; bun: {'pig': 1, 'bun': 2, 'git': 1}; bun-a: {'pig-after': 1, 'bun': 2, 'git': 1}; node: {'pig': 1, 'node': 2, 'git': 1}; node-a: {'pig-after': 1, 'node': 2, 'git': 1}

| Metric | node | bun | aot | node-a | bun-a | aot-a |
|---|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 |
| **strace: memory syscalls, Extension host (and its start-check children), launch to ready** | | | | | | |
| mmap | 456 [455-457] | 523 [522-523] | 190 [190-191] | 454 [453-455] | 522 [522-522] | 191 [190-191] |
| munmap | 343 [342-345] | 9 [8-9] | 86 [85-87] | 342 [341-342] | 9 [8-9] | 85 [84-86] |
| brk | 51 [51-53] | 6 [6-6] | 24 [24-24] | 52 [51-52] | 6 [6-6] | 24 [24-24] |
| mprotect | 689 [630-706] | 50 [50-50] | 74 [74-74] | 649 [591-658] | 50 [49-50] | 74 [74-74] |
| madvise | 328 [327-328] | 1755 [1291-2053] | 184 [184-185] | 324 [324-327] | 1703 [1192-2024] | 183 [182-186] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 2686.7 [2623.2-2687.3] | 11751.5 [11751.5-11751.5] | 36835.6 [36835.6-36835.6] | 2685.8 [2685.3-2687.5] | 11751.5 [11751.0-11751.5] | 36835.6 [36835.6-36835.6] |
| **strace: memory syscalls, Extension host (and its start-check children), turn 1** | | | | | | |
| mmap | 3 [3-3] | 0 [0-0] | 0 [0-0] | 3 [3-3] | 0 [0-0] | 0 [0-0] |
| munmap | 2 [2-3] | 0 [0-0] | 0 [0-0] | 2 [2-2] | 0 [0-0] | 0 [0-0] |
| brk | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| mprotect | 36 [36-37] | 0 [0-0] | 1 [1-1] | 36 [35-37] | 0 [0-0] | 1 [1-1] |
| madvise | 2 [2-2] | 23 [22-39] | 0 [0-1] | 2 [2-2] | 23 [23-32] | 0 [0-2] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 65.0 [65.0-129.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 65.0 [65.0-65.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| **strace: memory syscalls, Extension host (and its start-check children), per turn, turns 2-20** | | | | | | |
| mmap | 0.2 [0.2-0.2] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.2 [0.2-0.2] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| munmap | 0.1 [0.1-0.2] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.1 [0.1-0.2] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| brk | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| mprotect | 9.6 [9.3-10.5] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 10.1 [9.7-12.2] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| madvise | 0.1 [0.1-0.1] | 7.2 [6.7-9.6] | 1.5 [1.1-3.0] | 0.1 [0.1-0.1] | 7.3 [6.7-7.6] | 1.3 [0.7-3.2] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 10.1 [10.1-10.2] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 10.1 [10.1-10.2] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| **strace: memory syscalls, Go host, launch to ready** | | | | | | |
| mmap | 81 [80-84] | 81 [80-81] | 78 [78-80] | 81 [80-81] | 81 [78-84] | 79 [79-82] |
| munmap | 9 [9-11] | 8 [8-10] | 8 [8-9] | 9 [9-9] | 10 [8-11] | 9 [8-9] |
| brk | 3 [3-3] | 3 [3-3] | 3 [3-3] | 3 [3-3] | 3 [3-3] | 3 [3-3] |
| mprotect | 22 [22-24] | 22 [22-22] | 22 [20-22] | 22 [22-22] | 22 [20-24] | 22 [20-22] |
| madvise | 2 [2-2] | 2 [2-2] | 2 [2-2] | 2 [2-2] | 2 [2-2] | 2 [2-2] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 2416.2 [2412.2-2552.4] | 2416.2 [2352.4-2480.2] | 2351.9 [2343.9-2412.2] | 2416.2 [2412.2-2416.2] | 2416.2 [2343.9-2552.4] | 2352.1 [2280.2-2420.2] |
| **strace: memory syscalls, Go host, turn 1** | | | | | | |
| mmap | 3 [3-3] | 3 [2-3] | 3 [3-4] | 3 [2-3] | 2 [2-3] | 3 [2-3] |
| munmap | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| brk | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| mprotect | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| madvise | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 8.2 [8.2-8.2] | 8.2 [4.2-8.2] | 8.2 [8.2-68.3] | 8.2 [4.2-8.2] | 4.2 [4.2-8.2] | 8.2 [4.2-8.2] |
| **strace: memory syscalls, Go host, per turn, turns 2-20** | | | | | | |
| mmap | 0.4 [0.4-0.4] | 0.4 [0.4-0.5] | 0.4 [0.4-0.4] | 0.4 [0.4-0.4] | 0.4 [0.4-0.7] | 0.4 [0.4-0.6] |
| munmap | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| brk | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| mprotect | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| madvise | 0.0 [0.0-0.9] | 0.0 [0.0-1.0] | 0.4 [0.2-1.1] | 0.9 [0.0-1.1] | 0.0 [0.0-0.3] | 0.0 [0.0-0.5] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 0.7 [0.7-0.9] | 0.7 [0.7-4.2] | 0.7 [0.7-0.9] | 0.7 [0.7-0.9] | 0.9 [0.9-4.7] | 0.9 [0.7-4.5] |

Allocation, heap pass (3 rounds; the set plus `basket-probe.ts`, which reads the runtime's own heap numbers). Cells median [min-max].

| Metric | node | bun | aot | node-a | bun-a | aot-a |
|---|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 |
| **JS heap allocation (probe pass; Node: v8 total_allocated_bytes, Bun and aot: JSC GC log; a forced GC at each probe point)** | | | | | | |
| Allocated, launch to ready (MB) | 355.8 [353.3-355.9] | 137.8 [128.4-147.5] | 32.9 [32.9-32.9] | 360.9 [353.9-365.3] | 133.1 [126.7-133.2] | 32.9 [31.6-33.0] |
| Allocated, turn 1 incl. the probe (MB) | 3.3 [3.3-3.6] | 12.1 [11.2-31.5] | 4.7 [4.7-4.7] | 3.3 [3.1-3.9] | 15.5 [12.9-31.5] | 4.7 [4.6-4.7] |
| Allocated per turn, turns 2-20 (MB) | 0.67 [0.57-0.71] | 1.08 [0.57-1.13] | 0.32 [0.32-0.32] | 0.64 [0.56-0.69] | 1.08 [1.07-1.08] | 0.32 [0.32-0.32] |
| GC cycles over the run (JSC log; Node: PerformanceObserver gc entries) | 3 [3-3] | 17 [16-17] | 5 [5-5] | 3 [3-3] | 16 [15-16] | 5 [5-5] |
| **JS heap live after a forced GC, at ready** | | | | | | |
| process.memoryUsage().heapUsed (MB) | 38.0 [38.0-38.0] | 24.7 [24.7-24.7] | 9.6 [9.6-9.6] | 38.0 [38.0-38.0] | 24.7 [24.7-24.8] | 9.6 [9.6-9.6] |
| process.memoryUsage().external + arrayBuffers (MB) | 3.9 [3.9-4.2] | 10.5 [10.5-10.5] | 1.2 [1.2-1.2] | 3.9 [3.9-3.9] | 10.5 [10.5-10.5] | 1.2 [1.2-1.2] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | 236620 [236598-236621] | 158320 [158320-158372] | n/a | 236618 [236601-236629] | 158372 [158322-158377] |
| JSC heapSize (MB; Node: n/a) | n/a | 24.7 [24.7-24.7] | 9.6 [9.6-9.6] | n/a | 24.7 [24.7-24.8] | 9.6 [9.6-9.6] |
| **JS heap live after a forced GC, after turn 1** | | | | | | |
| process.memoryUsage().heapUsed (MB) | 38.3 [38.3-38.3] | 25.4 [25.4-25.4] | 10.1 [10.1-10.1] | 38.3 [38.3-38.3] | 25.4 [25.4-25.4] | 10.1 [10.1-10.1] |
| process.memoryUsage().external + arrayBuffers (MB) | 3.9 [3.9-3.9] | 11.1 [11.1-11.1] | 1.6 [1.6-1.6] | 3.9 [3.9-3.9] | 11.1 [11.1-11.1] | 1.6 [1.6-1.6] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | 238586 [238584-238599] | 159839 [159837-159841] | n/a | 238617 [238602-238643] | 159858 [159845-159863] |
| JSC heapSize (MB; Node: n/a) | n/a | 25.4 [25.4-25.4] | 10.1 [10.1-10.1] | n/a | 25.4 [25.4-25.4] | 10.1 [10.1-10.1] |
| **JS heap live after a forced GC, after 20 turns** | | | | | | |
| process.memoryUsage().heapUsed (MB) | 38.8 [38.8-38.8] | 26.1 [26.1-26.1] | 10.3 [10.3-10.3] | 38.8 [38.8-38.8] | 26.1 [26.1-26.1] | 10.3 [10.3-10.3] |
| process.memoryUsage().external + arrayBuffers (MB) | 4.1 [4.1-4.1] | 11.5 [11.5-11.5] | 1.6 [1.6-1.6] | 4.1 [4.1-4.1] | 11.5 [11.5-11.6] | 1.6 [1.6-1.6] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | 242916 [242886-242928] | 164094 [164065-164098] | n/a | 242951 [242897-242966] | 164113 [164089-164118] |
| JSC heapSize (MB; Node: n/a) | n/a | 26.1 [26.1-26.1] | 10.3 [10.3-10.3] | n/a | 26.1 [26.1-26.1] | 10.3 [10.3-10.3] |

## Set `ten`

Timing pass: 12 interleaved rounds (order rotated each round, one recorded warm-up run per arm before them), cells are median [p10-p90] over rounds (fewer than 7 runs: median [min-max]), A/A columns are |median(a)-median(b)| / their mean.

Proof each arm ran its tier: every run's extension-host `/proc/<pid>/exe` is checked (node: a node binary; bun: the slot's bun; aot: `<slot>/aot/images/<key>/pig-ts-host`) and a run that differs is an error, not a number. Executables seen: node: /opt/node/26.12.1/bin/node; node2: /opt/node/26.12.1/bin/node; bun: <scratch>/basket/slot-pin/bun/bun; aot: <scratch>/basket/slot-pin/aot/images/a7cd851ad062fb1d-1794009310918408967/pig-ts-host; aot2: <scratch>/basket/slot-pin/aot/images/a7cd851ad062fb1d-1794009310918408967/pig-ts-host; node-a: /opt/node/26.12.1/bin/node; node-a2: /opt/node/26.12.1/bin/node; bun-a: <scratch>/basket/slot-after/bun/bun; aot-a: <scratch>/basket/slot-after/aot/images/a7cd851ad062fb1d-1794012959093885020/pig-ts-host; aot-a2: <scratch>/basket/slot-after/aot/images/a7cd851ad062fb1d-1794012959093885020/pig-ts-host

| Metric | node | node2 | bun | aot | aot2 | node-a | node-a2 | bun-a | aot-a | aot-a2 | A/A node-node2 (%) | A/A aot-aot2 (%) | A/A node-a-node-a2 (%) | A/A aot-a-aot-a2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | | | | |
| **Startup (launch to ready: get_commands answered with the set's commands)** | | | | | | | | | | | | | | |
| Launch to ready (ms) | 434 [408-458] | 431 [393-453] | 506 [445-552] | 598 [518-614] | 596 [579-651] | 440 [419-466] | 430 [395-438] | 406 [368-479] | 331 [318-338] | 323 [287-341] | 1 | 0 | 2 | 2 |
| Go host CPU to ready, user+sys (ms) | 190 [190-209] | 190 [190-209] | 290 [280-309] | 460 [383-479] | 470 [424-490] | 195 [162-209] | 190 [154-200] | 195 [151-227] | 200 [190-200] | 190 [151-209] | 0 | 2 | 3 | 5 |
|   Go host user (ms) | 150 [140-160] | 150 [150-160] | 230 [221-240] | 380 [303-400] | 395 [354-410] | 160 [122-160] | 150 [113-160] | 160 [111-170] | 150 [141-160] | 155 [121-160] | 0 | 4 | 6 | 3 |
|   Go host sys (ms) | 40 [40-50] | 40 [40-40] | 60 [51-60] | 80 [71-90] | 70 [70-80] | 40 [31-40] | 40 [40-50] | 40 [30-49] | 40 [31-50] | 40 [30-49] | 0 | 13 | 0 | 0 |
| Go host: CPU of reaped children by ready, the `--version` start checks (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-10] | 0 [0-10] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-9] | 0 [0-0] | - | - | - | - |
| Extension host CPU to ready, user+sys (ms) | 370 [350-379] | 355 [311-388] | 345 [303-359] | 100 [91-110] | 100 [100-128] | 375 [351-390] | 360 [331-370] | 345 [321-380] | 100 [90-110] | 100 [90-100] | 4 | 0 | 4 | 0 |
|   extension host user (ms) | 310 [291-329] | 300 [261-329] | 275 [233-290] | 65 [51-80] | 70 [60-88] | 310 [291-330] | 305 [272-310] | 275 [260-310] | 70 [60-79] | 60 [50-70] | 3 | 7 | 2 | 15 |
|   extension host sys (ms) | 50 [50-60] | 55 [50-60] | 70 [60-70] | 40 [30-40] | 40 [30-40] | 70 [51-79] | 55 [50-60] | 70 [60-80] | 30 [30-40] | 40 [30-49] | 10 | 0 | 24 | 29 |
| Digest record present beside the executable after ready (1 = yes; Node: n/a) | n/a | n/a | 0 [0-0] | 0 [0-0] | 0 [0-0] | n/a | n/a | 1 [1-1] | 1 [1-1] | 1 [1-1] | - | - | - | 0 |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 30 [30-40] | 30 [30-40] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 30 [30-30] | 30 [30-40] | - | 0 | - | 0 |
| Extension host CPU, ready to 0.5 s later (idle after ready) (ms) | 0 [0-10] | 0 [0-9] | 10 [0-10] | 0 [0-9] | 0 [0-9] | 0 [0-19] | 0 [0-0] | 5 [0-19] | 0 [0-0] | 0 [0-10] | - | - | - | - |
| **Turns (a prompt, one model tool call, the tool, a final answer; 20 per run)** | | | | | | | | | | | | | | |
| Turn 1 wall (ms) | 47 [45-52] | 46 [41-52] | 42 [39-51] | 41 [36-44] | 37 [35-42] | 49 [45-58] | 45 [43-51] | 42 [38-54] | 41 [36-43] | 38 [35-42] | 1 | 9 | 8 | 8 |
| Turn 2 wall (ms) | 40 [33-43] | 39 [35-42] | 31 [28-40] | 31 [29-38] | 28 [25-33] | 39 [35-43] | 38 [34-42] | 30 [28-37] | 32 [29-41] | 31 [28-62] | 3 | 11 | 3 | 3 |
| Steady wall, median of turns 6-20 (ms) | 39.6 [38.1-41.8] | 41.7 [40.1-46.7] | 33.3 [31.8-36.3] | 33.0 [30.2-35.4] | 33.0 [29.3-34.4] | 39.3 [37.3-42.8] | 39.8 [38.8-42.0] | 31.8 [29.4-33.8] | 33.9 [29.6-38.9] | 32.9 [31.0-35.3] | 5 | 0 | 1 | 3 |
| Turn 1 / steady wall (ratio) | 1.17 [1.07-1.34] | 1.09 [0.93-1.33] | 1.27 [1.13-1.50] | 1.25 [1.07-1.31] | 1.13 [1.05-1.38] | 1.23 [1.07-1.45] | 1.11 [1.08-1.22] | 1.33 [1.18-1.63] | 1.20 [1.11-1.37] | 1.18 [1.01-1.32] | 7 | 10 | 10 | 2 |
| **CPU per phase (user+sys, 10 ms ticks; the first turn is measured with 0.5 s settled before and 0.2 s after)** | | | | | | | | | | | | | | |
| Go host CPU, turn 1 (ms) | 20 [20-30] | 30 [20-30] | 30 [20-30] | 25 [20-30] | 20 [20-30] | 30 [20-30] | 25 [20-30] | 25 [20-30] | 30 [20-39] | 30 [20-30] | 40 | 22 | 18 | 0 |
| Extension host CPU, turn 1 (ms) | 45 [40-50] | 50 [41-59] | 65 [51-90] | 20 [20-30] | 20 [20-30] | 50 [40-60] | 50 [40-59] | 70 [51-90] | 30 [20-30] | 20 [10-30] | 11 | 0 | 0 | 40 |
| Go host CPU per turn, turns 2-20 (ms) | 17.9 [16.9-18.9] | 18.4 [17.4-19.9] | 17.6 [16.4-19.4] | 18.2 [15.5-18.9] | 16.8 [16.3-17.8] | 18.4 [17.4-19.9] | 17.9 [17.4-18.9] | 17.4 [16.4-20.3] | 17.9 [16.4-20.8] | 17.6 [16.8-18.4] | 3 | 8 | 3 | 1 |
| Extension host CPU per turn, turns 2-20 (ms) | 41.6 [40.5-46.2] | 40.5 [38.5-46.5] | 35.8 [33.7-38.4] | 24.7 [22.2-27.3] | 24.7 [23.2-29.6] | 42.4 [39.6-44.2] | 41.1 [39.5-43.6] | 34.5 [33.2-39.5] | 25.3 [23.2-28.7] | 25.0 [23.2-26.8] | 3 | 0 | 3 | 1 |
|   extension host sys per turn, turns 2-20 (ms) | 6.3 [5.3-7.3] | 5.8 [5.3-6.3] | 8.9 [8.4-9.4] | 5.8 [5.3-6.3] | 5.8 [5.3-6.3] | 6.1 [5.8-7.4] | 6.1 [4.8-6.8] | 8.7 [7.4-9.9] | 5.8 [5.3-6.3] | 5.8 [5.3-6.3] | 9 | 0 | 0 | 0 |
| **Page faults (cumulative counters of the process; extension host = its process tree)** | | | | | | | | | | | | | | |
| Extension host minor faults to 0.5 s after ready | 16698 [16178-17080] | 16348 [15971-16938] | 16556 [16289-16672] | 12990 [12568-13410] | 13043 [12625-13399] | 16833 [16127-17292] | 16654 [15899-17036] | 16598 [16487-16687] | 12870 [12599-13406] | 12954 [12614-13419] | 2 | 0 | 1 | 1 |
| Extension host major faults to 0.5 s after ready | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | - | - | - | - |
| Extension host minor faults, turn 1 | 1982 [720-2054] | 2092 [1519-2121] | 1760 [1325-2103] | 1836 [1767-1886] | 1819 [1762-1865] | 2052 [1096-2092] | 2059 [429-2137] | 1842 [1310-2105] | 1778 [1398-1858] | 1770 [1291-1852] | 5 | 1 | 0 | 0 |
| Extension host minor faults per turn, turns 2-20 | 1042.7 [937.0-1134.6] | 1030.0 [936.4-1108.6] | 1439.7 [1333.4-1493.2] | 1072.6 [1021.4-1139.1] | 1080.1 [1037.2-1164.2] | 1041.7 [966.8-1128.4] | 1062.6 [926.7-1128.2] | 1404.0 [1340.9-1435.2] | 1056.9 [1014.2-1102.4] | 1050.7 [990.3-1103.7] | 1 | 1 | 2 | 1 |
| Extension host major faults, whole run to the last turn | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | - | - | - | - |
| Go host minor faults to 0.5 s after ready | 13242 [13166-13378] | 13288 [13178-13330] | 13344 [13311-13380] | 13337 [13277-13381] | 13387 [13288-13406] | 13220 [13144-13281] | 13202 [13128-13276] | 13250 [13181-13389] | 13433 [13381-13495] | 13432 [13389-13530] | 0 | 0 | 0 | 0 |
| Go host minor faults per turn, turns 2-20 | 248.6 [216.4-385.2] | 262.0 [225.7-332.6] | 265.0 [241.6-340.2] | 257.7 [243.9-291.3] | 256.1 [229.6-365.4] | 262.0 [238.4-382.1] | 260.6 [237.5-370.3] | 312.7 [239.3-386.4] | 266.6 [249.7-354.9] | 256.9 [229.1-314.1] | 5 | 1 | 1 | 4 |
| **Memory 0.5 s after ready (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | | | | | | |
| Extension host RSS | 110.8 [109.4-112.9] | 109.7 [106.3-112.5] | 97.1 [96.2-98.2] | 97.9 [96.3-99.6] | 98.1 [96.6-99.5] | 111.4 [109.2-112.9] | 110.4 [108.2-112.8] | 97.2 [96.0-97.9] | 97.5 [96.3-99.5] | 97.8 [96.5-99.4] | 1 | 0 | 1 | 0 |
| Extension host PSS | 105.4 [85.6-108.7] | 104.5 [102.1-108.3] | 94.8 [94.0-96.0] | 95.6 [94.1-97.3] | 95.8 [94.2-97.2] | 106.4 [104.1-108.8] | 105.5 [103.0-108.5] | 95.0 [93.8-95.7] | 95.2 [94.1-97.2] | 95.5 [94.1-97.2] | 1 | 0 | 1 | 0 |
|   Pss_Anon | 58.3 [56.9-60.4] | 57.2 [53.9-59.9] | 57.1 [56.4-58.2] | 46.6 [45.0-48.3] | 46.7 [45.2-48.2] | 58.9 [56.7-60.4] | 57.9 [55.7-60.2] | 57.3 [56.1-58.1] | 46.2 [45.1-48.2] | 46.4 [45.1-48.2] | 2 | 0 | 2 | 0 |
|   Pss_File | 47.6 [28.6-48.4] | 47.6 [47.0-48.4] | 37.7 [37.6-37.8] | 49.0 [49.0-49.1] | 49.0 [49.0-49.2] | 47.6 [47.1-48.4] | 47.9 [47.2-48.4] | 37.7 [37.6-37.7] | 49.0 [49.0-49.1] | 49.0 [49.0-49.1] | 0 | 0 | 1 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | - | - | - | - |
|   Private_Dirty | 58.3 [56.9-60.4] | 57.2 [53.9-59.9] | 57.1 [56.4-58.2] | 46.6 [45.0-48.3] | 46.7 [45.2-48.2] | 58.9 [56.7-60.4] | 57.9 [55.7-60.2] | 57.3 [56.1-58.1] | 46.2 [45.1-48.2] | 46.4 [45.1-48.2] | 2 | 0 | 2 | 0 |
|   Private_Clean | 44.8 [8.5-46.3] | 44.8 [44.8-46.3] | 37.5 [37.5-37.6] | 48.8 [48.8-48.8] | 48.8 [48.8-48.9] | 44.9 [44.8-46.3] | 45.5 [44.8-46.3] | 37.5 [37.5-37.5] | 48.8 [48.8-48.9] | 48.8 [48.8-48.8] | 0 | 0 | 2 | 0 |
|   Shared_Clean+Shared_Dirty | 7.7 [6.1-44.0] | 7.7 [6.3-7.8] | 2.3 [2.3-2.4] | 2.5 [2.4-2.6] | 2.5 [2.5-2.6] | 7.6 [6.1-7.7] | 7.0 [6.2-7.8] | 2.4 [2.3-2.5] | 2.5 [2.4-2.6] | 2.5 [2.5-2.6] | 0 | 2 | 9 | 0 |
| Go host RSS | 87.6 [87.5-87.8] | 87.7 [87.3-87.9] | 88.1 [87.8-88.2] | 87.8 [87.6-88.1] | 87.9 [87.8-88.4] | 89.9 [89.6-90.0] | 89.9 [89.4-90.1] | 89.9 [89.7-90.4] | 90.7 [90.5-90.9] | 90.7 [90.5-91.0] | 0 | 0 | 0 | 0 |
| Go host PSS | 86.1 [85.9-86.3] | 86.1 [85.7-86.3] | 86.5 [86.2-86.6] | 86.3 [86.1-86.6] | 86.4 [86.3-86.9] | 88.4 [88.0-88.5] | 88.3 [87.9-88.5] | 88.4 [88.1-88.9] | 89.2 [88.9-89.4] | 89.2 [89.0-89.4] | 0 | 0 | 0 | 0 |
| **Memory after turn 1 (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | | | | | | |
| Extension host RSS | 117.5 [113.9-120.5] | 117.6 [112.3-120.4] | 94.3 [92.9-99.2] | 105.0 [103.6-106.8] | 105.4 [104.1-106.8] | 118.5 [114.8-120.1] | 117.1 [111.3-120.1] | 94.0 [92.7-95.3] | 103.7 [95.4-106.5] | 103.7 [96.0-105.2] | 0 | 0 | 1 | 0 |
| Extension host PSS | 112.5 [90.2-116.4] | 112.2 [107.0-116.1] | 92.0 [90.8-97.0] | 102.7 [101.4-104.5] | 103.1 [101.7-104.5] | 113.5 [110.0-116.1] | 112.6 [106.6-114.8] | 91.8 [90.5-93.0] | 101.5 [93.1-104.2] | 101.3 [93.8-102.9] | 0 | 0 | 1 | 0 |
|   Pss_Anon | 64.9 [61.3-68.0] | 65.0 [59.7-67.7] | 54.3 [53.0-59.3] | 53.4 [52.1-55.2] | 53.8 [52.4-55.2] | 65.9 [62.2-67.6] | 64.4 [58.6-67.3] | 54.0 [52.7-55.3] | 52.1 [43.8-54.8] | 52.0 [44.5-53.5] | 0 | 1 | 2 | 0 |
|   Pss_File | 47.7 [28.7-48.5] | 47.7 [47.1-48.5] | 37.8 [37.7-37.8] | 49.3 [49.3-49.4] | 49.3 [49.3-49.4] | 47.7 [47.2-48.5] | 48.4 [47.3-48.6] | 37.7 [37.7-37.8] | 49.3 [49.3-49.4] | 49.3 [49.3-49.4] | 0 | 0 | 1 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | - | - | - | - |
|   Private_Dirty | 64.9 [61.3-68.0] | 65.0 [59.7-67.7] | 54.3 [53.0-59.3] | 53.4 [52.1-55.2] | 53.8 [52.4-55.2] | 65.9 [62.2-67.6] | 64.4 [58.6-67.3] | 54.0 [52.7-55.3] | 52.1 [43.8-54.8] | 52.0 [44.5-53.5] | 0 | 1 | 2 | 0 |
|   Private_Clean | 44.9 [8.5-46.4] | 44.9 [44.9-46.4] | 37.6 [37.6-37.6] | 49.1 [49.1-49.1] | 49.1 [49.1-49.1] | 44.9 [44.9-46.4] | 46.4 [44.9-46.5] | 37.6 [37.6-37.6] | 49.1 [49.1-49.1] | 49.1 [49.1-49.1] | 0 | 0 | 3 | 0 |
|   Shared_Clean+Shared_Dirty | 7.7 [6.2-44.1] | 7.7 [6.2-7.8] | 2.4 [2.3-2.4] | 2.5 [2.4-2.6] | 2.5 [2.5-2.6] | 7.7 [6.1-7.7] | 6.3 [6.1-7.8] | 2.4 [2.3-2.4] | 2.5 [2.4-2.6] | 2.5 [2.5-2.6] | 0 | 2 | 20 | 0 |
| Go host RSS | 94.4 [94.1-94.5] | 94.3 [94.1-94.6] | 94.5 [94.4-94.9] | 94.6 [94.3-94.9] | 94.6 [94.5-94.8] | 96.3 [96.1-96.5] | 96.2 [95.8-96.5] | 96.3 [96.1-96.7] | 97.1 [96.9-97.3] | 97.2 [96.9-97.3] | 0 | 0 | 0 | 0 |
| Go host PSS | 92.9 [92.5-93.0] | 92.8 [92.6-93.0] | 93.0 [92.9-93.3] | 93.1 [92.7-93.3] | 93.1 [92.9-93.3] | 94.8 [94.5-95.0] | 94.7 [94.3-94.9] | 94.8 [94.5-95.1] | 95.6 [95.3-95.8] | 95.6 [95.4-95.8] | 0 | 0 | 0 | 0 |
| **Memory after 20 turns (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | | | | | | |
| Extension host RSS | 196.4 [182.9-201.4] | 194.7 [183.5-197.0] | 147.2 [139.3-151.8] | 134.2 [124.7-140.1] | 128.6 [116.6-140.2] | 194.4 [184.4-203.4] | 193.1 [181.4-203.2] | 146.0 [140.8-151.4] | 128.1 [119.0-136.3] | 125.3 [119.0-142.9] | 1 | 4 | 1 | 2 |
| Extension host PSS | 190.8 [176.2-196.1] | 188.9 [177.7-192.8] | 145.0 [137.0-149.6] | 131.9 [122.3-137.9] | 126.3 [114.3-137.9] | 189.8 [179.1-198.4] | 187.7 [174.4-199.0] | 143.8 [138.6-149.1] | 125.8 [116.8-133.9] | 123.0 [116.7-140.6] | 1 | 4 | 1 | 2 |
|   Pss_Anon | 143.7 [130.2-148.7] | 142.1 [130.9-144.3] | 104.4 [96.5-108.9] | 82.5 [72.9-88.5] | 76.9 [64.9-88.5] | 141.8 [131.7-150.7] | 140.4 [128.8-150.6] | 103.1 [98.0-108.4] | 76.5 [67.4-84.6] | 73.6 [67.3-91.2] | 1 | 7 | 1 | 4 |
|   Pss_File | 47.6 [47.2-48.6] | 47.8 [47.2-48.5] | 40.6 [40.6-40.7] | 49.4 [49.3-49.5] | 49.4 [49.4-49.4] | 47.6 [47.3-48.6] | 48.2 [47.2-48.6] | 40.7 [40.6-40.7] | 49.4 [49.4-49.5] | 49.4 [49.4-49.5] | 0 | 0 | 1 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | - | - | - | - |
|   Private_Dirty | 143.7 [130.2-148.7] | 142.1 [130.9-144.3] | 104.4 [96.5-108.9] | 82.5 [72.9-88.5] | 76.9 [64.9-88.5] | 141.8 [131.7-150.7] | 140.4 [128.8-150.6] | 103.1 [98.0-108.4] | 76.5 [67.4-84.6] | 73.6 [67.3-91.2] | 1 | 7 | 1 | 4 |
|   Private_Clean | 45.0 [45.0-46.5] | 45.0 [45.0-46.3] | 40.5 [40.5-40.5] | 49.2 [49.1-49.3] | 49.1 [49.1-49.2] | 45.0 [45.0-46.5] | 45.7 [45.0-46.5] | 40.5 [40.5-40.5] | 49.1 [49.1-49.2] | 49.1 [49.1-49.2] | 0 | 0 | 2 | 0 |
|   Shared_Clean+Shared_Dirty | 7.6 [6.2-7.7] | 7.7 [6.3-7.8] | 2.4 [2.3-2.4] | 2.5 [2.4-2.6] | 2.5 [2.5-2.6] | 7.7 [6.1-7.8] | 7.0 [6.1-7.8] | 2.4 [2.3-2.4] | 2.5 [2.4-2.6] | 2.5 [2.5-2.6] | 1 | 1 | 9 | 0 |
| Go host RSS | 110.5 [109.2-116.9] | 110.0 [108.4-112.8] | 112.7 [110.0-114.3] | 111.0 [110.3-114.2] | 111.1 [110.5-117.8] | 112.7 [111.2-117.8] | 112.2 [111.4-116.8] | 116.2 [113.6-120.8] | 113.7 [113.0-116.0] | 114.2 [112.7-117.3] | 0 | 0 | 0 | 0 |
| Go host PSS | 108.9 [107.7-115.4] | 108.5 [106.9-111.3] | 111.2 [108.6-112.8] | 109.5 [108.9-112.6] | 109.6 [109.1-116.2] | 111.3 [109.7-116.2] | 110.7 [109.8-115.2] | 114.6 [112.1-119.2] | 112.2 [111.4-114.5] | 112.7 [111.2-115.7] | 0 | 0 | 1 | 0 |
| **Process** | | | | | | | | | | | | | | |
| Extension host threads after the last turn | 12 [12-12] | 12 [12-12] | 19 [18-19] | 7 [7-7] | 7 [7-7] | 12 [12-12] | 12 [12-12] | 19 [18-19] | 7 [7-7] | 7 [7-7] | 0 | 0 | 0 | 0 |
| Extension host executable size (MB) | 150.3 [150.3-150.3] | 150.3 [150.3-150.3] | 79.5 [79.5-79.5] | 240.1 [240.1-240.1] | 240.1 [240.1-240.1] | 150.3 [150.3-150.3] | 150.3 [150.3-150.3] | 79.5 [79.5-79.5] | 240.1 [240.1-240.1] | 240.1 [240.1-240.1] | 0 | 0 | 0 | 0 |

**Cold starts** (5 rounds per arm): the digest cache's record beside the executable is removed before the start, so the Go host hashes the Bun or aot executable (and, with the cache, records it). The timing table above is the warm start (record present). An arm whose Go host has no digest cache hashes every time, so its cold and warm cells agree.

| Metric | bun | aot | aot2 | bun-a | aot-a | aot-a2 | A/A aot-aot2 (%) | A/A aot-a-aot-a2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 5/5 | 5/5 | 5/5 | 5/5 | 5/5 | 5/5 | | |
| Launch to ready, cold (ms) | 468 [440-509] | 560 [461-638] | 582 [452-622] | 448 [377-546] | 514 [461-602] | 571 [506-601] | 4 | 10 |
| Go host CPU to ready, cold, user+sys (ms) | 280 [260-290] | 430 [340-520] | 460 [320-490] | 260 [200-350] | 390 [330-470] | 430 [380-470] | 7 | 10 |
| Digest records removed before the start | 0 [0-0] | 0 [0-0] | 0 [0-0] | 1 [1-1] | 1 [1-3] | 1 [1-1] | - | 0 |
| Digest record present after the start (1 = yes) | 0 [0-0] | 0 [0-0] | 0 [0-0] | 1 [1-1] | 1 [1-1] | 1 [1-1] | - | 0 |

**Before / after** (medians of the timing pass; delta = after against before)

| Metric | node | node-a | delta % | bun | bun-a | delta % | aot | aot-a | delta % |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Launch to ready (ms) | 434 | 440 | +1 | 506 | 406 | -20 | 598 | 331 | -45 |
| Go host CPU to ready, user+sys (ms) | 190 | 195 | +3 | 290 | 195 | -33 | 460 | 200 | -57 |
| Extension host CPU to ready, user+sys (ms) | 370 | 375 | +1 | 345 | 345 | +0 | 100 | 100 | +0 |
| Digest record present beside the executable after ready (1 = yes; Node: n/a) | n/a | n/a | - | 0 | 1 | - | 0 | 1 | - |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 | 0 | - | 0 | 0 | - | 30 | 30 | +0 |
| Turn 1 wall (ms) | 47 | 49 | +4 | 42 | 42 | -1 | 41 | 41 | -0 |
| Turn 2 wall (ms) | 40 | 39 | -2 | 31 | 30 | -2 | 31 | 32 | +3 |
| Steady wall, median of turns 6-20 (ms) | 39.6 | 39.3 | -1 | 33.3 | 31.8 | -4 | 33.0 | 33.9 | +3 |
| Turn 1 / steady wall (ratio) | 1.17 | 1.23 | +5 | 1.27 | 1.33 | +4 | 1.25 | 1.20 | -4 |
| Go host CPU, turn 1 (ms) | 20 | 30 | +50 | 30 | 25 | -17 | 25 | 30 | +20 |
| Extension host CPU, turn 1 (ms) | 45 | 50 | +11 | 65 | 70 | +8 | 20 | 30 | +50 |
| Go host CPU per turn, turns 2-20 (ms) | 17.9 | 18.4 | +3 | 17.6 | 17.4 | -1 | 18.2 | 17.9 | -1 |
| Extension host CPU per turn, turns 2-20 (ms) | 41.6 | 42.4 | +2 | 35.8 | 34.5 | -4 | 24.7 | 25.3 | +2 |
| Extension host minor faults to 0.5 s after ready | 16698 | 16833 | +1 | 16556 | 16598 | +0 | 12990 | 12870 | -1 |
| Extension host minor faults, turn 1 | 1982 | 2052 | +4 | 1760 | 1842 | +5 | 1836 | 1778 | -3 |
| Extension host minor faults per turn, turns 2-20 | 1042.7 | 1041.7 | -0 | 1439.7 | 1404.0 | -2 | 1072.6 | 1056.9 | -1 |
| Extension host RSS @0.5 s after ready | 110.8 | 111.4 | +1 | 97.1 | 97.2 | +0 | 97.9 | 97.5 | -0 |
| Extension host PSS @0.5 s after ready | 105.4 | 106.4 | +1 | 94.8 | 95.0 | +0 | 95.6 | 95.2 | -0 |
| Pss_Anon @0.5 s after ready | 58.3 | 58.9 | +1 | 57.1 | 57.3 | +0 | 46.6 | 46.2 | -1 |
| Pss_File @0.5 s after ready | 47.6 | 47.6 | -0 | 37.7 | 37.7 | +0 | 49.0 | 49.0 | +0 |
| Private_Dirty @0.5 s after ready | 58.3 | 58.9 | +1 | 57.1 | 57.3 | +0 | 46.6 | 46.2 | -1 |
| Private_Clean @0.5 s after ready | 44.8 | 44.9 | +0 | 37.5 | 37.5 | +0 | 48.8 | 48.8 | +0 |
| Go host RSS @0.5 s after ready | 87.6 | 89.9 | +3 | 88.1 | 89.9 | +2 | 87.8 | 90.7 | +3 |
| Extension host RSS @after turn 1 | 117.5 | 118.5 | +1 | 94.3 | 94.0 | -0 | 105.0 | 103.7 | -1 |
| Extension host PSS @after turn 1 | 112.5 | 113.5 | +1 | 92.0 | 91.8 | -0 | 102.7 | 101.5 | -1 |
| Pss_Anon @after turn 1 | 64.9 | 65.9 | +2 | 54.3 | 54.0 | -0 | 53.4 | 52.1 | -2 |
| Pss_File @after turn 1 | 47.7 | 47.7 | +0 | 37.8 | 37.7 | -0 | 49.3 | 49.3 | +0 |
| Private_Dirty @after turn 1 | 64.9 | 65.9 | +2 | 54.3 | 54.0 | -0 | 53.4 | 52.1 | -2 |
| Private_Clean @after turn 1 | 44.9 | 44.9 | +0 | 37.6 | 37.6 | -0 | 49.1 | 49.1 | +0 |
| Go host RSS @after turn 1 | 94.4 | 96.3 | +2 | 94.5 | 96.3 | +2 | 94.6 | 97.1 | +3 |
| Extension host RSS @after 20 turns | 196.4 | 194.4 | -1 | 147.2 | 146.0 | -1 | 134.2 | 128.1 | -5 |
| Extension host PSS @after 20 turns | 190.8 | 189.8 | -1 | 145.0 | 143.8 | -1 | 131.9 | 125.8 | -5 |
| Pss_Anon @after 20 turns | 143.7 | 141.8 | -1 | 104.4 | 103.1 | -1 | 82.5 | 76.5 | -7 |
| Pss_File @after 20 turns | 47.6 | 47.6 | +0 | 40.6 | 40.7 | +0 | 49.4 | 49.4 | -0 |
| Private_Dirty @after 20 turns | 143.7 | 141.8 | -1 | 104.4 | 103.1 | -1 | 82.5 | 76.5 | -7 |
| Private_Clean @after 20 turns | 45.0 | 45.0 | -0 | 40.5 | 40.5 | +0 | 49.2 | 49.1 | -0 |
| Go host RSS @after 20 turns | 110.5 | 112.7 | +2 | 112.7 | 116.2 | +3 | 111.0 | 113.7 | +2 |
| Extension host executable size (MB) | 150.3 | 150.3 | +0 | 79.5 | 79.5 | +0 | 240.1 | 240.1 | +0 |

Allocation, strace pass (3 rounds; the same run under `strace -f --seccomp-bpf -e trace=memory,process`; counts per process class, `ext` = every process the Go host started: the `--version` start checks and the extension host). Cells median [min-max].

Processes seen by strace (executable: count of processes, first run per arm): aot: {'pig': 1, 'pig-ts-host': 2, 'node': 1}; aot-a: {'pig-after': 1, 'pig-ts-host': 2, 'node': 1}; bun: {'pig': 1, 'bun': 2}; bun-a: {'pig-after': 1, 'bun': 2}; node: {'pig': 1, 'node': 2}; node-a: {'pig-after': 1, 'node': 2}

| Metric | node | bun | aot | node-a | bun-a | aot-a |
|---|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 |
| **strace: memory syscalls, Extension host (and its start-check children), launch to ready** | | | | | | |
| mmap | 275 [269-283] | 77 [77-77] | 190 [189-190] | 274 [274-279] | 77 [77-78] | 189 [189-190] |
| munmap | 161 [154-167] | 8 [8-8] | 86 [84-88] | 161 [157-164] | 9 [8-9] | 85 [85-88] |
| brk | 40 [39-42] | 6 [6-6] | 24 [24-24] | 40 [40-40] | 6 [6-6] | 24 [24-24] |
| mprotect | 333 [309-411] | 47 [47-47] | 72 [72-73] | 409 [377-424] | 47 [47-47] | 72 [72-73] |
| madvise | 148 [142-153] | 402 [380-497] | 152 [147-175] | 147 [147-152] | 428 [411-476] | 151 [138-171] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 2586.3 [2583.3-2592.9] | 11751.5 [11751.5-11751.5] | 36835.6 [36835.6-36963.6] | 2588.3 [2585.8-2649.8] | 11751.5 [11751.5-11751.5] | 36835.6 [36835.6-36963.6] |
| **strace: memory syscalls, Extension host (and its start-check children), turn 1** | | | | | | |
| mmap | 3 [2-6] | 0 [0-0] | 0 [0-0] | 1 [0-5] | 0 [0-0] | 0 [0-0] |
| munmap | 3 [2-6] | 0 [0-0] | 0 [0-0] | 1 [0-5] | 0 [0-0] | 0 [0-0] |
| brk | 0 [0-1] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| mprotect | 139 [107-184] | 0 [0-0] | 0 [0-0] | 41 [19-124] | 0 [0-0] | 0 [0-0] |
| madvise | 3 [2-6] | 26 [26-32] | 0 [0-0] | 1 [1-5] | 28 [28-138] | 2 [0-12] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 1.5 [1.0-3.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.5 [0.0-2.5] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| **strace: memory syscalls, Extension host (and its start-check children), per turn, turns 2-20** | | | | | | |
| mmap | 15.2 [14.7-17.9] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 15.2 [15.2-16.1] | 0.0 [0.0-0.1] | 0.0 [0.0-0.0] |
| munmap | 15.2 [14.7-18.2] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 15.2 [15.2-16.1] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| brk | 0.0 [0.0-0.3] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| mprotect | 4.4 [3.7-6.4] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 4.9 [2.9-5.9] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| madvise | 15.2 [14.7-17.9] | 23.8 [22.7-28.0] | 12.3 [9.8-15.9] | 15.2 [15.2-16.2] | 24.8 [23.0-25.8] | 4.1 [3.3-25.1] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 7.3 [7.1-8.7] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 7.4 [7.3-7.8] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| **strace: memory syscalls, Go host, launch to ready** | | | | | | |
| mmap | 80 [77-81] | 81 [79-81] | 82 [79-82] | 78 [77-78] | 81 [79-81] | 83 [81-84] |
| munmap | 9 [9-9] | 9 [9-9] | 9 [6-11] | 9 [7-9] | 9 [8-9] | 10 [7-11] |
| brk | 3 [3-3] | 3 [3-3] | 3 [3-3] | 3 [3-3] | 3 [3-3] | 3 [3-3] |
| mprotect | 22 [20-22] | 22 [22-22] | 22 [20-24] | 20 [20-22] | 22 [22-22] | 22 [22-24] |
| madvise | 2 [2-2] | 2 [2-2] | 2 [2-2] | 2 [2-2] | 2 [2-2] | 2 [2-2] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 2352.4 [2280.1-2416.2] | 2416.2 [2352.1-2416.2] | 2356.2 [2344.2-2488.4] | 2280.1 [2279.9-2351.9] | 2416.2 [2352.1-2416.2] | 2484.4 [2292.4-2552.4] |
| **strace: memory syscalls, Go host, turn 1** | | | | | | |
| mmap | 4 [2-4] | 2 [2-3] | 2 [1-2] | 2 [2-2] | 2 [2-5] | 2 [2-2] |
| munmap | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| brk | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| mprotect | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| madvise | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 68.3 [4.2-72.1] | 4.2 [4.2-8.2] | 4.2 [4.0-4.2] | 4.2 [4.2-4.2] | 8.0 [4.2-72.3] | 4.2 [4.2-4.2] |
| **strace: memory syscalls, Go host, per turn, turns 2-20** | | | | | | |
| mmap | 0.5 [0.4-0.7] | 0.5 [0.4-0.5] | 0.5 [0.4-0.6] | 0.5 [0.5-0.9] | 0.5 [0.4-0.5] | 0.6 [0.4-0.7] |
| munmap | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| brk | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| mprotect | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| madvise | 0.6 [0.0-1.5] | 0.7 [0.4-3.5] | 0.3 [0.0-2.3] | 1.2 [0.5-1.9] | 0.9 [0.9-1.3] | 0.8 [0.3-1.6] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 0.9 [0.9-1.5] | 1.1 [0.9-4.2] | 1.1 [0.9-4.3] | 4.2 [1.1-5.1] | 0.9 [0.9-1.1] | 1.5 [0.9-4.3] |

Allocation, heap pass (3 rounds; the set plus `basket-probe.ts`, which reads the runtime's own heap numbers). Cells median [min-max].

| Metric | node | bun | aot | node-a | bun-a | aot-a |
|---|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 |
| **JS heap allocation (probe pass; Node: v8 total_allocated_bytes, Bun and aot: JSC GC log; a forced GC at each probe point)** | | | | | | |
| Allocated, launch to ready (MB) | 93.4 [93.0-97.3] | 75.2 [73.2-85.1] | 39.2 [39.2-39.2] | 88.0 [86.5-89.6] | 60.6 [49.1-69.4] | 52.3 [40.6-52.3] |
| Allocated, turn 1 incl. the probe (MB) | 6.6 [5.6-7.1] | 24.6 [9.8-25.3] | 3.8 [3.8-3.8] | 7.9 [7.6-9.2] | 9.7 [6.9-42.0] | 3.8 [3.8-5.7] |
| Allocated per turn, turns 2-20 (MB) | 6.91 [6.83-6.95] | 15.50 [13.40-16.15] | 11.85 [11.50-12.86] | 7.23 [6.91-7.54] | 11.22 [9.27-15.85] | 9.49 [7.76-11.81] |
| GC cycles over the run (JSC log; Node: PerformanceObserver gc entries) | 18 [18-18] | 27 [25-27] | 25 [24-26] | 18 [18-18] | 26 [26-26] | 25 [24-26] |
| **JS heap live after a forced GC, at ready** | | | | | | |
| process.memoryUsage().heapUsed (MB) | 14.5 [14.5-14.6] | 9.3 [9.3-9.3] | 6.1 [6.1-6.1] | 14.5 [14.5-14.6] | 9.3 [9.3-9.3] | 6.1 [6.1-6.1] |
| process.memoryUsage().external + arrayBuffers (MB) | 3.0 [3.0-3.0] | 3.2 [3.2-3.2] | 0.9 [0.9-0.9] | 3.0 [3.0-3.0] | 3.2 [3.2-3.2] | 0.9 [0.9-0.9] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | 113750 [113743-113824] | 105737 [105737-105737] | n/a | 113811 [113728-113874] | 105737 [105737-105737] |
| JSC heapSize (MB; Node: n/a) | n/a | 9.3 [9.3-9.3] | 6.1 [6.1-6.1] | n/a | 9.3 [9.3-9.3] | 6.1 [6.1-6.1] |
| **JS heap live after a forced GC, after turn 1** | | | | | | |
| process.memoryUsage().heapUsed (MB) | 14.8 [14.8-14.9] | 9.9 [9.9-9.9] | 6.3 [6.3-6.3] | 14.8 [14.8-14.9] | 9.9 [9.9-9.9] | 6.3 [6.3-6.3] |
| process.memoryUsage().external + arrayBuffers (MB) | 3.1 [3.1-3.1] | 3.7 [3.7-3.7] | 0.9 [0.9-0.9] | 3.1 [3.1-3.1] | 3.7 [3.7-3.7] | 0.9 [0.9-0.9] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | 116184 [116152-116853] | 108400 [108365-108431] | n/a | 116203 [116123-116265] | 108398 [108382-108401] |
| JSC heapSize (MB; Node: n/a) | n/a | 9.9 [9.9-9.9] | 6.3 [6.3-6.3] | n/a | 9.9 [9.9-9.9] | 6.3 [6.3-6.3] |
| **JS heap live after a forced GC, after 20 turns** | | | | | | |
| process.memoryUsage().heapUsed (MB) | 15.5 [15.5-15.5] | 10.6 [10.6-10.6] | 6.6 [6.6-6.6] | 15.4 [15.4-15.5] | 10.6 [10.6-10.6] | 6.6 [6.6-6.6] |
| process.memoryUsage().external + arrayBuffers (MB) | 3.1 [3.1-3.1] | 4.1 [4.1-4.1] | 1.0 [1.0-1.0] | 3.1 [3.1-3.1] | 4.1 [4.1-4.1] | 1.0 [1.0-1.0] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | 125888 [125785-125924] | 117469 [117392-117500] | n/a | 125762 [125694-125767] | 117519 [117410-117529] |
| JSC heapSize (MB; Node: n/a) | n/a | 10.6 [10.6-10.6] | 6.6 [6.6-6.6] | n/a | 10.6 [10.6-10.6] | 6.6 [6.6-6.6] |

## Set `rewrite`

Timing pass: 12 interleaved rounds (order rotated each round, one recorded warm-up run per arm before them), cells are median [p10-p90] over rounds (fewer than 7 runs: median [min-max]), A/A columns are |median(a)-median(b)| / their mean.

Proof each arm ran its tier: every run's extension-host `/proc/<pid>/exe` is checked (node: a node binary; bun: the slot's bun; aot: `<slot>/aot/images/<key>/pig-ts-host`) and a run that differs is an error, not a number. Executables seen: node: /opt/node/26.12.1/bin/node; node2: /opt/node/26.12.1/bin/node; bun: <scratch>/basket/slot-pin/bun/bun; aot: ; aot2: ; node-a: /opt/node/26.12.1/bin/node; node-a2: /opt/node/26.12.1/bin/node; bun-a: <scratch>/basket/slot-after/bun/bun; aot-a: <scratch>/basket/slot-after/aot/images/01a358f4a489aeb8-1794012989792337337/pig-ts-host; aot-a2: <scratch>/basket/slot-after/aot/images/01a358f4a489aeb8-1794012989792337337/pig-ts-host

| Metric | node | node2 | bun | aot | aot2 | node-a | node-a2 | bun-a | aot-a | aot-a2 | A/A node-node2 (%) | A/A aot-aot2 (%) | A/A node-a-node-a2 (%) | A/A aot-a-aot-a2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 12/12 | 12/12 | 12/12 | 0/1 | 0/1 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | | | | |
| **Startup (launch to ready: get_commands answered with the set's commands)** | | | | | | | | | | | | | | |
| Launch to ready (ms) | 829 [779-1069] | 894 [803-1033] | 968 [805-1058] | n/a | n/a | 922 [751-1045] | 855 [766-1038] | 889 [683-969] | 346 [313-387] | 338 [298-388] | 8 | - | 8 | 2 |
| Go host CPU to ready, user+sys (ms) | 170 [140-199] | 180 [140-189] | 260 [210-280] | n/a | n/a | 180 [140-199] | 170 [131-190] | 140 [130-199] | 180 [140-190] | 185 [140-199] | 6 | - | 6 | 3 |
|   Go host user (ms) | 135 [110-150] | 140 [110-150] | 210 [170-230] | n/a | n/a | 150 [110-160] | 140 [101-150] | 110 [101-159] | 145 [110-150] | 150 [110-159] | 4 | - | 7 | 3 |
|   Go host sys (ms) | 35 [30-49] | 35 [30-40] | 50 [40-50] | n/a | n/a | 30 [30-40] | 30 [30-40] | 30 [21-40] | 35 [30-40] | 40 [30-40] | 0 | - | 0 | 13 |
| Go host: CPU of reaped children by ready, the `--version` start checks (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | n/a | n/a | 0 [0-0] | 0 [0-0] | 0 [0-0] | 10 [0-10] | 0 [0-10] | - | - | - | 200 |
| Extension host CPU to ready, user+sys (ms) | 920 [852-1160] | 980 [872-1150] | 1030 [813-1089] | n/a | n/a | 1035 [850-1130] | 960 [851-1135] | 1025 [800-1069] | 135 [120-150] | 130 [120-149] | 6 | - | 8 | 4 |
|   extension host user (ms) | 790 [722-1018] | 860 [743-999] | 900 [703-950] | n/a | n/a | 880 [730-980] | 805 [713-1002] | 885 [690-928] | 105 [91-120] | 100 [90-119] | 8 | - | 9 | 5 |
|   extension host sys (ms) | 135 [121-159] | 140 [120-159] | 130 [110-149] | n/a | n/a | 135 [120-160] | 135 [120-159] | 130 [110-150] | 30 [30-30] | 30 [30-30] | 4 | - | 0 | 0 |
| Digest record present beside the executable after ready (1 = yes; Node: n/a) | n/a | n/a | 0 [0-0] | n/a | n/a | n/a | n/a | 1 [1-1] | 1 [1-1] | 1 [1-1] | - | - | - | 0 |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 [0-0] | 0 [0-0] | 0 [0-0] | n/a | n/a | 0 [0-0] | 0 [0-0] | 0 [0-0] | 30 [20-30] | 30 [20-30] | - | - | - | 0 |
| Extension host CPU, ready to 0.5 s later (idle after ready) (ms) | 80 [70-100] | 80 [70-100] | 70 [51-79] | n/a | n/a | 80 [71-90] | 80 [70-99] | 65 [60-80] | 0 [0-9] | 0 [0-10] | 0 | - | 0 | - |
| **Turns (a prompt, one model tool call, the tool, a final answer; 20 per run)** | | | | | | | | | | | | | | |
| Turn 1 wall (ms) | 28 [23-31] | 29 [23-31] | 35 [29-38] | n/a | n/a | 29 [26-31] | 25 [24-32] | 36 [31-39] | 22 [18-28] | 24 [22-27] | 3 | - | 14 | 9 |
| Turn 2 wall (ms) | 18 [15-19] | 18 [16-19] | 18 [16-22] | n/a | n/a | 18 [16-21] | 19 [17-22] | 17 [15-22] | 16 [15-18] | 16 [13-18] | 2 | - | 4 | 2 |
| Steady wall, median of turns 6-20 (ms) | 11.8 [10.2-13.3] | 12.4 [11.4-13.9] | 11.8 [10.4-12.4] | n/a | n/a | 11.4 [9.5-15.2] | 12.0 [10.2-13.3] | 11.7 [9.9-13.4] | 11.4 [8.9-12.4] | 11.3 [9.6-12.1] | 5 | - | 5 | 1 |
| Turn 1 / steady wall (ratio) | 2.34 [2.06-2.64] | 2.22 [1.95-2.67] | 3.01 [2.33-3.37] | n/a | n/a | 2.53 [1.94-2.98] | 2.29 [2.02-2.59] | 3.18 [2.29-3.84] | 2.17 [1.74-2.40] | 2.20 [1.96-2.44] | 5 | - | 10 | 1 |
| **CPU per phase (user+sys, 10 ms ticks; the first turn is measured with 0.5 s settled before and 0.2 s after)** | | | | | | | | | | | | | | |
| Go host CPU, turn 1 (ms) | 10 [10-20] | 20 [20-20] | 20 [10-20] | n/a | n/a | 20 [10-30] | 10 [10-29] | 20 [11-30] | 15 [10-29] | 20 [10-20] | 67 | - | 67 | 29 |
| Extension host CPU, turn 1 (ms) | 10 [10-20] | 10 [10-20] | 40 [21-50] | n/a | n/a | 15 [10-30] | 20 [10-20] | 40 [30-50] | 10 [0-20] | 10 [1-19] | 0 | - | 29 | 0 |
| Go host CPU per turn, turns 2-20 (ms) | 10.0 [8.9-11.1] | 9.7 [9.0-11.1] | 10.5 [8.9-11.5] | n/a | n/a | 9.7 [7.9-12.9] | 10.3 [8.5-11.1] | 10.3 [8.9-12.1] | 9.7 [7.9-11.6] | 10.0 [8.5-11.5] | 3 | - | 5 | 3 |
| Extension host CPU per turn, turns 2-20 (ms) | 8.9 [7.9-10.4] | 8.9 [7.9-10.0] | 8.4 [6.9-8.9] | n/a | n/a | 8.4 [7.4-9.5] | 8.4 [7.9-9.9] | 8.2 [6.8-9.9] | 3.7 [3.2-4.7] | 3.7 [3.2-4.2] | 0 | - | 0 | 0 |
|   extension host sys per turn, turns 2-20 (ms) | 1.3 [1.1-1.6] | 1.6 [1.1-2.1] | 1.6 [1.1-2.1] | n/a | n/a | 1.1 [0.6-1.6] | 1.1 [1.1-1.6] | 1.6 [1.1-2.1] | 1.1 [0.5-1.1] | 1.1 [0.5-1.1] | 18 | - | 0 | 0 |
| **Page faults (cumulative counters of the process; extension host = its process tree)** | | | | | | | | | | | | | | |
| Extension host minor faults to 0.5 s after ready | 36204 [35975-36405] | 36074 [35873-36336] | 31886 [31408-32113] | n/a | n/a | 36162 [36060-36386] | 36161 [36018-36211] | 31860 [31712-32013] | 12600 [12506-12680] | 12562 [12453-12645] | 0 | - | 0 | 0 |
| Extension host major faults to 0.5 s after ready | 0 [0-0] | 0 [0-0] | 0 [0-0] | n/a | n/a | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | - | - | - | - |
| Extension host minor faults, turn 1 | 608 [596-624] | 609 [599-632] | 588 [471-656] | n/a | n/a | 607 [387-615] | 610 [592-627] | 600 [487-660] | 238 [157-274] | 250 [146-273] | 0 | - | 1 | 5 |
| Extension host minor faults per turn, turns 2-20 | 167.3 [157.1-180.6] | 169.3 [161.6-189.3] | 126.1 [119.8-130.5] | n/a | n/a | 167.5 [158.6-185.3] | 160.5 [155.4-177.0] | 123.0 [114.8-129.7] | 120.2 [117.0-127.0] | 125.8 [120.4-145.2] | 1 | - | 4 | 5 |
| Extension host major faults, whole run to the last turn | 0 [0-0] | 0 [0-0] | 0 [0-0] | n/a | n/a | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | - | - | - | - |
| Go host minor faults to 0.5 s after ready | 13099 [13031-13204] | 13116 [13061-13163] | 13168 [13090-13257] | n/a | n/a | 13028 [12963-13159] | 13054 [12968-13122] | 13138 [12996-13178] | 13366 [13291-13436] | 13296 [13152-13424] | 0 | - | 0 | 1 |
| Go host minor faults per turn, turns 2-20 | 249.3 [206.7-338.5] | 231.0 [202.5-348.2] | 263.2 [208.0-325.8] | n/a | n/a | 247.9 [190.5-361.9] | 242.7 [211.5-310.4] | 284.9 [213.8-369.7] | 273.9 [208.7-333.3] | 239.2 [206.0-293.0] | 8 | - | 2 | 14 |
| **Memory 0.5 s after ready (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | | | | | | |
| Extension host RSS | 187.6 [187.1-189.1] | 187.8 [186.5-188.0] | 167.8 [162.1-171.8] | n/a | n/a | 187.6 [186.7-188.8] | 187.9 [186.8-188.3] | 167.7 [163.5-172.9] | 120.3 [119.5-120.7] | 119.7 [119.0-120.4] | 0 | - | 0 | 0 |
| Extension host PSS | 182.5 [182.0-183.5] | 182.1 [181.2-182.9] | 165.6 [159.9-169.7] | n/a | n/a | 182.3 [180.9-183.3] | 182.5 [181.4-183.0] | 165.5 [161.3-170.7] | 118.0 [117.3-118.3] | 117.5 [116.8-118.2] | 0 | - | 0 | 0 |
|   Pss_Anon | 130.4 [129.9-132.0] | 130.6 [129.2-130.8] | 101.5 [95.8-105.7] | n/a | n/a | 130.4 [129.4-131.7] | 130.7 [129.6-131.1] | 101.4 [97.2-106.7] | 43.4 [42.7-43.9] | 43.1 [42.2-43.6] | 0 | - | 0 | 1 |
|   Pss_File | 51.7 [51.5-52.8] | 51.8 [51.5-52.8] | 64.0 [64.0-64.1] | n/a | n/a | 51.8 [51.5-52.1] | 51.8 [51.5-52.7] | 64.0 [64.0-64.1] | 74.5 [74.5-74.6] | 74.5 [74.4-74.6] | 0 | - | 0 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | n/a | n/a | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | - | - | - | - |
|   Private_Dirty | 130.4 [129.9-132.0] | 130.6 [129.2-130.8] | 101.5 [95.8-105.7] | n/a | n/a | 130.4 [129.4-131.7] | 130.7 [129.6-131.1] | 101.4 [97.2-106.7] | 43.4 [42.7-43.9] | 43.1 [42.2-43.6] | 0 | - | 0 | 1 |
|   Private_Clean | 49.2 [49.2-50.6] | 49.2 [49.2-50.6] | 63.9 [63.9-63.9] | n/a | n/a | 49.2 [49.2-49.3] | 49.2 [49.2-50.5] | 63.9 [63.9-63.9] | 74.3 [74.3-74.4] | 74.3 [74.3-74.4] | 0 | - | 0 | 0 |
|   Shared_Clean+Shared_Dirty | 7.9 [6.5-8.0] | 8.0 [6.6-8.0] | 2.3 [2.3-2.4] | n/a | n/a | 7.9 [7.9-8.1] | 7.9 [6.6-8.1] | 2.3 [2.3-2.4] | 2.5 [2.4-2.6] | 2.4 [2.4-2.5] | 0 | - | 0 | 4 |
| Go host RSS | 87.3 [87.2-87.6] | 87.4 [87.1-87.7] | 87.7 [87.4-88.0] | n/a | n/a | 89.5 [89.1-89.9] | 89.7 [89.4-90.0] | 89.8 [89.6-90.1] | 90.8 [90.7-91.0] | 90.6 [90.1-91.0] | 0 | - | 0 | 0 |
| Go host PSS | 85.8 [85.7-86.1] | 85.9 [85.6-86.1] | 86.1 [85.9-86.4] | n/a | n/a | 88.0 [87.6-88.4] | 88.2 [87.9-88.4] | 88.3 [88.1-88.6] | 89.3 [89.1-89.6] | 89.1 [88.5-89.4] | 0 | - | 0 | 0 |
| **Memory after turn 1 (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | | | | | | |
| Extension host RSS | 190.0 [189.4-191.5] | 190.2 [188.6-190.4] | 165.2 [162.9-166.2] | n/a | n/a | 189.8 [188.7-191.2] | 190.2 [189.2-190.7] | 165.1 [163.9-167.3] | 117.9 [116.3-119.3] | 118.9 [116.4-120.3] | 0 | - | 0 | 1 |
| Extension host PSS | 184.9 [184.4-186.1] | 184.5 [183.6-185.3] | 163.1 [160.8-163.9] | n/a | n/a | 184.6 [183.4-185.7] | 185.0 [183.6-185.4] | 163.0 [161.7-165.1] | 115.7 [114.0-117.0] | 116.7 [114.2-118.0] | 0 | - | 0 | 1 |
|   Pss_Anon | 132.8 [132.3-134.3] | 133.0 [131.4-133.1] | 99.0 [96.7-99.9] | n/a | n/a | 132.5 [131.6-134.1] | 133.1 [132.1-133.5] | 98.9 [97.7-101.0] | 40.9 [39.4-42.2] | 42.0 [39.5-43.3] | 0 | - | 0 | 3 |
|   Pss_File | 51.8 [51.5-52.8] | 51.8 [51.5-52.9] | 64.0 [64.0-64.1] | n/a | n/a | 51.7 [51.5-52.2] | 51.7 [51.5-52.9] | 64.0 [64.0-64.1] | 74.7 [74.7-74.8] | 74.7 [74.6-74.8] | 0 | - | 0 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | n/a | n/a | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | - | - | - | - |
|   Private_Dirty | 132.8 [132.3-134.3] | 133.0 [131.4-133.1] | 99.0 [96.7-99.9] | n/a | n/a | 132.5 [131.6-134.1] | 133.1 [132.1-133.5] | 98.9 [97.7-101.0] | 40.9 [39.4-42.2] | 42.0 [39.5-43.3] | 0 | - | 0 | 3 |
|   Private_Clean | 49.2 [49.2-50.6] | 49.2 [49.2-50.8] | 63.9 [63.9-63.9] | n/a | n/a | 49.2 [49.2-49.3] | 49.2 [49.2-50.8] | 63.9 [63.9-63.9] | 74.5 [74.4-74.6] | 74.5 [74.4-74.5] | 0 | - | 0 | 0 |
|   Shared_Clean+Shared_Dirty | 7.9 [6.5-8.0] | 8.0 [6.5-8.0] | 2.3 [2.3-2.4] | n/a | n/a | 7.9 [7.9-8.1] | 7.9 [6.4-8.1] | 2.3 [2.3-2.4] | 2.5 [2.4-2.6] | 2.4 [2.4-2.5] | 0 | - | 0 | 4 |
| Go host RSS | 93.0 [92.7-93.2] | 93.0 [92.8-93.3] | 93.2 [92.9-93.5] | n/a | n/a | 94.6 [94.3-94.9] | 94.8 [94.5-95.0] | 95.0 [94.7-95.3] | 95.9 [95.7-96.1] | 95.8 [95.1-96.0] | 0 | - | 0 | 0 |
| Go host PSS | 91.4 [91.2-91.6] | 91.5 [91.3-91.7] | 91.7 [91.4-92.0] | n/a | n/a | 93.1 [92.8-93.4] | 93.3 [93.0-93.5] | 93.5 [93.2-93.8] | 94.3 [94.1-94.6] | 94.2 [93.6-94.5] | 0 | - | 0 | 0 |
| **Memory after 20 turns (MB; smaps_rollup summed over the extension host's process tree)** | | | | | | | | | | | | | | |
| Extension host RSS | 202.4 [201.7-204.2] | 202.1 [201.5-203.2] | 172.5 [171.3-173.7] | n/a | n/a | 202.2 [201.5-203.2] | 202.3 [201.6-202.7] | 172.3 [171.3-174.1] | 126.2 [125.1-128.0] | 128.1 [126.3-129.7] | 0 | - | 0 | 2 |
| Extension host PSS | 197.0 [196.2-198.6] | 197.1 [196.5-198.0] | 170.4 [169.0-171.6] | n/a | n/a | 196.7 [196.1-197.8] | 196.9 [196.4-197.3] | 170.1 [169.0-171.8] | 123.9 [122.9-125.8] | 126.0 [124.1-127.4] | 0 | - | 0 | 2 |
|   Pss_Anon | 145.2 [144.5-147.0] | 144.9 [144.2-146.0] | 106.3 [105.0-107.5] | n/a | n/a | 145.0 [144.1-146.1] | 145.1 [144.4-145.6] | 106.0 [105.0-107.8] | 48.4 [47.4-50.4] | 50.5 [48.7-52.0] | 0 | - | 0 | 4 |
|   Pss_File | 51.7 [51.5-52.8] | 51.8 [51.5-52.9] | 64.0 [64.0-64.1] | n/a | n/a | 51.7 [51.5-52.2] | 51.7 [51.5-52.8] | 64.0 [64.0-64.1] | 75.5 [75.4-75.5] | 75.4 [75.4-75.6] | 0 | - | 0 | 0 |
|   Pss_Shmem | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | n/a | n/a | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | - | - | - | - |
|   Private_Dirty | 145.2 [144.5-147.0] | 144.9 [144.2-146.0] | 106.3 [105.0-107.5] | n/a | n/a | 145.0 [144.1-146.1] | 145.1 [144.4-145.6] | 106.0 [105.0-107.8] | 48.4 [47.4-50.4] | 50.5 [48.7-52.0] | 0 | - | 0 | 4 |
|   Private_Clean | 49.2 [49.2-50.6] | 49.2 [49.2-50.8] | 63.9 [63.9-63.9] | n/a | n/a | 49.2 [49.2-49.3] | 49.2 [49.2-50.6] | 63.9 [63.9-63.9] | 75.3 [75.3-75.3] | 75.3 [75.3-75.3] | 0 | - | 0 | 0 |
|   Shared_Clean+Shared_Dirty | 7.9 [6.5-8.1] | 8.0 [6.4-8.0] | 2.3 [2.3-2.4] | n/a | n/a | 7.9 [7.9-8.1] | 7.9 [6.6-8.1] | 2.3 [2.3-2.4] | 2.5 [2.4-2.6] | 2.4 [2.4-2.5] | 0 | - | 0 | 3 |
| Go host RSS | 110.7 [107.5-117.1] | 109.2 [107.6-118.1] | 112.2 [107.7-117.1] | n/a | n/a | 111.8 [108.2-120.6] | 112.2 [109.7-117.4] | 115.6 [109.8-121.5] | 115.8 [110.4-119.5] | 112.2 [110.6-117.2] | 1 | - | 0 | 3 |
| Go host PSS | 109.1 [106.0-115.6] | 107.7 [106.0-116.6] | 110.7 [106.2-115.6] | n/a | n/a | 110.3 [106.7-119.1] | 110.7 [108.2-115.8] | 114.1 [108.3-120.0] | 114.2 [108.9-118.0] | 110.6 [109.1-115.6] | 1 | - | 0 | 3 |
| **Process** | | | | | | | | | | | | | | |
| Extension host threads after the last turn | 12 [12-12] | 12 [12-12] | 18 [18-19] | n/a | n/a | 12 [12-12] | 12 [12-12] | 18 [18-19] | 7 [7-7] | 7 [7-7] | 0 | - | 0 | 0 |
| Extension host executable size (MB) | 150.3 [150.3-150.3] | 150.3 [150.3-150.3] | 79.5 [79.5-79.5] | n/a | n/a | 150.3 [150.3-150.3] | 150.3 [150.3-150.3] | 79.5 [79.5-79.5] | 269.5 [269.5-269.5] | 269.5 [269.5-269.5] | 0 | - | 0 | 0 |

`aot`: 1 failed run(s); first error: `arm aot ran ['bun'], not pig-ts-host`

`aot2`: 1 failed run(s); first error: `arm aot2 ran ['bun'], not pig-ts-host`

**Cold starts** (5 rounds per arm): the digest cache's record beside the executable is removed before the start, so the Go host hashes the Bun or aot executable (and, with the cache, records it). The timing table above is the warm start (record present). An arm whose Go host has no digest cache hashes every time, so its cold and warm cells agree.

| Metric | bun | bun-a | aot-a | aot-a2 | A/A aot-a-aot-a2 (%) |
|---|---:|---:|---:|---:|---:|
| Runs ok / total | 5/5 | 5/5 | 5/5 | 5/5 | |
| Launch to ready, cold (ms) | 839 [779-919] | 802 [726-974] | 533 [485-587] | 587 [530-622] | 10 |
| Go host CPU to ready, cold, user+sys (ms) | 230 [200-260] | 210 [190-230] | 370 [350-400] | 420 [360-460] | 13 |
| Digest records removed before the start | 0 [0-0] | 1 [1-1] | 1 [1-3] | 1 [1-1] | 0 |
| Digest record present after the start (1 = yes) | 0 [0-0] | 1 [1-1] | 1 [1-1] | 1 [1-1] | 0 |

**Before / after** (medians of the timing pass; delta = after against before)

| Metric | node | node-a | delta % | bun | bun-a | delta % | aot | aot-a | delta % |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Launch to ready (ms) | 829 | 922 | +11 | 968 | 889 | -8 | n/a | 346 | - |
| Go host CPU to ready, user+sys (ms) | 170 | 180 | +6 | 260 | 140 | -46 | n/a | 180 | - |
| Extension host CPU to ready, user+sys (ms) | 920 | 1035 | +12 | 1030 | 1025 | -0 | n/a | 135 | - |
| Digest record present beside the executable after ready (1 = yes; Node: n/a) | n/a | n/a | - | 0 | 1 | - | n/a | 1 | - |
| Extension host CPU of reaped children by ready (e.g. aot's `node -p process.execPath`) (ms) | 0 | 0 | - | 0 | 0 | - | n/a | 30 | - |
| Turn 1 wall (ms) | 28 | 29 | +2 | 35 | 36 | +5 | n/a | 22 | - |
| Turn 2 wall (ms) | 18 | 18 | +2 | 18 | 17 | -4 | n/a | 16 | - |
| Steady wall, median of turns 6-20 (ms) | 11.8 | 11.4 | -4 | 11.8 | 11.7 | -1 | n/a | 11.4 | - |
| Turn 1 / steady wall (ratio) | 2.34 | 2.53 | +8 | 3.01 | 3.18 | +5 | n/a | 2.17 | - |
| Go host CPU, turn 1 (ms) | 10 | 20 | +100 | 20 | 20 | +0 | n/a | 15 | - |
| Extension host CPU, turn 1 (ms) | 10 | 15 | +50 | 40 | 40 | +0 | n/a | 10 | - |
| Go host CPU per turn, turns 2-20 (ms) | 10.0 | 9.7 | -3 | 10.5 | 10.3 | -3 | n/a | 9.7 | - |
| Extension host CPU per turn, turns 2-20 (ms) | 8.9 | 8.4 | -6 | 8.4 | 8.2 | -3 | n/a | 3.7 | - |
| Extension host minor faults to 0.5 s after ready | 36204 | 36162 | -0 | 31886 | 31860 | -0 | n/a | 12600 | - |
| Extension host minor faults, turn 1 | 608 | 607 | -0 | 588 | 600 | +2 | n/a | 238 | - |
| Extension host minor faults per turn, turns 2-20 | 167.3 | 167.5 | +0 | 126.1 | 123.0 | -2 | n/a | 120.2 | - |
| Extension host RSS @0.5 s after ready | 187.6 | 187.6 | -0 | 167.8 | 167.7 | -0 | n/a | 120.3 | - |
| Extension host PSS @0.5 s after ready | 182.5 | 182.3 | -0 | 165.6 | 165.5 | -0 | n/a | 118.0 | - |
| Pss_Anon @0.5 s after ready | 130.4 | 130.4 | +0 | 101.5 | 101.4 | -0 | n/a | 43.4 | - |
| Pss_File @0.5 s after ready | 51.7 | 51.8 | +0 | 64.0 | 64.0 | -0 | n/a | 74.5 | - |
| Private_Dirty @0.5 s after ready | 130.4 | 130.4 | +0 | 101.5 | 101.4 | -0 | n/a | 43.4 | - |
| Private_Clean @0.5 s after ready | 49.2 | 49.2 | +0 | 63.9 | 63.9 | -0 | n/a | 74.3 | - |
| Go host RSS @0.5 s after ready | 87.3 | 89.5 | +3 | 87.7 | 89.8 | +2 | n/a | 90.8 | - |
| Extension host RSS @after turn 1 | 190.0 | 189.8 | -0 | 165.2 | 165.1 | -0 | n/a | 117.9 | - |
| Extension host PSS @after turn 1 | 184.9 | 184.6 | -0 | 163.1 | 163.0 | -0 | n/a | 115.7 | - |
| Pss_Anon @after turn 1 | 132.8 | 132.5 | -0 | 99.0 | 98.9 | -0 | n/a | 40.9 | - |
| Pss_File @after turn 1 | 51.8 | 51.7 | -0 | 64.0 | 64.0 | +0 | n/a | 74.7 | - |
| Private_Dirty @after turn 1 | 132.8 | 132.5 | -0 | 99.0 | 98.9 | -0 | n/a | 40.9 | - |
| Private_Clean @after turn 1 | 49.2 | 49.2 | -0 | 63.9 | 63.9 | -0 | n/a | 74.5 | - |
| Go host RSS @after turn 1 | 93.0 | 94.6 | +2 | 93.2 | 95.0 | +2 | n/a | 95.9 | - |
| Extension host RSS @after 20 turns | 202.4 | 202.2 | -0 | 172.5 | 172.3 | -0 | n/a | 126.2 | - |
| Extension host PSS @after 20 turns | 197.0 | 196.7 | -0 | 170.4 | 170.1 | -0 | n/a | 123.9 | - |
| Pss_Anon @after 20 turns | 145.2 | 145.0 | -0 | 106.3 | 106.0 | -0 | n/a | 48.4 | - |
| Pss_File @after 20 turns | 51.7 | 51.7 | -0 | 64.0 | 64.0 | +0 | n/a | 75.5 | - |
| Private_Dirty @after 20 turns | 145.2 | 145.0 | -0 | 106.3 | 106.0 | -0 | n/a | 48.4 | - |
| Private_Clean @after 20 turns | 49.2 | 49.2 | -0 | 63.9 | 63.9 | -0 | n/a | 75.3 | - |
| Go host RSS @after 20 turns | 110.7 | 111.8 | +1 | 112.2 | 115.6 | +3 | n/a | 115.8 | - |
| Extension host executable size (MB) | 150.3 | 150.3 | +0 | 79.5 | 79.5 | +0 | n/a | 269.5 | - |

Allocation, strace pass (3 rounds; the same run under `strace -f --seccomp-bpf -e trace=memory,process`; counts per process class, `ext` = every process the Go host started: the `--version` start checks and the extension host). Cells median [min-max].

Processes seen by strace (executable: count of processes, first run per arm): aot-a: {'pig-after': 1, 'pig-ts-host': 2, 'node': 1}; bun: {'pig': 1, 'bun': 2}; bun-a: {'pig-after': 1, 'bun': 2}; node: {'pig': 1, 'node': 2}; node-a: {'pig-after': 1, 'node': 2}

| Metric | node | bun | node-a | bun-a | aot-a |
|---|---:|---:|---:|---:|---:|
| Runs ok / total | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 |
| **strace: memory syscalls, Extension host (and its start-check children), launch to ready** | | | | | |
| mmap | 520 [519-520] | 626 [625-626] | 518 [518-520] | 625 [625-625] | 190 [190-190] |
| munmap | 406 [403-407] | 8 [8-8] | 403 [401-406] | 8 [8-8] | 87 [85-87] |
| brk | 60 [57-60] | 6 [6-6] | 59 [59-60] | 6 [6-6] | 24 [24-24] |
| mprotect | 621 [568-625] | 51 [51-51] | 552 [534-623] | 51 [51-51] | 76 [76-76] |
| madvise | 388 [388-390] | 1190 [1062-1273] | 387 [386-389] | 1176 [1065-1182] | 199 [186-199] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 10976.2 [10974.9-11040.2] | 11751.5 [11751.5-11751.5] | 10975.2 [10974.4-10975.4] | 11751.5 [11751.5-11751.5] | 36835.6 [36835.6-36835.6] |
| **strace: memory syscalls, Extension host (and its start-check children), turn 1** | | | | | |
| mmap | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| munmap | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| brk | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| mprotect | 39 [21-40] | 0 [0-0] | 38 [28-39] | 0 [0-0] | 0 [0-0] |
| madvise | 0 [0-0] | 66 [33-76] | 0 [0-0] | 69 [21-80] | 0 [0-1] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| **strace: memory syscalls, Extension host (and its start-check children), per turn, turns 2-20** | | | | | |
| mmap | 0.2 [0.2-0.2] | 0.0 [0.0-0.0] | 0.2 [0.2-0.3] | 0.0 [0.0-0.0] | 0.0 [0.0-0.1] |
| munmap | 0.2 [0.2-0.2] | 0.0 [0.0-0.0] | 0.2 [0.2-0.3] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| brk | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| mprotect | 11.5 [9.3-12.3] | 0.0 [0.0-0.0] | 10.6 [9.8-13.1] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| madvise | 0.2 [0.2-0.2] | 8.8 [8.2-20.8] | 0.2 [0.2-0.3] | 8.7 [7.7-10.7] | 1.6 [0.9-3.4] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 0.1 [0.1-0.1] | 0.0 [0.0-0.0] | 0.1 [0.1-0.2] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| **strace: memory syscalls, Go host, launch to ready** | | | | | |
| mmap | 81 [80-83] | 82 [79-83] | 79 [77-81] | 81 [80-81] | 80 [78-81] |
| munmap | 9 [8-9] | 10 [9-11] | 9 [9-10] | 9 [8-9] | 9 [8-9] |
| brk | 3 [3-3] | 3 [3-3] | 3 [3-3] | 3 [3-3] | 3 [3-3] |
| mprotect | 22 [22-24] | 22 [22-24] | 20 [20-22] | 22 [22-22] | 22 [20-22] |
| madvise | 2 [2-2] | 2 [2-2] | 2 [2-2] | 2 [2-2] | 2 [2-2] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 2416.2 [2412.4-2420.1] | 2480.4 [2352.1-2548.4] | 2344.2 [2339.9-2416.2] | 2416.2 [2415.9-2416.2] | 2415.9 [2343.9-2416.2] |
| **strace: memory syscalls, Go host, turn 1** | | | | | |
| mmap | 2 [1-2] | 1 [1-2] | 2 [2-2] | 2 [1-2] | 2 [1-2] |
| munmap | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| brk | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| mprotect | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| madvise | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] | 0 [0-0] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 4.2 [4.0-4.2] | 0.2 [0.2-4.2] | 4.2 [4.2-4.2] | 4.2 [0.2-4.2] | 4.2 [0.2-4.2] |
| **strace: memory syscalls, Go host, per turn, turns 2-20** | | | | | |
| mmap | 0.6 [0.6-0.8] | 0.6 [0.5-0.6] | 0.5 [0.4-0.7] | 0.5 [0.4-0.6] | 0.5 [0.4-0.6] |
| munmap | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| brk | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| mprotect | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] | 0.0 [0.0-0.0] |
| madvise | 1.0 [0.0-1.3] | 0.0 [0.0-1.5] | 0.5 [0.0-0.7] | 0.0 [0.0-0.7] | 0.8 [0.0-1.1] |
| anonymous mmap requested (MiB of address space: reservations count in full, touched or not) | 1.5 [1.3-4.3] | 1.7 [0.9-4.3] | 0.9 [0.9-4.9] | 0.9 [0.7-1.1] | 0.9 [0.7-1.1] |

Allocation, heap pass (3 rounds; the set plus `basket-probe.ts`, which reads the runtime's own heap numbers). Cells median [min-max].

| Metric | node | bun | node-a | bun-a | aot-a |
|---|---:|---:|---:|---:|---:|
| Runs ok / total | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 |
| **JS heap allocation (probe pass; Node: v8 total_allocated_bytes, Bun and aot: JSC GC log; a forced GC at each probe point)** | | | | | |
| Allocated, launch to ready (MB) | 280.1 [274.4-285.1] | 85.4 [75.9-88.6] | 284.2 [282.4-290.7] | 76.1 [75.9-78.9] | 33.6 [31.0-33.6] |
| Allocated, turn 1 incl. the probe (MB) | 2.9 [2.8-3.2] | 5.5 [5.4-5.5] | 2.8 [2.7-2.9] | 5.5 [5.4-5.9] | 6.4 [6.4-15.4] |
| Allocated per turn, turns 2-20 (MB) | 1.19 [0.96-1.37] | 0.87 [0.85-1.20] | 0.96 [0.91-1.19] | 0.79 [0.77-0.95] | 0.49 [0.49-0.80] |
| GC cycles over the run (JSC log; Node: PerformanceObserver gc entries) | 5 [5-5] | 15 [14-15] | 5 [5-5] | 15 [15-16] | 6 [6-7] |
| **JS heap live after a forced GC, at ready** | | | | | |
| process.memoryUsage().heapUsed (MB) | 51.6 [51.6-51.6] | 32.1 [32.1-32.1] | 51.6 [51.5-51.6] | 32.1 [32.1-32.1] | 12.6 [12.6-12.6] |
| process.memoryUsage().external + arrayBuffers (MB) | 5.2 [5.2-5.2] | 12.9 [12.9-12.9] | 5.2 [5.2-5.2] | 12.9 [12.9-12.9] | 1.2 [1.2-1.2] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | 305801 [305743-305855] | n/a | 305779 [305697-305894] | 202657 [202657-202663] |
| JSC heapSize (MB; Node: n/a) | n/a | 32.1 [32.1-32.1] | n/a | 32.1 [32.1-32.1] | 12.6 [12.6-12.6] |
| **JS heap live after a forced GC, after turn 1** | | | | | |
| process.memoryUsage().heapUsed (MB) | 51.7 [51.7-51.7] | 32.8 [32.7-32.8] | 51.7 [51.7-51.7] | 32.8 [32.7-32.8] | 12.7 [12.7-12.7] |
| process.memoryUsage().external + arrayBuffers (MB) | 5.2 [5.2-5.2] | 13.5 [13.5-13.5] | 5.2 [5.2-5.2] | 13.5 [13.5-13.5] | 1.2 [1.2-1.2] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | 307214 [307163-307261] | n/a | 307200 [307099-307305] | 203827 [203825-203835] |
| JSC heapSize (MB; Node: n/a) | n/a | 32.8 [32.7-32.8] | n/a | 32.8 [32.7-32.8] | 12.7 [12.7-12.7] |
| **JS heap live after a forced GC, after 20 turns** | | | | | |
| process.memoryUsage().heapUsed (MB) | 52.7 [52.7-52.8] | 33.6 [33.5-33.6] | 52.7 [52.7-52.8] | 33.5 [33.5-33.6] | 12.9 [12.9-12.9] |
| process.memoryUsage().external + arrayBuffers (MB) | 5.5 [5.5-5.5] | 13.9 [13.9-13.9] | 5.5 [5.5-5.5] | 13.9 [13.9-13.9] | 1.3 [1.3-1.3] |
| JSC live objects (Node: n/a, V8 does not expose a count) | n/a | 313703 [313539-313799] | n/a | 313510 [313437-313884] | 207561 [207540-207580] |
| JSC heapSize (MB; Node: n/a) | n/a | 33.6 [33.5-33.6] | n/a | 33.5 [33.5-33.6] | 12.9 [12.9-12.9] |
