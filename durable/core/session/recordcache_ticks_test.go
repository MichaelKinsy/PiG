// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// A Transaction read of a record the Session's cache holds costs a cached answer's ticks (hopcost.HitTicks), one it must read from Storage the
// miss's, and the read keeps the record for the next one (scenarios/session-ticks.mjs `warm`/`cold`, hopcost's TestSessionModelMatchesTheMeasuredSession).
func TestReadsChargeTheCacheOrStorage(t *testing.T) {
	p := hopcost.GateCapsql
	mem := sessiontest.New()
	writer := newSession(mem, mem.NextID())
	writer.SetHopProfile(p)
	conv := rootConversation(t, writer)
	ticks := func(s *session.Session, change func(tx *session.Tx) error) int {
		t.Helper()
		if _, err := s.RunCommit(change, session.TxScope{}); err != nil {
			t.Fatal(err)
		}
		return s.PeekTicks()
	}
	read := func(tx *session.Tx) error { _, err := tx.Conversation(conv); return err }

	if got := ticks(writer, read); got != hopcost.HitTicks {
		t.Errorf("the writing Session reads its conversation in %d ticks, want a hit's %d", got, hopcost.HitTicks)
	}
	cold := newSession(mem, mem.NextID())
	cold.SetHopProfile(p)
	if got, want := ticks(cold, read), hopcost.TxConversation(p, hopcost.CacheMiss); got != want {
		t.Errorf("a Session that read nothing reads the conversation in %d ticks, want a miss's %d", got, want)
	}
	if got := ticks(cold, read); got != hopcost.HitTicks {
		t.Errorf("the same Session reads it again in %d ticks, want a hit's %d", got, hopcost.HitTicks)
	}
	// An append on a conversation the Session has not read awaits the read, then mints.
	other := newSession(mem, mem.NextID())
	other.SetHopProfile(p)
	appendEntry := func(tx *session.Tx) error { _, err := tx.AppendEntry(conv, draft("note")); return err }
	if got, want := ticks(other, appendEntry), hopcost.TxAppendEntry(p, false, hopcost.CacheMiss); got != want {
		t.Errorf("a first append takes %d ticks, want %d", got, want)
	}
	if got, want := ticks(other, appendEntry), hopcost.TxAppendEntry(p, false, hopcost.CacheHit); got != want {
		t.Errorf("an append on a conversation the Session read takes %d ticks, want %d", got, want)
	}
	// A read-only Session shows another process's Storage: nothing is a hit.
	readOnly := newSession(mem, mem.NextID())
	readOnly.SetHopProfile(p)
	readOnly.ReadOnly = true
	ticks(readOnly, read)
	if got, want := ticks(readOnly, read), hopcost.ReadHops(p, hopcost.ReadRow); got != want {
		t.Errorf("a read-only Session reads the conversation in %d ticks, want Storage's %d", got, want)
	}
}
