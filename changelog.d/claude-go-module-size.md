### Fixed

- `go install github.com/MichaelKinsy/PiG/cmd/pig@latest` works again. The 0.5.0 Go module was larger than the Go module size limit because it carried parity ledgers for every earlier Pi pin; only the current pin's ledgers ship now, and v0.5.0 is retracted.
