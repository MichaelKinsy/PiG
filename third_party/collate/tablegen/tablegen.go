// Package tablegen derives the root-locale collation tables of the collate fork from golang.org/x/text's collate/tables.go. x/text keeps one
// set of arrays for every locale, 1.25 MB in all; the root collator reaches about a tenth of them. Generate walks the trie the way
// the lookup does, keeps the windows, expansions and contractions it can reach, renumbers them densely, and returns them as Go declarations and as the bytes the fork inflates at first use.
package tablegen

import (
	"bytes"
	"compress/flate"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
)

const (
	// rootLookupOffset is `und` in x/text's locales table: the block of mainLookup holding the first-byte index of the root trie. Its values
	// offset is 0, which is why the ASCII values are the first 128 entries of mainValues.
	rootLookupOffset = 0x15
	blockSize        = 64
	firstContinue    = 0x80
	firstLead        = 0xC0
	endLead          = 0xF8
)

// source holds the arrays of x/text's tables.go the root table reads.
type source struct {
	lookup       []uint16
	values       []uint32
	expand       []uint32
	contractElem []uint32
	ctEntries    [][4]uint8
}

func evalLiteral(e ast.Expr) (uint64, error) {
	lit, ok := e.(*ast.BasicLit)
	if !ok {
		return 0, fmt.Errorf("tablegen: %T is not a literal", e)
	}
	return strconv.ParseUint(lit.Value, 0, 64)
}

// parseArray evaluates a composite literal of numbers, keyed or in sequence, into a slice as long as the array type declares.
func parseArray(cl *ast.CompositeLit) ([]uint64, error) {
	at, ok := cl.Type.(*ast.ArrayType)
	if !ok {
		return nil, fmt.Errorf("tablegen: composite literal of %T", cl.Type)
	}
	n, err := evalLiteral(at.Len)
	if err != nil {
		return nil, err
	}
	out := make([]uint64, n)
	next := 0
	for _, el := range cl.Elts {
		value := el
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			key, err := evalLiteral(kv.Key)
			if err != nil {
				return nil, err
			}
			next, value = int(key), kv.Value
		}
		v, err := evalLiteral(value)
		if err != nil {
			return nil, err
		}
		if next >= len(out) {
			return nil, fmt.Errorf("tablegen: index %d beyond %d entries", next, len(out))
		}
		out[next] = v
		next++
	}
	return out, nil
}

func parseSource(tablesGo []byte) (*source, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "tables.go", tablesGo, 0)
	if err != nil {
		return nil, err
	}
	src := &source{}
	found := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if len(vs.Values) != 1 {
				continue
			}
			cl, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			name := vs.Names[0].Name
			if name == "mainCTEntries" {
				at := cl.Type.(*ast.ArrayType)
				n, err := evalLiteral(at.Len)
				if err != nil {
					return nil, err
				}
				src.ctEntries = make([][4]uint8, n)
				for i, el := range cl.Elts {
					entry := el.(*ast.CompositeLit)
					if len(entry.Elts) != 4 {
						return nil, fmt.Errorf("tablegen: mainCTEntries[%d] has %d fields", i, len(entry.Elts))
					}
					for j, f := range entry.Elts {
						v, err := evalLiteral(f)
						if err != nil {
							return nil, err
						}
						src.ctEntries[i][j] = uint8(v)
					}
				}
				found[name] = true
				continue
			}
			switch name {
			case "mainLookup", "mainValues", "mainExpandElem", "mainContractElem":
			default:
				continue
			}
			raw, err := parseArray(cl)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			switch name {
			case "mainLookup":
				src.lookup = make([]uint16, len(raw))
				for i, v := range raw {
					src.lookup[i] = uint16(v)
				}
			case "mainValues":
				src.values = narrow(raw)
			case "mainExpandElem":
				src.expand = narrow(raw)
			case "mainContractElem":
				src.contractElem = narrow(raw)
			}
			found[name] = true
		}
	}
	for _, name := range []string{"mainLookup", "mainValues", "mainExpandElem", "mainContractElem", "mainCTEntries"} {
		if !found[name] {
			return nil, fmt.Errorf("tablegen: %s is missing from tables.go", name)
		}
	}
	return src, nil
}

func narrow(raw []uint64) []uint32 {
	out := make([]uint32, len(raw))
	for i, v := range raw {
		out[i] = uint32(v)
	}
	return out
}

