# apps/p2 targets. `make -C apps/p2 web-gate` runs what `go test` skips without Node and a browser (web/gate.sh).
.PHONY: test web-gate
test:
	go vet ./...
	go test -race ./...

web-gate:
	web/gate.sh
