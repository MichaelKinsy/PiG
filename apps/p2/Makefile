# apps/p2 targets. `make -C apps/p2 web-gate` runs what `go test` skips without Node and a browser (web/gate.sh).
.PHONY: test web-gate upstream-binary
test:
	go vet ./...
	go test -race ./...

web-gate:
	web/gate.sh

# A p2 binary embedding Pi's own (unbranded) web bundle, for the D472 oracle runs: make upstream-binary OUT=/tmp/p2-upstream
upstream-binary:
	web/build/upstream-binary.sh $(or $(OUT),p2-upstream)
