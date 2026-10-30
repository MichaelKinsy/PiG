package host

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Pi has no test file for uuid.ts; these are the checks of the properties its comment states: UUIDv7 text, ids ordered
// within one millisecond and across a clock that goes backwards.

var uuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestUuidv7HasTheVersionAndVariantBitsAndTheTimestamp(t *testing.T) {
	restore := setUUIDClock(func() int64 { return 0x0123_4567_89ab })
	defer restore()
	id := uuidv7()
	if !uuidV7.MatchString(id) || !strings.HasPrefix(id, "01234567-89ab-7") {
		t.Fatalf("uuidv7 = %q", id)
	}
}

func TestUuidv7IdsAreOrderedWithinAMillisecondAndAcrossABackwardsClock(t *testing.T) {
	clock := int64(1_000_000)
	restore := setUUIDClock(func() int64 { return clock })
	defer restore()
	var ids []string
	for range 50 {
		ids = append(ids, uuidv7())
	}
	clock = 999_000
	for range 50 {
		ids = append(ids, uuidv7())
	}
	clock = 1_000_001
	ids = append(ids, uuidv7())
	if !slices.IsSorted(ids) {
		t.Fatalf("ids are not in time order: %v", ids)
	}
	if len(slices.Compact(slices.Clone(ids))) != len(ids) {
		t.Fatal("ids are not unique")
	}
}

func TestUuidv7SequenceExhaustedFails(t *testing.T) {
	restore := setUUIDClock(func() int64 { return 5 })
	defer restore()
	defer exhaustUUIDSequence()()
	defer func() {
		if recover() == nil {
			t.Fatal("an exhausted sequence did not fail")
		}
	}()
	uuidv7()
}
