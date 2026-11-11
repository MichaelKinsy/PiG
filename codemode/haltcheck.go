//go:build !tinygo

package codemode

import (
	"errors"
	"fmt"
)

// haltExport names the mutable i32 global instrumentHalt adds to the module: a non-zero value makes the guest trap at
// its next halt poll, at most haltPollInterval loop iterations away.
const haltExport = "pig_halt"

// haltPollInterval is how many loop iterations pass between two polls. A poll leaves the guest for Go (see
// instrumentHalt), so it is rare enough to cost nothing and frequent enough to stop a guest within microseconds.
const haltPollInterval = 1024

const (
	sectionType     = 1
	sectionImport   = 2
	sectionFunction = 3
	sectionMemory   = 5
	sectionGlobal   = 6
	sectionExport   = 7
	sectionCode     = 10
)

type wasmSection struct {
	id      byte
	payload []byte
}

// instrumentHalt returns wasm with a halt poll at the head of every loop, and $pig_halt exported as haltExport. Each
// loop head counts down a tick global; every haltPollInterval iterations it calls a function the rewrite appends,
// which resets the tick, leaves the guest for Go with `memory.grow 0` and traps with `unreachable` if $pig_halt is
// set. The check on the way is a decrement and a branch in the compiled code.
//
// The call into Go is what makes the guest stoppable. Go can stop the world (every garbage collection does) only at
// a Go call, and compiled guest code never calls Go inside a loop: a stop-the-world waits for the loop, nothing else
// runs, not even the timer that would set the halt, and a loop that never ends holds the whole process. Pi stops the
// same loop with worker.terminate() from the main thread, which the worker cannot block. With the poll, a pending
// stop-the-world or preemption takes effect within haltPollInterval iterations.
//
// The check wazero emits for WithCloseOnContextDone also calls Go, but on every loop iteration, which made QuickJS
// about four times slower and the native loops of its string functions slower still.
func instrumentHalt(wasm []byte) ([]byte, error) {
	if len(wasm) < 8 || string(wasm[:8]) != "\x00asm\x01\x00\x00\x00" {
		return nil, errors.New("not a version 1 wasm module")
	}
	var sections []wasmSection
	for pos := 8; pos < len(wasm); {
		if pos+1 >= len(wasm) {
			return nil, errors.New("truncated section header")
		}
		size, next, err := readU32(wasm, pos+1)
		if err != nil {
			return nil, err
		}
		if uint64(next)+uint64(size) > uint64(len(wasm)) {
			return nil, errors.New("section extends past the end of the module")
		}
		sections = append(sections, wasmSection{wasm[pos], wasm[next : next+int(size)]})
		pos = next + int(size)
	}

	index := func(id byte) int {
		for i, s := range sections {
			if s.id == id {
				return i
			}
		}
		return -1
	}
	var imports importCounts
	if at := index(sectionImport); at >= 0 {
		var err error
		if imports, err = countImports(sections[at].payload); err != nil {
			return nil, fmt.Errorf("import section: %w", err)
		}
	}
	typeAt, functionAt, exportAt, codeAt := index(sectionType), index(sectionFunction), index(sectionExport), index(sectionCode)
	if typeAt < 0 || functionAt < 0 || exportAt < 0 || codeAt < 0 {
		return nil, errors.New("the module lacks a type, function, export or code section")
	}

	// Everything new comes last in its index space, so no existing index moves.
	typeCount, rest, err := readU32(sections[typeAt].payload, 0)
	if err != nil {
		return nil, fmt.Errorf("type section: %w", err)
	}
	sections[typeAt].payload = append(appendU32(nil, typeCount+1), sections[typeAt].payload[rest:]...)
	sections[typeAt].payload = append(sections[typeAt].payload, 0x60, 0x00, 0x00)

	functionCount, rest, err := readU32(sections[functionAt].payload, 0)
	if err != nil {
		return nil, fmt.Errorf("function section: %w", err)
	}
	pollIndex := imports.functions + functionCount
	sections[functionAt].payload = appendU32(append(appendU32(nil, functionCount+1), sections[functionAt].payload[rest:]...), typeCount)

	definedGlobals := uint32(0)
	if at := index(sectionGlobal); at >= 0 {
		count, rest, err := readU32(sections[at].payload, 0)
		if err != nil {
			return nil, fmt.Errorf("global section: %w", err)
		}
		definedGlobals = count
		sections[at].payload = append(appendU32(nil, count+2), sections[at].payload[rest:]...)
	}
	haltIndex := imports.globals + definedGlobals
	tickIndex := haltIndex + 1
	globals := append(mutableI32Global(0), mutableI32Global(haltPollInterval)...)
	if at := index(sectionGlobal); at >= 0 {
		sections[at].payload = append(sections[at].payload, globals...)
	} else {
		sections = insertSectionBefore(sections, wasmSection{sectionGlobal, append(appendU32(nil, 2), globals...)}, sectionExport)
	}

	// The poll needs a memory to grow by zero pages; a module without one gets an empty one it never touches.
	hasMemory := imports.memories > 0
	if at := index(sectionMemory); at >= 0 {
		count, _, err := readU32(sections[at].payload, 0)
		if err != nil {
			return nil, fmt.Errorf("memory section: %w", err)
		}
		hasMemory = hasMemory || count > 0
	}
	if !hasMemory {
		if at := index(sectionMemory); at >= 0 {
			sections[at].payload = []byte{0x01, 0x00, 0x00}
		} else {
			before := byte(sectionExport)
			if index(sectionGlobal) >= 0 {
				before = sectionGlobal
			}
			sections = insertSectionBefore(sections, wasmSection{sectionMemory, []byte{0x01, 0x00, 0x00}}, before)
		}
	}

	exportAt = index(sectionExport)
	count, rest, err := readU32(sections[exportAt].payload, 0)
	if err != nil {
		return nil, fmt.Errorf("export section: %w", err)
	}
	payload := appendU32(nil, count+1)
	payload = append(payload, sections[exportAt].payload[rest:]...)
	payload = appendU32(payload, uint32(len(haltExport)))
	payload = append(payload, haltExport...)
	payload = append(payload, 0x03)
	sections[exportAt].payload = appendU32(payload, haltIndex)

	codeAt = index(sectionCode)
	if sections[codeAt].payload, err = instrumentCode(sections[codeAt].payload, haltPoll{halt: haltIndex, tick: tickIndex, call: pollIndex}); err != nil {
		return nil, fmt.Errorf("code section: %w", err)
	}

	out := append([]byte{}, wasm[:8]...)
	for _, s := range sections {
		out = append(out, s.id)
		out = appendU32(out, uint32(len(s.payload)))
		out = append(out, s.payload...)
	}
	return out, nil
}

