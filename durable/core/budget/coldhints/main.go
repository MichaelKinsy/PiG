// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Command coldhints marks the functions that the first turn of a session runs in a core built by build-wasm.sh, as WebAssembly
// compilation hints, so that a host that enables hints compiles them with Liftoff when it compiles the module instead of
// one at a time on its first call.
//
//	coldhints [-allow-stale] <module.wasm> <coldset.txt>
//
// coldset.txt names one function of the module's name section per line, in the module's own spelling: the functions that V8
// compiles with Liftoff during open and the first turn (`node --trace-wasm-compilation-times`, see README.md). The command adds
// two custom sections before the code section, each of which a runtime ignores unless it implements and enables hints:
//
//   - "compilationHints", the legacy format that V8 13 (Node 24) reads under --experimental-wasm-compilation-hints with
//     --no-wasm-lazy-compilation: one byte per declared function, 0x06 (eager, Liftoff) for a named one and 0x01 (lazy) for the rest;
//   - "metadata.code.compilation_priority", the format of V8 14 and later (Node 26, workerd) under
//     --experimental-wasm-compilation-hints: for each named function its index, byte offset 0, size 1 and priority 0.
//
// It fails when a module already holds either section, when the module has no function names, and when fewer than minMatched
// of the names in the list match a function: a renamed function must not silently drop out of the hints. -allow-stale (for
// regenerating the list, README.md) skips the last check.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	legacySection = "compilationHints"
	prioritySect  = "metadata.code.compilation_priority"
	// minMatched is the share of the list that must name a function of the module.
	minMatched = 0.95
)

const (
	secCustom   = 0
	secImport   = 2
	secFunction = 3
	secCode     = 10
)

type section struct {
	id         byte
	start, end int // the whole section: id byte through the last payload byte
	body       int // first payload byte
}

type module struct {
	sections []section
	imported int      // function imports
	declared int      // functions with a body
	names    []string // by function index; "" where the name section gives none
}

func uleb(b []byte, p int) (uint32, int, error) {
	var r uint32
	for shift := uint(0); shift < 35; shift += 7 {
		if p >= len(b) {
			return 0, 0, errors.New("truncated LEB128")
		}
		x := b[p]
		p++
		r |= uint32(x&0x7f) << shift
		if x&0x80 == 0 {
			return r, p, nil
		}
	}
	return 0, 0, errors.New("LEB128 too long")
}

func appendULEB(dst []byte, v uint32) []byte {
	for {
		x := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			return append(dst, x)
		}
		dst = append(dst, x|0x80)
	}
}

