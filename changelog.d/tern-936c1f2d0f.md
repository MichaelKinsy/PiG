### Changed

- The TinyGo Durable core does less allocation work per turn: it builds no hash map for each record write or for each commit that writes no document, and compares member names by length first. A warm turn executes about 4-5% fewer Wasm instructions and makes about 2% fewer allocations. Behavior is unchanged.
