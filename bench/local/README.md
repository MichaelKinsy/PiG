# Local-model harness cost (bench/local)

What a coding harness costs while a local model answers: the prompt it sends (tokens, tools, system prompt), the time and CPU it adds, its memory, its state on disk, and whether the task gets done. Every number is PROVISIONAL until repeated on a quiet machine. The pack itself posts nothing; a harness that connects outside the loopback on its own is caught by the meter's socket audit (Linux) and listed per arm in the report. omp and OpenCode do (see `bench/evals/README.md`).

Everything runs against a pinned llama.cpp server (`runtimes.lock`: release tag, archive name, sha256, size; macOS arm64 Metal and Linux x64 CPU) and a pinned model (`models.lock`: Hugging Face repo, exact revision, file sha256 and size). Both install into a throwaway root or a model cache (`PIGPACK_CACHE`); nothing is installed globally. The server listens on 127.0.0.1. Harnesses are the pinned arms of `bench/evals/arms.lock` and talk to the server through a recording proxy (`proxy.py`) that stores sizes, counts, times and token usage, never bodies or keys.

```bash
make bench-test                                   # bench/evals and bench/local tests
python3 bench/local/localpack.py                  # install, serve, one completion (the dry run)
python3 bench/local/localrun.py --arms pig-0.5 pi-1.1.0 --turns 10 --rounds 3 --out tmp/bench/local   # per-turn harness cost, RPC for Pi-family arms
python3 bench/local/localrun.py --arms pig-0.5 codex copilot --mode print --out ...                    # one-shot prompts for harnesses without RPC
python3 bench/local/localrun.py --arms pig-0.5 pi-1.1.0 --task rangesum --attempts 3 --out ...       # task completion, the pinned check decides
python3 bench/local/report.py tmp/bench/local/raw.json pi-1.1.0                                       # report.md next to raw.json
```

`make bench-local BENCH_LOCAL_ARGS="--arms ... --model <name>"` runs the first form and renders the report.

## Models (`models.lock`)

| name | role | size |
|---|---|---|
| qwen2.5-0.5b-instruct-q4_k_m | dry run on a CPU only | 0.5 GB |
| qwen2.5-coder-7b-instruct-q4_k_m | small coding model | 4.7 GB |
| qwen3-coder-30b-a3b-instruct-q4_k_m | mid coding model, 30B MoE with 3B active | 18.6 GB |
| gpt-oss-20b-mxfp4 | third model, 20B MoE | 12.1 GB |

## A hosted model instead

`--hosted-base-url URL` with the key in `LOCAL_PACK_API_KEY`. The proxy puts the key on the upstream request only; it is never written to a file, a log or a record. The proxy runs in the bench's own process, so its upstream connection is not counted; the harness's own connections are audited as in the local mode. The model is whatever the endpoint serves.

## Running on an Apple-silicon Mac (the models are sized for 48 GB)

Only when the machine is idle: close other apps, plug in power, disable low-power mode, and note `memory_pressure` before and after. Use a context of at least 32768 for the 7B and larger models (`--context 32768`): Claude Code and Copilot refuse or overflow a 16384 context, which is a result, not an error. Use `--threads` equal to the performance core count (10 on an M4 Pro, for example) so the harness and the server do not fight over efficiency cores.

## Not covered yet

- MLX (`mlx-lm` server) has no pinned runtime entry: the venv needs `--require-hashes` pins for macOS arm64 wheels, which must be computed on that machine.
- omp, OpenCode and gi arms: they need their own configuration writers in `bench/evals/pigbench/harness.py`. gi has no print mode, so its print rows are "cannot run".
- Per-turn RSS after 50 turns uses `--turns 50`; the "mid-task" RSS is the value after 5 turns.
- The timings are CPU-only on Linux; Metal numbers exist only after a Mac run.