// The collation element types, as colltab.Elem.ctype has them.
const (
	maxNormal     = 0xAFFFFFFF
	maxContract   = 0xDFFFFFFF
	maxExpand     = 0xEFFFFFFF
	noIndex       = 0xFF
	maxNBits      = 4
	maxIndexBits  = 12
	maxOffsetBits = 13
)

// builder renumbers what the walk reaches.
type builder struct {
	src *source

	expand      []uint32
	expandOf    map[uint32]uint32 // old index -> new index
	contractTab []struct{ L, H, N, I uint8 }
	contractEl  []uint32
	contractOf  map[uint32]uint32 // old contraction element -> new

	values      [][blockSize]uint32
	valueOf     map[[blockSize]uint32]uint16
	valueWindow map[uint16]uint16 // old window -> new window
	index       [][blockSize]uint16
	indexOf     map[[blockSize]uint16]uint16
}

// windowStart is the position in x/text's arrays of window w: the trie indexes a window by the continuation byte 0x80..0xBF itself, so window w
// holds the entries at w*64+0x80 .. w*64+0xBF.
func windowStart(w uint16) int { return int(w)*blockSize + firstContinue }

func (b *builder) remapExpansion(old uint32) (uint32, error) {
	if n, ok := b.expandOf[old]; ok {
		return n, nil
	}
	if int(old) >= len(b.src.expand) {
		return 0, fmt.Errorf("tablegen: expansion index %d beyond %d", old, len(b.src.expand))
	}
	count := int(b.src.expand[old])
	if int(old)+1+count > len(b.src.expand) {
		return 0, fmt.Errorf("tablegen: expansion at %d overruns the table", old)
	}
	n := uint32(len(b.expand))
	b.expand = append(b.expand, b.src.expand[old:int(old)+1+count]...)
	b.expandOf[old] = n
	return n, nil
}

// remapElem rewrites an element that holds an index (expansion or contraction) to the renumbered one.
func (b *builder) remapElem(ce uint32) (uint32, error) {
	switch {
	case ce <= maxNormal:
		return ce, nil
	case ce <= maxContract:
		return b.remapContraction(ce)
	case ce <= maxExpand:
		if ce&^0xFFFF != 0xE0000000 {
			return 0, fmt.Errorf("tablegen: expansion element %#x has bits above the index", ce)
		}
		n, err := b.remapExpansion(ce & 0xFFFF)
		if err != nil {
			return 0, err
		}
		if n > 0xFFFF {
			return 0, fmt.Errorf("tablegen: expansion index %d does not fit 16 bits", n)
		}
		return 0xE0000000 | n, nil
	}
	return ce, nil // decomposition: the tertiary weights only
}

// remapContraction copies the contraction trie states and contraction elements that a contraction starting element can reach. The states of a
// node are n entries; an entry that is not final names its child at an offset relative to the node, so the reachable states are
// copied as one range from the start of the node to the end of its deepest descendant, keeping every relative offset.
func (b *builder) remapContraction(ce uint32) (uint32, error) {
	if n, ok := b.contractOf[ce]; ok {
		return n, nil
	}
	n := int(ce & (1<<maxNBits - 1))
	index := int(ce >> maxNBits & (1<<maxIndexBits - 1))
	offset := int(ce >> (maxNBits + maxIndexBits) & (1<<maxOffsetBits - 1))
	end := index
	maxI := -1
	type node struct{ base, n int }
	todo := []node{{index, n}}
	for len(todo) > 0 {
		nd := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if nd.base+nd.n > len(b.src.ctEntries) {
			return 0, fmt.Errorf("tablegen: contraction node at %d with %d states overruns the table", nd.base, nd.n)
		}
		end = max(end, nd.base+nd.n)
		for _, e := range b.src.ctEntries[nd.base : nd.base+nd.n] {
			L, H, N, I := e[0], e[1], e[2], e[3]
			if I != noIndex {
				last := int(I)
				if N == 0 {
					last += int(H) - int(L) // a final entry covers the range L..H
				}
				maxI = max(maxI, last)
			}
			if N != 0 {
				todo = append(todo, node{nd.base + int(H) + nd.n, int(N)})
			}
		}
	}
	if maxI < 0 || offset+maxI >= len(b.src.contractElem) {
		return 0, fmt.Errorf("tablegen: contraction element %#x reaches elements %d..%d of %d", ce, offset, offset+maxI, len(b.src.contractElem))
	}
	newIndex := len(b.contractTab)
	for _, e := range b.src.ctEntries[index:end] {
		b.contractTab = append(b.contractTab, struct{ L, H, N, I uint8 }{e[0], e[1], e[2], e[3]})
	}
	newOffset := len(b.contractEl)
	for _, el := range b.src.contractElem[offset : offset+maxI+1] {
		if el > maxNormal && (el <= maxContract || el > maxExpand) {
			return 0, fmt.Errorf("tablegen: contraction element %#x is neither a weight nor an expansion", el)
		}
		r, err := b.remapElem(el)
		if err != nil {
			return 0, err
		}
		b.contractEl = append(b.contractEl, r)
	}
	if newIndex >= 1<<maxIndexBits || newOffset >= 1<<maxOffsetBits {
		return 0, fmt.Errorf("tablegen: renumbered contraction (%d, %d) does not fit its bits", newIndex, newOffset)
	}
	out := ce&^(1<<(maxNBits+maxIndexBits+maxOffsetBits)-1) | uint32(newOffset)<<(maxNBits+maxIndexBits) | uint32(newIndex)<<maxNBits | uint32(n)
	b.contractOf[ce] = out
	return out, nil
}

