package collate

import (
	"compress/flate"
	_ "embed"
	"io"
	"strings"
	"sync"
)

// The tables are 157 KB as arrays and 31 KB as DEFLATE: tables.bin holds them compressed (see tablegen.Tables for the layout), and the first
// collator inflates them into the arrays tables.go declares, which are zero until then and take no space in the binary. A process that never
// compares strings pays neither the time (under 1 ms, BenchmarkLoadTables) nor the memory.

//go:embed tables.bin
var compressedTables string

var tablesLoaded sync.Once

// loadTables fills the table arrays on first use. It returns after every goroutine that calls it has seen the arrays complete.
func loadTables() { tablesLoaded.Do(decodeTables) }

func decodeTables() {
	r := flate.NewReader(strings.NewReader(compressedTables))
	d := planeDecoder{r: r, plane: make([]byte, len(valueWindows))}
	d.uint16s(leadWindows[:])
	d.uint16s(indexWindows[:])
	d.uint32s(asciiValues[:])
	d.uint32s(valueWindows[:])
	d.uint32s(expansions[:])
	d.uint32s(contractionElems[:])
	for i := range 4 {
		plane := d.read(len(contractionTries))
		for j, b := range plane {
			node := &contractionTries[j]
			switch i {
			case 0:
				node.L = b
			case 1:
				node.H = b
			case 2:
				node.N = b
			default:
				node.I = b
			}
		}
	}
	if _, err := io.ReadFull(r, d.plane[:1]); err != io.EOF {
		panic("collate: tables.bin holds more than tables.go declares")
	}
}

// planeDecoder reads arrays stored as planes of bytes over all their entries, the most significant plane first.
type planeDecoder struct {
	r     io.Reader
	plane []byte // scratch, as long as the longest plane
}

func (d *planeDecoder) read(n int) []byte {
	p := d.plane[:n]
	if _, err := io.ReadFull(d.r, p); err != nil {
		panic("collate: tables.bin is corrupt: " + err.Error())
	}
	return p
}

func (d *planeDecoder) uint16s(dst []uint16) {
	clear(dst)
	for shift := 8; shift >= 0; shift -= 8 {
		for i, b := range d.read(len(dst)) {
			dst[i] |= uint16(b) << shift
		}
	}
}

func (d *planeDecoder) uint32s(dst []uint32) {
	clear(dst)
	for shift := 24; shift >= 0; shift -= 8 {
		for i, b := range d.read(len(dst)) {
			dst[i] |= uint32(b) << shift
		}
	}
}

const blockSize = 64
