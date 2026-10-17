# CONTRACT gate at dcore-integrate bafe0c18bf

`contract all --strict --impls pi,native,wasm-tinygo,wasm-go` (run from `durable/contract`, Pi reference at `da866ada`). `report.json` is the gate's report; `contract-all.txt` its summary.

- TinyGo, Go and native: 105 pass, 0 fail, 2 pending (E16, W4f: `pending-owner`, provider recordings that need the owner's API keys), 7 skipped.
- The reference `pi` against itself: every trace matched. Five bench rows (W1-1000, W1-3500, W2-1000, W2-3500, W4c-1000) exceed the memory bound, which `durable/contract/lib/memory.mjs` defines for candidates and checks only after the trace matched; `contract-record.mjs` records them as `rows.reference_over_memory_bound`. The gate's exit status is 1 for that reason alone.
- Two ready rows (U-provider-session-cache-e2e, U-system-order-cache-e2e) are skipped by the gate for every implementation because the reference runs none of their tests here; recorded as `rows.skipped_by_reference`.
