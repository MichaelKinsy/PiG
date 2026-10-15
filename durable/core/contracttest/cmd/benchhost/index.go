package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

const (
	roleUser = iota + 1
	roleAssistant
	roleToolResult
	roleSystem
)

// msg is what the scripted model and the faux provider's usage estimate read of one model message: its role, the length of
// "<role>:<text>" the faux provider hashes, the tool count a user message plans, and the bench fingerprint item.
type msg struct {
	role  int
	chars int
	plan  int
	item  []byte // JSON.stringify([role, text, calls]); nil for a role the fingerprint skips
}

var toolsRe = regexp.MustCompile(`tools=(\d+)`)

func planOf(text string) int {
	m := toolsRe.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// u16 is a JavaScript string's length: UTF-16 code units. A lone surrogate is held as its three-byte encoding.
func u16(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 {
			n++
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r >= 0x10000 {
			n++
		}
		n++
		i += size
	}
	return n
}

func str(o *jv.Object, key string) string {
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

// blocks is the content as a block list: a string is one text block.
func blocks(o *jv.Object) []*jv.Object {
	v, _ := o.Get("content")
	switch c := v.(type) {
	case string:
		b := jv.NewObject()
		b.Set("type", "text")
		b.Set("text", c)
		return []*jv.Object{b}
	case []any:
		out := make([]*jv.Object, 0, len(c))
		for _, x := range c {
			if b, ok := x.(*jv.Object); ok {
				out = append(out, b)
			}
		}
		return out
	}
	return nil
}

func blockText(b *jv.Object) string {
	if str(b, "type") == "text" {
		return str(b, "text")
	}
	return fmt.Sprintf("[image:%s:%d]", str(b, "mimeType"), u16(str(b, "data")))
}

// systemText is faux.ts systemText: the system prompt, its sections, and the added and removed tool lines.
func systemText(o *jv.Object) string {
	parts := []string{}
	if c, ok := o.Get("content"); ok {
		switch x := c.(type) {
		case string:
			parts = append(parts, x)
		case []any:
			var texts []string
			for _, b := range blocks(o) {
				if str(b, "type") == "text" {
					texts = append(texts, str(b, "text"))
				}
			}
			parts = append(parts, strings.Join(texts, "\n"))
		}
	}
	if s, ok := o.Get("sections"); ok {
		if so, ok := s.(*jv.Object); ok {
			so.Entries(func(_ string, v any) bool {
				if t, ok := v.(string); ok {
					parts = append(parts, t)
				}
				return true
			})
		}
	}
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	lines := []string{strings.Join(keep, "\n\n")}
	for _, k := range [][2]string{{"toolsRemoved", "tool-:"}, {"toolsAdded", "tool+:"}} {
		if v, ok := o.Get(k[0]); ok {
			if arr, ok := v.([]any); ok {
				for _, t := range arr {
					lines = append(lines, k[1]+jv.Stringify(t))
				}
			}
		}
	}
	var out []string
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// info reads one pi-ai message (faux.ts messageChars and the bench fingerprint's convert).
func info(raw []byte) (msg, error) {
	v, err := jv.Parse(raw)
	if err != nil {
		return msg{}, err
	}
	o, ok := v.(*jv.Object)
	if !ok {
		return msg{}, fmt.Errorf("message is not an object")
	}
	role := str(o, "role")
	var m msg
	var text string
	var calls []string
	switch role {
	case "user":
		bs := blocks(o)
		var texts []string
		n := 0
		for i, b := range bs {
			if str(b, "type") == "text" {
				texts = append(texts, str(b, "text"))
			}
			n += u16(blockText(b))
			if i > 0 {
				n++
			}
		}
		text = strings.Join(texts, "")
		m = msg{role: roleUser, chars: 4 + 1 + n, plan: planOf(text)}
	case "assistant":
		n := 0
		var texts []string
		for i, b := range blocks(o) {
			if i > 0 {
				n++
			}
			switch str(b, "type") {
			case "text":
				n += u16(str(b, "text"))
				texts = append(texts, str(b, "text"))
			case "thinking":
				n += u16(str(b, "thinking"))
			default:
				args, _ := b.Get("arguments")
				n += u16(str(b, "name") + ":" + jv.Stringify(args))
				if a, ok := args.(*jv.Object); ok {
					if nv, ok := a.Get("n"); ok {
						calls = append(calls, jv.Stringify(nv))
					}
				}
			}
		}
		text = strings.Join(texts, "")
		m = msg{role: roleAssistant, chars: len("assistant") + 1 + n}
	case "toolResult":
		n := u16(str(o, "toolName"))
		var texts []string
		for _, b := range blocks(o) {
			n += 1 + u16(blockText(b))
			if str(b, "type") == "text" {
				texts = append(texts, str(b, "text"))
			}
		}
		text = strings.Join(texts, "")
		m = msg{role: roleToolResult, chars: len("toolResult") + 1 + n}
	case "system":
		return msg{role: roleSystem, chars: len("system") + 1 + u16(systemText(o))}, nil
	default:
		return msg{}, fmt.Errorf("message role %q is outside this host's workload", role)
	}
	name := role
	if role == "toolResult" {
		name = "tool"
	}
	item := []byte{'['}
	item = jv.AppendString(item, name)
	item = append(item, ',')
	item = jv.AppendString(item, text)
	item = append(item, ',', '[')
	item = append(item, strings.Join(calls, ",")...)
	item = append(item, ']', ']')
	m.item = item
	return m, nil
}

// index is the transcript as the scripted model reads it.
type index struct {
	msgs  []msg
	total int
}

func (ix *index) add(m msg) {
	ix.msgs = append(ix.msgs, m)
	ix.total += m.chars
}

// promptChars is faux.ts serializeContext(...).length over the messages: their texts joined by two characters.
func (ix *index) promptChars() int {
	if len(ix.msgs) == 0 {
		return 0
	}
	return ix.total + 2*(len(ix.msgs)-1)
}

// next is durable-bench's next(context): a lookup number, or -1 with the lookups done this turn.
func (ix *index) next() (call, done int) {
	last, total, d := -1, 0, 0
	for i, m := range ix.msgs {
		switch m.role {
		case roleUser:
			last, d = i, 0
		case roleToolResult:
			total++
			d++
		}
	}
	planned := 0
	if last >= 0 {
		planned = ix.msgs[last].plan
	}
	if d < planned {
		return total + 1, d
	}
	return -1, d
}

// fingerprint is the bench fingerprint of the context: 8 bytes of sha256 over [role, text, calls] of every kept message.
func (ix *index) fingerprint() string {
	h := sha256.New()
	h.Write([]byte{'['})
	first := true
	for _, m := range ix.msgs {
		if m.item == nil {
			continue
		}
		if !first {
			h.Write([]byte{','})
		}
		first = false
		h.Write(m.item)
	}
	h.Write([]byte{']'})
	return hex.EncodeToString(h.Sum(nil)[:8])
}
