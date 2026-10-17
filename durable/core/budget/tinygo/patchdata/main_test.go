package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// A wasm-ld map excerpt (TinyGo 0.42.0, wasip1 c-shared): output section names start in the Out column, input sections
// 8 bytes and symbols 16 bytes further right. A symbol line has four fields, like an output section line.
const sampleMap = `    Addr      Off     Size Out     In      Symbol
       -        8      4c8 TYPE
       -      ab2        a GLOBAL
       0        0        0         __stack_pointer
       -    bb562    11d7e DATA
   10000    bb563    11888 .rodata
   10000    bb563       80         main.lto..o:(.rodata..Lswitch.table)
   10000    bb563       80                 .Lswitch.table
   14fb0    cd3f4      8d8         main.lto..o:(.rodata.strconv$alloc.34)
   14fb0    cd3f4      8d8                 strconv$alloc.34
   21888    ccdf4      ba4 .data
   21888    ccdfc        4         main.lto..o:(.data.runtime.xorshift32State)
   21888    ccdfc        4                 runtime.xorshift32State
   21900    ccd6c      338         main.lto..o:(.data.strconv$alloc)
   21900    ccd6c      338                 strconv$alloc
   21c38    cd0a4      108         main.lto..o:(.data.strconv$alloc.33)
   21c38    cd0a4      108                 strconv$alloc.33
   21d40    cd1ac       10         main.lto..o:(.data.runtime$alloc)
   21d40    cd1ac       10                 runtime$alloc
   21d50    cd1bc       f0         main.lto..o:(.data.strconv$alloc.35)
   21d50    cd1bc       f0                 strconv$alloc.35
   22430    bb562     1b40 .bss
   22430        0      228         lib.a(arc4random.c.o):(.bss.arc4random_buf.rng_state)
   22430        0      228                 arc4random_buf.rng_state
   22660        0        8         main.lto..o:(.bss.runtime.gcMallocs)
   22660        0        8                 runtime.gcMallocs
   22668        0        8         main.lto..o:(.bss.runtime.growHeapMallocs)
   22668        0        8                 runtime.growHeapMallocs
   22670        0        4         main.lto..o:(.bss.runtime.metadataStart)
   22670        0        4                 runtime.metadataStart
   22678        0        8         main.lto..o:(.bss.runtime.gcTotalAlloc)
   22678        0        8                 runtime.gcTotalAlloc
   22680        0        4         main.lto..o:(.bss.main.output.0)
   22680        0        4                 main.output.0
   22684        0       40         main.lto..o:(.bss.main.stats)
   22684        0       40                 main.stats
   226c8        0       18         main.lto..o:(.bss.main.allocs)
   226c8        0       18                 main.allocs
       -    cd998    18626 CUSTOM(name)
`

func TestReadMapTakesTheDataOutputSection(t *testing.T) {
	got, err := readMap(strings.NewReader(sampleMap))
	if err != nil || got.data != 0x21888 {
		t.Fatalf("readMap = %#x, %v; want .data at 0x21888", got.data, err)
	}
}

func TestReadMapFailsWithoutData(t *testing.T) {
	for name, m := range map[string]string{
		"renamed":   strings.ReplaceAll(sampleMap, " .data\n", " .tdata\n"),
		"no header": sampleMap[strings.Index(sampleMap, "\n")+1:],
		"twice":     sampleMap + "   22b70    cd998       10 .data\n",
	} {
		if _, err := readMap(strings.NewReader(m)); err == nil {
			t.Errorf("%s: a map without exactly one .data output section must fail", name)
		}
	}
}

// Only an output section counts: a symbol or an input section named like one is not where .data starts.
func TestReadMapSkipsSymbolsAndInputSections(t *testing.T) {
	m := strings.Replace(sampleMap, "                 .Lswitch.table\n", "                 .data\n", 1)
	m = strings.Replace(m, "         main.lto..o:(.rodata..Lswitch.table)\n", "         .data\n", 1)
	got, err := readMap(strings.NewReader(m))
	if err != nil || got.data != 0x21888 {
		t.Fatalf("readMap = %#x, %v; want .data at 0x21888", got.data, err)
	}
}

