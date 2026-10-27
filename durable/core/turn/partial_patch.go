// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// The partial patch protocol (host/cf/src/partial.ts): the host does not serialize the whole partial message on every model event,
// whose cost follows the length of the answer, but what grew since the partial it sent last, as Pi's PartialChanges takes only the
// new text of a partial commit (harness/partial-changes.ts):
//
//   - a text or thinking block carries its field emptied and `$a`, the text appended since the previous partial;
//   - a tool call carries `arguments: {}` and `$o`, the chord operations that its streamed argument text made since the previous
//     partial, on the arguments;
//   - any other block, and a block the host could not describe as an append, carries its whole value, which replaces what the core
//     accumulated for it.
//
// A partial with no marker is a whole message, so a host that sends whole partials needs nothing of this.

// accBlock is what the core accumulated of one content block: the text of a text or thinking block, or the arguments of a call.
type accBlock struct {
	text []byte
	has  bool
	args any
}

// partialAcc rebuilds the partial message from the patches the host sent, in order.
type partialAcc struct {
	skeleton *jv.Object
	blocks   []accBlock
}

// member is the value of a key, nil when absent.
func member(o *jv.Object, key string) any {
	v, _ := o.Get(key)
	return v
}

// textField is the member that holds the text of a content block of the given type, or "".
func textField(typ string) string {
	switch typ {
	case "text":
		return "text"
	case "thinking":
		return "thinking"
	}
	return ""
}

// apply takes one patched partial (a JSON object): its skeleton is the newest, and each block adds to what was accumulated.
func (a *partialAcc) apply(p *jv.Object) {
	content, _ := member(p, "content").([]any)
	a.skeleton = p
	if len(content) < len(a.blocks) {
		a.blocks = a.blocks[:len(content)]
	}
	for len(a.blocks) < len(content) {
		a.blocks = append(a.blocks, accBlock{})
	}
	for j, v := range content {
		block, _ := v.(*jv.Object)
		if block == nil {
			continue
		}
		acc := &a.blocks[j]
		typ := str(block, "type")
		if field := textField(typ); field != "" {
			if add, ok := block.Get("$a"); ok {
				text, _ := add.(string)
				if !acc.has {
					acc.text, acc.has = acc.text[:0], true
				}
				acc.text = delta.AppendText(acc.text, text)
			} else {
				text, _ := member(block, field).(string)
				acc.text, acc.has = append(acc.text[:0], text...), true
			}
			continue
		}
		if typ != "toolCall" {
			acc.has, acc.args = false, nil
			continue
		}
		if o, ok := block.Get("$o"); ok {
			ops, err := delta.DecodeOps(o)
			args := acc.args
			if args == nil {
				args = jv.NewObject()
			}
			if err == nil {
				if next, err := delta.Apply(args, ops); err == nil {
					args = next
				}
			}
			acc.args = args
			continue
		}
		acc.args = member(block, "arguments")
	}
}

// message is the partial message the patches add up to: the newest skeleton with each block's accumulated text and arguments.
func (a *partialAcc) message() *jv.Object {
	content, _ := member(a.skeleton, "content").([]any)
	for j, v := range content {
		block, _ := v.(*jv.Object)
		if block == nil || j >= len(a.blocks) {
			continue
		}
		acc := &a.blocks[j]
		if field := textField(str(block, "type")); field != "" {
			if acc.has {
				block.Set(field, string(acc.text))
			}
			block.Delete("$a")
			continue
		}
		block.Delete("$o")
		if acc.args != nil {
			block.Set("arguments", acc.args)
		}
	}
	return a.skeleton
}
