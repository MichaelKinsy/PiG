// Command patchdata writes the start address of .data, and the ranges of the globals that hold no heap pointers, into a
// TinyGo core built with the runtime fork in budget/tinygo.
//
//	patchdata <module.wasm> <linker.map>
//
// The fork's findGlobals scans the globals from the address stored after the 8-byte marker "PiGDDATA" (runtime
// pigDataStart) instead of from __global_base, so the GC does not take .rodata words for heap pointers. wasm-ld defines no
// symbol for the start of .data, so build-wasm.sh links with --Map and this command takes the address of the .data output
// section from the map. It fails when the map has no .data section, when a memory section other than .rodata starts below
// .data, when the module holds the marker other than exactly once, or when the word is already set: a build cannot fall
// back to scanning .rodata, or skip a writable section, without failing.
//
// The words after the address are [start, end) pairs the scan leaves out: the .data and .bss symbols in pointerFreeGlobals,
// merged where adjacent. It fails when they need more pairs than the runtime holds, or when the pairs are not unset.
package main

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// marker is the runtime's pigDataStart[0:2] in memory order; the data-start word follows it, then skipPairs pairs.
var marker = []byte("PiGDDATA")

// skipPairs is the runtime's pigSkips: the [start, end) pairs after the data-start word, each word ^0 until patched.
const skipPairs = 12

// pointerFreeGlobals matches the .data and .bss symbols the GC need not scan, whose words are not pointers but read as
// heap addresses, each pinning the heap object it lands in:
//   - the objects TinyGo's package initializer allocated for strconv's package-level slices (strconv$alloc,
//     strconv$alloc.N), its constant tables (isPrint16, isNotPrint16, isGraphic, isPrint32, powtab, float64pow10,
//     leftcheats, whose strings are in .rodata). Nothing writes them after initialization. Their words are code points
//     and powers of ten, and isPrint16's pairs of uint16s read as addresses of 30 to 130 MB: each pinned the heap object
//     it landed in (a 20,000-task transaction's staged records, a grown map's old buckets) for the module's life.
//   - wasi-libc's arc4random state (arc4random_buf.rng_state in .bss): 552 bytes of ChaCha output, left there by the
//     runtime's hardwareRand (arc4random), so random words, a few of them in the heap's range on each run. On
//     U-harness-scheduler-retention's 20,000-deep chain they pinned a dead 1.5 MB store write list (with the 20,000
//     statements' parameters) and a 2.7 MB array, and which objects they pin changes with every code change. The
//     runtime's fastrand state (runtime.xorshift32State in .data), seeded from hardwareRand and advanced by every map
//     the core makes, is a random word too.
//   - the allocator's counters: runtime.gcMallocs and runtime.gcTotalAlloc, the fork's runtime.growHeapMallocs (a copy of
//     gcMallocs), and the copies the core's ABI answers hold, main.stats (mem_stats: the session's byte counts and the heap
//     in use) and main.allocs (alloc_stats: bytes allocated, allocations, collections). A count of allocations or bytes
//     sweeps through the heap's addresses as the instance runs (300,000 allocations is 0x493e0, inside a heap that
//     starts at 0x26210), so each collection it pins another object. In the count gate's 6-turn replay gcMallocs held
//     1,008 and then 176 bytes of dead objects at the last two readings, and wasm-tinygo's warm.liveGrowthPerTurn read 20
//     to 64 B/turn, moving with every change of the allocation count, while the same core read 21 natively and on Go.
//     With the counters left out it reads one figure at every fixture size.
var pointerFreeGlobals = regexp.MustCompile(`^(strconv\$alloc(\.[0-9]+)?|arc4random_buf\.rng_state|runtime\.(xorshift32State|gcMallocs|gcTotalAlloc|growHeapMallocs)|main\.(stats|allocs))$`)

// layout is what patchdata takes from a wasm-ld map: the .data output section's address and the ranges to skip.
type layout struct {
	data  uint32
	skips [][2]uint32
}

