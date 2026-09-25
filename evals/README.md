# Evals

`pigeval` measures coding-agent harnesses. It is stdlib Python 3.11 or later
and runs through `make`:

| Target | What it does |
|---|---|
| `make evals` | Overhead: startup, one prompt round trip, and a resumed 2,000-exchange Session, against a mock model. `HARNESSES=all RUNS=10`. |
| `make evals-requests` | Saves every request body to `tmp/evals/requests/` and diffs the first two harnesses (default pig against pi). |
| `make evals-live MODEL=provider/model` | Runs `evals/tasks` with a real model and checks each result. |
| `make perf-check` | Applies `budgets.toml` to the last overhead run (`EVAL_RESULTS`). |
| `make evals-publish` | Copies a reviewed run to `results/latest.json` and regenerates `docs/site/docs/evals.md`. |
| `make profile`, `make pgo` | Profiles pig round trips with `PIG_PROFILE`; `pgo` merges CPU profiles for a profile-guided build. |
| `make bench`, `make bench-compare` | Go benchmarks and a median comparison against a saved base. |
| `make evals-test` | Unit tests for pigeval. |

`make setup SETUP_ARGS=--harnesses=all` installs the pinned third-party
harnesses under `~/.cache/pig-dev/harnesses`, never globally.

## Method

**Overhead.** The mock model answers every request with one fixed reply in the
harness's own protocol: OpenAI Chat Completions, OpenAI Responses, or Anthropic
Messages. Replies take no model time, so what remains is the harness: process
startup, request construction, streaming, Session I/O, and teardown. Each
harness runs with an isolated `HOME` and no credentials in its environment. We
report the median, p90, CPU time, and the peak RSS of the largest process in the
harness's process tree.

**Requests.** The mock records every request body. `--diff` compares system
prompt, tool definitions, tool order, and top-level fields. We use this to keep
PiG's requests byte-for-byte faithful to Pi's, apart from recorded divergences.

**Live tasks.** Each task in `tasks/` is a small repository, a prompt, a check
command, and a list of protected files. A run passes when the check exits 0,
no protected file changed, and the harness did not time out. Every task's check
fails before the fix; the unit tests assert that.

**Mutation tasks.** `make evals-mutate` generates seeded bug-fix tasks from PiG's
own Go source, following the method in Can Bölük's "The Harness Problem" and
oh-my-pi's edit benchmark: take a real file, apply one mechanical bug with a
known inverse (inverted comparison, flipped boolean, off-by-one bound, swapped
logical operator, removed guard clause), and describe it in plain English. A run
passes when the file's gofmt output hashes to the original's, so any fix that
restores the code counts. The generator keeps a task only after checking that it
fails as mutated and passes restored.

**Live metrics.** For Pi-family, Claude Code, and Codex output, each live run
records turns, tool calls, polling calls, context tokens, output tokens, and cost.
The report shows cost per task, cost per passing task, and pass rate with the
standard error across runs, the comparison Can Bölük used for oh-my-pi's no-poll
change. `slow-build` exists to make waiting behavior visible.

## Adding a harness

Add a table to `harnesses.toml`. Pin the npm version, give the print-mode argv
with `{prompt}`, and say which protocol it speaks (`api`) and how it learns the
mock's base URL (`config`, `env`, or argv placeholders such as `{base_url}`).
Unknown keys are rejected. Harnesses without a custom-endpoint mechanism set
`mock = false` and run only in live evals. A harness whose `--version` prints
another tool's version sets `own_version` to an argv whose first line ends with
its own version. The report shows both. PiG needs none: `pig --version` prints
its composite version, for example `0.2.0+0.87.1` (D63).

## Adding a task

Create `tasks/<id>/task.toml` with `prompt`, `check`, and `protected`, and put
the starting repository in `tasks/<id>/files/`. Keep tasks small, deterministic
to check, and failing before the fix.

## Reading results

Compare numbers from one run on one machine only. Overhead numbers say nothing
about model quality. Publish third-party results neutrally, in registry order,
with the pinned versions shown, and describe differences without adjectives.
