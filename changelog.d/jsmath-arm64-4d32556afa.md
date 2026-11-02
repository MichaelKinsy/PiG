### Fixed

- `Math.exp` in `internal/jsmath` returns Node's bits on arm64. V8's fdlibm build contracts products into multiply-adds there, and `Exp` rounded them, so the last bit differed from `Math.exp` for about 1 in 2500 arguments on arm64 hosts. GOARCH=amd64 results are unchanged. <!-- fix: 4d32556afa6bcc5937402bcab72fdf9348ad5c5f -->
