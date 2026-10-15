# Durable core design

Design for PiG's synchronous Durable core: one Go core that reproduces Pi Durable's store and behaviour, runs as Wasm inside a Cloudflare Durable Object and natively in PiG and other hosts with a synchronous SQLite. Status: design, 2026-10-06, adversarially reviewed the same day (lane rev-do-core-design: citations checked against the Pi 1.0.4 mirror, Cloudflare facts re-fetched, ABI gaps and bake-off fairness fixed in place); the bake-off lanes start from [BAKEOFF.md](BAKEOFF.md).

| document | content |
|---|---|
| [CONTRACT.md](CONTRACT.md) | what "same as pi-durable 1.0.4" means, the semantics, the corpus, and the capture and diff tooling that checks it |
| [ADR-0001-core-architecture.md](ADR-0001-core-architecture.md) | the architecture decisions, each with its evidence or the hypothesis that tests it |
| [ABI.md](ABI.md) | ABI 1: the host interface shared by every Wasm lane, and the native Go binding of the same core |
| [BAKEOFF.md](BAKEOFF.md) | lanes, hypotheses, workloads, metrics, baselines, acceptance gates, run rules and the decision rule |
| [LAYOUT.md](LAYOUT.md) | the production core's packages, owners, seams, core rules and gate |
| [RISKS-AND-QUESTIONS.md](RISKS-AND-QUESTIONS.md) | owner questions, items for the researcher to verify, and risks |

Inputs: the researcher's notes and spike bundle, pi-durable 1.0.4 (`packages/durable`: `docs/spec.md`, `src/testing`, `test/examples`), PiG's `durable/` port, durable-bench, and the durable-bench-pig, durable-perf and durable-wasm-spike lanes' results.
