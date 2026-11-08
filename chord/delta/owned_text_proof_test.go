package delta

import (
	"strings"
	"testing"
	"unsafe"
)

// What Pi's ownedSlice protects (utils/text.ts, Pi f715a15c1: an append op cut from a long, growing string must not keep that
// whole string alive), tested in PiG's own way: an append op the diff emits owns its text, in the bytes of no revision.

func TestADiffsAppendOpOwnsItsTextAndKeepsNoRevisionAlive(t *testing.T) {
	before := strings.Repeat("a", 4096)
	after := before + strings.Repeat("b", 64)
	ops := DiffRevisions(JsonObjectOf("t", before), JsonObjectOf("t", after))
	var text string
	for _, op := range ops {
		if Verb(op) == "a" {
			text, _ = op[2].(string)
		}
	}
	if text != strings.Repeat("b", 64) {
		t.Fatalf("append text = %q, ops = %v", text, ops)
	}
	start, end := uintptr(unsafe.Pointer(unsafe.StringData(after))), uintptr(unsafe.Pointer(unsafe.StringData(after)))+uintptr(len(after))
	if at := uintptr(unsafe.Pointer(unsafe.StringData(text))); at >= start && at < end { //nolint:gosec // G103: the test compares data addresses to prove the op copies
		t.Fatal("the append op's text is a view into the revision it was cut from: it would keep all of it alive")
	}
}