// The runtime scans no global below .data, so a writable memory section there must stop the build.
func TestReadMapRefusesAWritableSectionBelowData(t *testing.T) {
	for _, sec := range []string{".tdata", "init_array", ".bss"} {
		m := strings.Replace(sampleMap, "   21888    ccdf4      ba4 .data\n", "   21800    ccd6c       88 "+sec+"\n   21888    ccdf4      ba4 .data\n", 1)
		if got, err := readMap(strings.NewReader(m)); err == nil {
			t.Errorf("%s below .data: readMap = %#x, want an error", sec, got.data)
		}
	}
}

// strconv's tables in .data, the random state (the runtime's fastrand state in .data, libc's arc4random state in .bss) and
// the allocator's counters with the core's copies of them in .bss are left out of the scan: adjacent ones as one range,
// the globals between them that hold pointers (runtime$alloc, metadataStart, output) kept, the table in .rodata (not
// scanned anyway) not listed.
func TestReadMapSkipsThePointerFreeGlobals(t *testing.T) {
	got, err := readMap(strings.NewReader(sampleMap))
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]uint32{{0x21888, 0x2188c}, {0x21900, 0x21d40}, {0x21d50, 0x21e40}, {0x22430, 0x22658}, {0x22660, 0x22670}, {0x22678, 0x22680}, {0x22684, 0x226c4}, {0x226c8, 0x226e0}}
	if !slices.Equal(got.skips, want) {
		t.Fatalf("skips = %#x, want %#x", got.skips, want)
	}
}

func TestReadMapRefusesMoreTablesThanTheRuntimeHolds(t *testing.T) {
	var extra strings.Builder
	for i := range skipPairs {
		// Each table is followed by a global that is scanned, so none merge with the next.
		fmt.Fprintf(&extra, "   %x    cd2ac        8                 strconv$alloc.9%d\n", 0x22000+0x20*i, i)
		fmt.Fprintf(&extra, "   %x    cd2b4        8                 runtime$alloc.9%d\n", 0x22008+0x20*i, i)
	}
	m := strings.Replace(sampleMap, "   22430    bb562     1b40 .bss\n", extra.String()+"   22430    bb562     1b40 .bss\n", 1)
	if got, err := readMap(strings.NewReader(m)); err == nil {
		t.Fatalf("readMap = %#x, want an error for %d ranges", got.skips, len(got.skips))
	}
}

// module is a module whose marker words are followed by skipPairs unset pairs, as the runtime's initializer leaves them.
func module(markers int, word uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x00asm\x01\x00\x00\x00 data ")
	for range markers {
		b.Write(marker)
		_ = binary.Write(&b, binary.LittleEndian, word)
		for range 2 * skipPairs {
			_ = binary.Write(&b, binary.LittleEndian, ^uint32(0))
		}
		b.WriteString(" tail ")
	}
	return b.Bytes()
}

func TestPatchWritesTheWordsAfterTheMarker(t *testing.T) {
	mod := module(1, 0)
	if err := patch(mod, layout{data: 0x21888, skips: [][2]uint32{{0x21900, 0x21d40}}}); err != nil {
		t.Fatal(err)
	}
	at := bytes.Index(mod, marker) + len(marker)
	word := func(i int) uint32 { return binary.LittleEndian.Uint32(mod[at+4*i:]) }
	if word(0) != 0x21888 || word(1) != 0x21900 || word(2) != 0x21d40 || word(3) != ^uint32(0) || word(2*skipPairs) != ^uint32(0) {
		t.Fatalf("words = %#x %#x %#x %#x, want 0x21888 0x21900 0x21d40 and the rest unset", word(0), word(1), word(2), word(3))
	}
}

func TestPatchRefusesAMissingDuplicatedOrSetMarker(t *testing.T) {
	skipsSet := module(1, 0)
	binary.LittleEndian.PutUint32(skipsSet[bytes.Index(skipsSet, marker)+len(marker)+4:], 0x21900)
	for name, mod := range map[string][]byte{"missing": module(0, 0), "twice": module(2, 0), "set": module(1, 0x10000), "skips set": skipsSet, "short": module(1, 0)[:bytes.Index(module(1, 0), marker)+len(marker)+8]} {
		if err := patch(mod, layout{data: 0x21888}); err == nil {
			t.Errorf("%s: patch succeeded, want an error", name)
		}
	}
}
