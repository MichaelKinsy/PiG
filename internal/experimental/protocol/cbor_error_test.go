package protocol

import (
	"errors"
	"testing"
)

// Pi packages/protocol/src/cbor.ts: a value outside the strict subset throws CborError named "CborError".
func TestCborErrorIsNamedLikeUpstreamAndComesFromTheDecoder(t *testing.T) {
	err := NewCborError("bad")
	if err.Error() != "bad" || err.Name() != "CborError" {
		t.Fatalf("error %q name %q", err.Error(), err.Name())
	}
	_, decodeErr := DecodeCbor([]byte{0xff}, CborOptions{})
	var cborErr *CborError
	if !errors.As(decodeErr, &cborErr) || cborErr.Name() != "CborError" {
		t.Fatalf("DecodeCbor error = %#v", decodeErr)
	}
}
