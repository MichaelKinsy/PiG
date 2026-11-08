// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"slices"
	"strings"
)

// `sequenceDiagram`: participants, messages, notes and block dividers. Ported
// from lovely-mermaid diagrams/sequence.ts. Lenient: an unreadable statement is
// dropped and recorded in warnings.
//
// Sequence diagrams have their own model, participants in declaration order
// plus a flat list of items, and their own geometry in layout_seq.go.

var sequenceHeaders = []string{"sequencediagram"}

var sequenceDiagram = diagram{
	kind:    "sequence",
	headers: sequenceHeaders,
	render: func(src string) (*canvas, drawnMeta, bool) {
		seq := parseSequence(src)
		if seq == nil {
			return nil, drawnMeta{}, false
		}
		c := layoutSequence(seq)
		if c == nil {
			return nil, drawnMeta{}, false
		}
		return c, drawnMeta{warnings: seq.warnings, classDefs: map[string]map[string]string{}}, true
	},
}

type seqHead string

const (
	seqArrow seqHead = "arrow"
	seqCross seqHead = "cross"
)

type seqOp struct {
	op     string
	dashed bool
	head   seqHead
}

// seqOps are the message operators, longest-first so `-->>` wins over `-->`.
var seqOps = []seqOp{
	{"-->>", true, seqArrow},
	{"->>", false, seqArrow},
	{"--x", true, seqCross},
	{"-x", false, seqCross},
	{"--)", true, seqArrow},
	{"-)", false, seqArrow},
	{"-->", true, seqArrow},
	{"->", false, seqArrow},
}

const maxSeqOp = 4

type noteKind string

const (
	noteOver  noteKind = "over"
	noteLeft  noteKind = "left"
	noteRight noteKind = "right"
)

type noteAnchor struct {
	kind noteKind
	from int // over
	to   int // over
	at   int // left/right
}

type seqItemKind string

const (
	seqMessage seqItemKind = "message"
	seqNote    seqItemKind = "note"
	seqDivider seqItemKind = "divider"
)

type seqItem struct {
	kind seqItemKind
	// message
	from   int
	to     int
	text   *string
	dashed bool
	head   seqHead
	// note
	anchor noteAnchor
	// note / divider text
	str string
}

// activation is a hot span of one lifeline: item indices, to == activationOpen
// while still open.
type activation struct {
	at, from, to int
}

const activationOpen = -1

type sequence struct {
	labels      []string
	index       map[string]int
	items       []seqItem
	activations []activation
	// truncated is set when a size cap was hit; the parser stops and renders the prefix.
	truncated string
	// warnings lists statements the grammar could not read and dropped.
	warnings []string
}

func newSequence() *sequence {
	return &sequence{index: map[string]int{}}
}

func (s *sequence) participant(id string, label *string) (int, bool) {
	if existing, ok := s.index[id]; ok {
		if label != nil {
			s.labels[existing] = *label
		}
		return existing, true
	}
	if len(s.labels) >= maxNodes {
		if s.truncated == "" {
			s.truncated = "participant cap (" + itoa(maxNodes) + ") reached"
		}
		return 0, false
	}
	s.index[id] = len(s.labels)
	lbl := id
	if label != nil {
		lbl = *label
	}
	s.labels = append(s.labels, lbl)
	return len(s.labels) - 1, true
}

// pushItem appends an item, or flags truncated when the item cap is reached.
func (s *sequence) pushItem(item seqItem) {
	if len(s.items) >= maxEdges {
		if s.truncated == "" {
			s.truncated = "item cap (" + itoa(maxEdges) + ") reached"
		}
	} else {
		s.items = append(s.items, item)
	}
}

// drop records an unreadable statement; skipped once truncated.
func (s *sequence) drop(st string) {
	if s.truncated == "" {
		s.warnings = append(s.warnings, "dropped, unreadable statement: \""+st+"\"")
	}
}

// activate opens an activation at item from.
func (s *sequence) activate(at, from int) {
	s.activations = append(s.activations, activation{at: at, from: from, to: activationOpen})
}

// deactivate closes the innermost open activation of at at item to.
func (s *sequence) deactivate(at, to int) {
	for i := len(s.activations) - 1; i >= 0; i-- {
		if a := &s.activations[i]; a.at == at && a.to == activationOpen {
			a.to = to
			return
		}
	}
}

