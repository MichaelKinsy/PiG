// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package gridterm

import (
	"slices"
	"strings"
)

// Pair is, for every Model, a terminal that receives a stream as the renderers wrote it and one that receives what a writer under the
// terminal made of it. There is no golden screen: the stream as written is the reference on the same terminal.
type Pair struct {
	reference, translated []*Terminal
	// Steps and Differences count the calls of Step and those after which a model's two screens differed.
	Steps, Differences int
}

// NewPair returns a pair of empty screens of cols by rows cells for every model.
func NewPair(cols, rows int) *Pair {
	p := &Pair{}
	for _, model := range Models {
		p.reference = append(p.reference, New(cols, rows, model))
		p.translated = append(p.translated, New(cols, rows, model))
	}
	return p
}

// Resize is the terminal changing size.
func (p *Pair) Resize(cols, rows int) {
	for i := range p.reference {
		p.reference[i].Resize(cols, rows)
		p.translated[i].Resize(cols, rows)
	}
}

// Step feeds in to the reference screens and out to the others and returns how the first model's screens that differ do, "" when all
// are equal. After a difference the screens that differ carry on from the reference, so that one difference is reported once.
func (p *Pair) Step(in, out string) string {
	p.Steps++
	first := ""
	for i, reference := range p.reference {
		reference.Write(in)
		p.translated[i].Write(out)
		if d := Differ(reference, p.translated[i]); d != "" {
			if first == "" {
				first = "widths " + Models[i].Name + ": " + d
			}
			p.translated[i] = reference.clone()
		}
	}
	if first != "" {
		p.Differences++
	}
	return first
}

// Reference is the first model's screen of the stream as written.
func (p *Pair) Reference() *Terminal { return p.reference[0] }

func (v *Terminal) clone() *Terminal {
	c := *v
	c.last = nil
	c.grid = make([][]cell, len(v.grid))
	for r := range v.grid {
		c.grid[r] = slices.Clone(v.grid[r])
	}
	c.unknown = map[string]int{}
	return &c
}

const (
	syncBegin = "\x1b[?2026h"
	syncEnd   = "\x1b[?2026l"
)

// SplitWrites cuts a recorded stream into the writes a terminal took it in: each synchronized update, and what lies between two.
func SplitWrites(stream string) []string {
	var writes []string
	for stream != "" {
		begin := strings.Index(stream, syncBegin)
		if begin < 0 {
			return append(writes, stream)
		}
		if begin > 0 {
			writes = append(writes, stream[:begin])
			stream = stream[begin:]
		}
		end := strings.Index(stream, syncEnd)
		if end < 0 {
			return append(writes, stream)
		}
		writes = append(writes, stream[:end+len(syncEnd)])
		stream = stream[end+len(syncEnd):]
	}
	return writes
}
