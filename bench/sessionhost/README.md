# sessionhost: memory of N concurrent RPC sessions

`sessionhost.py` compares three ways to serve N RPC sessions (D232): arm `listen`, one `pig --mode rpc --listen` process with N connections whose sessions have their own extension processes (the default); arm `listen-shared`, the same with `--listen-share-extensions`; and arm `rpc`, N `pig --mode rpc` processes. Both arms use the same agent directory, the same scripted model and the same workload, and sample the memory of the whole process tree before the first turn and after every turn.

```sh
go build -trimpath -o /tmp/pig-listen ./cmd/pig
python3 bench/sessionhost/sessionhost.py --pig /tmp/pig-listen --sessions 1 10 100 --settle 40 --json-out sessionhost.json
python3 bench/sessionhost/sessionhost.py --pig /tmp/pig-listen --extensions --sessions 1 10 100 --settle 40 --json-out sessionhost-ext.json
```

- `--extensions` adds the two extension fixtures of `coding/cli/testdata` (`listen-counter-go`, `listen-counter.mjs`) to the agent directory, so every session loads a Go and a Node extension. It needs Node on `PATH` and the Go toolchain. A one-session warm-up run builds the Go extension before any arm is measured.
- `--settle S` idles S seconds after the last turn before the final sample (default 0): a process that started seconds ago holds more memory than it will once idle (see the result below). The slope and the tables use the final sample.
- `--turns T` runs the first T of the five prompts; `--arms listen listen-shared rpc` selects the arms. Every run also records the time until the server's socket exists (`listen` arms) and until every session has answered `get_state`; `sessions_ready_s` counts from the connections for the `listen` arms, whose builds run one at a time, and from the first process start for `rpc`, whose processes start in parallel.
- Every process gets its own scratch `PIG_CODING_AGENT_DIR`, `PI_CODING_AGENT_DIR`, `PIG_HOME` and `PI_HOME`, and runs with `--no-session`.
- Memory is `phys_footprint` (`footprint`) on macOS and PSS (`/proc/<pid>/smaps_rollup`) on Linux, as `private`, beside RSS. The extension process count is the number of descendants of the `pig` processes. The slope is the change of the last point between the smallest and the largest N, per additional session.
- The script exits 1 if any run has an error: a refused prompt, an errored assistant turn, a closed stream, a timeout, a model request count other than 2 per turn per session, or with `--extensions` a request that did not declare both fixture tools (the self-check; run `--sessions 1 --turns 1` for the minimal case).

## Workload

The five prompts of jcode's `scripts/bench_headless_memory.py`, verbatim:

1. `Use a tool to list the files in the current directory, then reply with just the number of entries.`
2. `Read the first 20 lines of README.md with a tool and reply with its first heading only.`
3. `Search the repository for the string 'fn main' with a tool and reply with how many files matched.`
4. `In one sentence, summarize what you have learned about this repository so far.`
5. `Reply with exactly the word: done`

The model is `scripted_model.py`, served through `bench/compare/fake_server.py`. `bench/compare/fake_model_ctx.py` answers each prompt with four tool turns, so it could not meet the two-request check; `scripted_model.py` answers every prompt with one `read` call on `README.md` and then the text `done`, whatever the prompt says. The prompts therefore fix the request sizes, not the work.

## Result (2026-11-06, Linux, Intel Xeon Platinum 8468 host xd6702, `tern-ci:v7` container, `pss` after turn 5, 0 errors)

Memory is sampled 40 s after the last turn (`--settle 40`), because a process that started seconds ago holds more than it will: a Node process 8 MB more (V8 trims its heap when idle), a Go server whatever its start-up burst allocated until a collection returns it.

Memory, no extensions:

| Sessions | `listen` (isolated) | `listen-shared` | `rpc` |
|---|---|---|---|
| 1 | 51.6 MB | 51.4 MB | 50.7 MB |
| 10 | 67.2 MB | 65.1 MB | 147.8 MB |
| 100 | 143.6 MB | 136.0 MB | 1106.3 MB |
| slope per additional session (N 1 to 100) | 0.93 MB | 0.85 MB | 10.66 MB |

With the two extensions (`--extensions`), memory and extension processes:

| Sessions | `listen` (isolated) | ext | `listen-shared` | ext | `rpc` | ext |
|---|---|---|---|---|---|---|
| 1 | 166.8 MB | 2 | 163.4 MB | 2 | 198.8 MB | 2 |
| 10 | 623.7 MB | 20 | 265.3 MB | 2 | 1132.4 MB | 20 |
| 100 | 5252.0 MB | 200 | 892.4 MB | 2 | 10108.2 MB | 200 |
| slope per additional session (N 1 to 100) | 51.4 MB | | 7.4 MB | | 100.1 MB | |

