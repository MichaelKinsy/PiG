### Fixed

- `pig piglet build` on Windows no longer fails a healthy Binary with "basic artifact verification timed out after 1m0s": the 60 s budget of the `--version` run was spent while Windows scanned the freshly signed executable before starting it, and now counts only from the moment the process exists. A hung artifact still fails after 60 s, naming how long the start took and what the process printed, and a canceled build reports its cancellation. <!-- fix: edd520d31 -->
