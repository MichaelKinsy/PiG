### Changed

- The root module zip that `go install github.com/MichaelKinsy/PiG/cmd/pig@version` downloads is 52 MB smaller, 129.5 MB instead of 181.9 MB uncompressed. The committed Node runtime source tree, the documentation tree, and test and tooling data that no build reads now sit in data-only nested modules: they stay in the repository and are read by path in a checkout, but the module zip omits them. The `pig` binary is unchanged (the same SHA-256), and `make module-size` now holds the zip to a 133 MB budget.
