package delta

import (
	"strings"
	"testing"
	"unsafe"
)

// Pi delta/index.ts:118-120 ownedSlice(text, start, end = text.length): `text.slice(start, end)` as a string of its own, so an append
// op cut from a long, growing string does not retain that whole string.
func TestOwnedSliceIsACopyOfTheSlice(t *testing.T) {
	source := strings.Repeat("0123456789", 1000)
	for _, c := range []struct {
		name      string
		got, want string
	}{
		{"start and end", OwnedSlice(source, 5, 25), source[5:25]},
		{"end omitted", OwnedSlice(source, 9995), "56789"},
		{"negative start", OwnedSlice(source, -3), "789"},
		{"empty", OwnedSlice(source, 4, 4), ""},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
	got := OwnedSlice(source, 0, 500)
	start, part := uintptr(unsafe.Pointer(unsafe.StringData(source))), uintptr(unsafe.Pointer(unsafe.StringData(got))) //nolint:gosec // the test compares backing-array addresses
	if part >= start && part < start+uintptr(len(source)) {
		t.Error("OwnedSlice result shares the source's backing array")
	}
}
