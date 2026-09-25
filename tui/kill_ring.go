package tui

// KillRing is an Emacs-style kill/yank ring buffer.
//
// Tracks killed (deleted) text entries. Consecutive kills accumulate into a
// single entry via Push(accumulate=true). Supports yank (paste most recent)
// and yank-pop (cycle through older entries via Rotate).
//
// Mirrors upstream kill-ring.ts.
type KillRing struct {
	ring []string
}

// Push adds text to the kill ring.
//
//   - prepend: when accumulating, prepend text (backward deletion) vs append (forward).
//   - accumulate: merge with the most recent entry instead of creating a new one.
//
// Empty text is a no-op.
func (k *KillRing) Push(text string, prepend bool, accumulate bool) {
	if text == "" {
		return
	}
	if accumulate && len(k.ring) > 0 {
		last := k.ring[len(k.ring)-1]
		k.ring[len(k.ring)-1] = func() string {
			if prepend {
				return text + last
			}
			return last + text
		}()
		return
	}
	k.ring = append(k.ring, text)
}

// Peek returns the most recent entry without modifying the ring.
// Returns "" if the ring is empty.
func (k *KillRing) Peek() string {
	if len(k.ring) == 0 {
		return ""
	}
	return k.ring[len(k.ring)-1]
}

// Rotate moves the last entry to the front (for yank-pop cycling).
// No-op if the ring has fewer than 2 entries.
func (k *KillRing) Rotate() {
	if len(k.ring) < 2 {
		return
	}
	last := k.ring[len(k.ring)-1]
	k.ring = append([]string{last}, k.ring[:len(k.ring)-1]...)
}

// Len returns the number of entries in the ring.
func (k *KillRing) Len() int { return len(k.ring) }

// Length mirrors upstream's `get length()` surface.
func (k *KillRing) Length() int { return len(k.ring) }
