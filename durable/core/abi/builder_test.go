// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package abi

import "testing"

// ABI section 3, invariant 3: a step with reads has no effects, and its commits come before its reads (the host applies them
// first). Cold open relies on a commit before a read: the schema prelude, then the version read.
func TestBuilderOrdersCommitsBeforeReads(t *testing.T) {
	panics := func(name string, f func(b *Builder)) {
		t.Helper()
		b := NewBuilder()
		defer func() {
			t.Helper()
			if recover() == nil {
				t.Errorf("%s: the builder accepted it", name)
			}
		}()
		f(b)
	}
	b := NewBuilder()
	b.Commit(-1, []Stmt{{SQL: 1}, {SQL: 2}})
	id := b.Read(3, nil)
	if s := b.Step(); len(s.Commits) != 1 || len(s.Reads) != 1 || s.Reads[0].ID != id {
		t.Fatalf("a commit then a read: %+v", s)
	}
	panics("a commit after a read", func(b *Builder) { b.Read(3, nil); b.Commit(-1, nil) })
	panics("a read after an effect", func(b *Builder) { b.Effect(EffectLiveness, F64Payload(-1)); b.Read(3, nil) })
	panics("an effect after a read", func(b *Builder) { b.Read(3, nil); b.Effect(EffectLiveness, F64Payload(-1)) })
}
