### Changed

- `EstimateTokens` and `FindCutPoint` in the Go, Rust and Python SDKs count the UTF-16 length of ASCII text without visiting each character: a 1 KiB ASCII string takes about a third of the time in Go, a twentieth in Rust and a seventh in Python, and `EstimateTokens` over the messages of a 10k-entry branch of ASCII text takes about half as long. The counts are unchanged, and text with other characters takes the time it did.
