// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package abi

import (
	"encoding/binary"
	"math"
)

// The body recipe of a model_context effect (ABI section 10.2): the request's JSON text as the segments it is made of,
//
//	recipe  := u32 nSegments, segment*
//	segment := u8 0, u32 len, bytes                 text the core built
//	         | u8 1, u32 conv, u32 head, u32 tail    the model messages of a committed context, as JSON text
//
// so the core does not copy the transcript into the effect: the host derives the context the range names from storage and
// concatenates. A range segment stands for the messages of conversation conv's context cut off at entry tail under the
// head marker entry head (0: no head marker), as comma-separated JSON values: the messages spec section 2.1 derives from the
// entries in [head, tail] visible from conv. The expansion is exactly the JSON text of the ModelContextEffect.

// MaxRecipeID is the largest conversation or entry id a range can name; a request whose ids exceed it carries its messages
// as text.
const MaxRecipeID = math.MaxUint32

// Recipe builds a recipe. Adjacent text segments merge into one.
type Recipe struct {
	b    []byte
	n    uint32
	open int // offset of the open text segment's length field, or -1
}

// NewRecipe starts a recipe in buf's storage (buf is reset).
func NewRecipe(buf []byte) *Recipe {
	return &Recipe{b: append(buf[:0], 0, 0, 0, 0), open: -1}
}

// Text returns the buffer to append the next text to; the caller passes the grown buffer back to SetText.
func (r *Recipe) Text() []byte {
	if r.open < 0 {
		r.b = append(r.b, 0)
		r.open = len(r.b)
		r.b = append(r.b, 0, 0, 0, 0)
		r.n++
	}
	return r.b
}

// SetText stores the buffer Text returned after the caller appended to it.
func (r *Recipe) SetText(b []byte) { r.b = b }

// Range appends a range segment: the model messages of conversation conv's context cut off at entry tail under head marker
// head (0: none).
func (r *Recipe) Range(conv, head, tail int64) {
	r.closeText()
	r.b = append(r.b, 1)
	r.b = binary.LittleEndian.AppendUint32(r.b, uint32(conv))
	r.b = binary.LittleEndian.AppendUint32(r.b, uint32(head))
	r.b = binary.LittleEndian.AppendUint32(r.b, uint32(tail))
	r.n++
}

func (r *Recipe) closeText() {
	if r.open >= 0 {
		binary.LittleEndian.PutUint32(r.b[r.open:], uint32(len(r.b)-r.open-4))
		r.open = -1
	}
}

// Bytes finishes the recipe and returns it.
func (r *Recipe) Bytes() []byte {
	r.closeText()
	binary.LittleEndian.PutUint32(r.b, r.n)
	return r.b
}

type recipeError string

func (e recipeError) Error() string { return string(e) }

const (
	// ErrRecipe reports a recipe that is truncated, has an unknown segment kind or trailing bytes, or a range segment
	// where the host cannot derive one.
	ErrRecipe = recipeError("abi: malformed recipe")
	// ErrRecipeRecord reports a range the host's storage does not agree with: a head marker, tail or conversation it lacks.
	ErrRecipeRecord = recipeError("abi: recipe names a context storage does not have")
)

// RangeLoader returns the comma-separated JSON text of the model messages of conversation conv's committed context cut
// off at entry tail under head marker entry head (0: none).
type RangeLoader func(conv, head, tail uint32) ([]byte, error)

// Expand returns the text the recipe stands for. rng derives a range segment; it may be nil for a recipe without one.
func Expand(recipe []byte, rng RangeLoader) ([]byte, error) {
	n, rest, ok := u32(recipe)
	if !ok {
		return nil, ErrRecipe
	}
	var out []byte
	for range n {
		if len(rest) < 1 {
			return nil, ErrRecipe
		}
		kind := rest[0]
		rest = rest[1:]
		switch kind {
		case 0:
			l, r2, ok := u32(rest)
			if !ok || uint64(l) > uint64(len(r2)) {
				return nil, ErrRecipe
			}
			out = append(out, r2[:l]...)
			rest = r2[l:]
		case 1:
			if len(rest) < 12 || rng == nil {
				return nil, ErrRecipe
			}
			text, err := rng(binary.LittleEndian.Uint32(rest), binary.LittleEndian.Uint32(rest[4:]), binary.LittleEndian.Uint32(rest[8:]))
			if err != nil {
				return nil, err
			}
			out = append(out, text...)
			rest = rest[12:]
		default:
			return nil, ErrRecipe
		}
	}
	if len(rest) != 0 {
		return nil, ErrRecipe
	}
	return out, nil
}

func u32(b []byte) (uint32, []byte, bool) {
	if len(b) < 4 {
		return 0, nil, false
	}
	return binary.LittleEndian.Uint32(b), b[4:], true
}