func parseSequence(src string) *sequence {
	statements := statementsOf(src)
	kind := headerKind(statements)
	if kind == "" || !hasHeader(sequenceHeaders, kind) {
		return nil
	}

	seq := newSequence()
	autonumber := false
	msgCount := 0
	// One entry per open block; true when it draws a divider on `end`.
	var blocks []bool

	for _, st := range statements[1:] {
		first := firstWord(st)
		lower := asciiLower(first)

		switch {
		case lower == "participant" || lower == "actor":
			rest := trimJS(st[len(first):])
			if rest == "" {
				seq.drop(st)
			} else if id, label, ok := splitOnce(rest, " as "); ok {
				l := cleanLabel(label)
				seq.participant(trimJS(id), &l)
			} else {
				seq.participant(rest, nil)
			}
		case lower == "autonumber":
			autonumber = true
		case lower == "activate" || lower == "deactivate":
			// Applies to the preceding message's row.
			if who, ok := seq.index[trimJS(st[len(first):])]; ok && len(seq.items) > 0 {
				if lower == "activate" {
					seq.activate(who, len(seq.items)-1)
				} else {
					seq.deactivate(who, len(seq.items)-1)
				}
			}
		case isOneOf(lower, "create", "destroy", "title", "acctitle", "accdescr", "links", "link", "properties"):
			// No layout meaning.
		case lower == "note":
			if text, anchor, ok := parseNoteAnchor(trimJS(st[len(first):]), seq); !ok {
				seq.drop(st)
			} else {
				seq.pushItem(seqItem{kind: seqNote, anchor: anchor, str: text})
			}
		case isOneOf(lower, "loop", "alt", "opt", "par", "critical", "break", "else", "and", "option"):
			// A continuation only divides a block that opened one.
			continues := isOneOf(lower, "else", "and", "option")
			if !continues {
				blocks = append(blocks, true)
			}
			if !continues || (len(blocks) > 0 && blocks[len(blocks)-1]) {
				seq.pushItem(seqItem{kind: seqDivider, str: decodeHtmlEntities(st)})
			}
		case lower == "rect" || lower == "box":
			blocks = append(blocks, false)
		case lower == "end":
			if len(blocks) > 0 {
				divides := blocks[len(blocks)-1]
				blocks = blocks[:len(blocks)-1]
				if divides {
					seq.pushItem(seqItem{kind: seqDivider, str: "end"})
				}
			}
		default:
			msg := parseSeqMessage(st, seq)
			if msg == nil {
				seq.drop(st)
				break
			}
			text := msg.text
			if autonumber {
				msgCount++
				t := itoa(msgCount) + "."
				if text != nil {
					t += " " + *text
				}
				text = &t
			}
			item := len(seq.items)
			seq.pushItem(seqItem{kind: seqMessage, from: msg.from, to: msg.to, text: text, dashed: msg.dashed, head: msg.head})
			// `+` activates the receiver on this row; `-` deactivates the sender.
			// Only once the item was accepted: an activation pointing past the
			// item cap would dereference a message that does not exist.
			if len(seq.items) == item+1 {
				if strings.Contains(msg.marks, "+") {
					seq.activate(msg.to, item)
				}
				if strings.Contains(msg.marks, "-") {
					seq.deactivate(msg.from, item)
				}
			}
		}
		if seq.truncated != "" {
			seq.warnings = append(seq.warnings, "diagram truncated: "+seq.truncated)
			break
		}
	}

	if len(seq.labels) == 0 {
		return nil
	}
	return seq
}

func isOneOf(s string, options ...string) bool {
	return slices.Contains(options, s)
}

func parseNoteAnchor(rest string, seq *sequence) (text string, anchor noteAnchor, ok bool) {
	lower := asciiLower(rest)
	var kind noteKind
	var idsAndText string
	switch {
	case strings.HasPrefix(lower, "over "):
		kind = noteOver
		idsAndText = rest[len("over "):]
	case strings.HasPrefix(lower, "left of "):
		kind = noteLeft
		idsAndText = rest[len("left of "):]
	case strings.HasPrefix(lower, "right of "):
		kind = noteRight
		idsAndText = rest[len("right of "):]
	default:
		return "", noteAnchor{}, false
	}

	pre, post, split := splitOnce(idsAndText, ":")
	if !split {
		return "", noteAnchor{}, false
	}
	text = decodeHtmlEntities(trimJS(post))
	parts := splitCommaList(pre)
	if len(parts) == 0 {
		return "", noteAnchor{}, false
	}
	a, ok := seq.participant(parts[0], nil)
	if !ok {
		return "", noteAnchor{}, false
	}

	if kind != noteOver {
		return text, noteAnchor{kind: kind, at: a}, true
	}
	b := a
	if len(parts) > 1 {
		second, ok := seq.participant(parts[1], nil)
		if !ok {
			return "", noteAnchor{}, false
		}
		b = second
	}
	return text, noteAnchor{kind: noteOver, from: min(a, b), to: max(a, b)}, true
}

type parsedSeqMessage struct {
	from   int
	to     int
	text   *string
	dashed bool
	head   seqHead
	marks  string
}

func parseSeqMessage(st string, seq *sequence) *parsedSeqMessage {
	chars := []rune(st)
	var found *seqOp
	var foundPos int
outer:
	for pos := range chars {
		tail := string(chars[pos:min(pos+maxSeqOp, len(chars))])
		for i := range seqOps {
			op := &seqOps[i]
			if !strings.HasPrefix(tail, op.op) {
				continue
			}
			// `-x` / `-)` embedded in a hyphenated token (`pre-x->>B`) is not an
			// operator: the real one follows. Require a non-link char after.
			after := charAt(chars, pos+len(op.op))
			if (strings.HasSuffix(op.op, "x") || strings.HasSuffix(op.op, ")")) && (after == '-' || after == '>') {
				continue
			}
			found, foundPos = op, pos
			break outer
		}
	}
	if found == nil {
		return nil
	}

	fromID := trimJS(string(chars[:foundPos]))
	if fromID == "" {
		return nil
	}
	// `+` activates the receiver, `-` deactivates the sender.
	afterOp := trimStartJS(string(chars[foundPos+len(found.op):]))
	rest := strings.TrimLeft(afterOp, "+-")
	marks := afterOp[:len(afterOp)-len(rest)]

	toID := rest
	var text *string
	if pre, post, ok := splitOnce(rest, ":"); ok {
		toID = pre
		text = nonEmpty(decodeHtmlEntities(trimJS(post)))
	}
	toID = trimJS(toID)
	if toID == "" {
		return nil
	}

	from, ok := seq.participant(fromID, nil)
	if !ok {
		return nil
	}
	to, ok := seq.participant(toID, nil)
	if !ok {
		return nil
	}
	return &parsedSeqMessage{from: from, to: to, text: text, dashed: found.dashed, head: found.head, marks: marks}
}
