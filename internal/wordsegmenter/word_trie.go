//go:build !pig_strip_word_dictionaries

package wordsegmenter

// wordTrie borrows one embedded dictionary trie in place (dictionary_format.py writes it and documents the record layout; README: Regeneration). Lookup walks one label per step through small records of the embedded string. It decodes nothing, allocates nothing and retains nothing.
type wordTrie struct {
	data     string
	width    int // bytes per label: 1 for the Southeast Asian byte alphabets, 2 for CJK UTF-16 units
	weighted bool
}

// trieNode is one parsed record.
type trieNode struct {
	terminal                bool
	weight                  uint32
	skipLen, leaves, inner  int
	skipAt, leafAt, innerAt int
}

// trieCursor is a position in the trie: a record, and how many of its skip labels were already matched.
type trieCursor struct {
	node trieNode
	skip int
}

// trieStep describes the position a successful step reached.
type trieStep struct {
	weight   uint32 // the word's weight, when terminal
	terminal bool   // a word ends here
	more     bool   // a longer word may follow
}

const triePointer = 3

func (t wordTrie) root() trieCursor { return trieCursor{node: t.node(0)} }

func (t wordTrie) label(at int) int {
	if t.width == 1 {
		return int(t.data[at])
	}
	return int(t.data[at]) | int(t.data[at+1])<<8
}

func (t wordTrie) leb(at int) (value, next int) {
	for shift := 0; ; shift += 7 {
		b := t.data[at]
		at++
		value |= int(b&0x7f) << shift
		if b < 0x80 {
			return value, at
		}
	}
}

func (t wordTrie) node(at int) trieNode {
	flags := t.data[at]
	at++
	n := trieNode{terminal: flags&0x80 != 0, leaves: int(flags >> 1 & 7), inner: int(flags >> 4 & 7)}
	if n.terminal && t.weighted {
		n.weight = uint32(t.data[at])
		at++
	}
	if flags&1 != 0 {
		n.skipLen = int(t.data[at])
		at++
		n.skipAt = at
		at += n.skipLen * t.width
	}
	if n.leaves == 7 {
		n.leaves, at = t.leb(at)
	}
	if n.inner == 7 {
		n.inner, at = t.leb(at)
	}
	n.leafAt = at
	n.innerAt = at + n.leaves*t.leafStride()
	return n
}

func (t wordTrie) leafStride() int {
	if t.weighted {
		return t.width + 1
	}
	return t.width
}

// find returns the record index of label among count sorted fixed-size records at at, or -1.
func (t wordTrie) find(at, count, stride, label int) int {
	low, high := 0, count
	for low < high {
		mid := int(uint(low+high) >> 1)
		if t.label(at+mid*stride) < label {
			low = mid + 1
		} else {
			high = mid
		}
	}
	if low < count && t.label(at+low*stride) == label {
		return low
	}
	return -1
}

// step consumes one label. It reports false when no word continues with the label. Otherwise the cursor has advanced: a leaf edge ends its word with no longer word (more is false, and the cursor must not be stepped again), and an inner edge reaches a record whose terminal flag and weight describe a word ending here.
func (t wordTrie) step(c *trieCursor, label int) (trieStep, bool) {
	n := &c.node
	if c.skip < n.skipLen {
		if t.label(n.skipAt+c.skip*t.width) != label {
			return trieStep{}, false
		}
		c.skip++
		return trieStep{more: true}, true
	}
	if i := t.find(n.leafAt, n.leaves, t.leafStride(), label); i >= 0 {
		step := trieStep{terminal: true}
		if t.weighted {
			step.weight = uint32(t.data[n.leafAt+i*t.leafStride()+t.width])
		}
		return step, true
	}
	stride := t.width + triePointer
	i := t.find(n.innerAt, n.inner, stride, label)
	if i < 0 {
		return trieStep{}, false
	}
	at := n.innerAt + i*stride + t.width
	c.node = t.node(int(t.data[at]) | int(t.data[at+1])<<8 | int(t.data[at+2])<<16)
	c.skip = 0
	if c.node.skipLen > 0 {
		return trieStep{more: true}, true
	}
	return trieStep{terminal: c.node.terminal, weight: c.node.weight, more: true}, true
}
