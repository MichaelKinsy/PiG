### Fixed

- `auth.json` locking follows symlinks on the store file so writers using different links to the same credential store share one lock. <!-- fix: 4a2d5cf2b1 -->
