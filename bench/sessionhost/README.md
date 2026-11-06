# sessionhost: memory of N concurrent RPC sessions

`sessionhost.py` compares three ways to serve N RPC sessions (D232): arm `listen`, one `pig --mode rpc --listen` process with N connections whose sessions have their own extension processes (the default); arm `listen-shared`, the same with `--listen-share-extensions`; and arm `rpc`, N `pig --mode rpc` processes. Both arms use the same agent directory, the same scripted model and the same workload, and sample the memory of the whole process tree before the first turn and after every turn.

```sh
go build -trimpath -o /tmp/pig-listen ./cmd/pig
python3 bench/sessionhost/sessionhost.py --pig /tmp/pig-listen --sessions 1 10 100 --json-out sessionhost.json
python3 bench/sessionhost/sessionhost.py --pig /tmp/pig-listen --extensions --sessions 1 10 100 --json-out sessionhost-ext.json
```

- `--extensions` adds the two extension fixtures of `coding/cli/testdata` (`listen-counter-go`, `listen-counter.mjs`) to the agent directory, so every session loads a Go and a Node extension. It needs Node on `PATH` and the Go toolchain. A one-session warm-up run builds the Go extension before any arm is measured.
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

Memory, no extensions:

| Sessions | `listen` (isolated) | `listen-shared` | `rpc` |
|---|---|---|---|
| 1 | 54.3 MB | 53.7 MB | 51.1 MB |
| 10 | 70.7 MB | 69.7 MB | 146.0 MB |
| 100 | 137.3 MB | 146.2 MB | 1101.0 MB |
| slope per additional session (N 1 to 100) | 0.84 MB | 0.93 MB | 10.61 MB |

With the two extensions (`--extensions`), memory and extension processes:

| Sessions | `listen` (isolated) | ext | `listen-shared` | ext | `rpc` | ext |
|---|---|---|---|---|---|---|
| 1 | 188.0 MB | 2 | 187.2 MB | 2 | 209.9 MB | 2 |
| 10 | 877.5 MB | 20 | 303.2 MB | 2 | 1240.2 MB | 20 |
| 100 | 6537.8 MB | 200 | 1146.6 MB | 2 | 10661.5 MB | 200 |
| slope per additional session (N 1 to 100) | 64.1 MB | | 9.7 MB | | 105.6 MB | |

Start-up. `server` is the time until the server's socket exists; `each` is the time from the connections until a session's first `get_state` answer, divided by N; `all` is the time until the last of N sessions answered.

| Arm | Extensions | N | server | each | all |
|---|---|---|---|---|---|
| `listen` | none | 1 / 10 / 100 | 73 / 33 / 44 ms | 5 / 5 / 2 ms | 0.00 / 0.05 / 0.16 s |
| `listen-shared` | none | 1 / 10 / 100 | 34 / 35 / 34 ms | 6 / 7 / 5 ms | 0.01 / 0.07 / 0.53 s |
| `rpc` | none | 1 / 10 / 100 | | 35 / 13 / 3 ms | 0.03 / 0.13 / 0.32 s |
| `listen` | 2 | 1 / 10 / 100 | 321 / 298 / 299 ms | 213 / 53 / 49 ms | 0.21 / 0.53 / 4.95 s |
| `listen-shared` | 2 | 1 / 10 / 100 | 303 / 331 / 291 ms | 202 / 58 / 37 ms | 0.20 / 0.58 / 3.74 s |
| `rpc` | 2 | 1 / 10 / 100 | | 573 / 69 / 55 ms | 0.57 / 0.69 / 5.54 s |

What made the default slow, and what changed. A default session does not rebuild its extensions: the content-addressed artifact is built once and every later session finds it (14 ms to check a Go cell, with the per-digest publisher lock). What cost about 240 ms per session was that the server built its sessions one at a time (`buildMu`), so every session's Node process start (about 190 ms) waited for the one before it, and that a Go process capped at 2 Ps (D174's general cap) did the server-side work of 100 starting sessions on two cores. The default server now builds sessions concurrently (the shared mode keeps `buildMu`, because its Hosts use one registry) and releases the cap at startup (`procpolicy.Release`). Before the change, with the same bench: `listen` with two extensions at 100 sessions was ready after 23.9 s, `listen-shared` after 9.8 s; the 2-P cap alone accounts for 13.2 s of the default's time with concurrent builds (`GOMAXPROCS=2`) against 4.95 s without it.

The price: concurrent starts raise the memory of the default with extensions, 5414 MB before and 6538 MB now at 100 sessions (64.1 MB against 52.8 MB per session). With `GOMAXPROCS=2` and concurrent builds it was 6217 MB, so about 800 MB of the rise comes with the concurrent starts and about 300 MB with the wider scheduler. The cause of the first part was not isolated (a guess is that Node processes that start together find less in the shared caches than processes that start one after the other).

Reading it. The server removes the per-session Go runtime (about 10 MB of the `rpc` slope) in both modes. Only `--listen-share-extensions` removes the extension processes, and the price of that is shared module-level state (D232). The default now comes up in about the time of the `rpc` arm (4.95 s against 5.54 s for 100 sessions with two extensions) and uses 61% of its memory. The `listen` slope without extensions is above the 0.5 MB per session of the Go SDK spike in `docs/specs/multi-session-rpc-server.md`: the server builds each connection's own settings, resources, `ModelRuntime` and Session, and the spike shared them. The bench does not separate the `ModelRuntime` from the rest of the build. The numbers of the 2026-11-01 run on an Apple M3 Max (`phys_footprint`) were 315.0 MB against 2853.4 MB for 100 sessions without extensions.
