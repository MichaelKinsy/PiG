# bench/compare: PiG against Pi-Bolt, gi, omp and Pi (PROVISIONAL)

Same-machine comparison of PiG with the other Pi-family harnesses on a scripted mock model (no network, no live keys). Everything in this
directory is PROVISIONAL until a gate (lead or pig-evals) accepts it; do not publish a number from it.

| file | purpose |
|---|---|
| `arms.lock` | The permanent arms: version, source URL, artifact sha256 (also checked against each project's own SHA256SUMS), source commit. |
| `arms.py` | `install` downloads, verifies and unpacks every arm into a fresh root and builds the source arms; `specs` prints the harness `--build` specs; `record` prints the run record. No `curl \| sh`. |
| `adapters/` | One-exec launchers that translate the Pi-Bolt harness's `PI_CODING_AGENT_DIR` contract into an arm's own config (PiG, gi, omp, OpenCode, rpi). Pi, Pi-Bolt and Pi on Node/Bun run natively. HOME is per agent directory. |
| `run_suite.py` | Runs Pi-Bolt's published suite (`bench/` of `opensec-git/Pi-Bolt` at the commit in `arms.lock`, unmodified, not vendored) in interleaved rounds with the arm order rotated. |
| `fake_model_ctx.py` | Scripted OpenAI-compatible mock with honest usage and a per-request log (bytes, tools, system prompt). |
| `ctx_scale.py` | Memory/CPU/latency while the conversation grows to 100k, 500k, 850k, 1M, 2M and 4M tokens (RSS, PSS, private dirty). |
| `agent_scale.py` | N concurrent sessions (1, 10, 100, 1000) per arm: memory per session, total CPU, failures. |
| `cpumon.py` | Host and pinned-core busy fraction during a run (how contended the host was). |
| `report.py` | Aggregates a run directory into the matrix: medians with ranges, "cannot run" rows. |
| `stream_shapes.py` | Streaming CPU per answer shape (prose, code blocks, tables, lists) and wire API (Chat Completions, Anthropic Messages) through Pi-Bolt's Tty harness and `fake_model_stress.py` `mdfile:` scenario; `--aa` adds an A/A copy of every build. |
| `shapes_report.py` | Aggregates `stream_shapes.py` rows: samples that did not run, per-cell medians with A/A deltas, and the ratio and wire-API ranges a summary may quote. |
| `startup_ready.py` | The end of a Pi-family TUI's startup for a harness: the terminal title the TUI sets once startup bound the session (after Pi's "Startup is still in progress" window); a quiet settle for arms that never set a title. |
| `idle_settled.py` | Pi-Bolt's `idle.py`, unmodified, whose first prompt waits for `startup_ready.py`'s marker instead of going out at the first "fake-model". |
| `screen_ready.py` | Pi-Bolt's `e2e_screen.py`, unmodified, with the same wait on the tmux pane title. Takes the bench directory of a whole Pi-Bolt checkout (the scenario streams `scripts/lib/training.md`); PiG goes through `adapters/pig`, since tmux passes only `PI_*` variables. |
| `tests/` | Unit tests for the statistics, failure accounting, mock, reports, streaming-shapes driver and lock checks: `python3 -m unittest discover -s bench/compare/tests`; `arms.py check` fails when a PiG source arm is not a full commit with `repo = "self"`, or when an artifact here is pinned with another sha256 in `bench/evals/arms.lock`. |

Quick start:

```sh
python3 bench/compare/arms.py install --root /path/to/root
lane-run --heavy -- python3 bench/compare/run_suite.py --root /path/to/root --out /path/to/out --rounds 5 --cpus 128-131
python3 bench/compare/report.py --out /path/to/out
```

On a shared host take one heavy slot, pin to idle cores, run 5 or more interleaved rounds, and read `cpumon.jsonl` before trusting a number.
Pi-Bolt's published figures came from pinned idle cores on an AMD EPYC 7B13 (GCP); ratios, not absolute times, are comparable across machines.
