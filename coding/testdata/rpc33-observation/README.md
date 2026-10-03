# RPC33 exact-Pi fixtures

`probe.mjs` runs the reference implementation. It pins Pi 1.0.0 and OpenAI 7.19.0 before constructing the matrix. `inputs.json` exports the probe's axes and exact HTTP response bodies. `pi.json` retains the complete raw first fresh oracle execution, including clocks. Do not replace it with Go output.

The owning explanation is [`docs/findings/rpc33-observation-matrix.md`](../../../docs/findings/rpc33-observation-matrix.md). The Go differential test is `TestRPC33ObservationMatrix` in `coding/rpc33_observation_matrix_test.go`. Declared durability is three complete executions. No Node process or credentials are needed to replay the checked-in oracle against Go.

## Reproduce Pi

Run from the repository root with the pinned Node toolchain and installed Pi dependency. Set `EVIDENCE` to an external writable directory. Keep the evidence directory across runs.

```bash
private=$(mktemp -d)
mkdir -p "$private/home" "$private/pig" "$private/pi" "$EVIDENCE"
export PI_PACKAGE_ROOT="$(realpath extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent)"
for run in 1 2 3; do
  env HOME="$private/home" PIG_CODING_AGENT_DIR="$private/pig" PI_CODING_AGENT_DIR="$private/pi" \
    node coding/testdata/rpc33-observation/probe.mjs \
    "$EVIDENCE/pi-$run.json" "$EVIDENCE/inputs-$run.json" \
    >"$EVIDENCE/pi-$run.log" 2>&1
done
rm -rf "$private"
```

The optional second output argument exports the input fixture. The probe never updates checked-in goldens implicitly. Review changes against the exact source and all retained observations before accepting a new fixture. Go checks the Pi pin and derives every case from the input axes.

## Run Go

Use private HOME and agent directories for the outer Go process as well. The test additionally isolates those directories internally. A unique `run-*` child retains each execution's complete records and full diffs, so `-count=3` never overwrites a prior result.

```bash
private=$(mktemp -d)
mkdir -p "$private/home" "$private/pig" "$private/pi"
env HOME="$private/home" PIG_CODING_AGENT_DIR="$private/pig" PI_CODING_AGENT_DIR="$private/pi" \
  go test ./coding -run '^TestRPC33Observation' -parallel 8 -count=3 -timeout=240s \
  -rpc33-observation-output="$EVIDENCE/go"
rm -rf "$private"
```

Preserve normal toolchain cache variables outside the private HOME if the local Go installation requires them. The retained lane's `isolated.sh` does this without reading worker auth or settings.

`go.json` contains all compared records. Each case also has its own `.go.json`. `.native.json` retains every native Agent callback before the explicit tagged-union projection, including Go timing events. `.diff` contains the complete clock-canonicalized Pi-to-Go difference. A missing diff means that case compared equal, not that the aggregate matrix passed. Only an exit-zero complete execution qualifies.

## Fixture provenance

The retained source probe is `rpc33-probe.mjs` in the `integrate-030-r2` evidence directory recorded in the maintainer handoff. Its original complete runs are `rpc33-pi-final-{1,2,3}.json` in that directory. Fresh evidence resides in the `rpc33-observation-h4` evidence directory.

The checked-in raw oracle is the first execution of the maintained `probe.mjs` against the real Pi 1.0.0 package (Node 24.19.0), and its SHA-256 is `4d69deb7e8adcec414a138fb4242fe8fddd9cdd83a1584e6cd9c838f8e130420`. Two further executions compare equal to it after numeric assistant/tool-result timestamp canonicalization, and equal the previous Pi 0.99.2 oracle under the same canonicalization.

The exact OpenAI source hashes used for continuation investigation are:

- `core/streaming.mjs`: `6ea8902e35b980775b9bf0effba19ec4f09ac57030d9ed339c7af861682f6d31`.
- `internal/shims.mjs`: `0eeb939ee18f8df00273d819ffe94d84fa2c3e990485902c4c94a108669ffaea`.

The source-red harness is not yet approved for default integration. The parent must first qualify the dependent production changes against the unchanged full observations.
