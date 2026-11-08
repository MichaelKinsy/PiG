package delta

import (
	"strings"
	"testing"
	"unsafe"
)

// What Pi's ownedSlice protects (utils/text.ts, Pi f715a15c1: an append op cut from a long, growing string must not keep that
// whole string alive), tested in PiG's own way: an append op the diff emits owns its text, in the bytes of no revision, whether
// the text only grew (the prefix case) or its head overlaps the old tail.

func appendTextOf(t *testing.T, before, after string) string {
	t.Helper()
	var text string
	for _, op := range DiffRevisions(JsonObjectOf("t", before), JsonObjectOf("t", after)) {
		if Verb(op) == "a" {
			text, _ = op[2].(string)
		}
	}
	if text == "" {
		t.Fatalf("no append op for a change of %d to %d bytes", len(before), len(after))
	}
	return text
}

func insideBytesOf(text, source string) bool {
	start := uintptr(unsafe.Pointer(unsafe.StringData(source))) //nolint:gosec // G103: the test compares data addresses to prove the op copies
	at := uintptr(unsafe.Pointer(unsafe.StringData(text)))      //nolint:gosec // G103: as above
	return at >= start && at < start+uintptr(len(source))
}

func TestADiffsAppendOpOwnsItsTextAndKeepsNoRevisionAlive(t *testing.T) {
	before := strings.Repeat("a", 4096)
	after := before + strings.Repeat("b", 64)
	text := appendTextOf(t, before, after)
	if text != strings.Repeat("b", 64) {
		t.Fatalf("append text = %q", text)
	}
	if insideBytesOf(text, after) {
		t.Fatal("the append op's text is a view into the revision it was cut from: it would keep all of it alive")
	}
}

func TestADiffsAppendOpAfterAnOverlapOwnsItsTextToo(t *testing.T) {
	before := strings.Repeat("Z", 4096) + "tail"
	after := "tail" + strings.Repeat("B", 4096)
	text := appendTextOf(t, before, after)
	if text != strings.Repeat("B", 4096) {
		t.Fatalf("append text has %d bytes, want the 4096 after the shared tail", len(text))
	}
	if insideBytesOf(text, after) {
		t.Fatal("the append op after an overlap is a view into the revision it was cut from")
	}
}
