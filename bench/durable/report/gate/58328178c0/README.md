# CONTRACT gate at dcore-land 58328178c0

`contract all --strict --impls pi,native,wasm-tinygo,wasm-go` (run from `durable/contract`, Pi reference at `da866ada`, Go 1.27.2 from `.go-version`). `report.json` is the gate's report; `contract-all.txt` its summary. Exit status 0.

- The reference `pi` and TinyGo, Go and native: 149 pass, 0 fail, 2 pending (E16, W4f: `pending-owner`, provider recordings that need the owner's API keys), 7 skipped.
- Two ready rows (U-provider-session-cache-e2e, U-system-order-cache-e2e) are skipped by the gate for every implementation because the reference runs none of their tests here; recorded as `rows.skipped_by_reference`.
- Module sha256 (recorded by `contract-record.mjs`): TinyGo `910986e1dd54c874…`, Go `c52592e105ed476f…`, native bench host `2021a256af9edb9a…`. The TinyGo module equals the one the k8s bundle builds from the same runtime.
- 58328178c0 is the landing tip before its merge into the integration line. Numbers measured on it are provisional until the merge and its crash check.
