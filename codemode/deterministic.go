package codemode

import (
	"math"
	"strconv"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// Deterministic mode (VmStartData.Deterministic, used by Sandbox.Start): a script must make the same calls with the same
// arguments every time it runs with the same results, so a host can replay it after a restart. The VM is otherwise
// deterministic, because it only runs when the host delivers something and has no timers. What remains is fixed here:
//   - Time: every WASI clock reads virtual time, StartedAt and then the now of the result being processed, never going
//     backwards. While the context is created, the realtime clock reads a value derived from the seed instead, because
//     QuickJS seeds Math.random() from the first clock read, and which read comes first is the engine's business.
//   - Randomness: random_get draws from a PRNG over the seed.
//   - Timezone: UTC, not the host's.
//   - Garbage collection: WeakRef and FinalizationRegistry are not available; collection timing is not.
//   - Delivery order: results carry a seq and are applied strictly in that order, buffering early ones.
//
// Ports packages/codemode/src/runtime/vm.ts (deterministic mode).

// deterministicVersion is bumped whenever this file or the prelude changes what a deterministic script observes, beyond
// what the fingerprint's other parts (engine versions, prelude hash, limits) already capture.
//
// Ports packages/codemode/src/runtime/vm.ts (DETERMINISTIC_VERSION).
const deterministicVersion = 1

// quickjsWasiVersion is the version of the quickjs-wasi package whose module is embedded (assets/PROVENANCE.md); the
// fingerprint names it as `vm.versions["quickjs-wasi"]` does upstream.
const quickjsWasiVersion = "3.6.2"

// weakRefIntrinsic is Intrinsics.WEAK_REF of quickjs-wasi: `WeakRef` and `FinalizationRegistry`.
const weakRefIntrinsic = 1 << 9

// deterministicIntrinsics is `Intrinsics.ALL & ~Intrinsics.WEAK_REF`.
const deterministicIntrinsics = uint32(0xFFFFFFFF) &^ weakRefIntrinsic

// preludeHash is FNV-1a over the UTF-16 code units of the prelude, as 8 hex digits: it identifies the prelude in the
// fingerprint.
//
// Ports packages/codemode/src/runtime/vm.ts (fnv1a, PRELUDE_HASH).
func preludeHash(prelude string) string {
	hash := uint32(0x811c9dc5)
	for _, unit := range utf16.Encode([]rune(prelude)) {
		hash ^= uint32(unit)
		hash *= 0x01000193
	}
	text := strconv.FormatUint(uint64(hash), 16)
	for len(text) < 8 { //portlint:allow utf16units text is hex digits, whose bytes are UTF-16 code units (padStart(8, "0"))
		text = "0" + text
	}
	return text
}

// prng is mulberry32: a small, fast PRNG; one 32-bit draw per call.
//
// Ports packages/codemode/src/runtime/vm.ts (prng).
func prng(seed uint32) func() uint32 {
	state := seed
	return func() uint32 {
		state += 0x6d2b79f5
		value := (state ^ (state >> 15)) * (1 | state)
		value = (value + (value^(value>>7))*(61|value)) ^ value
		return value ^ (value >> 14)
	}
}

// virtualClock is the deterministic VM's clock: seed-derived while the context is created, then virtual time.
//
// Ports packages/codemode/src/runtime/vm.ts (VirtualClock).
type virtualClock struct {
	creating  bool
	startedAt float64
	now       float64
	seed      uint32
}

func newVirtualClock(d *VmDeterministic) *virtualClock {
	return &virtualClock{creating: true, startedAt: d.StartedAt, now: d.StartedAt, seed: d.Seed}
}

// round is BigInt(Math.round(x)) for the finite clock readings (run.go rejects a StartedAt that is not finite and advance
// a now that is not), below 2^63; the multiplication by 1_000_000 then wraps mod 2^64 as setBigUint64 stores it.
func round(x float64) int64 {
	//portlint:allow numbers x is finite and integral after Math.round, and a clock reading in ms is far below 2^63
	return int64(jsnumber.Round(x))
}

// realtimeNanos is the WASI realtime clock (id 0); other clocks read monotonicNanos.
//
// QuickJS seeds Math.random() from this reading in microseconds and turns 0 into 1, so seed s reads s + 1.
func (c *virtualClock) realtimeNanos() int64 {
	if c.creating {
		return (int64(c.seed) + 1) * 1000
	}
	return round(c.now) * 1_000_000
}

func (c *virtualClock) monotonicNanos() int64 {
	if c.creating {
		return 0
	}
	return round(c.now-c.startedAt) * 1_000_000
}

// advance moves the clock to the now of the result being processed; it never goes backwards, because a resumed host's clock
// may be behind the one that started the run.
func (c *virtualClock) advance(now float64) {
	if !math.IsNaN(now) && !math.IsInf(now, 0) {
		c.now = math.Max(c.now, now)
	}
}

// randomSource is the WASI random_get of a deterministic VM: one PRNG draw per four bytes, least significant byte first.
type randomSource struct{ next func() uint32 }

// randomSeedMask is XORed into the run's seed for the random_get PRNG (vm.ts: `prng(clock.seed ^ 0x9e3779b9)`).
const randomSeedMask = 0x9e3779b9

func newRandomSource(seed uint32) *randomSource { return &randomSource{prng(seed ^ randomSeedMask)} }

func (r *randomSource) Read(p []byte) (int, error) {
	for index := 0; index < len(p); index += 4 {
		value := r.next()
		for b := 0; b < 4 && index+b < len(p); b++ {
			p[index+b] = byte(value >> (b * 8))
		}
	}
	return len(p), nil
}

// fingerprint says what the VM is, so a replay can refuse a different one.
func fingerprint(quickjsVersion, prelude string, maxStack, heapLimit uint32, budget *int) string {
	heap := "none"
	if heapLimit != 0 {
		heap = strconv.FormatUint(uint64(heapLimit), 10)
	}
	budgetText := "none"
	if budget != nil {
		budgetText = strconv.Itoa(*budget)
	}
	return "quickjs-wasi " + quickjsWasiVersion + ", quickjs " + quickjsVersion + ", prelude " + preludeHash(prelude) +
		", deterministic " + strconv.Itoa(deterministicVersion) + ", stack " + strconv.FormatUint(uint64(maxStack), 10) +
		", heap " + heap + ", budget " + budgetText
}
