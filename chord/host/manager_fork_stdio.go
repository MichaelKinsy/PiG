package host

import (
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf16"
	"unsafe"
)

// The CRT's descriptor flags (libuv process-stdio.c).
const (
	crtOpen   = 0x01
	crtPipe   = 0x08
	crtDevice = 0x40
)

// The two buffers manager.ts forkDriver's Windows spawn builds for CreateProcess; they hold no handle of their own, so
// they are plain functions of their input.

// childStdio is lpReserved2's descriptor table: int32 count, count CRT flag bytes, count handles.
func childStdio(flags []byte, handles []uintptr) []byte {
	pointer := int(unsafe.Sizeof(uintptr(0)))
	buffer := make([]byte, 4+len(flags)+pointer*len(flags))
	binary.LittleEndian.PutUint32(buffer, uint32(len(flags)))
	copy(buffer[4:], flags)
	for i, handle := range handles {
		at := 4 + len(flags) + pointer*i
		if pointer == 8 {
			binary.LittleEndian.PutUint64(buffer[at:], uint64(handle))
		} else {
			binary.LittleEndian.PutUint32(buffer[at:], uint32(handle))
		}
	}
	return buffer
}

// environmentBlock is the child's environment as CreateProcess takes it: name=value entries, a later one replacing an
// earlier of the same name (names are case-insensitive), sorted by name, each ended by NUL and the block by another.
func environmentBlock(env []string) ([]uint16, error) {
	type entry struct{ key, text string }
	var entries []entry
	index := map[string]int{}
	for _, text := range env {
		if strings.IndexByte(text, 0) >= 0 {
			return nil, errors.New("environment entry contains NUL")
		}
		// Entries such as =C:=C:\dir start with '=': the name ends at the next one.
		end := strings.IndexByte(text[min(1, len(text)):], '=')
		if end < 0 {
			continue
		}
		key := strings.ToUpper(text[:end+min(1, len(text))])
		if at, ok := index[key]; ok {
			entries[at].text = text
			continue
		}
		index[key] = len(entries)
		entries = append(entries, entry{key, text})
	}
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].key < entries[j-1].key; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
	var block []uint16
	for _, entry := range entries {
		block = append(block, utf16.Encode([]rune(entry.text))...)
		block = append(block, 0)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil
}
