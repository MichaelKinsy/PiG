# Parity reliability

## Read first

- `../docs/project/CONTEXT.md`
- `README.md`
- `coverage.md`
- `../PORT_MAP.md`
- `../DIVERGENCES.md`

## Contract

Pinned Pi is the behavior oracle. A scenario proves only its asserted path. Generated inventories define review obligations, not acceptance. A comparator must expose every observed difference unless a numbered divergence owns it.

## Failure modes

| Failure | Detection | Required result |
|---|---|---|
| A scenario passes without reaching the feature | Mutate the owning branch | The scenario fails behaviorally |
| Normalization hides a defect | Inspect raw and normalized output | Fix PiG or cite an approved divergence |
| A crop excludes the difference | Compare the complete owning surface | Keep the difference visible |
| Timing changes queue order | Use barriers and acknowledgements | Assert exact order without retries |
| A generated count is treated as proof | Trace it to reviewed mappings and execution | Keep denominator and evidence separate |
| Oracle version drifts | Check binary version, tag, commit, and package lock | Fail before comparison |
| Evidence cannot be repeated | Replay its command in an isolated environment | Reproduce the same contract |

## Evidence

Probe upstream first. Record the pin, environment, inputs, raw output, comparator, and expected result before production changes. Run at least one compiling mutation for each new behavioral scenario. Retain failure and final-pass artifacts outside the source tree. Do not weaken comparators, add retries, widen timeouts, or update expected output to hide a diagnosed defect.
