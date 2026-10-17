# PTY journeys

A journey is a long session in a real terminal. The harness in `test/journeys/harness` starts PiG and Pi on the same scripted OpenAI-compatible provider, drives both through the same steps, and compares the provider requests, the session files, the agent files named by the journey and every rendered frame. A difference fails the journey. A known difference is a `Normalizer` that needs a `Reason`.

## Running

```sh
make journeys                                   # every journey, as CI does
make journeys-changed                           # journeys whose Touches prefixes contain a change since JOURNEY_BASE (default @{upstream})
JOURNEY_ONLY=compaction JOURNEY_SEED=3 go test -tags parity ./test/journeys -run TestJourneys -count=1 -v
```

| variable | effect |
|---|---|
| `JOURNEY_SEED` | seeds the random order of a journey's action pool; a failure prints the replay command |
| `JOURNEY_SHRINK_BUDGET` | extra runs allowed to shrink a failing plan (0 disables) |
| `JOURNEY_PARALLEL` | journeys in flight (default 2); set it to the CPU slot of the lane |
| `JOURNEY_PIG_VARIANT` | `instrumented` (default: race detector, lock-order wrappers, request validity) or `plain` |
| `JOURNEY_ALWAYS_ARTIFACTS`, `JOURNEY_KEEP` | keep frames, requests and sessions of passing runs, and the sandboxes of failing ones |
| `JOURNEY_TIMEOUT_SCALE` | multiplies every wait deadline on a loaded host |
| `JOURNEY_STRICT_NORMALIZERS` | with the whole set, fails a normalizer that no longer matches anything |

The comparator is the pinned Pi (Pi 1.3.0). `make parity-deps` builds it from source at the pinned commit and links it into `extensions/sdk-ts`. `JOURNEY_PI_BIN` or `PIG_PARITY_PI_BIN` names another Pi binary. With `PIG_PARITY_PI_PUBLISHED=1` and a release-tag pin, the journeys also accept the published package that mise installed. Put `fd` and `rg` on `PATH` for the `@` file mentions.

## Checks in every journey

- The provider request checker rejects a malformed transcript on the wire.
- The instrumented PiG binary reports any lock-order cycle (`internal/lockorder`) and any data race.
- A hang is a failure: a wait ends after 25 seconds without a terminal or provider event, and the product's goroutines are dumped with SIGQUIT. Waits are events, never sleeps.
- Peak resident memory of the PiG process tree (checkpoints and the main process high-water mark) is bounded per journey: 200 MiB by default (`Journey.MemMaxMB`), four times that for the instrumented build. No child process survives the session.
- Any output on stderr fails the journey.

## Writing one

A journey has a backbone of steps and a pool of actions. The seed picks the order of the pool, so each run is a different but replayable session. Every action starts and ends at an idle prompt and names the actions it needs. Wait for the text an action's outcome produces, with a marker that no earlier action left on the screen. Put a difference between the products in a `Normalizer` only after checking that it is not a bug in PiG.

### Journeys over pipes

`Env{Pipe: true, Args: []string{"--mode", "rpc"}}` drives the process through standard input and output instead of a terminal. The screen is the standard output so far, one JSON record per line, and a frame is what the process wrote since the previous frame. Use `c.Send` for a command, `c.WaitFor` and `c.WaitCount` to wait for records, and normalizers for wall-clock times, ids and paths. `c.Quit` closes standard input. `j15_rpc_test.go` is the example.

## Gating

`make journeys-changed` selects by `Journey.Touches` (path prefixes). A change under `cmd/pig/`, `test/journeys/`, `internal/`, `ai/`, `agent/`, `go.mod` or `go.sum` selects every journey: every journey compares provider requests, session files and frames, so a package on those paths affects all of them. Documentation selects none. A port that changes a constructor or the initialization order must run the journeys in its gate whether or not a prefix matches: use `make journeys`.

Timings are in `tmp/journeys/timing.json`. The 14 journeys take about 2 minutes of wall time at `JOURNEY_PARALLEL=2` and about 3.5 minutes of summed journey time.
