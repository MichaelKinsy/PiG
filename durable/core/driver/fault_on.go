// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build pigfault

package driver

import (
	"context"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// OpenHarnessFault is OpenHarness with a hook that runs on the owner goroutine before the store applies each commit of a step.
// A test blocks in it to hold the line between two commits, as Pi's ControlledStorage.holdCommits holds a commit (session-support.ts),
// and counts or records the commits in order. It exists only in the pigfault test build (make test-core-fault); the production
// build has no such entry point.
func OpenHarnessFault(ctx context.Context, path string, options harness.HarnessOptions, onCommit func(c abi.Commit, sql []string)) (harness.Harness, error) {
	return openHarness(ctx, path, options, onCommit, func(s *core.Session) sqlhost.Stepper { return &countedCore{Session: s} })
}

// countedCore counts the events the owner delivered to the core, by kind, so a test knows the core took a caller's request or a
// completion without holding the owner: a Session line held by a commit callback (Pi's holdLine) leaves the owner free.
type countedCore struct {
	*core.Session
	delivered [256]atomic.Int64
}

func (c *countedCore) Step(ev abi.Event) *abi.Step {
	step := c.Session.Step(ev)
	c.delivered[ev.Kind].Add(1)
	return step
}

// Delivered is the number of events of kind the core has stepped for a Harness that OpenHarnessFault opened.
func Delivered(h harness.Harness, kind abi.EventKind) int {
	return int(h.(*coreSession).cfg.Core.(*countedCore).delivered[kind].Load())
}

// Flush returns once the owner has nothing queued and no hops chain open: the work that arrived before the call, and the microtask
// chains it started, ran to their end (Pi's `await flush()`).
func Flush(ctx context.Context, h harness.Harness) error { return h.(*coreSession).host.Flush(ctx) }

// QueuedPosts is the number of requests and completions waiting for the owner goroutine of a Harness that OpenHarnessFault opened.
func QueuedPosts(h harness.Harness) int { return h.(*coreSession).host.QueuedPosts() }
