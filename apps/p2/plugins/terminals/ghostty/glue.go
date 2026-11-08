package ghostty

// glueModule assembles a WebAssembly module that grows the guest's function table by two slots and puts the
// host's two callbacks (imported from the "host" module as wp and cs) into them, which is what ghostty.ts does with
// a throwaway module per callback: the guest calls its C callbacks through the table, and a Go host function can
// only get into a table by way of a module that imports both. Its `install` export returns the first new slot
// (the write-pty callback; the color-scheme callback follows it).
func glueModule(guest string) []byte {
	uleb := func(n int) []byte {
		var out []byte
		for {
			b := byte(n & 0x7f)
			n >>= 7
			if n != 0 {
				out = append(out, b|0x80)
				continue
			}
			return append(out, b)
		}
	}
	name := func(s string) []byte { return append(uleb(len(s)), s...) }
	section := func(id byte, body ...[]byte) []byte {
		var content []byte
		for _, part := range body {
			content = append(content, part...)
		}
		return append(append([]byte{id}, uleb(len(content))...), content...)
	}
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, part := range parts {
			out = append(out, part...)
		}
		return out
	}
	const (
		i32       = 0x7f
		funcref   = 0x70
		refNull   = 0xd0
		refFunc   = 0xd2
		tableGrow = 0xfc
		tableSet  = 0x26
	)
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	// Types: 0 (i32 i32 i32 i32) -> (); 1 (i32 i32 i32) -> i32; 2 () -> i32.
	out = append(out, section(1,
		uleb(3),
		[]byte{0x60, 4, i32, i32, i32, i32, 0},
		[]byte{0x60, 3, i32, i32, i32, 1, i32},
		[]byte{0x60, 0, 1, i32},
	)...)
	out = append(out, section(2,
		uleb(3),
		name("host"), name("wp"), []byte{0x00, 0},
		name("host"), name("cs"), []byte{0x00, 1},
		name(guest), name("__indirect_function_table"), []byte{0x01, funcref, 0x00, 0x00},
	)...)
	out = append(out, section(3, uleb(1), uleb(2))...)
	out = append(out, section(7, uleb(1), name("install"), []byte{0x00, 2})...)
	// Declares functions 0 and 1 referencable by ref.func.
	out = append(out, section(9, uleb(1), []byte{0x03, 0x00}, uleb(2), uleb(0), uleb(1))...)
	body := cat(
		[]byte{1, 1, i32}, // one local: i32
		[]byte{refNull, funcref, 0x41, 2, tableGrow, 0x0f, 0x00, 0x21, 0x00},
		[]byte{0x20, 0x00, refFunc, 0, tableSet, 0x00},
		[]byte{0x20, 0x00, 0x41, 1, 0x6a, refFunc, 1, tableSet, 0x00},
		[]byte{0x20, 0x00, 0x0b},
	)
	out = append(out, section(10, uleb(1), uleb(len(body)), body)...)
	return out
}