// haltPoll holds the indices the loop check and the poll function refer to.
type haltPoll struct{ halt, tick, call uint32 }

// mutableI32Global encodes a mutable i32 global initialised to value.
func mutableI32Global(value int32) []byte {
	return appendI32(append([]byte{}, 0x7f, 0x01, 0x41), value, 0x0b)
}

// appendI32 appends value as a signed LEB128 followed by tail.
func appendI32(b []byte, value int32, tail ...byte) []byte {
	for {
		c := byte(value & 0x7f)
		value >>= 7
		if (value == 0 && c&0x40 == 0) || (value == -1 && c&0x40 != 0) {
			return append(append(b, c), tail...)
		}
		b = append(b, c|0x80)
	}
}

// insertSectionBefore puts s right before the first section with id before, or at the end.
func insertSectionBefore(sections []wasmSection, s wasmSection, before byte) []wasmSection {
	at := len(sections)
	for i, o := range sections {
		if o.id == before {
			at = i
			break
		}
	}
	sections = append(sections, wasmSection{})
	copy(sections[at+1:], sections[at:])
	sections[at] = s
	return sections
}

// importCounts counts the imports of each kind the rewrite must index past: imports precede the module's own
// functions and globals in their index spaces.
type importCounts struct{ functions, memories, globals uint32 }