Start-up. `server` is the time until the server's socket exists; `each` is the time from the connections until a session's first `get_state` answer, divided by N; `all` is the time until the last of N sessions answered.

| Arm | Extensions | N | server | each | all |
|---|---|---|---|---|---|
| `listen` | none | 1 / 10 / 100 | 73 / 33 / 34 ms | 3 / 3 / 2 ms | 0.00 / 0.03 / 0.21 s |
| `listen-shared` | none | 1 / 10 / 100 | 43 / 34 / 46 ms | 6 / 7 / 6 ms | 0.01 / 0.07 / 0.65 s |
| `rpc` | none | 1 / 10 / 100 | | 37 / 13 / 5 ms | 0.04 / 0.13 / 0.53 s |
| `listen` | 2 | 1 / 10 / 100 | 320 / 300 / 324 ms | 217 / 51 / 49 ms | 0.22 / 0.51 / 4.95 s |
| `listen-shared` | 2 | 1 / 10 / 100 | 300 / 289 / 258 ms | 195 / 54 / 37 ms | 0.20 / 0.54 / 3.69 s |
| `rpc` | 2 | 1 / 10 / 100 | | 573 / 62 / 54 ms | 0.57 / 0.62 / 5.42 s |

What made the default slow, and what changed. A default session does not rebuild its extensions: the content-addressed artifact is built once and every later session finds it (14 ms to check a Go cell, with the per-digest publisher lock). What cost about 240 ms per session was that the server built its sessions one at a time (`buildMu`), so every session's Node process start (about 190 ms) waited for the one before it, and that a Go process capped at 2 Ps (D174's general cap) did the server-side work of 100 starting sessions on two cores. The default server now builds sessions concurrently (the shared mode keeps `buildMu`, because its Hosts use one registry) and releases the cap at startup (`procpolicy.Release`). Before the change, with the same bench: `listen` with two extensions at 100 sessions was ready after 23.9 s, `listen-shared` after 9.8 s; the 2-P cap alone accounts for 13.2 s of the default's time with concurrent builds (`GOMAXPROCS=2`) against 4.95 s without it. The default's start-up with two extensions at 100 sessions: 4.95 s, against 5.42 s for 100 `rpc` processes and 3.69 s for the shared mode.

Memory of the concurrent default, and what was measured. Sampled at turn 5 (before `--settle`), the default with extensions read 6538 MB at 100 sessions against 5414 MB when it built one session at a time. A per-process breakdown (`pss` of the server, the 100 Node processes and the 100 Go cells, old and new binary, three repetitions) shows three transient causes and one small steady one: (1) the Node processes: 4.1 GB with serialized starts against 4.9 GB with concurrent ones at turn 5, and 3.8 GB against 3.8 GB after 30 to 120 s idle. The old run only looked smaller because its first 79 sessions had been idle for 5 to 20 s when it was sampled (V8 trims a Node heap after it has been idle for a while, about 8 MB per process); the last 21 old processes read 48 MB like every new one. Limiting concurrent process starts to 4 did not change it, so it is not start contention. (2) The server heap: live heap is 421 MB in both (4.2 MB per session), but sessions that start together allocate their builds' garbage side by side, so the heap goal of the burst reached 1.8 GB (736 MB serialized), and the pages stay resident until a collection runs, which an idle server does after two minutes. The default server now returns them 3 s after its last session build (`heapSettler`, `debug.FreeOSMemory`): the server reads 0.79 GB at 100 sessions, 0.88 GB before the change with serialized starts, 1.25 to 1.43 GB with concurrent starts and no settler. (3) The wider scheduler (16 Ps) leaves about 150 MB more of the server's heap spread after the final collection (953 against 798 MB). With all three accounted for, the settled default with extensions is 5252 MB at 100 sessions, below the 5414 MB the serialized server read unsettled.

Reading it. The server removes the per-session Go runtime (about 10 MB of the `rpc` slope) in both modes. Only `--listen-share-extensions` removes the extension processes, and the price of that is shared module-level state (D232). The default now comes up in about the time of the `rpc` arm (4.95 s against 5.54 s for 100 sessions with two extensions) and uses about half of its memory (5252 against 10108 MB). The `listen` slope without extensions is above the 0.5 MB per session of the Go SDK spike in `docs/specs/multi-session-rpc-server.md`: the server builds each connection's own settings, resources, `ModelRuntime` and Session, and the spike shared them. The bench does not separate the `ModelRuntime` from the rest of the build. The numbers of the 2026-11-01 run on an Apple M3 Max (`phys_footprint`) were 315.0 MB against 2853.4 MB for 100 sessions without extensions.
