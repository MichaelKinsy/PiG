# Progress: port-99-f6c (family 6, lane 6C)

Lane branch `port-99-f6c` from `staging/porter/pi-0.99.1` (15904a765). Evidence: `docs/plan/evidence/family-6c-resources.md`.

| stage | commit | notes |
|---|---|---|
| red | a9c131c48 | upstream 0.99.1 cases ported with stubs; 84 failing subtests recorded in `family-6c-resources.red.txt` |
| green | 1d0ac3423 | extension set, builtin resolution and config group, git install args, pinned temp ref; ported tests unchanged |
| refactor, mutation, load | this commit | `go fix` in `PackageManagerName`; 15 of 15 mutations killed; race and load runs in the evidence |
