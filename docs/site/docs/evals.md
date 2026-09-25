# Harness benchmarks

Every harness below talks to the same local mock model. The mock answers each request with one fixed reply over the harness's own wire protocol (OpenAI Chat Completions, OpenAI Responses, or Anthropic Messages), so the model adds no variance. The differences come from the harness: process startup, request construction, streaming, Session I/O, and teardown.

These numbers do not measure model quality or task success. `pigeval live` runs fixed coding tasks with a real model for that.

Measured 2026-09-23T11:11:39Z on Linux-6.18.44-fc-v37-x86_64-with-glibc2.39 (x86_64, 1 CPUs). Each scenario ran 1 warm-up run and 10 measured runs. Prompt: `Say hello`.

## Harnesses

| Harness | Version | Version command output | Status |
|---|---|---|---|
| PiG | not recorded | `pig --version`: `0.87.1` | measured |
| Pi | `0.87.1` | `pi --version`: `0.87.1` | measured |

Version is each harness's own version. Version command output records the startup command separately. The PiG binary measured in this run predates the composite version string and printed only the pinned Pi release. Current PiG's `--version` prints its composite version, `<PiG release>+<Pi release>` (D63).

## Startup (--version)

| Harness | Median | p90 | CPU | Peak RSS |
|---|---:|---:|---:|---:|
| PiG | 21.5 ms | 22.4 ms | 21.0 ms | 24.6 MiB |
| Pi | 300.3 ms | 309.6 ms | 299.0 ms | 101.5 MiB |

## One prompt, streamed reply

| Harness | Median | p90 | CPU | Peak RSS | Requests | First request | System prompt | Tools |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| PiG | 29.1 ms | 30.7 ms | 27.9 ms | 29.8 MiB | 1 | 5,655 B | 2,466 B | 4 |
| Pi | 439.8 ms | 453.7 ms | 436.6 ms | 110.5 MiB | 1 | 5,766 B | 2,656 B | 4 |

## One prompt after resuming a large Session (2,000 exchanges)

| Harness | Median | p90 | CPU | Peak RSS | Requests | First request | System prompt | Tools |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| PiG | 128.7 ms | 135.1 ms | 124.6 ms | 49.4 MiB | 1 | 425,203 B | 2,454 B | 4 |
| Pi | 614.6 ms | 626.8 ms | 608.4 ms | 126.4 MiB | 1 | 425,315 B | 2,645 B | 4 |

Peak RSS is the largest single process in the harness's process tree. Request sizes are the bytes of the HTTP request body.

## Reproduce

```bash
make setup SETUP_ARGS=--harnesses=all   # installs the pinned versions above
make evals
```

Results depend on the machine. Compare numbers from one run only. The harness registry, including every command line, is `evals/harnesses.toml`; the method is in `evals/README.md`.
