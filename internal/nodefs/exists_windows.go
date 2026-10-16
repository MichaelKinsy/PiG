package nodefs

import (
	"os"
	"sync"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// attributePathUnits is the longest path read through GetFileAttributes: the legacy MAX_PATH, so the \\?\ prefix handling that
// os.Stat applies to longer paths stays with os.Stat.
const attributePathUnits = windows.MAX_PATH

var attributeBuffers = sync.Pool{New: func() any { return new([attributePathUnits]uint16) }}

// Exists is fs.existsSync: whether name names an existing file system entry, following symbolic links. os.Stat allocates about
// five objects per call on Windows (the UTF-16 path and the FileInfo); GetFileAttributes on a pooled buffer allocates none. A
// reparse point, a path with a NUL or one of MAX_PATH units or more asks os.Stat, so what a link or such a path names is as before.
func Exists(name string) bool {
	attrs, ok, decided := attributes(name)
	if !decided {
		return existsStat(name)
	}
	if !ok {
		return false
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return existsStat(name)
	}
	return true
}

// PlainEntry reports whether name exists and is not a reparse point, from its attributes alone: such an entry is neither a link
// nor a mount, so a realpath walk passes it without a FileInfo. False means ask os.Lstat: a reparse point, a missing entry, an
// error, or a path GetFileAttributes is not used for.
func PlainEntry(name string) bool {
	attrs, ok, decided := attributes(name)
	return decided && ok && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}

// attributes reads name's attributes through a pooled UTF-16 buffer. decided is false when name is empty, holds a NUL or does not
// fit in MAX_PATH units, and the caller must use os.Stat or os.Lstat; ok is false when the entry cannot be read.
func attributes(name string) (attrs uint32, ok, decided bool) {
	if len(name) == 0 || len(name) >= attributePathUnits {
		return 0, false, false
	}
	buf := attributeBuffers.Get().(*[attributePathUnits]uint16)
	defer attributeBuffers.Put(buf)
	n := 0
	for _, r := range name {
		if r == 0 || n+2 >= attributePathUnits {
			return 0, false, false
		}
		// A rune past the BMP takes a surrogate pair; EncodeRune reports U+FFFD for both halves of any other rune.
		if r1, r2 := utf16.EncodeRune(r); r1 != '\uFFFD' || r2 != '\uFFFD' {
			buf[n], buf[n+1] = uint16(r1), uint16(r2)
			n += 2
			continue
		}
		buf[n] = uint16(r)
		n++
	}
	buf[n] = 0
	attrs, err := windows.GetFileAttributes(&buf[0])
	if err != nil {
		return 0, false, true
	}
	return attrs, true, true
}

func existsStat(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}