func parse(b []byte) (*module, error) {
	if len(b) < 8 || !bytes.Equal(b[:4], []byte("\x00asm")) {
		return nil, errors.New("not a WebAssembly module")
	}
	m := &module{}
	for p := 8; p < len(b); {
		start := p
		id := b[p]
		size, q, err := uleb(b, p+1)
		if err != nil {
			return nil, err
		}
		end := q + int(size)
		if end > len(b) {
			return nil, errors.New("a section runs past the end of the module")
		}
		m.sections = append(m.sections, section{id, start, end, q})
		p = end
	}
	for _, s := range m.sections {
		switch s.id {
		case secImport:
			if err := m.countImports(b, s); err != nil {
				return nil, err
			}
		case secFunction:
			n, _, err := uleb(b, s.body)
			if err != nil {
				return nil, err
			}
			m.declared = int(n)
		}
	}
	m.names = make([]string, m.imported+m.declared)
	for _, s := range m.sections {
		if s.id != secCustom {
			continue
		}
		l, p, err := uleb(b, s.body)
		if err != nil {
			return nil, err
		}
		if string(b[p:p+int(l)]) != "name" {
			continue
		}
		if err := m.readNames(b, p+int(l), s.end); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *module) countImports(b []byte, s section) error {
	n, p, err := uleb(b, s.body)
	if err != nil {
		return err
	}
	skip := func() error {
		l, q, err := uleb(b, p)
		p = q + int(l)
		return err
	}
	limits := func() error {
		flags := b[p]
		p++
		for range 1 + int(flags&1) {
			_, q, err := uleb(b, p)
			if err != nil {
				return err
			}
			p = q
		}
		return nil
	}
	for range n {
		if err := skip(); err != nil {
			return err
		}
		if err := skip(); err != nil {
			return err
		}
		kind := b[p]
		p++
		switch kind {
		case 0:
			_, q, err := uleb(b, p)
			if err != nil {
				return err
			}
			p = q
			m.imported++
		case 1:
			p++
			if err := limits(); err != nil {
				return err
			}
		case 2:
			if err := limits(); err != nil {
				return err
			}
		case 3:
			p += 2
		default:
			return fmt.Errorf("import kind %d is not supported", kind)
		}
	}
	return nil
}

func (m *module) readNames(b []byte, p, end int) error {
	for p < end {
		sub := b[p]
		size, q, err := uleb(b, p+1)
		if err != nil {
			return err
		}
		next := q + int(size)
		if sub == 1 {
			n, r, err := uleb(b, q)
			if err != nil {
				return err
			}
			for range n {
				idx, r2, err := uleb(b, r)
				if err != nil {
					return err
				}
				l, r3, err := uleb(b, r2)
				if err != nil {
					return err
				}
				if int(idx) < len(m.names) {
					m.names[idx] = string(b[r3 : r3+int(l)])
				}
				r = r3 + int(l)
			}
		}
		p = next
	}
	return nil
}

func customSection(name string, payload []byte) []byte {
	body := appendULEB(nil, uint32(len(name)))
	body = append(body, name...)
	body = append(body, payload...)
	return append(appendULEB([]byte{secCustom}, uint32(len(body))), body...)
}

// hint returns mod with the two hint sections for the functions named in coldset.
func hint(mod []byte, coldset []string, allowStale bool) ([]byte, error) {
	m, err := parse(mod)
	if err != nil {
		return nil, err
	}
	var code *section
	for i, s := range m.sections {
		if s.id == secCode {
			code = &m.sections[i]
		}
		if s.id == secCustom {
			l, p, err := uleb(mod, s.body)
			if err != nil {
				return nil, err
			}
			if n := string(mod[p : p+int(l)]); n == legacySection || n == prioritySect {
				return nil, fmt.Errorf("the module already has a %s section", n)
			}
		}
	}
	if code == nil {
		return nil, errors.New("the module has no code section")
	}
	byName := map[string]int{}
	for i, n := range m.names {
		if n != "" {
			if _, dup := byName[n]; dup {
				byName[n] = -1 // ambiguous: not hinted
			} else {
				byName[n] = i
			}
		}
	}
	if len(byName) == 0 {
		return nil, errors.New("the module has no function names")
	}
	hinted := make([]bool, len(m.names))
	matched := 0
	var missing []string
	for _, n := range coldset {
		if i, ok := byName[n]; ok && i >= m.imported {
			hinted[i] = true
			matched++
		} else {
			missing = append(missing, n)
		}
	}
	if !allowStale && float64(matched) < minMatched*float64(len(coldset)) {
		show := missing
		if len(show) > 5 {
			show = show[:5]
		}
		return nil, fmt.Errorf("%d of the %d functions in the cold set name no function of the module (for example %q): regenerate it", len(missing), len(coldset), show)
	}
	legacy := appendULEB(nil, uint32(m.declared))
	for i := m.imported; i < len(hinted); i++ {
		if hinted[i] {
			legacy = append(legacy, 0x02|1<<2) // strategy eager, baseline tier Liftoff
		} else {
			legacy = append(legacy, 0x01) // strategy lazy
		}
	}
	var items []byte
	count := 0
	for i := m.imported; i < len(hinted); i++ {
		if hinted[i] {
			items = appendULEB(items, uint32(i))
			items = append(items, 0, 1, 0) // byte offset 0 (the whole function), size 1, compilation priority 0
			count++
		}
	}
	priority := append(appendULEB(nil, uint32(count)), items...)
	out := make([]byte, 0, len(mod)+len(legacy)+len(priority)+96)
	out = append(out, mod[:code.start]...)
	out = append(out, customSection(legacySection, legacy)...)
	out = append(out, customSection(prioritySect, priority)...)
	return append(out, mod[code.start:]...), nil
}

func readList(path string) ([]string, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var list []string
	//portlint:allow pathseparators each line drops its trailing \r right below, so CRLF lists read as LF ones; Go-only build tool
	for l := range strings.SplitSeq(string(text), "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" && !strings.HasPrefix(l, "#") {
			list = append(list, l)
		}
	}
	if len(list) == 0 {
		return nil, errors.New("the cold set is empty")
	}
	return list, nil
}

func run(modPath, listPath string, allowStale bool) error {
	list, err := readList(listPath)
	if err != nil {
		return err
	}
	mod, err := os.ReadFile(modPath)
	if err != nil {
		return err
	}
	out, err := hint(mod, list, allowStale)
	if err != nil {
		return fmt.Errorf("%s: %w", modPath, err)
	}
	return os.WriteFile(modPath, out, 0o644)
}

func main() {
	args := os.Args[1:]
	allowStale := len(args) > 0 && args[0] == "-allow-stale"
	if allowStale {
		args = args[1:]
	}
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: coldhints [-allow-stale] <module.wasm> <coldset.txt>")
		os.Exit(2)
	}
	if err := run(args[0], args[1], allowStale); err != nil {
		fmt.Fprintln(os.Stderr, "coldhints:", err)
		os.Exit(1)
	}
}
