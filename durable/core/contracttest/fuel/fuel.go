// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package fuel meters a WebAssembly module: Instrument rewrites a module so that every basic block adds its own length
// to a counter, the Wasm instruction count the count gate ratchets (docs/plan/durable-core, Durable plan 2 item 9). The
// module is the shipped core with a counter; no wazero or runtime flag is involved, so the count is the same on every
// engine and every machine, and it includes the language runtime (TinyGo's collector, Go's scheduler) as well as the core.
//
// The unit is one instruction of the binary, structural instructions (block, loop, if, else, end) included, counted per
// basic block when the block is entered. A block that traps part-way counts all of itself; the count is a gate metric, not
// a cost model. Instrumenting adds no function, no block and no label, so function indices and branch depths are unchanged.
// SIMD, atomics and tail calls are refused: the core does not use them, and a module that does needs this decoder extended
// before its count can be trusted.
package fuel

import (
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

// Export is the name of the exported mutable i64 global holding the count.
const Export = "pig_fuel"

// Read returns the instruction count of an instance of an instrumented module.
func Read(mod api.Module) (uint64, error) {
	g := mod.ExportedGlobal(Export)
	if g == nil {
		return 0, errors.New("fuel: the module has no " + Export + " global; it was not instrumented")
	}
	return g.Get(), nil
}

// Instrument returns wasm with an instruction counter: a mutable i64 global exported as Export, added to at the start of
// every basic block.
func Instrument(wasm []byte) ([]byte, error) {
	if len(wasm) < 8 || string(wasm[:4]) != "\x00asm" {
		return nil, errors.New("fuel: not a WebAssembly module")
	}
	secs, err := readSections(wasm[8:])
	if err != nil {
		return nil, err
	}
	importedGlobals, definedGlobals := 0, 0
	for _, s := range secs {
		switch s.id {
		case 2:
			n, err := countImportedGlobals(s.body)
			if err != nil {
				return nil, err
			}
			importedGlobals = n
		case 6:
			n, _, err := uleb(s.body, 0)
			if err != nil {
				return nil, err
			}
			definedGlobals = int(n)
		}
	}
	counter := uint32(importedGlobals + definedGlobals)
	var out []byte
	out = append(out, wasm[:8]...)
	haveGlobal, haveExport := false, false
	global := []byte{0x7E, 0x01, 0x42, 0x00, 0x0B} // i64, mutable, init i64.const 0
	for _, s := range secs {
		if !haveGlobal && s.id != 0 && rank(s.id) > rank(6) {
			out = appendSection(out, 6, append([]byte{1}, global...))
			haveGlobal = true
		}
		body := s.body
		switch s.id {
		case 6:
			n, at, _ := uleb(body, 0)
			body = append(appendULEB(nil, n+1), body[at:]...)
			body = append(body, global...)
			haveGlobal = true
		case 7:
			n, at, _ := uleb(body, 0)
			entry := appendName(nil, Export)
			entry = append(entry, 0x03)
			entry = appendULEB(entry, uint64(counter))
			body = append(appendULEB(nil, n+1), body[at:]...)
			body = append(body, entry...)
			haveExport = true
		case 10:
			var err error
			if body, err = instrumentCode(body, counter); err != nil {
				return nil, err
			}
		}
		out = appendSection(out, s.id, body)
	}
	if !haveGlobal || !haveExport {
		return nil, errors.New("fuel: the module has no export section to publish the counter in")
	}
	return out, nil
}

type section struct {
	id   byte
	body []byte
}

// rank orders section ids as the binary format requires (the tag section, id 13, sits between memory and global).
func rank(id byte) int {
	switch id {
	case 13:
		return 6
	case 6:
		return 7
	case 7:
		return 8
	case 8:
		return 9
	case 9:
		return 10
	case 12:
		return 11
	case 10:
		return 12
	case 11:
		return 13
	}
	return int(id)
}

func readSections(b []byte) ([]section, error) {
	var out []section
	for i := 0; i < len(b); {
		id := b[i]
		n, at, err := uleb(b, i+1)
		if err != nil {
			return nil, err
		}
		if at+int(n) > len(b) {
			return nil, errors.New("fuel: a section runs past the end of the module")
		}
		out = append(out, section{id, b[at : at+int(n)]})
		i = at + int(n)
	}
	return out, nil
}

func appendSection(dst []byte, id byte, body []byte) []byte {
	dst = append(dst, id)
	dst = appendULEB(dst, uint64(len(body)))
	return append(dst, body...)
}

func appendName(dst []byte, s string) []byte {
	dst = appendULEB(dst, uint64(len(s)))
	return append(dst, s...)
}

// countImportedGlobals reads an import section and returns how many of its entries are globals.
func countImportedGlobals(b []byte) (int, error) {
	n, i, err := uleb(b, 0)
	if err != nil {
		return 0, err
	}
	globals := 0
	for range n {
		for range 2 { // module and field names
			l, at, err := uleb(b, i)
			if err != nil {
				return 0, err
			}
			i = at + int(l)
		}
		if i >= len(b) {
			return 0, errors.New("fuel: truncated import")
		}
		kind := b[i]
		i++
		switch kind {
		case 0: // function: type index
			_, at, err := uleb(b, i)
			if err != nil {
				return 0, err
			}
			i = at
		case 1: // table: reference type, limits
			i++
			at, err := skipLimits(b, i)
			if err != nil {
				return 0, err
			}
			i = at
		case 2: // memory: limits
			at, err := skipLimits(b, i)
			if err != nil {
				return 0, err
			}
			i = at
		case 3: // global: value type, mutability
			globals++
			i += 2
		case 4: // tag: attribute, type index
			i++
			_, at, err := uleb(b, i)
			if err != nil {
				return 0, err
			}
			i = at
		default:
			return 0, fmt.Errorf("fuel: unknown import kind %d", kind)
		}
	}
	return globals, nil
}

func skipLimits(b []byte, i int) (int, error) {
	if i >= len(b) {
		return 0, errors.New("fuel: truncated limits")
	}
	flags := b[i]
	i++
	for range 1 + int(flags&1) {
		_, at, err := uleb(b, i)
		if err != nil {
			return 0, err
		}
		i = at
	}
	return i, nil
}

// instrumentCode rewrites a code section.
func instrumentCode(b []byte, counter uint32) ([]byte, error) {
	n, i, err := uleb(b, 0)
	if err != nil {
		return nil, err
	}
	out := appendULEB(nil, n)
	for f := range n {
		size, at, err := uleb(b, i)
		if err != nil {
			return nil, err
		}
		if at+int(size) > len(b) {
			return nil, fmt.Errorf("fuel: function %d runs past the code section", f)
		}
		body, err := instrumentBody(b[at:at+int(size)], counter)
		if err != nil {
			return nil, fmt.Errorf("fuel: function %d: %w", f, err)
		}
		out = appendULEB(out, uint64(len(body)))
		out = append(out, body...)
		i = at + int(size)
	}
	return out, nil
}

// inst is one decoded instruction: its bytes in the body and whether it ends a basic block.
type inst struct {
	from, to int
	ends     bool
}

// instrumentBody inserts the counter code at the start of each basic block of one function body (its locals declaration,
// then its expression).
func instrumentBody(body []byte, counter uint32) ([]byte, error) {
	nlocals, i, err := uleb(body, 0)
	if err != nil {
		return nil, err
	}
	for range nlocals {
		_, at, err := uleb(body, i)
		if err != nil {
			return nil, err
		}
		i = at + 1 // value type
	}
	head := body[:i]
	var insts []inst
	for i < len(body) {
		start := i
		op := body[i]
		i++
		next, ends, err := immediates(body, op, i)
		if err != nil {
			return nil, fmt.Errorf("at byte %d: %w", start, err)
		}
		i = next
		insts = append(insts, inst{start, i, ends})
	}
	out := append([]byte(nil), head...)
	for k := 0; k < len(insts); {
		// A basic block runs from k through the next instruction that ends one (or the function's end).
		last := k
		for last < len(insts)-1 && !insts[last].ends {
			last++
		}
		out = append(out, 0x23)
		out = appendULEB(out, uint64(counter))
		out = append(out, 0x42)
		out = appendSLEB(out, int64(last-k+1))
		out = append(out, 0x7C, 0x24)
		out = appendULEB(out, uint64(counter))
		for _, in := range insts[k : last+1] {
			out = append(out, body[in.from:in.to]...)
		}
		k = last + 1
	}
	return out, nil
}

// immediates returns the offset after the immediates of the instruction whose opcode byte precedes i, and whether the
// instruction ends a basic block (control flow leaves or a new block starts after it).
func immediates(b []byte, op byte, i int) (int, bool, error) {
	u := func() error {
		_, at, err := uleb(b, i)
		i = at
		return err
	}
	switch op {
	case 0x00, 0x0F: // unreachable, return
		return i, true, nil
	case 0x01, 0x1A, 0x1B: // nop, drop, select
		return i, false, nil
	case 0x02, 0x03, 0x04: // block, loop, if: block type (a signed 33-bit LEB)
		_, at, err := sleb(b, i)
		return at, true, err
	case 0x05, 0x0B: // else, end
		return i, true, nil
	case 0x0C, 0x0D: // br, br_if
		return i, true, u()
	case 0x0E: // br_table
		n, at, err := uleb(b, i)
		if err != nil {
			return 0, false, err
		}
		i = at
		for range n + 1 {
			if err := u(); err != nil {
				return 0, false, err
			}
		}
		return i, true, nil
	case 0x10: // call
		return i, false, u()
	case 0x11: // call_indirect: type, table
		if err := u(); err != nil {
			return 0, false, err
		}
		return i, false, u()
	case 0x12, 0x13:
		return 0, false, errors.New("tail calls are not supported")
	case 0x1C: // select with types
		n, at, err := uleb(b, i)
		return at + int(n), false, err
	case 0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26: // local and global access, table.get/set
		return i, false, u()
	case 0x3F, 0x40: // memory.size, memory.grow
		return i + 1, false, nil
	case 0x41, 0x42: // i32.const, i64.const
		_, at, err := sleb(b, i)
		return at, false, err
	case 0x43:
		return i + 4, false, nil
	case 0x44:
		return i + 8, false, nil
	case 0xD0: // ref.null
		return i + 1, false, nil
	case 0xD1: // ref.is_null
		return i, false, nil
	case 0xD2: // ref.func
		return i, false, u()
	case 0xFC:
		sub, at, err := uleb(b, i)
		if err != nil {
			return 0, false, err
		}
		i = at
		switch {
		case sub <= 7: // trunc_sat
			return i, false, nil
		case sub == 8: // memory.init: data, memory
			if err := u(); err != nil {
				return 0, false, err
			}
			return i + 1, false, nil
		case sub == 9, sub == 13: // data.drop, elem.drop
			return i, false, u()
		case sub == 10: // memory.copy
			return i + 2, false, nil
		case sub == 11: // memory.fill
			return i + 1, false, nil
		case sub == 12, sub == 14: // table.init, table.copy
			if err := u(); err != nil {
				return 0, false, err
			}
			return i, false, u()
		case sub >= 15 && sub <= 17: // table.grow, table.size, table.fill
			return i, false, u()
		}
		return 0, false, fmt.Errorf("unknown 0xFC instruction %d", sub)
	case 0xFD, 0xFE:
		return 0, false, errors.New("SIMD and atomic instructions are not supported")
	}
	switch {
	case op >= 0x28 && op <= 0x3E: // loads and stores: alignment, offset
		if err := u(); err != nil {
			return 0, false, err
		}
		return i, false, u()
	case op >= 0x45 && op <= 0xC4: // numeric instructions
		return i, false, nil
	}
	return 0, false, fmt.Errorf("unknown opcode 0x%02x", op)
}

func uleb(b []byte, i int) (uint64, int, error) {
	var r uint64
	for s := uint(0); ; s += 7 {
		if i >= len(b) || s > 63 {
			return 0, i, errors.New("fuel: bad LEB128")
		}
		c := b[i]
		i++
		r |= uint64(c&0x7F) << s
		if c&0x80 == 0 {
			return r, i, nil
		}
	}
}

func sleb(b []byte, i int) (int64, int, error) {
	var r int64
	for s := uint(0); ; s += 7 {
		if i >= len(b) || s > 63 {
			return 0, i, errors.New("fuel: bad LEB128")
		}
		c := b[i]
		i++
		r |= int64(c&0x7F) << s
		if c&0x80 == 0 {
			if s+7 < 64 && c&0x40 != 0 {
				r |= -1 << (s + 7)
			}
			return r, i, nil
		}
	}
}

func appendULEB(dst []byte, v uint64) []byte {
	for {
		c := byte(v & 0x7F)
		v >>= 7
		if v == 0 {
			return append(dst, c)
		}
		dst = append(dst, c|0x80)
	}
}

func appendSLEB(dst []byte, v int64) []byte {
	for {
		c := byte(v & 0x7F)
		v >>= 7
		if (v == 0 && c&0x40 == 0) || (v == -1 && c&0x40 != 0) {
			return append(dst, c)
		}
		dst = append(dst, c|0x80)
	}
}
