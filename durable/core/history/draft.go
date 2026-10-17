// SPDX-License-Identifier: MIT

package history

// AppendEntryRecord appends the stored record of an entry written from draft (src/session/transaction.ts
// #appendEntry): JSON.stringify(copyJson({...rest, id, conversationId, head, byTaskId})) where rest is the draft
// without its head. The draft keeps its own key order; id and conversationId take the draft's position when it
// already carries those keys and follow it otherwise; a head of "self" becomes the new id; an absent head and an
// absent task attribution leave no key. A draft is a JSON object, as a host or an entry builder supplies it.
func AppendEntryRecord(dst, draft []byte, id, conv int64, task int64, hasTask bool) ([]byte, error) {
	canon, err := Canonical(nil, draft)
	if err != nil {
		return dst, err
	}
	s := newScanner(canon)
	it, err := s.object()
	if err != nil {
		return dst, err
	}
	var headVal []byte
	hasHead := false
	type kv struct{ key, val []byte }
	var fields []kv
	for it.next() {
		if it.keyIs("head") {
			headVal, hasHead = it.value(), true
			continue
		}
		fields = append(fields, kv{it.key, it.value()})
	}
	if it.err != nil {
		return dst, it.err
	}
	idText := appendInt(nil, id)
	convText := appendInt(nil, conv)
	var taskText []byte
	if hasTask {
		taskText = appendInt(nil, task)
	}
	var headText []byte
	if hasHead {
		if valueKind(headVal) == '"' && keyIs(headVal[1:len(headVal)-1], "self") {
			headText = idText
		} else {
			headText = headVal
		}
	}
	setIn := func(name string, val []byte, present bool) bool {
		for i := range fields {
			if keyIs(fields[i].key, name) {
				if present {
					fields[i].val = val
				} else {
					fields = append(fields[:i], fields[i+1:]...)
				}
				return true
			}
		}
		return false
	}
	if !setIn("id", idText, true) {
		fields = append(fields, kv{[]byte("id"), idText})
	}
	if !setIn("conversationId", convText, true) {
		fields = append(fields, kv{[]byte("conversationId"), convText})
	}
	if hasHead {
		fields = append(fields, kv{[]byte("head"), headText})
	}
	if hasTask {
		if !setIn("byTaskId", taskText, true) {
			fields = append(fields, kv{[]byte("byTaskId"), taskText})
		}
	} else {
		setIn("byTaskId", nil, false)
	}
	dst = append(dst, '{')
	for i, f := range fields {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '"')
		dst = append(dst, f.key...)
		dst = append(dst, '"', ':')
		dst = append(dst, f.val...)
	}
	return append(dst, '}'), nil
}

// DraftIsReset reports whether a write's entry draft starts a new context: its head is "self" (src/harness/inbox.ts
// applyBoundary, which turns a postTools boundary into final).
func DraftIsReset(draft []byte) bool {
	h, ok := draftHead(draft)
	return ok && valueKind(h) == '"' && keyIs(h[1:len(h)-1], "self")
}

// DraftHeadTarget returns the entry id a draft's numeric head names.
func DraftHeadTarget(draft []byte) (int64, bool) {
	h, ok := draftHead(draft)
	if !ok {
		return 0, false
	}
	return intValue(h)
}

func draftHead(draft []byte) ([]byte, bool) {
	s := newScanner(draft)
	it, err := s.object()
	if err != nil {
		return nil, false
	}
	var head []byte
	has := false
	for it.next() {
		if it.keyIs("head") {
			head, has = it.value(), true
		}
	}
	return head, has && it.err == nil
}

// IsStaleHeadWrite reports whether a head write targets an entry before the active range, so placing it would
// bring back cut history (src/harness/inbox.ts isStale). activeHead is the start of the active range, the newest
// head marker's head; hasActive is false when the conversation has no head marker.
func IsStaleHeadWrite(draft []byte, activeHead int64, hasActive bool) bool {
	target, ok := DraftHeadTarget(draft)
	return ok && hasActive && target < activeHead
}

// AppendResetDraft appends the entry draft of Conversation.reset (src/harness/harness.ts): a pi.reset entry that
// starts a new context, carrying handoff, a JavaScript string, as a user message when there is one.
func AppendResetDraft(dst []byte, handoff []byte, hasHandoff bool, now float64) ([]byte, error) {
	dst = append(dst, `{"kind":"pi.reset","head":"self"`...)
	if hasHandoff {
		dst = append(dst, `,"model":[{"role":"user","content":`...)
		dst = appendString(dst, handoff)
		dst = append(dst, `,"timestamp":`...)
		var err error
		if dst, err = AppendNumber(dst, now); err != nil {
			return dst, err
		}
		dst = append(dst, "}]"...)
	}
	return append(dst, '}'), nil
}
