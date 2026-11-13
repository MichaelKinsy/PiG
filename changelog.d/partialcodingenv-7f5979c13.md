### Fixed

- Ctrl-C at the secret prompt of `pig auth login` now drops the rest of its input chunk, so an Enter arriving in the same read can no longer submit the secret, as in Pi. <!-- fix: 7f5979c13 -->
