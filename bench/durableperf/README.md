# durableperf: the durable-bench workload on PiG's Go Durable and on pi-durable (Node)

Workload: github.com/clavia-labs/durable-bench. A scripted model calls a `lookup` tool; historical turns repeat a one-tool, one-tool, zero-tool pattern; the measured turns are ten eight-tool-call turns on a copy of a seeded store. Stores are pi-durable 1.0.4's own SQLite stores (`durable/interop/bench.mjs seed`); PiG opens them unchanged, and both runtimes reproduce the benchmark's history fingerprints (50 turns `b017b487524e44a4`, 250 turns `dcea9f30b0917245`) and its row counts and database sizes.

```sh
make durable-interop-deps                                   # the pinned pi-durable 1.0.4 packages
node --no-warnings durable/interop/bench.mjs seed DIR 50,250,1000,3500
go build -o durableperf ./bench/durableperf
./durableperf run -dir DIR -prefix pi -samples 3 -model walk -provider faux -out results.jsonl
./durableperf run -dir DIR -prefix pi -samples 3 -model incremental -provider scripted
OUT=results.jsonl node --no-warnings durable/interop/bench.mjs run DIR 50,250,1000,3500 3
./durableperf sample -fixture DIR/pi-3500.sqlite -turns 3500 -cpuprofile cpu.pprof   # one sample, profiles
```

Every sample runs in a fresh process. Cold is open plus the first measured turn; warm is the median of the other nine. `walk` is the benchmark's model as written (it rebuilds its view of the whole transcript on every call) with pi-ai's faux provider, which also serializes the transcript to estimate usage; `incremental` reads only the messages added since its previous call, and `scripted` answers without the usage estimate. Only `walk` + `faux` is the published workload; the other pair is the per-turn floor of the Durable layer. Each result line carries the statements, reads, writes, rows read and transactions of the measured turns (`sqlPerTenTurns`), and the last transcript's fingerprint. `openCpu` and `turnCpu[]` are the process CPU time (user plus system, ms) of the open and of each measured turn, as `durable/interop/bench.mjs` reports them for pi-durable; with more than one P, Go's CPU time can exceed the wall time.

## Results (this host: 128 cores, shared, load average 30-55; medians of three samples; ms cold/warm)

| turns | pi-durable 1.0.4, node:sqlite | PiG at the parent commit (1 sample) | PiG, walk + faux, default GOMAXPROCS | PiG, walk + faux, GOMAXPROCS=1 | PiG, incremental + scripted, default | PiG, incremental + scripted, GOMAXPROCS=1 |
|---|---|---|---|---|---|---|
| 50    | 86/46    | 252/364   | 56/88   | 59/50   | 52/105 | 54/42 |
| 250   | 144/86   | 927/1115  | 72/120  | 72/59   | 55/76  | 59/47 |
| 1,000 | 321/231  | 3351/3331 | 213/193 | 127/82  | 82/110 | 92/48 |
| 3,500 | 969/739  | 9129/9601 | 487/366 | 318/201 | 252/135| 172/64 |

Peak RSS (MB) at 50/250/1,000/3,500 turns: pi-durable on Node 92/155/204/290; PiG 28-38 / 30-42 / 37-50 / 55-72, bounded by the working-set budget (64 MiB of decoded entries by default).

Rows read per warm turn at 3,500 turns: PiG about 540, pi-durable 1.0.4 about 212,000. Statements per warm turn: PiG about 970, pi-durable 2,639. Write statements per turn: PiG 354 in 77 transactions, pi-durable 372 in 77.

