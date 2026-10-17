package codemode

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
)

// inlineLauncher runs the VM on the calling goroutine under an interrupt budget, with no goroutine of its own.
//
// Problem: QuickJS runs synchronously. While a script computes, the goroutine that drives the VM does nothing else, so
// neither the deadline timer nor a cancelled context can take effect there; only QuickJS's own interrupt poll runs.
// Solution: an interrupt budget. QuickJS polls its interrupt handler periodically while a script runs and the job drain
// polls it between batches of jobs; the launcher counts the polls across the whole execution and interrupts the script once
// the count passes the budget. The execution then fails with ErrorTimeout. The deadline and the context still work whenever
// the script waits on a tool.
//
// Once the VM reports done or crash the result is decided, so the VM is interrupted at the next poll instead of running
// on until the host stops it.
//
// Reentrancy: VM callbacks run inside QuickJS. Messages from the VM are queued while it runs and delivered to the host
// afterwards, by whichever goroutine pumps (start or post), so host code (tool calls, finish, disposal) never runs inside a
// VM callback and never re-enters the VM. A post that arrives while another goroutine pumps only enqueues its result:
// the pumping goroutine delivers it, so a script that makes many sequential calls does not deepen any stack.
//
// Ports packages/codemode/src/runtime/inline.ts (inlineLauncher).
func inlineLauncher(load engineLoader, prelude string, budget int) vmLauncher {
	return func(ctx context.Context, data VmStartData, events vmEvents) (vmChannel, error) {
		eng, err := load(ctx, data.MemoryLimitBytes)
		if err != nil {
			return nil, fmt.Errorf("Failed to load QuickJS: %w", err)
		}
		// A caller that drives the VM outside an execution has no deadline to arm and no host events to hold off.
		if events.ready == nil {
			events.ready = func() {}
		}
		if events.turn == nil {
			events.turn = &sync.Mutex{}
		}
		events.ready()
		c := &inlineChannel{budget: budget, events: events}
		// The first segment of the script is a turn: the host's deadline and context wait for it. The turn lasts until start
		// has delivered the messages the segment queued, as Node runs those microtasks before any timer.
		events.turn.Lock()
		session, err := startVM(startVMOptions{
			ctx: ctx, engine: eng, prelude: prelude, data: data,
			maxStackSize:    inlineMaxStackSize,
			post:            c.enqueue,
			shouldInterrupt: c.shouldInterrupt,
			created:         c.guard.created,
			interruptBudget: &budget,
		})
		if err != nil {
			events.turn.Unlock()
			return nil, err
		}
		c.session = session
		return c, nil
	}
}

type inlineChannel struct {
	budget  int
	events  vmEvents
	session *vmSession
	guard   haltGuard

	// stopped is raised by stop; settled is set when the VM reported its verdict; polls counts the interrupt polls and
	// exhausted records that the budget ran out.
	stopped   atomic.Bool
	settled   atomic.Bool
	exhausted atomic.Bool
	polls     atomic.Int64

	// vmMu serializes calls into the VM and its disposal.
	vmMu     sync.Mutex
	stopOnce sync.Once

	// mu guards the queues; pumping is set while a goroutine delivers from them.
	mu      sync.Mutex
	pumping bool
	outbox  []vmMessage
	inbox   []callResult
}

// shouldInterrupt is what QuickJS and the job drain poll.
//
// Ports packages/codemode/src/runtime/inline.ts (shouldInterrupt).
func (c *inlineChannel) shouldInterrupt() bool {
	if c.stopped.Load() || c.settled.Load() || c.exhausted.Load() {
		return true
	}
	if c.polls.Add(1) <= int64(c.budget) {
		return false
	}
	c.exhausted.Store(true)
	return true
}

// enqueue is the VM's post. Only the first verdict counts: a crash caused by interrupting a settled VM is noise.
func (c *inlineChannel) enqueue(message vmMessage) {
	if terminal(message) && c.settled.Swap(true) {
		return
	}
	c.mu.Lock()
	c.outbox = append(c.outbox, message)
	c.mu.Unlock()
}

func terminal(message vmMessage) bool {
	return message.kind == vmMessageDone || message.kind == vmMessageCrash
}

// start delivers what the VM reported while it started, inside the turn the launcher took for the first segment, and then
// ends that turn.
func (c *inlineChannel) start() {
	defer c.events.turn.Unlock()
	c.mu.Lock()
	if c.pumping {
		// A goroutine that posted early waits for this turn and delivers once it ends.
		c.mu.Unlock()
		return
	}
	c.pumping = true
	c.mu.Unlock()
	c.deliverQueued()
}

func (c *inlineChannel) post(r callResult) {
	c.mu.Lock()
	c.inbox = append(c.inbox, r)
	c.mu.Unlock()
	c.pump()
}

// pump delivers queued messages to the host, then queued results to the VM, until both queues are empty. Only one
// goroutine pumps at a time.
func (c *inlineChannel) pump() {
	c.mu.Lock()
	if c.pumping {
		c.mu.Unlock()
		return
	}
	c.pumping = true
	c.mu.Unlock()
	c.events.turn.Lock()
	defer c.events.turn.Unlock()
	c.deliverQueued()
}

// deliverQueued is the pumping goroutine's loop: messages to the host first, then results to the VM, until both queues are
// empty. The caller has set pumping and holds the turn.
func (c *inlineChannel) deliverQueued() {
	c.mu.Lock()
	for {
		switch {
		case len(c.outbox) > 0:
			messages := c.outbox
			c.outbox = nil
			c.mu.Unlock()
			for _, message := range messages {
				c.report(message)
			}
		case len(c.inbox) > 0:
			r := c.inbox[0]
			c.inbox = c.inbox[1:]
			c.mu.Unlock()
			c.vmMu.Lock()
			if !c.stopped.Load() {
				c.session.deliver(r)
			}
			c.vmMu.Unlock()
		default:
			c.pumping = false
			c.mu.Unlock()
			return
		}
		c.mu.Lock()
	}
}

// report delivers one message to the host. The interrupt surfaces as a crash or a failed done; the host is told what caused
// it instead.
func (c *inlineChannel) report(message vmMessage) {
	if c.stopped.Load() {
		return
	}
	if c.exhausted.Load() && terminal(message) {
		c.events.failure(&Error{Kind: ErrorTimeout, Message: "Execution exceeded its interrupt budget of " + strconv.Itoa(c.budget)})
		return
	}
	c.events.message(message)
}

// stop interrupts the VM, escalating to halting the instance if the script does not reach a poll, and releases it. It is
// safe to call more than once. It drops the session: the halt guard's timer reaches the channel, and a stopped timer stays
// in its P's timer heap until that P next runs, so the session would keep the closed instance and its linear memory
// reachable.
func (c *inlineChannel) stop() {
	c.stopOnce.Do(func() {
		c.stopped.Store(true)
		c.guard.arm()
		c.vmMu.Lock()
		c.session.dispose()
		c.session = nil
		c.vmMu.Unlock()
		c.guard.exited()
	})
}
