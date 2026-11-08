// Package collate orders strings as ICU's root collation does, with the tables and the algorithm of golang.org/x/text/collate v0.43.0 (CLDR 23,
// Unicode 6.2) for the root locale and the default options: tertiary strength, non-ignorable variable elements, no numeric ordering. It is a
// fork of x/text's collate and internal/colltab that keeps only that configuration and the root tables (see FORK.md); for every string
// it returns what collate.New(language.Und) returns.
package collate

import "unicode"

// Elem is a collation element, as colltab.Elem is in x/text.
type Elem uint32

const (
	maxCE       Elem = 0xAFFFFFFF
	minContract      = 0xC0000000
	maxContract      = 0xDFFFFFFF
	minExpand        = 0xE0000000
	maxExpand        = 0xEFFFFFFF
	minDecomp        = 0xF0000000
)

type ceType int

const (
	ceNormal           ceType = iota // ceNormal includes implicits (ce == 0)
	ceContractionIndex               // rune can be a start of a contraction
	ceExpansionIndex                 // rune expands into a sequence of collation elements
	ceDecompose                      // rune expands using NFKC decomposition
)

func (ce Elem) ctype() ceType {
	if ce <= maxCE {
		return ceNormal
	}
	if ce <= maxContract {
		return ceContractionIndex
	}
	if ce <= maxExpand {
		return ceExpansionIndex
	}
	return ceDecompose
}

// For normal collation elements, we assume that a collation element either has
// a primary or non-default secondary value, not both.
// Collation elements with a primary value are of the form
//
//	01pppppp pppppppp ppppppp0 ssssssss
//	  - p* is primary collation value
//	  - s* is the secondary collation value
//	00pppppp pppppppp ppppppps sssttttt, where
//	  - p* is primary collation value
//	  - s* offset of secondary from default value.
//	  - t* is the tertiary collation value
//	100ttttt cccccccc pppppppp pppppppp
//	  - t* is the tertiar collation value
//	  - c* is the canonical combining class
//	  - p* is the primary collation value
//
// Collation elements with a secondary value are of the form
//
//	1010cccc ccccssss ssssssss tttttttt, where
//	  - c* is the canonical combining class
//	  - s* is the secondary collation value
//	  - t* is the tertiary collation value
const (
	ceTypeMask         = 0xC0000000
	ceTypeMaskExt      = 0xE0000000
	ceType1            = 0x40000000
	ceType2            = 0x00000000
	ceType3or4         = 0x80000000
	ceType4            = 0xA0000000
	firstNonPrimary    = 0x80000000
	lastSpecialPrimary = 0xA0000000
	hasTertiaryMask    = 0x40000000
	primaryValueMask   = 0x3FFFFE00

	primaryShift          = 9
	compactSecondaryShift = 5
	minCompactSecondary   = defaultSecondary - 4

	defaultSecondary = 0x20
	defaultTertiary  = 0x2
	maxTertiary      = 0x1F
)

func makeImplicitCE(primary int) Elem {
	return ceType1 | Elem(primary<<primaryShift) | defaultSecondary
}

// ccc returns the canonical combining class associated with the underlying character, if applicable, or 0 otherwise.
func (ce Elem) ccc() uint8 {
	if ce&ceType3or4 != 0 {
		if ce&ceType4 == ceType3or4 {
			return uint8(ce >> 16)
		}
		return uint8(ce >> 20)
	}
	return 0
}

// primary returns the primary collation weight for ce.
func (ce Elem) primary() int {
	if ce >= firstNonPrimary {
		if ce > lastSpecialPrimary {
			return 0
		}
		return int(uint16(ce))
	}
	return int(ce&primaryValueMask) >> primaryShift
}

// secondary returns the secondary collation weight for ce.
func (ce Elem) secondary() int {
	switch ce & ceTypeMask {
	case ceType1:
		return int(uint8(ce))
	case ceType2:
		return minCompactSecondary + int((ce>>compactSecondaryShift)&0xF)
	case ceType3or4:
		if ce < ceType4 {
			return defaultSecondary
		}
		return int(ce>>8) & 0xFFF
	}
	return 0 // a quaternary value
}