func (b *builder) valueWindowOf(old uint16) (uint16, error) {
	if w, ok := b.valueWindow[old]; ok {
		return w, nil
	}
	start := windowStart(old)
	if start+blockSize > len(b.src.values) {
		return 0, fmt.Errorf("tablegen: value window %d overruns mainValues", old)
	}
	var win [blockSize]uint32
	for i, ce := range b.src.values[start : start+blockSize] {
		r, err := b.remapElem(ce)
		if err != nil {
			return 0, err
		}
		win[i] = r
	}
	w, ok := b.valueOf[win]
	if !ok {
		if len(b.values) > 0xFFFF {
			return 0, fmt.Errorf("tablegen: more than 65536 value windows")
		}
		w = uint16(len(b.values))
		b.values = append(b.values, win)
		b.valueOf[win] = w
	}
	b.valueWindow[old] = w
	return w, nil
}

// indexWindowOf renumbers an index window; levels is how many more index windows lie below it (0: it holds value windows).
func (b *builder) indexWindowOf(old uint16, levels int) (uint16, error) {
	start := windowStart(old)
	if start+blockSize > len(b.src.lookup) {
		return 0, fmt.Errorf("tablegen: index window %d overruns mainLookup", old)
	}
	var win [blockSize]uint16
	for i, next := range b.src.lookup[start : start+blockSize] {
		var w uint16
		var err error
		if levels == 0 {
			w, err = b.valueWindowOf(next)
		} else {
			w, err = b.indexWindowOf(next, levels-1)
		}
		if err != nil {
			return 0, err
		}
		win[i] = w
	}
	w, ok := b.indexOf[win]
	if !ok {
		w = uint16(len(b.index))
		b.index = append(b.index, win)
		b.indexOf[win] = w
	}
	return w, nil
}

// Tables is the generated form of the root tables.
type Tables struct {
	// Source is tables.go: the declarations of the arrays and the size of Raw.
	Source []byte
	// Raw holds the contents of the arrays, in the order the declarations list them, each array as planes of bytes over all its entries, the most
	// significant plane first: the 56 lead windows and the 23*64 index entries as two planes of uint16, the 128 ASCII elements, the windows
	// of 64 elements, the expansion words and the contraction elements as four planes of uint32, the contraction trie nodes as four planes
	// (L, H, N, I). A plane holds the same byte of every entry, which compresses to a fifth of the entries stored whole. tables.bin is Raw
	// compressed with Compress.
	Raw []byte
}