// readMap reads a wasm-ld map. The map's columns are Addr, Off, Size, Out, In and Symbol: an output section's name starts
// in the Out column, an input section's and a symbol's further right, and a section that is not in linear memory has the
// address "-". The runtime scans no global below .data, so readMap fails unless .rodata is the only memory output section
// that starts below .data: a writable section there (a .tdata, an init_array or a //go:section of the core) could hold the
// only pointer to a live heap object. The skips are the pointerFreeGlobals symbols inside .data or .bss, ascending and
// merged.
func readMap(r io.Reader) (layout, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return layout{}, err
		}
		return layout{}, errors.New("the map is empty")
	}
	col := strings.Index(sc.Text(), " Out ") + 1
	if col <= 0 || !strings.HasPrefix(strings.TrimSpace(sc.Text()), "Addr ") {
		return layout{}, fmt.Errorf("the map's first line %q is not the Addr Off Size Out In Symbol header", sc.Text())
	}
	type section struct {
		name       string
		addr, size uint32
	}
	var mem, tables, scanned []section
	var data section
	datas := 0
	for sc.Scan() {
		line := sc.Text()
		if len(line) <= col || line[col-1] != ' ' {
			continue
		}
		f := strings.Fields(line[:col])
		name := strings.TrimSpace(line[col:])
		if len(f) != 3 || f[0] == "-" {
			continue // not in linear memory
		}
		addr, err := strconv.ParseUint(f[0], 16, 32)
		if err != nil {
			return layout{}, fmt.Errorf("the %s line's address %q is not a 32-bit hex number", name, f[0])
		}
		size, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil {
			return layout{}, fmt.Errorf("the %s line's size %q is not a 32-bit hex number", name, f[2])
		}
		s := section{name, uint32(addr), uint32(size)}
		if line[col] == ' ' {
			if pointerFreeGlobals.MatchString(name) {
				tables = append(tables, s) // a symbol (an input section's name has its object file before it)
			}
			continue
		}
		if name == ".data" {
			if addr == 0 {
				return layout{}, errors.New("the .data section's address is 0")
			}
			data, datas = s, datas+1
		}
		if name == ".data" || name == ".bss" {
			scanned = append(scanned, s)
		}
		mem = append(mem, s)
	}
	if err := sc.Err(); err != nil {
		return layout{}, err
	}
	if datas != 1 {
		return layout{}, fmt.Errorf("the map has %d .data output sections, want 1", datas)
	}
	for _, s := range mem {
		if s.addr < data.addr && s.name != ".rodata" {
			return layout{}, fmt.Errorf("the memory section %s at %#x lies below .data at %#x: the runtime would not scan it", s.name, s.addr, data.addr)
		}
	}
	out := layout{data: data.addr}
	slices.SortFunc(tables, func(a, b section) int { return cmp.Compare(a.addr, b.addr) })
	for _, t := range tables {
		if t.size == 0 || !slices.ContainsFunc(scanned, func(s section) bool { return s.addr <= t.addr && t.addr+t.size <= s.addr+s.size }) {
			continue // a table in .rodata is not scanned anyway
		}
		if n := len(out.skips); n > 0 && out.skips[n-1][1] >= t.addr {
			out.skips[n-1][1] = max(out.skips[n-1][1], t.addr+t.size)
			continue
		}
		out.skips = append(out.skips, [2]uint32{t.addr, t.addr + t.size})
	}
	if len(out.skips) > skipPairs {
		return layout{}, fmt.Errorf("the pointer-free globals make %d ranges, the runtime holds %d", len(out.skips), skipPairs)
	}
	return out, nil
}

// patch stores the data start and the skip ranges in the words after the module's one marker.
func patch(mod []byte, l layout) error {
	at := bytes.Index(mod, marker)
	if at < 0 || bytes.Contains(mod[at+1:], marker) {
		return fmt.Errorf("the module holds the runtime marker %d times, want 1 (not built with the budget/tinygo runtime fork?)", bytes.Count(mod, marker))
	}
	words := mod[at+len(marker):]
	if len(words) < 4*(1+2*skipPairs) {
		return errors.New("the module ends inside the words after the marker")
	}
	if binary.LittleEndian.Uint32(words) != 0 {
		return errors.New("the data-start word is already set")
	}
	for i := range 2 * skipPairs {
		if binary.LittleEndian.Uint32(words[4+4*i:]) != ^uint32(0) {
			return errors.New("the skip ranges are already set")
		}
	}
	binary.LittleEndian.PutUint32(words, l.data)
	for i, s := range l.skips {
		binary.LittleEndian.PutUint32(words[4+8*i:], s[0])
		binary.LittleEndian.PutUint32(words[8+8*i:], s[1])
	}
	return nil
}

func run(modPath, mapPath string) error {
	m, err := os.ReadFile(mapPath)
	if err != nil {
		return err
	}
	l, err := readMap(bytes.NewReader(m))
	if err != nil {
		return fmt.Errorf("%s: %w", mapPath, err)
	}
	mod, err := os.ReadFile(modPath)
	if err != nil {
		return err
	}
	if err := patch(mod, l); err != nil {
		return fmt.Errorf("%s: %w", modPath, err)
	}
	return os.WriteFile(modPath, mod, 0o644)
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: patchdata <module.wasm> <linker.map>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "patchdata:", err)
		os.Exit(1)
	}
}
