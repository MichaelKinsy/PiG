package ai

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// upstream: packages/ai/test/uuid.test.ts

var uuidV7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

const uuidTimestamp = int64(0x0123456789ab)

func uuidTimestampOf(t *testing.T, id string) int64 {
	t.Helper()
	value, err := strconv.ParseInt(strings.ReplaceAll(id, "-", "")[:12], 16, 64)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// fakeClockGenerator stands in for vi.useFakeTimers: the test moves the clock.
func fakeClockGenerator(clock *int64) *uuidv7Generator {
	generator := newUUIDv7Generator(func() int64 { return *clock }, nil)
	return generator
}

func mustUUID(t *testing.T, generator *uuidv7Generator, timestamp *int64) string {
	t.Helper()
	id, err := generator.next(timestamp)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUUIDv7GeneratesOrderedIDsWhilePreservingFollowerTimestamps(t *testing.T) {
	// upstream: uuid.test.ts:17 "generates ordered UUIDv7s while preserving follower timestamps"
	clock := uuidTimestamp
	generator := fakeClockGenerator(&clock)

	first := mustUUID(t, generator, nil)
	second := mustUUID(t, generator, nil)
	clock = uuidTimestamp - 1
	afterRollback := mustUUID(t, generator, nil)
	clock = uuidTimestamp + 1
	afterAdvance := mustUUID(t, generator, nil)
	ordinary := []string{first, second, afterRollback, afterAdvance}
	followerTimestamp := uuidTimestamp - 1_000
	followers := []string{mustUUID(t, generator, &followerTimestamp), mustUUID(t, generator, &followerTimestamp)}

	for _, id := range append(slices.Clone(ordinary), followers...) {
		if !uuidV7Pattern.MatchString(id) {
			t.Fatalf("%q is not a UUIDv7", id)
		}
	}
	if !slices.IsSorted(ordinary) {
		t.Fatalf("ordinary ids are not ordered: %v", ordinary)
	}
	if len(slices.Compact(slices.Clone(ordinary))) != len(ordinary) {
		t.Fatalf("ordinary ids repeat: %v", ordinary)
	}
	var got []int64
	for _, id := range ordinary {
		got = append(got, uuidTimestampOf(t, id))
	}
	if want := []int64{uuidTimestamp, uuidTimestamp, uuidTimestamp, uuidTimestamp + 1}; !slices.Equal(got, want) {
		t.Fatalf("ordinary timestamps = %v, want %v", got, want)
	}
	for _, id := range followers {
		if got := uuidTimestampOf(t, id); got != followerTimestamp {
			t.Fatalf("follower timestamp = %d, want %d", got, followerTimestamp)
		}
	}
	if followers[0] == followers[1] {
		t.Fatal("follower ids repeat")
	}
}

func TestUUIDv7UsesFreshRandomnessForEveryTail(t *testing.T) {
	// upstream: uuid.test.ts:39 "uses fresh randomness for every UUID tail"
	randomByte := byte(0)
	generator := newUUIDv7Generator(func() int64 { return 0 }, func(bytes []byte) error {
		randomByte++
		for i := range bytes {
			bytes[i] = randomByte
		}
		return nil
	})
	timestamp := uuidTimestamp
	var tails []string
	for range 2 {
		id := mustUUID(t, generator, &timestamp)
		tails = append(tails, id[len(id)-8:])
		// The low bit of byte 11 is random data, not sequence data.
		if low, err := strconv.ParseUint(id[len(id)-10:len(id)-8], 16, 8); err != nil || low&1 != uint64(randomByte&1) {
			t.Fatalf("byte 11 of %q = %#x (%v), want the random low bit %d", id, low, err, randomByte&1)
		}
	}
	if want := []string{"01010101", "02020202"}; !slices.Equal(tails, want) {
		t.Fatalf("tails = %v, want %v", tails, want)
	}
}

func TestUUIDv7AcceptsTimestampBoundaries(t *testing.T) {
	// upstream: uuid.test.ts:50 "accepts timestamp boundary %s"
	for _, timestamp := range []int64{0, 1<<48 - 1} {
		generator := newUUIDv7Generator(func() int64 { return 0 }, nil)
		if got := uuidTimestampOf(t, mustUUID(t, generator, &timestamp)); got != timestamp {
			t.Fatalf("timestamp = %d, want %d", got, timestamp)
		}
	}
}

func TestUUIDv7RejectsInvalidTimestamps(t *testing.T) {
	// upstream: uuid.test.ts:54 "rejects invalid timestamp %s"
	// 1.5, NaN and Infinity cannot be expressed as an int64 timestamp; -1 and 2**48 are the representable rows.
	for _, timestamp := range []int64{-1, 1 << 48} {
		generator := newUUIDv7Generator(func() int64 { return 0 }, nil)
		if id, err := generator.next(&timestamp); err == nil {
			t.Fatalf("timestamp %d produced %q", timestamp, id)
		}
	}
}

func TestUUIDv7RejectsAnExhaustedSequence(t *testing.T) {
	// upstream: packages/ai/src/utils/uuid.ts "UUIDv7 generator sequence exhausted"
	generator := newUUIDv7Generator(func() int64 { return 0 }, nil)
	mustUUID(t, generator, nil)
	generator.sequence = 1<<41 - 1
	if id, err := generator.next(nil); err == nil {
		t.Fatalf("exhausted generator produced %q", id)
	}
}

func TestUUIDv7UsesTheProcessWideGenerator(t *testing.T) {
	// upstream: uuid.ts module state lastOrdinaryTimestamp and sequence
	first, err := UUIDv7(nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := UUIDv7(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !uuidV7Pattern.MatchString(first) || !uuidV7Pattern.MatchString(second) || first >= second {
		t.Fatalf("ids = %q, %q", first, second)
	}
}

func TestUUIDv7ReportsAFailingRandomSource(t *testing.T) {
	failure := errors.New("entropy unavailable")
	generator := newUUIDv7Generator(func() int64 { return 0 }, func([]byte) error { return failure })
	timestamp := uuidTimestamp
	if _, err := generator.next(&timestamp); !errors.Is(err, failure) {
		t.Fatalf("err = %v, want %v", err, failure)
	}
}

func TestUUIDv7SetsTheVersionAndVariantBitsOverTheSequence(t *testing.T) {
	generator := newUUIDv7Generator(func() int64 { return 0 }, func(bytes []byte) error {
		for index := range bytes {
			bytes[index] = 0xff
		}
		return nil
	})
	timestamp := uuidTimestamp
	if id := mustUUID(t, generator, &timestamp); !uuidV7Pattern.MatchString(id) {
		t.Fatalf("%q is not a UUIDv7", id)
	}
}

// The expected ids are Pi's uuidv7 (packages/ai/src/utils/uuid.ts at 1.0.0) under Node 24 with the same getRandomValues stub and clock: three follower ids at uuidTimestamp, uuidTimestamp and uuidTimestamp+5, then ordinary ids with Date.now at uuidTimestamp+7 and uuidTimestamp+3.
func TestUUIDv7MatchesPisIDsByteForByteForAFixedRandomSource(t *testing.T) {
	now := uuidTimestamp + 7
	call := 0
	generator := newUUIDv7Generator(func() int64 { return now }, func(bytes []byte) error {
		call++
		for index := range bytes {
			bytes[index] = byte(index*37 + call*11 + 0xa5)
		}
		return nil
	})
	first, later := uuidTimestamp, uuidTimestamp+5
	got := []string{mustUUID(t, generator, &first), mustUUID(t, generator, &first), mustUUID(t, generator, &later), mustUUID(t, generator, nil)}
	now = uuidTimestamp + 3
	got = append(got, mustUUID(t, generator, nil))
	want := []string{
		"01234567-89ab-76af-b43e-88d36c91b6db",
		"01234567-89ab-76af-b43e-88d4779cc1e6",
		"01234567-89b0-76af-b43e-88d782a7ccf1",
		"01234567-89b2-76af-b43e-88d88db2d7fc",
		"01234567-89b2-76af-b43e-88db98bde207",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ids =\n%q\nwant\n%q", got, want)
	}
}
