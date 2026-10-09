# CONTRACT gate at dcore-integrate 112aef14b3

`contract all --strict --impls pi,native,wasm-tinygo,wasm-go` (run from `durable/contract`, Pi reference at `da866ada`). `report.json` is the gate's report; `contract-all.txt` its summary. Exit status 0.

- The reference `pi` and TinyGo, Go and native: 105 pass, 0 fail, 2 pending (E16, W4f: `pending-owner`, provider recordings that need the owner's API keys), 7 skipped.
- Two ready rows (U-provider-session-cache-e2e, U-system-order-cache-e2e) are skipped by the gate for every implementation because the reference runs none of their tests here; recorded as `rows.skipped_by_reference`.
- Module sha256 (recorded by `contract-record.mjs`): TinyGo `557c1d0ded720acd…`, Go `3bdc13f653d0d648…`, native bench host `9505fadb585e12cf…`. A second build from a clean checkout of the same commit gave the same TinyGo and Go modules.
