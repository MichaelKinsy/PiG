// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"
)

func sec(id byte, payload ...byte) []byte {
	return append(appendULEB([]byte{id}, uint32(len(payload))), payload...)
}

// testModule has one function import ("f", index 0) and three declared functions a, b and c (indexes 1-3).
func testModule(extra ...[]byte) []byte {
	m := []byte("\x00asm\x01\x00\x00\x00")
	m = append(m, sec(1, 1, 0x60, 0, 0)...)                          // type ()->()
	m = append(m, sec(2, 1, 1, 'm', 1, 'f', 0, 0)...)                // import m.f, function of type 0
	m = append(m, sec(3, 3, 0, 0, 0)...)                             // three functions
	m = append(m, sec(10, 3, 2, 0, 0x0b, 2, 0, 0x0b, 2, 0, 0x0b)...) // bodies
	names := []byte{4, 0, 1, 'f', 1, 1, 'a', 2, 1, 'b', 3, 1, 'c'}
	sub := append(appendULEB([]byte{1}, uint32(len(names))), names...)
	m = append(m, sec(0, append([]byte{4, 'n', 'a', 'm', 'e'}, sub...)...)...)
	for _, e := range extra {
		m = append(m, e...)
	}
	return m
}

func TestHintLayout(t *testing.T) {
	in := testModule()
	out, err := hint(in, []string{"a", "c"}, false)
	if err != nil {
		t.Fatal(err)
	}
	codeAt := bytes.Index(in, sec(10, 3, 2, 0, 0x0b, 2, 0, 0x0b, 2, 0, 0x0b))
	legacy := customSection(legacySection, []byte{3, 0x06, 0x01, 0x06})
	// c is function index 3 (one import): index, byte offset 0, size 1, priority 0.
	priority := customSection(prioritySect, []byte{2, 1, 0, 1, 0, 3, 0, 1, 0})
	want := append(append(append(append([]byte{}, in[:codeAt]...), legacy...), priority...), in[codeAt:]...)
	if !bytes.Equal(out, want) {
		t.Fatalf("module:\n got %x\nwant %x", out, want)
	}
}

func TestHintRefusals(t *testing.T) {
	hinted, err := hint(testModule(), []string{"a"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		mod  []byte
		list []string
		want string
	}{
		"already hinted":  {hinted, []string{"a"}, "already has a"},
		"stale list":      {testModule(), []string{"a", "renamed"}, "1 of the 2 functions"},
		"an import":       {testModule(), []string{"f"}, "1 of the 1 functions"},
		"no names":        {[]byte("\x00asm\x01\x00\x00\x00" + string(sec(1, 1, 0x60, 0, 0)) + string(sec(3, 1, 0)) + string(sec(10, 1, 2, 0, 0x0b))), []string{"a"}, "no function names"},
		"not a module":    {[]byte("nope nope nope"), []string{"a"}, "not a WebAssembly module"},
		"no code section": {[]byte("\x00asm\x01\x00\x00\x00"), []string{"a"}, "no code section"},
	} {
		if _, err := hint(tc.mod, tc.list, false); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want one containing %q", name, err, tc.want)
		}
	}
}

func TestHintAmbiguousNameIsNotHinted(t *testing.T) {
	m := []byte("\x00asm\x01\x00\x00\x00")
	m = append(m, sec(1, 1, 0x60, 0, 0)...)
	m = append(m, sec(3, 2, 0, 0)...)
	m = append(m, sec(10, 2, 2, 0, 0x0b, 2, 0, 0x0b)...)
	names := []byte{2, 0, 1, 'x', 1, 1, 'x'}
	m = append(m, sec(0, append([]byte{4, 'n', 'a', 'm', 'e', 1}, append(appendULEB(nil, uint32(len(names))), names...)...)...)...)
	if _, err := hint(m, []string{"x"}, false); err == nil || !strings.Contains(err.Error(), "1 of the 1 functions") {
		t.Fatalf("a name two functions share must not be hinted: %v", err)
	}
}

func TestHintAllowStaleKeepsWhatMatches(t *testing.T) {
	out, err := hint(testModule(), []string{"a", "renamed"}, true)
	if err != nil {
		t.Fatal(err)
	}
	want, err := hint(testModule(), []string{"a"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("-allow-stale must hint exactly the functions that match")
	}
}
