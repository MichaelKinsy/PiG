### Upgrading

- **Breaking (Go API, test-only):** `coding`: building the test-only `test-faux` provider under `PIG_TEST_FAUX=1` needs `coding.EnableTestFauxProvider()`, which the `pig` command calls where it starts. A program that serves `test-faux` through `coding.BuildModel` calls it once first; until then the build fails with an error that names the call. A program that never serves it needs no change.

### Changed

- A program that embeds PiG without the `pig` command no longer links the test-only `test-faux` provider and its scenario classifier. `examples/headless` on linux/arm64, stripped as released, is 131,072 bytes smaller (28,180,640 to 28,049,568).
