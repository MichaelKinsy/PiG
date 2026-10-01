package codemode

import (
	"errors"
	"fmt"
)

// haltExport names the mutable i32 global instrumentHalt adds to the module: a non-zero value makes the guest trap at
// its next loop iteration.
const haltExport = "pig_halt"

const (
	sectionImport = 2
	sectionGlobal = 6
	sectionExport = 7
	sectionCode   = 10
)

// haltGlobal is a mutable i32 global initialised to zero.
var haltGlobal = []byte{0x7f, 0x01, 0x41, 0x00, 0x0b}

type wasmSection struct {
	id      byte
	payload []byte
}

// instrumentHalt returns wasm with a halt check at the head of every loop: `global.get $pig_halt; if; unreachable;
// end`, with $pig_halt exported as haltExport. The check is a load and a branch in the compiled code.
//
// A running guest cannot be stopped from outside, and the check wazero emits for WithCloseOnContextDone calls back
// into Go on every loop iteration, which made QuickJS about four times slower and the native loops of its string
// functions slower still. This check gives the hard stop (execution.go, terminationGrace) without that cost.
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
	importedGlobals := uint32(0)
	if at := index(sectionImport); at >= 0 {
		var err error
		if importedGlobals, err = countGlobalImports(sections[at].payload); err != nil {
			return nil, fmt.Errorf("import section: %w", err)
		}
	}
	exportAt := index(sectionExport)
	if exportAt < 0 {
		return nil, errors.New("the module has no export section")
	}

	// The new global comes last, so no existing global index moves.
	definedGlobals := uint32(0)
	if at := index(sectionGlobal); at >= 0 {
		count, rest, err := readU32(sections[at].payload, 0)
		if err != nil {
			return nil, fmt.Errorf("global section: %w", err)
		}
		definedGlobals = count
		payload := appendU32(nil, count+1)
		payload = append(payload, sections[at].payload[rest:]...)
		payload = append(payload, haltGlobal...)
		sections[at].payload = payload
	} else {
		// Section ids ascend, so the global section goes right before the export section.
		sections = append(sections, wasmSection{})
		copy(sections[exportAt+1:], sections[exportAt:])
		sections[exportAt] = wasmSection{sectionGlobal, append(appendU32(nil, 1), haltGlobal...)}
		exportAt++
	}
	haltIndex := importedGlobals + definedGlobals

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

	if at := index(sectionCode); at >= 0 {
		if sections[at].payload, err = instrumentCode(sections[at].payload, haltIndex); err != nil {
			return nil, fmt.Errorf("code section: %w", err)
		}
	}

	out := append([]byte{}, wasm[:8]...)
	for _, s := range sections {
		out = append(out, s.id)
		out = appendU32(out, uint32(len(s.payload)))
		out = append(out, s.payload...)
	}
	return out, nil
}

// countGlobalImports counts the global imports of an import section; they precede the module's own globals in the
// global index space.
func countGlobalImports(payload []byte) (uint32, error) {
	count, pos, err := readU32(payload, 0)
	if err != nil {
		return 0, err
	}
	var globals uint32
	for range count {
		// module name, field name, then the kind and its descriptor
		for range 2 {
			n, next, err := readU32(payload, pos)
			if err != nil {
				return 0, err
			}
			pos = next + int(n)
		}
		if pos >= len(payload) {
			return 0, errors.New("truncated import")
		}
		kind := payload[pos]
		pos++
		switch kind {
		case 0: // function: type index
			_, pos, err = readU32(payload, pos)
		case 1: // table: reference type, limits
			pos, err = skipLimits(payload, pos+1)
		case 2: // memory: limits
			pos, err = skipLimits(payload, pos)
		case 3: // global: value type, mutability
			globals++
			pos += 2
		case 4: // tag: attribute, type index
			_, pos, err = readU32(payload, pos+1)
		default:
			err = fmt.Errorf("unknown import kind %#x", kind)
		}
		if err != nil {
			return 0, err
		}
	}
	return globals, nil
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

// instrumentCode inserts the halt check after every loop header of a code section.
func instrumentCode(payload []byte, haltIndex uint32) ([]byte, error) {
	count, pos, err := readU32(payload, 0)
	if err != nil {
		return nil, err
	}
	check := append(append([]byte{0x23}, appendU32(nil, haltIndex)...), 0x04, 0x40, 0x00, 0x0b)
	out := appendU32(make([]byte, 0, len(payload)+len(payload)/16), count)
	for i := range count {
		size, bodyAt, err := readU32(payload, pos)
		if err != nil {
			return nil, err
		}
		if uint64(bodyAt)+uint64(size) > uint64(len(payload)) {
			return nil, fmt.Errorf("function %d extends past the section", i)
		}
		body, err := instrumentBody(payload[bodyAt:bodyAt+int(size)], check)
		if err != nil {
			return nil, fmt.Errorf("function %d: %w", i, err)
		}
		out = appendU32(out, uint32(len(body)))
		out = append(out, body...)
		pos = bodyAt + int(size)
	}
	if pos != len(payload) {
		return nil, errors.New("bytes after the last function")
	}
	return out, nil
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
