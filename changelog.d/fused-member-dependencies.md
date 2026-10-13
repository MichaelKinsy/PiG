### Fixed

- `pig piglet build` can fuse a Go extension that depends on third-party modules. The build's `go.mod` and `go.sum` now live beside the build overlay as a `-modfile` pair, seeded with every fused module's checksums and local `replace` directives, and `go mod tidy` runs over them before compiling. A fused module's own dependencies can therefore raise the versions Pig pins, where the build used to stop with `go: updates to go.mod needed; to update it: go mod tidy` against a staged source tree nobody can tidy.
