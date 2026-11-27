### Changed

- Rust extension builds no longer run cargo one job at a time. The packed cell build and the standalone Rust build now share one memory-bounded job budget: one job per GiB of the half of the machine's physical memory, capped at its CPU count, divided between the Rust builds the process runs at once, with one job guaranteed to a build that starts while the budget is spent. A cold cell build took 192.4 s at one job against 47.7 s at sixteen on a 16-CPU, 16 GiB machine. `CARGO_BUILD_JOBS` still overrides the budget.
