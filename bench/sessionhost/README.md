# sessionhost: memory of N concurrent RPC sessions

`sessionhost.py` compares two ways to serve N RPC sessions: arm `listen`, one `pig --mode rpc --listen` process with N connections, and arm `rpc`, N `pig --mode rpc` processes (D232). Both arms use the same agent directory, the same scripted model and the same workload, and sample the memory of the whole process tree before the first turn and after every turn.

```sh
go build -trimpath -o /tmp/pig-listen ./cmd/pig
python3 bench/sessionhost/sessionhost.py --pig /tmp/pig-listen --sessions 1 10 50 100 --json-out sessionhost.json
python3 bench/sessionhost/sessionhost.py --pig /tmp/pig-listen --extensions --sessions 1 10 20 --json-out sessionhost-ext.json
```

- `--extensions` adds the two extension fixtures of `coding/cli/testdata` (`listen-counter-go`, `listen-counter.mjs`) to the agent directory, so every session loads a Go and a Node extension. It needs Node on `PATH` and the Go toolchain. A one-session warm-up run builds the Go extension before any arm is measured.
- `--turns T` runs the first T of the five prompts; `--arms listen rpc` selects the arms.
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

## Result (2026-11-01, Apple M3 Max, macOS 27.2, `phys_footprint` after turn 5, 0 errors)

| Sessions | `listen` | `rpc` |
|---|---|---|
| 1 | 20.8 MB | 28.3 MB |
| 10 | 52.4 MB | 284.7 MB |
| 50 | 166.5 MB | 1430.0 MB |
| 100 | 315.0 MB | 2853.4 MB |
| slope per additional session (N 1 to 100) | 2.97 MB | 28.5 MB |

With the two extensions (`--extensions`):

| Sessions | `listen` | extension processes | `rpc` | extension processes |
|---|---|---|---|---|
| 1 | 87.6 MB | 2 | 103.8 MB | 2 |
| 10 | 209.4 MB | 2 | 1047.4 MB | 20 |
| 20 | 307.1 MB | 2 | 2132.9 MB | 40 |
| slope per additional session (N 1 to 20) | 11.6 MB | | 106.8 MB | |

The `listen` slope without extensions is above the 0.5 MB per session of the Go SDK spike in `docs/specs/multi-session-rpc-server.md`: the server builds each connection's own settings, resources, `ModelRuntime` and Session, and the spike shared them. The bench does not separate the `ModelRuntime` from the rest of the build. The extension slope is the memory of each session's extension instances inside the two shared processes.
