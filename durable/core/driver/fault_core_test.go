// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build pigfault

package driver_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// The Pi tests that need Storage's ControlledStorage (session-support.ts): commits are counted as they enter the store and one
// can be held, with the owner goroutine blocked before the store applies it, so every other call queues behind the line as it does
// behind Pi's held Storage.commit. They run in the pigfault build only (make test-core-fault, part of make test); the production
// build has no hook. The reads Pi's tests hold (holdFindDocument) have no native counterpart: the core answers reads inside a step.

// controlledStore counts commits and holds them.
type controlledStore struct {
	mu      sync.Mutex
	commits int
	gate    *commitGate
	tasks   []taskWrite
	heads   [][]string // the statements of each commit, as their SQL text
}

// taskWrite is one write of a task record: ControlledStorage's commits filtered to `write.type === "task"`.
type taskWrite struct {
	commit         int
	id             durable.TaskId
	status         string
	abortRequested bool
}

// commitGate is ControlledStorage's Gate: entered closes when a commit reaches the hold, release lets the held commits go.
type commitGate struct {
	entered chan struct{}
	freed   chan struct{}
	once    sync.Once
}

func (g *commitGate) release() { g.once.Do(func() { close(g.freed) }) }

func (s *controlledStore) onCommit(c abi.Commit, sql []string) {
	s.mu.Lock()
	s.commits++
	statements := make([]string, 0, len(c.Stmts))
	for _, stmt := range c.Stmts {
		statements = append(statements, sql[stmt.SQL])
		// "INSERT INTO tasks_v2" is migration 2's table rebuild, a parameterless INSERT ... SELECT, not a task write.
		if strings.HasPrefix(sql[stmt.SQL], "INSERT INTO tasks ") {
			s.tasks = append(s.tasks, taskWrite{commit: s.commits, id: durable.TaskId(stmt.Params[0].Int), status: string(stmt.Params[3].Bytes), abortRequested: stmt.Params[4].Int == 1})
		}
	}
	s.heads = append(s.heads, statements)
	g := s.gate
	s.mu.Unlock()
	if g == nil {
		return
	}
	select {
	case <-g.entered:
	default:
		close(g.entered)
	}
	<-g.freed
}

// holdCommits holds every commit from now until release (ControlledStorage.holdCommits).
func (s *controlledStore) holdCommits() *commitGate {
	g := &commitGate{entered: make(chan struct{}), freed: make(chan struct{})}
	s.mu.Lock()
	s.gate = g
	s.mu.Unlock()
	return g
}

// taskWrites lists the writes of one task in commit order.
func (s *controlledStore) taskWrites(id durable.TaskId) []taskWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []taskWrite
	for _, w := range s.tasks {
		if w.id == id {
			out = append(out, w)
		}
	}
	return out
}

// lastCommitTaskWrites lists the task writes of the last commit that entered the store.
func (s *controlledStore) lastCommitTaskWrites() []taskWrite { return s.commitTaskWrites(0) }

// commitTaskWrites lists the task writes of the commit that entered the store n commits before the last one (storage.commits.at(-1-n)).
func (s *controlledStore) commitTaskWrites(n int) []taskWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []taskWrite
	for _, w := range s.tasks {
		if w.commit == s.commits-n {
			out = append(out, w)
		}
	}
	return out
}

func statuses(writes []taskWrite) []string {
	out := make([]string, len(writes))
	for i, w := range writes {
		out[i] = w.status
	}
	return out
}

// statementsOf lists the SQL of the nth commit that entered the store (0-based).
func (s *controlledStore) statementsOf(n int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heads[n]
}

func (s *controlledStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commits
}

func (g *commitGate) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("no commit reached the hold")
	}
}

func openFault(t *testing.T, store *controlledStore, options harness.HarnessOptions, tasks ...durable.AnyTask) (harness.Harness, harness.Conversation) {
	t.Helper()
	registry := harness.CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "tasks", Tasks: tasks}); err != nil {
		t.Fatal(err)
	}
	options.Models, options.Registry = ai.CreateModels(), registry
	h, err := driver.OpenHarnessFault(t.Context(), filepath.Join(t.TempDir(), "store.db"), options, store.onCommit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return h, root
}

// queuedAbove is waitQueuedAbove for a goroutine that cannot fail the test: it reports whether the mailbox got that many items.
func queuedAbove(h harness.Harness, n int) bool {
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if driver.QueuedPosts(h) > n {
			return true
		}
	}
	return false
}

// waitQueuedAbove waits until the owner mailbox holds more than n items: the post a caller made after the owner was held has arrived.
func waitQueuedAbove(t *testing.T, h harness.Harness, n int) {
	t.Helper()
	if queuedAbove(h, n) {
		return
	}
	t.Fatalf("no post reached the owner mailbox (holds %d, want more than %d)", driver.QueuedPosts(h), n)
}

// queuedBy runs post, which starts a call in a goroutine, and waits until its request is in the owner mailbox behind the held commit.
func queuedBy(t *testing.T, h harness.Harness, post func()) {
	t.Helper()
	n := driver.QueuedPosts(h)
	post()
	waitQueuedAbove(t, h, n)
}

// closeBegun starts Close in a goroutine and returns once it has sealed admission and queued the scheduler seal: Pi's close() does both
// synchronously, before any continuation the caller resumes next. The owner must be held, or the seal is consumed at once.
func closeBegun(t *testing.T, h harness.Harness) <-chan error {
	t.Helper()
	closing := make(chan error, 1)
	queuedBy(t, h, func() { go func() { closing <- h.Close(context.Background()) }() })
	if !h.(*driver.Session).CloseBegan() {
		t.Fatal("the seal was queued before admission closed")
	}
	return closing
}

// closeBegunFree is closeBegun for an owner that is not held: the seal is consumed at once, so the test waits for the admission seal,
// which Close sets before it posts the scheduler seal.
func closeBegunFree(t *testing.T, h harness.Harness) <-chan error {
	t.Helper()
	closing := make(chan error, 1)
	go func() { closing <- h.Close(context.Background()) }()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if h.(*driver.Session).CloseBegan() {
			return closing
		}
	}
	t.Fatal("Close never sealed admission")
	return closing
}
