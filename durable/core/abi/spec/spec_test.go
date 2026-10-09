// SPDX-License-Identifier: MIT

package spec

import (
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

// TestGeneratedFilesAreCurrent fails when a table changed without go generate ./durable/core/abi.
func TestGeneratedFilesAreCurrent(t *testing.T) {
	if abi.ID != ID() {
		t.Fatalf("abi.ID %s is stale; tables hash to %s (go generate ./durable/core/abi)", abi.ID, ID())
	}
	have, err := os.ReadFile("../abi.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := JSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(have) != string(want) {
		t.Fatal("abi.json is stale (go generate ./durable/core/abi)")
	}
}

// TestS0TablesUnchanged pins abi_id to the value durable-s0 (58b037a23) froze for ABI 1, so the production core and the
// S0 shim interoperate. A deliberate table change updates this constant in the same commit as the ABI document.
func TestS0TablesUnchanged(t *testing.T) {
	const s0 = "f82162f61f856df71b0d2d25a1016666bd3deda899713aa5b1e1c028827d1fa1"
	if ID() != s0 {
		t.Fatalf("abi_id %s differs from the S0 value %s", ID(), s0)
	}
}

func TestIDIsContentIdentity(t *testing.T) {
	id := ID()
	if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
		t.Fatalf("abi_id %q is not 64 hex digits", id)
	}
	old := eventRows[0].Layout
	eventRows[0].Layout += "!"
	changed := ID()
	eventRows[0].Layout = old
	if changed == id || ID() != id {
		t.Fatal("a table change must change the ID and restoring it must restore the ID")
	}
}