// countImports counts the imports of an import section.
func countImports(payload []byte) (importCounts, error) {
	var counts importCounts
	count, pos, err := readU32(payload, 0)
	if err != nil {
		return counts, err
	}
	for range count {
		// module name, field name, then the kind and its descriptor
		for range 2 {
			n, next, err := readU32(payload, pos)
			if err != nil {
				return counts, err
			}
			pos = next + int(n)
		}
		if pos >= len(payload) {
			return counts, errors.New("truncated import")
		}
		kind := payload[pos]
		pos++
		switch kind {
		case 0: // function: type index
			counts.functions++
			_, pos, err = readU32(payload, pos)
		case 1: // table: reference type, limits
			pos, err = skipLimits(payload, pos+1)
		case 2: // memory: limits
			counts.memories++
			pos, err = skipLimits(payload, pos)
		case 3: // global: value type, mutability
			counts.globals++
			pos += 2
		case 4: // tag: attribute, type index
			_, pos, err = readU32(payload, pos+1)
		default:
			err = fmt.Errorf("unknown import kind %#x", kind)
		}
		if err != nil {
			return counts, err
		}
	}
	return counts, nil
}

// skipLimits returns the position after a limits record: flags, minimum and, when flag bit 0 is set, maximum.
func skipLimits(b []byte, pos int) (int, error) {
	flags, pos, err := readU32(b, pos)
	if err != nil {
		return 0, err
	}
	if pos, err = skipLEB(b, pos); err != nil {
		return 0, err
	}
	if flags&1 != 0 {
		pos, err = skipLEB(b, pos)
	}
	return pos, err
}

// instrumentCode inserts the halt poll after every loop header of a code section and appends the poll function.
func instrumentCode(payload []byte, poll haltPoll) ([]byte, error) {
	count, pos, err := readU32(payload, 0)
	if err != nil {
		return nil, err
	}
	// global.get tick; i32.const 1; i32.sub; global.set tick; global.get tick; i32.eqz; if; call poll; end
	check := []byte{0x23}
	check = appendU32(check, poll.tick)
	check = append(check, 0x41, 0x01, 0x6b, 0x24)
	check = appendU32(check, poll.tick)
	check = append(check, 0x23)
	check = appendU32(check, poll.tick)
	check = append(check, 0x45, 0x04, 0x40, 0x10)
	check = appendU32(check, poll.call)
	check = append(check, 0x0b)

	// The poll function: reset the tick, memory.grow 0 (an exit into Go), then trap if the halt global is set.
	body := []byte{0x00, 0x41}
	body = appendI32(body, haltPollInterval, 0x24)
	body = appendU32(body, poll.tick)
	body = append(body, 0x41, 0x00, 0x40, 0x00, 0x1a, 0x23)
	body = appendU32(body, poll.halt)
	body = append(body, 0x04, 0x40, 0x00, 0x0b, 0x0b)

	out := appendU32(make([]byte, 0, len(payload)+len(payload)/8), count+1)
	for i := range count {
		size, bodyAt, err := readU32(payload, pos)
		if err != nil {
			return nil, err
		}
		if uint64(bodyAt)+uint64(size) > uint64(len(payload)) {
			return nil, fmt.Errorf("function %d extends past the section", i)
		}
		instrumented, err := instrumentBody(payload[bodyAt:bodyAt+int(size)], check)
		if err != nil {
			return nil, fmt.Errorf("function %d: %w", i, err)
		}
		out = appendU32(out, uint32(len(instrumented)))
		out = append(out, instrumented...)
		pos = bodyAt + int(size)
	}
	if pos != len(payload) {
		return nil, errors.New("bytes after the last function")
	}
	out = appendU32(out, uint32(len(body)))
	return append(out, body...), nil
}

// instrumentBody copies one function body, inserting check after each loop opcode and its block type.
func instrumentBody(body, check []byte) ([]byte, error) {
	// locals: a vector of (count, value type); reference types with a heap type are not used by the module
	groups, pos, err := readU32(body, 0)
	if err != nil {
		return nil, err
	}
	for range groups {
		if _, pos, err = readU32(body, pos); err != nil {
			return nil, err
		}
		if pos >= len(body) || body[pos] == 0x63 || body[pos] == 0x64 {
			return nil, errors.New("unsupported local type")
		}
		pos++
	}
	out := make([]byte, 0, len(body)+len(body)/16)
	out = append(out, body[:pos]...)
	for pos < len(body) {
		op := body[pos]
		end, err := skipInstruction(body, pos)
		if err != nil {
			return nil, err
		}
		out = append(out, body[pos:end]...)
		if op == 0x03 {
			out = append(out, check...)
		}
		pos = end
	}
	return out, nil
}

