package main

import "testing"

// The stdin-end flush waits until every unwritten command response belongs to a command its runtime reports suspended, and no response is written once the flush ends.
func TestRPCCommandJoinWaitsForCommandsThatAreNotSuspended(t *testing.T) {
	join := &rpcCommandJoin{}
	join.begin()
	join.begin()
	idle := join.idle()
	join.setSuspended(1)
	select {
	case <-idle:
		t.Fatal("the join ended while one command was neither suspended nor written")
	default:
	}
	join.end()
	select {
	case <-idle:
	default:
		t.Fatal("the join did not end once the only unwritten response was a suspended command's")
	}
	if join.isClosed() {
		t.Fatal("the join closed before the flush ended it")
	}
	join.close()
	if !join.isClosed() {
		t.Fatal("close did not end the join")
	}
}

// No response is written after the join closed, so a command still counted unwritten can never make it busy again: waiters end at close, and a later idle() is already closed.
func TestRPCCommandJoinIsIdleOnceClosed(t *testing.T) {
	join := &rpcCommandJoin{}
	join.begin()
	waiting := join.idle()
	select {
	case <-waiting:
		t.Fatal("the join ended while a command was neither suspended nor written")
	default:
	}
	join.close()
	select {
	case <-waiting:
	default:
		t.Fatal("close left a waiter on a response that is never written")
	}
	select {
	case <-join.idle():
	default:
		t.Fatal("a closed join with an unwritten command was not idle")
	}
}

type fakeSuspendSource struct{ report func(int) }

func (f *fakeSuspendSource) SetCommandSuspendHandler(fn func(int)) { f.report = fn }

func (f *fakeSuspendSource) SetCommandWindowHandler(func(int)) {}

// Each extension host reports into a join of its own. A replacement host that reports zero suspended commands must not overwrite the count of an earlier host whose command is still suspended, or the flush would wait for a response that never comes.
func TestRPCCommandJoinKeepsSuspendedStatePerHost(t *testing.T) {
	first, second := &fakeSuspendSource{}, &fakeSuspendSource{}
	oldJoin, newJoin := attachRPCCommandJoin(first), attachRPCCommandJoin(second)
	if oldJoin == newJoin {
		t.Fatal("two hosts share one join")
	}
	oldJoin.begin()
	first.report(1)
	second.report(0)
	select {
	case <-oldJoin.idle():
	default:
		t.Fatal("the replacement host's report changed the earlier host's suspended count")
	}
	newJoin.begin()
	select {
	case <-newJoin.idle():
		t.Fatal("the earlier host's report changed the replacement host's count")
	default:
	}
}

// Pi answers a command that settles in its line's microtasks and check phase before it reads stdin's end, so the shutdown waits for such a command's response, and only for those: a command counted suspended at that point never answers.
func TestRPCCommandJoinWindowIdleWaitsForCommandsPiAnswers(t *testing.T) {
	join := &rpcCommandJoin{}
	join.begin()
	join.begin()
	idle := join.windowIdle()
	join.setWindowSuspended(1)
	select {
	case <-idle:
		t.Fatal("window idle before the unsuspended command was written")
	default:
	}
	join.end()
	select {
	case <-idle:
	default:
		t.Fatal("window idle did not follow the answered command's write")
	}
	select {
	case <-join.windowIdle():
	default:
		t.Fatal("a window join with only a suspended command left was not idle")
	}
	// The flush join still counts the host's suspended number, not the window's.
	select {
	case <-join.idle():
		t.Fatal("the flush join took the window's count")
	default:
	}
}
