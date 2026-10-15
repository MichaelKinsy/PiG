// Package clean imports what the core may: strconv, and encoding/binary (whose reflect import is the accepted route).
package clean

import (
	"encoding/binary"
	"strconv"
)

func Put(dst []byte, v uint32) []byte {
	return strconv.AppendUint(binary.LittleEndian.AppendUint32(dst, v), uint64(v), 10)
}