// skipInstruction returns the position after the instruction at pos: its opcode and immediates.
func skipInstruction(b []byte, pos int) (int, error) {
	op := b[pos]
	pos++
	var err error
	leb := func(n int) {
		for range n {
			if pos, err = skipLEB(b, pos); err != nil {
				return
			}
		}
	}
	switch {
	case op == 0x00 || op == 0x01 || op == 0x05 || op == 0x0b || op == 0x0f || op == 0x1a || op == 0x1b || op == 0xd1:
	case op == 0x02 || op == 0x03 || op == 0x04: // block type: 0x40, a value type or a type index
		leb(1)
	case op == 0x0c || op == 0x0d || op == 0x10 || op == 0x12 || op == 0x14 || op == 0x15 || (op >= 0x20 && op <= 0x26) ||
		op == 0x3f || op == 0x40 || op == 0x41 || op == 0x42 || op == 0xd0 || op == 0xd2:
		leb(1)
	case op == 0x11 || op == 0x13: // type index, table index
		leb(2)
	case op == 0x0e: // br_table: labels, default
		var n uint32
		if n, pos, err = readU32(b, pos); err == nil && int(n) < len(b)-pos {
			leb(int(n) + 1)
		} else if err == nil {
			err = errors.New("br_table longer than the function")
		}
	case op == 0x1c: // typed select: value types
		var n uint32
		if n, pos, err = readU32(b, pos); err == nil {
			pos += int(n)
		}
	case op >= 0x28 && op <= 0x3e: // memory access: alignment (bit 6: memory index follows), offset
		var align uint32
		if align, pos, err = readU32(b, pos); err == nil {
			if align&0x40 != 0 {
				leb(1)
			}
			leb(1)
		}
	case op == 0x43:
		pos += 4
	case op == 0x44:
		pos += 8
	case op >= 0x45 && op <= 0xc4: // numeric
	case op == 0xfc:
		var sub uint32
		if sub, pos, err = readU32(b, pos); err != nil {
			break
		}
		switch sub {
		case 0, 1, 2, 3, 4, 5, 6, 7: // saturating truncation
		case 8, 10, 12, 14: // memory.init, memory.copy, table.init, table.copy
			leb(2)
		case 9, 11, 13, 15, 16, 17: // data.drop, memory.fill, elem.drop, table.grow, table.size, table.fill
			leb(1)
		default:
			return 0, fmt.Errorf("unsupported opcode 0xfc %d at %d", sub, pos)
		}
	default:
		return 0, fmt.Errorf("unsupported opcode %#x at %d", op, pos-1)
	}
	if err != nil {
		return 0, err
	}
	if pos > len(b) {
		return 0, errors.New("instruction extends past the function")
	}
	return pos, nil
}

// skipLEB returns the position after a LEB128 value of up to 64 bits, signed or unsigned.
func skipLEB(b []byte, pos int) (int, error) {
	for range 10 {
		if pos >= len(b) {
			return 0, errors.New("truncated LEB128")
		}
		pos++
		if b[pos-1]&0x80 == 0 {
			return pos, nil
		}
	}
	return 0, errors.New("LEB128 too long")
}

// readU32 decodes an unsigned LEB128 value of at most 32 bits and returns the position after it.
func readU32(b []byte, pos int) (uint32, int, error) {
	var value uint64
	for shift := uint(0); shift < 35; shift += 7 {
		if pos >= len(b) {
			return 0, 0, errors.New("truncated LEB128")
		}
		c := b[pos]
		pos++
		value |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return uint32(value), pos, nil
		}
	}
	return 0, 0, errors.New("LEB128 too long")
}

func appendU32(b []byte, v uint32) []byte {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			return append(b, c)
		}
		b = append(b, c|0x80)
	}
}
