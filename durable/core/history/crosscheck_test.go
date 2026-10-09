// SPDX-License-Identifier: MIT

package history_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/contracttest"
	"github.com/MichaelKinsy/PiG/durable/core/history"
)

func canon(in []byte) ([]byte, error) { return history.Canonical(nil, in) }

// TestCanonicalEncodeVectors runs the conformance lane's encode vectors (JSON.stringify of Pi's object) against Canonical.
func TestCanonicalEncodeVectors(t *testing.T) { contracttest.CheckEncode(t, canon) }

func FuzzCanonicalVsReference(f *testing.F) {
	contracttest.AddEncodeSeeds(f)
	f.Fuzz(contracttest.EncodeFuzz(canon))
}
