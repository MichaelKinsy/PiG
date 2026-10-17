// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build pigfault

package driver

import (
	"context"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// OpenHarnessFault is OpenHarness with a hook that runs on the owner goroutine before the store applies each commit of a step.
// A test blocks in it to hold the line between two commits, as Pi's ControlledStorage.holdCommits holds a commit (session-support.ts),
// and counts or records the commits in order. It exists only in the pigfault test build (make test-core-fault); the production
// build has no such entry point.
func OpenHarnessFault(ctx context.Context, path string, options harness.HarnessOptions, onCommit func(c abi.Commit, sql []string)) (harness.Harness, error) {
	return openHarness(ctx, path, options, onCommit)
}

// QueuedPosts is the number of requests and completions waiting for the owner goroutine of a Harness that OpenHarnessFault opened.
func QueuedPosts(h harness.Harness) int { return h.(*coreSession).host.QueuedPosts() }
