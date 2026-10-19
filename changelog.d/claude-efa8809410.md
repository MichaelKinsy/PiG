### Fixed

- `go install github.com/MichaelKinsy/PiG/cmd/pig@<version>` downloads a root module of 181 MB instead of 304 MB: nine data-only directories (parity interface ledgers, pin ledgers, upstream-sync ledgers and large test data) have their own `go.mod`, so Go leaves them out of the root module zip. The files stay in the repository.
- `make module-size` fails a pull request when the root module zip exceeds the committed byte budget, and proves that `go install` of `cmd/pig` works from the module zip.