// Compress returns the DEFLATE stream of raw that tables.bin holds.
func Compress(raw []byte) ([]byte, error) {
	var out bytes.Buffer
	w, err := flate.NewWriter(&out, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Generate derives the tables of package collate for the root locale from x/text's tables.go, tagged with version.
func Generate(tablesGo []byte, version string) (*Tables, error) {
	src, err := parseSource(tablesGo)
	if err != nil {
		return nil, err
	}
	b := &builder{
		src:         src,
		expandOf:    map[uint32]uint32{},
		contractOf:  map[uint32]uint32{},
		valueOf:     map[[blockSize]uint32]uint16{},
		valueWindow: map[uint16]uint16{},
		indexOf:     map[[blockSize]uint16]uint16{},
	}
	if len(src.values) < 128 {
		return nil, fmt.Errorf("tablegen: mainValues is too short")
	}
	ascii := make([]uint32, 128)
	for i := range ascii {
		if ascii[i], err = b.remapElem(src.values[i]); err != nil {
			return nil, err
		}
	}
	// The lead byte index is the root's own block of mainLookup. c0 in 0xC0..0xDF leads a two-byte sequence: its entry is a value window.
	// 0xE0..0xEF: an index window of value windows. 0xF0..0xF7: an index window of index windows of value windows. The lookup reads every
	// one of these entries for the bytes, valid UTF-8 or not, so the walk keeps them all.
	base := blockSize * rootLookupOffset
	if base+endLead > len(src.lookup) {
		return nil, fmt.Errorf("tablegen: the root index block overruns mainLookup")
	}
	lead := make([]uint16, endLead-firstLead)
	for c0 := firstLead; c0 < endLead; c0++ {
		old := src.lookup[base+c0]
		var w uint16
		switch {
		case c0 < 0xE0:
			w, err = b.valueWindowOf(old)
		case c0 < 0xF0:
			w, err = b.indexWindowOf(old, 0)
		default:
			w, err = b.indexWindowOf(old, 1)
		}
		if err != nil {
			return nil, fmt.Errorf("lead byte %#x: %w", c0, err)
		}
		lead[c0-firstLead] = w
	}

	var raw []byte
	raw = appendPlanes16(raw, lead)
	raw = appendPlanes16(raw, slices.Concat(sliceWindows(b.index)...))
	raw = appendPlanes32(raw, ascii)
	raw = appendPlanes32(raw, slices.Concat(sliceWindows(b.values)...))
	raw = appendPlanes32(raw, b.expand)
	raw = appendPlanes32(raw, b.contractEl)
	for plane := range 4 {
		for _, e := range b.contractTab {
			raw = append(raw, [4]uint8{e.L, e.H, e.N, e.I}[plane])
		}
	}

	var out bytes.Buffer
	fmt.Fprintf(&out, "// Code generated by gen.go from %s; DO NOT EDIT.\n\npackage collate\n\n", version)
	out.WriteString("// The arrays are zero until loadTables fills them from tables.bin (see tablegen.Tables.Raw for the layout, which it reads). A window of 64\n// entries is at the window number times 64.\n\n")
	out.WriteString("// leadWindows maps a lead byte 0xC0..0xF7 to a window: of values for a two-byte sequence, of index windows otherwise.\n")
	fmt.Fprintf(&out, "var leadWindows [%d]uint16\n\n", len(lead))
	out.WriteString("// indexWindows holds windows of 64 entries, indexed by a continuation byte less 0x80, that name the next window.\n")
	fmt.Fprintf(&out, "var indexWindows [%d * %d]uint16\n\n", len(b.index), blockSize)
	out.WriteString("// asciiValues are the collation elements of the 128 ASCII characters.\n")
	fmt.Fprintf(&out, "var asciiValues [%d]uint32\n\n", len(ascii))
	out.WriteString("// valueWindows holds windows of 64 collation elements, indexed by the last continuation byte less 0x80.\n")
	fmt.Fprintf(&out, "var valueWindows [%d * %d]uint32\n\n", len(b.values), blockSize)
	out.WriteString("// expansions holds, at an index an expansion element names, the count of elements followed by the elements.\n")
	fmt.Fprintf(&out, "var expansions [%d]uint32\n\n", len(b.expand))
	out.WriteString("// contractionElems holds the elements a contraction trie names, from the offset of the contraction element.\n")
	fmt.Fprintf(&out, "var contractionElems [%d]uint32\n\n", len(b.contractEl))
	out.WriteString("// contractionTries holds the nodes of the contraction tries (see ctScanner).\n")
	fmt.Fprintf(&out, "var contractionTries [%d]contractionState\n", len(b.contractTab))
	source, err := format.Source(out.Bytes())
	if err != nil {
		return nil, err
	}
	return &Tables{Source: source, Raw: raw}, nil
}

func appendPlanes16(raw []byte, vals []uint16) []byte {
	for shift := 8; shift >= 0; shift -= 8 {
		for _, v := range vals {
			raw = append(raw, byte(v>>shift))
		}
	}
	return raw
}

func appendPlanes32(raw []byte, vals []uint32) []byte {
	for shift := 24; shift >= 0; shift -= 8 {
		for _, v := range vals {
			raw = append(raw, byte(v>>shift))
		}
	}
	return raw
}

func sliceWindows[T uint16 | uint32](windows [][blockSize]T) [][]T {
	out := make([][]T, len(windows))
	for i := range windows {
		out[i] = windows[i][:]
	}
	return out
}
