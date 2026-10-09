### Changed

- `pig piglet build` now names the external Go modules the fused vet did not check. The vet reads only the members' own modules and their workspace modules, so when a fused member requires other modules, the build prints one `note:` after the vet passes, listing each as `path@version` at the version the build selects, sorted by module path, with its replacement when a `replace` directive supplies its source. A build with no such modules prints nothing. The Piglet Binaries guide says what the vet covers and why.