// tertiary returns the tertiary collation weight for ce.
func (ce Elem) tertiary() uint8 {
	if ce&hasTertiaryMask == 0 {
		if ce&ceType3or4 == 0 {
			return uint8(ce & 0x1F)
		}
		if ce&ceType4 == ceType4 {
			return uint8(ce)
		}
		return uint8(ce>>24) & 0x1F // type 2
	} else if ce&ceTypeMask == ceType1 {
		return defaultTertiary
	}
	// ce is a quaternary value.
	return 0
}

func (ce Elem) updateTertiary(t uint8) Elem {
	if ce&ceTypeMask == ceType1 {
		// convert to type 4
		nce := ce & primaryValueMask
		nce |= Elem(uint8(ce)-minCompactSecondary) << compactSecondaryShift
		ce = nce
	} else if ce&ceTypeMaskExt == ceType3or4 {
		ce &= ^Elem(maxTertiary << 24)
		return ce | (Elem(t) << 24)
	} else {
		// type 2 or 4
		ce &= ^Elem(maxTertiary)
	}
	return ce | Elem(t)
}

// For contractions, collation elements are of the form
// 110bbbbb bbbbbbbb iiiiiiii iiiinnnn, where
//   - n* is the size of the first node in the contraction trie.
//   - i* is the index of the first node in the contraction trie.
//   - b* is the offset into the contraction collation element table.
const (
	maxNBits         = 4
	maxTrieIndexBits = 12
)

func splitContractIndex(ce Elem) (index, n, offset int) {
	n = int(ce & (1<<maxNBits - 1))
	ce >>= maxNBits
	index = int(ce & (1<<maxTrieIndexBits - 1))
	ce >>= maxTrieIndexBits
	offset = int(ce & (1<<13 - 1))
	return
}

// For expansions, Elems are of the form 11100000 00000000 bbbbbbbb bbbbbbbb, where b* is the index into the expansion sequence table.
func splitExpandIndex(ce Elem) (index int) {
	return int(uint16(ce))
}

// Some runes can be expanded using NFKD decomposition. Instead of storing the full sequence of collation elements, we decompose the rune and
// lookup the collation elements for each rune in the decomposition and modify the tertiary weights. The Elem, in this case, is of the form
// 11110000 00000000 wwwwwwww vvvvvvvv, where
//   - v* is the replacement tertiary weight for the first rune,
//   - w* is the replacement tertiary weight for the second rune,
//
// Tertiary weights of subsequent runes should be replaced with maxTertiary.
func splitDecompose(ce Elem) (t1, t2 uint8) {
	return uint8(ce), uint8(ce >> 8)
}

const (
	minUnified       rune = 0x4E00
	maxUnified            = 0x9FFF
	minCompatibility      = 0xF900
	maxCompatibility      = 0xFAFF
)

const (
	commonUnifiedOffset = 0x10000
	rareUnifiedOffset   = 0x20000 // largest rune in common is U+FAFF
	otherOffset         = 0x50000 // largest rune in rare is U+2FA1D
)

// implicitPrimary returns the primary weight for the given rune for which there is no entry for the rune in the collation table. It keeps the
// relative ordering of the runes the Unicode implicit weights give.
func implicitPrimary(r rune) int {
	if unicode.Is(unicode.Ideographic, r) {
		if r >= minUnified && r <= maxUnified {
			// The most common case for CJK.
			return int(r) + commonUnifiedOffset
		}
		if r >= minCompatibility && r <= maxCompatibility {
			// This will typically not hit. The DUCET explicitly specifies mappings
			// for all characters that do not decompose.
			return int(r) + commonUnifiedOffset
		}
		return int(r) + rareUnifiedOffset
	}
	return int(r) + otherOffset
}
