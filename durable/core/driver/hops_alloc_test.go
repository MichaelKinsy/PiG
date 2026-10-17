// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// A hopped event whose chain parked nothing is the empty hop the host's `hop` export answers with 0 (ABI section 4.1): the core
// takes the event and builds a step with nothing in it. The event used to be copied to the heap on every step (it is handled
// through an interface, so a local escapes); the step now costs no allocation, so a hop that has no work costs no garbage.
// The host's owner goroutine steps the same Session (the open's own hops chains still run after driver.Open returns), so the
// test takes the core from it: lockedCore holds the owner's Step while the test steps the Session itself.
func TestAnEmptyHoppedStepAllocatesNothing(t *testing.T) {
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	uuid := func() string { id, _ := ai.UUIDv7(nil); return id }
	session := &lockedCore{Session: core.NewSession(uuid)}
	s, err := driver.OpenSession(t.Context(), driver.SessionConfig{Core: session, SQL: core.SQLTable(), DB: db, Harness: harness.HarnessOptions{Models: ai.CreateModels(), Registry: harness.CreateRegistry()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(t.Context()) })
	if err := s.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	step := session.Session.Step(abi.Event{Kind: abi.EventHopped, ID: 999999})
	if step.Status != abi.StatusOK || len(step.Commits)+len(step.Reads)+len(step.Notices)+len(step.Effects) != 0 {
		t.Fatalf("a hopped event for a chain nobody parked is not an empty step: %+v", step)
	}
	if allocs := testing.AllocsPerRun(1000, func() { session.Session.Step(abi.Event{Kind: abi.EventHopped, ID: 999999}) }); allocs != 0 {
		t.Fatalf("an empty hopped step allocates %v times, want 0", allocs)
	}
}

// lockedCore serializes Step: the host's owner goroutine and the test step one Session.
type lockedCore struct {
	mu sync.Mutex
	*core.Session
}

func (c *lockedCore) Step(ev abi.Event) *abi.Step {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Session.Step(ev)
}
