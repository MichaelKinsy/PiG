# Port lens

`make port-lens` (or `go run ./automation/portlens -out build/portlens`) renders a static HTML report of the port. The output is one directory: `index.html`, `assets/`, `views/` and `data/`. It needs no CDN, no server and no network, and it works from `file://`. The output directory must be new, empty or an earlier report: the build replaces its `data/`, `views/` and `samples/` subdirectories, so it refuses any other directory. The same commit gives the same bytes: the report holds no clock and no random value.

```sh
make port-lens                                   # build/portlens/index.html
make port-lens PORT_LENS_OUT=/tmp/lens           # another directory
make port-lens PORT_LENS_SERVE=127.0.0.1:8650    # also serve it with a local http.Server
make port-lens PORT_LENS_TOTALS_LOG=<file>       # add a ledger totals log to the trend
```

## Views

| View | Reads |
|---|---|
| Overview | `.upstream/pin`; `test/parity/interfaces/mapping-v*.json`; the gap baselines under `test/parity/interface-closure/gaps/`; the ratchet baselines; git history of the gap baseline files (or a totals log); `test/parity/upstream-sync`, the test mapping and the behavior-input mapping of the follow target |
| Port map | `mapping-v<pin>.json` for the committed pin and each newer ledger (the main-follow target), joined to the upstream inventory (`upstream-v*.json`, `cli-v*.json`) and `test/parity/families.toml`; Go line numbers come from parsing the Go files each row cites |
| Divergences | `docs/parity/DIVERGENCES.md`, `docs/additive-features.md`, `docs/bug-ledger.json`; `pig divergence (D<N>)` and `pig additive (D<N>)` source markers; Pi source from `.upstream/v<pin>` |
| Bug ledger | `docs/bug-ledger.json` |
| Evals | `bench/durableperf/results/*.jsonl`, `durable/contract/baseline.toml`, `bench/durable/report/gate/*/report.json`, `automation/perf/scoreboard.toml`, `ratchet.toml` and `scoreboard-measured.json`, `bench/compare/results/*.json` |

Nothing is typed into the report by hand. A source that is absent shows as absent: no Pi snippets without the `.upstream` mirror, no proof status without `build/ledger-proof-classes/unproven-behaviour.tsv` (`make port-lens` and `make ledger-check` write it, for the pinned ledger only), no comparison matrix without a committed `bench/compare/results/*.json`.

Raw sample files named by a matrix row or by the Durable arm table are copied into `samples/` and linked, so the output directory stays self-contained.

## Row status and proof class

A row is **ported** when a person reviewed it, **derived** when the gap detector bound it (its rationale starts with "Derived by the interface gap detector"), **designed out** when its disposition says so, and **gap** when its disposition is pending or the gap baseline lists it. The proof class is the required behaviour-proof class of a closed row (D, W, R, U, F, N), derived from the row kind as `test/parity/interface-closure/breakdown/proofclass.py` does. Whether the proof exists comes from that tool's output; without it a row is "not read", never "proven".

The owner family of a row is the first entry of `families.toml` whose scope lists the row's upstream source file. Rows whose file no family lists have no owner family.

## Divergence class

The class is derived and each record states its basis: `additive` for a record in `docs/additive-features.md`; `Pi bug fix` when a bug ledger entry fixed it ahead of Pi or the record cites a Pi issue; then `perf` and `platform` from the title; otherwise `other difference`.

## The Durable bar

The four conditions are judged from committed data. An item with no committed evidence is **not met**, and a measure PiG loses is shown at the same weight as one it wins:

1. `durable/contract/baseline.toml` has no entry.
2. At every size the headline arm (`pig-walk-faux-default-procs`) is below `pi-durable-1.0.4-node` on warm wall, cold wall, whole-sample CPU and peak RSS.
3. Peak RSS grows less than 1 KiB per turn between 1,000 and 3,500 turns.
4. A cluster compare result is committed under `bench/k8s/results/*.jsonl`. The report does not judge such a file, so the item reads not met either way, and the verdict says which case holds.

## Benchmark matrices

`bench/compare/results/*.json` holds same-machine comparison matrices:

```json
{"title": "...", "provisional": true, "machine": "...", "note": "...", "arms": ["a", "b"],
 "rows": [{"id": "r1", "scenario": "startup", "unit": "ms", "lowerIsBetter": true, "provisional": true,
           "samples": "path/to/raw.jsonl",
           "cells": {"a": {"median": 10, "min": 9, "max": 12, "n": 5}, "b": {"cannotRun": "no print mode"}}}]}
```

## Privacy

The report is built so it could be published. Text copied from commit messages loses its `Signed-off-by` and `Co-authored-by` trailers and every e-mail address. A totals log contributes only a time, a pin label and counts. `TestOutputHasNoPrivateData` scans every output file for private hosts, home paths, addresses and organization names.

## Tests

`go test ./automation/portlens` builds the report from the repository and checks that every ledger row of every pin, every D-number of both divergence documents and every bug ledger entry appears; that the gap baseline rows are marked as gaps; that snippets are highlighted at generation time; that the output is deterministic, self-contained and free of private data; that the search index finds every row by each word of its name; and that the color tokens keep WCAG AA contrast in both themes.
