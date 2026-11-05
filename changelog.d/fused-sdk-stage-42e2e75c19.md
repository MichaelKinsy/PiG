### Fixed

- A Piglet Binary whose extensions are all fused no longer stages the extension SDK sources into a clean config root before its first prompt. The first start of a Piglet with plan mode fused took 78 ms to the first usable prompt against 30 ms for a bare pig, and now takes 32 ms; later starts were never affected. <!-- fix: 42e2e75c19 -->
