//go:generate go run gen.go

package collate

// Collator compares strings for the root collation order. Like x/text's it keeps scratch buffers, so one Collator must not be shared by goroutines.
type Collator struct {
	iter [2]iter
}

// New returns a Collator for the root locale.
func New() *Collator {
	c := &Collator{}
	c.iter[0].Elems = c.iter[0].wa[:0]
	c.iter[1].Elems = c.iter[1].wa[:0]
	return c
}

// CompareString returns an integer comparing the two strings. The result is 0 if a and b have the same weights at the primary, secondary and
// tertiary levels, -1 if a sorts before b and +1 if a sorts after b.
func (c *Collator) CompareString(a, b string) int {
	ia, ib := &c.iter[0], &c.iter[1]
	ia.setInputString(a)
	ib.setInputString(b)
	if res := compareLevel((*iter).nextPrimary, ia, ib); res != 0 {
		return res
	}
	if res := compareLevel((*iter).nextSecondary, ia, ib); res != 0 {
		return res
	}
	return compareLevel((*iter).nextTertiary, ia, ib)
}

func compareLevel(f func(i *iter) int, a, b *iter) int {
	a.pce = 0
	b.pce = 0
	for {
		va := f(a)
		vb := f(b)
		if va != vb {
			if va < vb {
				return -1
			}
			return 1
		} else if va == 0 {
			break
		}
	}
	return 0
}

// AppendKey appends the collation key of s to dst and returns the extended slice. Keys compare with bytes.Compare as the strings compare with
// CompareString, except that CompareString does not tell apart strings whose keys are equal.
func (c *Collator) AppendKey(dst []byte, s string) []byte {
	i := &c.iter[0]
	i.setInputString(s)
	for i.next() {
	}
	ws := i.Elems
	for _, v := range ws {
		if w := v.primary(); w > 0 {
			dst = appendPrimary(dst, w)
		}
	}
	dst = append(dst, 0, 0)
	for _, v := range ws {
		if w := v.secondary(); w > 0 {
			dst = append(dst, uint8(w>>8), uint8(w))
		}
	}
	dst = append(dst, 0, 0)
	for _, v := range ws {
		if w := v.tertiary(); w > 0 {
			dst = append(dst, uint8(w))
		}
	}
	return dst
}

func appendPrimary(key []byte, p int) []byte {
	// Convert to variable length encoding; supports up to 23 bits.
	if p <= 0x7FFF {
		key = append(key, uint8(p>>8), uint8(p))
	} else {
		key = append(key, uint8(p>>16)|0x80, uint8(p>>8), uint8(p))
	}
	return key
}

// An iter incrementally converts chunks of the input text to collation elements, while ensuring that the collation elements are in normalized
// order (that is, they are in the order as if the input text were normalized first).
type iter struct {
	wa [512]Elem

	Elems []Elem
	// N is the number of elements in Elems that will not be reordered on subsequent iterations, N <= len(Elems).
	N int

	str string
	// Because the Elems buffer may contain collation elements that are needed for look-ahead, we need two positions in the text: one for the end
	// position in the text for the current iteration and one for the start of the next call to appendNext.
	pEnd  int // end position in text corresponding to N.
	pNext int // pEnd <= pNext.

	pce int
}

func (i *iter) setInputString(s string) {
	i.str = s
	i.Elems = i.Elems[:0]
	i.N = 0
	i.pEnd = 0
	i.pNext = 0
}

func (i *iter) done() bool {
	return i.pNext >= len(i.str)
}

func (i *iter) appendNext() bool {
	if i.done() {
		return false
	}
	var sz int
	i.Elems, sz = appendNext(i.Elems, i.str[i.pNext:])
	if sz == 0 {
		sz = 1
	}
	i.pNext += sz
	return true
}

// next appends Elems to the internal array. On each iteration, it will either add starters or modifiers. In the majority of cases, an Elem
// with a primary value > 0 will have a CCC of 0. The CCC values of collation elements are also used to detect if the input string was not
// normalized and to adjust the result accordingly.
func (i *iter) next() bool {
	if i.N == len(i.Elems) && !i.appendNext() {
		return false
	}

	// Check if the current segment starts with a starter.
	prevCCC := i.Elems[len(i.Elems)-1].ccc()
	if prevCCC == 0 {
		i.N = len(i.Elems)
		i.pEnd = i.pNext
		return true
	} else if i.Elems[i.N].ccc() == 0 {
		// set i.N to only cover part of i.Elems for which prevCCC == 0 and
		// use rest for the next call to next.
		for i.N++; i.N < len(i.Elems) && i.Elems[i.N].ccc() == 0; i.N++ {
		}
		i.pEnd = i.pNext
		return true
	}

	// The current (partial) segment starts with modifiers. We need to collect
	// all successive modifiers to ensure that they are normalized.
	for {
		p := len(i.Elems)
		i.pEnd = i.pNext
		if !i.appendNext() {
			break
		}

		if ccc := i.Elems[p].ccc(); ccc == 0 || len(i.Elems)-i.N > maxCombiningCharacters {
			// Leave the starter for the next iteration. This ensures that we
			// do not return sequences of collation elements that cross two
			// segments.
			i.N = p
			return true
		} else if ccc < prevCCC {
			i.doNorm(p, ccc) // should be rare, never occurs for NFD and FCC.
		} else {
			prevCCC = ccc
		}
	}

	done := len(i.Elems) != i.N
	i.N = len(i.Elems)
	return done
}

const maxCombiningCharacters = 30

// doNorm reorders the collation elements in i.Elems. It assumes that blocks of collation elements added with appendNext either start and end
// with the same CCC or start with CCC == 0. This allows for a single insertion point for the entire block.
func (i *iter) doNorm(p int, ccc uint8) {
	n := len(i.Elems)
	k := p
	for p--; p > i.N && ccc < i.Elems[p-1].ccc(); p-- {
	}
	i.Elems = append(i.Elems, i.Elems[p:k]...)
	copy(i.Elems[p:], i.Elems[k:])
	i.Elems = i.Elems[:n]
}

func (i *iter) nextPrimary() int {
	for {
		for ; i.pce < i.N; i.pce++ {
			if v := i.Elems[i.pce].primary(); v != 0 {
				i.pce++
				return v
			}
		}
		if !i.next() {
			return 0
		}
	}
}

func (i *iter) nextSecondary() int {
	for ; i.pce < len(i.Elems); i.pce++ {
		if v := i.Elems[i.pce].secondary(); v != 0 {
			i.pce++
			return v
		}
	}
	return 0
}

func (i *iter) nextTertiary() int {
	for ; i.pce < len(i.Elems); i.pce++ {
		if v := i.Elems[i.pce].tertiary(); v != 0 {
			i.pce++
			return int(v)
		}
	}
	return 0
}
