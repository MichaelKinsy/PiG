# apps/p2 targets. `make -C apps/p2 web-gate` runs what `go test` skips without Node and a browser (web/gate.sh).
.PHONY: test web-gate upstream-binary voice-segmenter-test
test:
	go vet ./...
	go test -race ./...

web-gate:
	web/gate.sh

# A p2 binary embedding Pi's own (unbranded) web bundle, for the D472 oracle runs: make upstream-binary OUT=/tmp/p2-upstream
upstream-binary:
	web/build/upstream-binary.sh $(or $(OUT),p2-upstream)

# Pi's test_segmenter.py needs sherpa-onnx and speech models (HuggingFace, blocked by TLS interception on the hosts), so this
# runs its intention (quiet first word kept, contiguous slice, no reach into the previous utterance) against the vendored
# segmenter.py with a stub VAD. The vendored files stay pinned by plugins/voice/vendor_test.go.
voice-segmenter-test:
	docker run --rm -v $(CURDIR)/voice-server:/app:ro -w /app python:3.12-slim sh -c 'pip install -q --no-cache-dir numpy==2.5.4 && python test_segmenter_stub.py'
