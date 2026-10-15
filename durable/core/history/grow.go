// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package history

// push appends v to s, growing a full slice by an eighth. The runtime's own rule rounds a grown slice up to the next power
// of two under TinyGo, which leaves up to half of each per-entry index slice unused, and in a heap that never returns
// memory the unused half is most of the index.
func push[T any](s []T, v T) []T {
	if len(s) == cap(s) {
		n := len(s) + 1
		t := make([]T, len(s), n+max(n/8, 8))
		copy(t, s)
		s = t
	}
	return append(s, v)
}

// room makes s able to take add more elements without growing, with an eighth of slack when it must be reallocated.
func room[T any](s []T, add int) []T {
	if cap(s)-len(s) >= add {
		return s
	}
	n := len(s) + add
	t := make([]T, len(s), n+max(n/8, 8))
	copy(t, s)
	return t
}