For reference, pi-durable and tardigrade 0.44.0 under Miniflare on the same host (durable-bench's own runner, medians of three): pi-durable 159/101, 221/147, 431/317, 1158/920; tardigrade 349/146, 485/172, 1428/238, 3290/545. The published numbers (365/313 and 880/123 at 3,500 turns) come from a faster machine.

## Why the default runtime is 2-3x slower on this host

A Durable turn is a strictly sequential chain: about 77 commits, about 420 goroutine hand-offs and 2,600 syscalls. With the default 128 Ps the same code is 2-3x slower than with one P, and uses 3x the CPU. What was measured (walk + faux, 50 turns, warm median over nine turns; the host is a 2-socket Xeon 8462Y+ with 128 hardware threads, governor `schedutil`, idle cores at 800-900 MHz, C6 exit latency 290 us):

| configuration | warm ms | CPU s |
|---|---|---|
| default (128 Ps, all cores) | 88-120 | 1.7-1.8 |
| GOMAXPROCS=1 | 34-54 | 0.4-0.5 |
| `taskset -c 0` (Go 1.25+ then sets GOMAXPROCS=1) | 52-58 | 0.5 |
| 128 Ps forced onto 2 cores (`taskset -c 0,1`, GOMAXPROCS=128) | 58-60 | 0.7 |
| 2 Ps free to use 8 cores | 58-65 | 0.8 |
| 8 Ps on 8 cores | 97-114 | 1.1-1.3 |
| default, GOGC=off | 86-108 | 1.0-1.1 |

Findings:
- The number of Ps is not the cause: 128 Ps on two cores is as fast as one P. Running on many cores at once is.
- The chain does not stay on one core. In a runtime trace the default run used 40 Ps, and 1,658 of 34,941 goroutine resumes happened on a different P than the goroutine's last. The same goroutines accumulated 1,494 ms of running time against 452 ms with one P.
- Those cores are slow. Sampling the clock of every core that had a runnable Go thread, the default run executed on 62 distinct cores with a median of 800 MHz (the minimum), and the GOMAXPROCS=1 run on two cores at 2,800 MHz. A core woken for a few hundred microseconds never leaves its idle clock under `schedutil`; the single busy core ramps and stays up.
- It is not scheduler latency (runnable-to-running totals about 100 ms of 1,300 ms in the trace), not user-lock contention (the mutex profile has none in PiG code), and only partly the garbage collector: GOGC=off saves 15-30 ms of the gap. In the mutex profile 92% of the runtime-lock wait is `mheap.allocSpan` called from GC mark workers' work-buffer allocation, the cost of waking up to 128 GC workers.
- Plain goroutine ping-pong costs 0.45 us at every GOMAXPROCS, so channel hand-offs themselves are cheap. The hand-offs that matter are the ones where the waker keeps running and an idle P steals the readied goroutine.

Where the hand-offs come from (4,653 wake-ups in eleven turns, faux provider): the Session line, 33% (each commit's `close(done)` wakes the job queued behind it) and `resolveAgent`, 7% (a goroutine per agent resolution that the caller waits for at once), the `ai` continuation executor that reproduces pi-ai's promise ordering, 27% (faux provider only; 9% with the scripted provider), and runtime and GC wake-ups, 30%. They belong to the Pi concurrency model PiG ports. The remedy is a deployment that keeps the process on one or two CPUs (a cgroup CPU limit or cpuset; Go 1.25+ follows both) or a CPU governor that keeps active cores up. PiG does not set GOMAXPROCS.

## Store identity and cold turns

`rowdiff.py A.sqlite B.sqlite` compares every row of two stores after zeroing timestamps and replacing random identifiers (faux API names, UUIDs). PiG's `seed` and pi-durable 1.0.4's `seed` produce row-identical stores at 50, 250, 1,000 and 3,500 turns: 0 differing rows in every table, equal database sizes (237,568 and 9,265,152 bytes at 50 and 3,500) and equal fingerprints (`b017b487524e44a4`, `dcea9f30b0917245`, `ac520308146f2a8f`, `0a8c8e4b0d9a0794`). PiG seeds 3,500 turns in about two minutes.

`sample -restart-each` closes and reopens the harness before every measured turn, so each measured turn starts from a freshly opened harness (the process stays warm). Cold turn medians, ms, walk + faux / incremental + scripted: GOMAXPROCS=1: 59/53 at 50 turns, 104/60 at 1,000, 228/123 at 3,500; default GOMAXPROCS at 3,500 turns: 542/292.

`sample -compact` runs one manual compaction before the measured turns: the active context then starts at a head marker, and the working set loads only the range from it. Cold-turn medians with `-restart-each`, GOMAXPROCS=1, incremental + scripted: 3,500 turns 154 ms without compaction and 59 ms with it, 1,000 turns 84 and 57 ms; rows read per ten cold turns 123,178 and 16,428 at 3,500 turns. With compaction the cold turn does not grow with history.

Raw results are in `results/`. The SQLite driver was measured on the captured writes of ten turns (3,544 statements, 776 transactions): C sqlite3 72 ms, ncruces/go-sqlite3 86 ms, modernc.org/sqlite 102 ms. The driver is worth under 20% of a write floor of about 10 ms per turn.
