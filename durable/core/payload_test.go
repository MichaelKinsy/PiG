package core

import "testing"

// A model request may be a megabyte-scale buffer. Two requests of one step must not share storage (the Builder keeps both until the
// step is encoded), and the next step must get a used buffer back instead of allocating: TinyGo's collector frees large
// blocks late and the linear memory doubled until the core trapped (W1-3500).
func TestPayloadBuffersAreRecycledOnlyAfterTheStepIsEncoded(t *testing.T) {
	h := &handler{}
	first := append(h.payloadBuffer(0), "first"...)
	h.keepPayload(first)
	second := append(h.payloadBuffer(0), "second"...)
	h.keepPayload(second)
	if &first[0] == &second[0] || string(first) != "first" {
		t.Fatal("two payloads of one step share storage")
	}
	h.StepStart()
	reused := h.payloadBuffer(0)
	if len(reused) != 0 || cap(reused) == 0 || (&reused[:1][0] != &first[0] && &reused[:1][0] != &second[0]) {
		t.Fatalf("the next step does not reuse a buffer: len %d cap %d", len(reused), cap(reused))
	}
	for range 2 * maxPayloadBuffers {
		h.keepPayload(make([]byte, 0, 8))
	}
	h.StepStart()
	if len(h.payloadFree) > maxPayloadBuffers {
		t.Fatalf("%d buffers kept, bound is %d", len(h.payloadFree), maxPayloadBuffers)
	}
}

// A whole-context request leaves a buffer of megabytes: the next step does not keep it (TinyGo kept it, and the copies its
// growth left, alive through the first turn at 3,500 turns), and a request is sized once, not grown by appending.
func TestALargePayloadBufferIsNotKept(t *testing.T) {
	h := &handler{}
	big := h.payloadBuffer(payloadKeep + 1)
	if cap(big) < payloadKeep+1 {
		t.Fatalf("payloadBuffer(%d) has cap %d", payloadKeep+1, cap(big))
	}
	h.keepPayload(big)
	small := h.payloadBuffer(16)
	h.keepPayload(small)
	h.StepStart()
	if len(h.payloadFree) != 1 || cap(h.payloadFree[0]) != cap(small) {
		t.Fatalf("kept %d buffers after a large and a small payload; want only the small one", len(h.payloadFree))
	}
	if got := h.payloadBuffer(cap(small) + 1); cap(got) < cap(small)+1 {
		t.Fatalf("a kept buffer smaller than the request was returned (cap %d)", cap(got))
	}
}
