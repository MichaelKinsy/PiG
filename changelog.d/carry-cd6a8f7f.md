### Fixed

- `pig -e npm:<pkg>` no longer stays on the version from the first download. On each start and `/reload`, `pig` now reinstalls an `-e` npm package without an exact version (as `name@latest`, or its configured range) when the registry has a newer matching version; offline mode and a failed refresh keep the cached copy. Carried forward from Pi cd6a8f7f (earendil-works/pi#11375), past the 1.4.0+b3a29f11 pin. <!-- fix: cd6a8f7f -->
