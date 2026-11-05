### Fixed

- `pig piglet build` produced a dynamically linked Piglet Binary unless `CGO_ENABLED=0` was set, so a Binary needed libc although nothing in it uses cgo. The build now sets `CGO_ENABLED=0` unless you set `CGO_ENABLED` yourself (in the environment or with `go env -w`) or a package of the Binary has cgo sources, so a cgo-free Binary is static and runs in an empty container.
