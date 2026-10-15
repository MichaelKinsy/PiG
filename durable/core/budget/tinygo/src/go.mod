// The TinyGo runtime fork is a module of its own, so the root module's `go build ./...` and `go vet ./...` do not
// load it as a Go package (its files build only inside TinyGo's GOROOT overlay; budget/build-wasm.sh copies the .go
// files under src/ over the toolchain's and ignores this file).
module pig-tinygo-runtime-fork

go 1.26
