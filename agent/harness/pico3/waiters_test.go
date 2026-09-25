package pico3

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitersRegisterAtomicallyWithTheStateRead(t *testing.T) {
	env := openEnv(t, openOptions{})
	quick := quickKind(t, "quick", func(Task) JsonValue { return nil })
	env2 := openEnv(t, openOptions{taskKinds: []*Kind{quick}})
	for range 200 {
		ref := createBackground(t, env2, quick, nil)
		equal(t, must(env2.h.WaitForTask(bg, ref.Id)).Status, TaskTerminal, "waited task")
	}
	for index := range 50 {
		input := must(env.root.Send(bg, SendInput{Content: "m" + string(rune('0'+index%10))}))
		var group sync.WaitGroup
		var settled Input
		var errs [3]error
		group.Go(func() { settled, errs[0] = input.Wait(bg) })
		group.Go(func() { errs[1] = env.root.WaitForIdle(bg) })
		group.Go(func() { errs[2] = env.h.WaitForIdle(bg) })
		group.Wait()
		check(t, errors.Join(errs[:]...))
		equal(t, settled.Status, "done", "settled")
	}
	first := env2.tasks()[0]
	equal(t, must(env2.h.WaitForTask(bg, first.Id)).Status, TaskTerminal, "already terminal")
}

func TestAbortedWaiterContextRejectsOnlyTheWaiter(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	input := env.send(env.root, "A")
	waitCtx, cancel := context.WithCancelCause(bg)
	generation := firstOfKind(t, env.tasks(), "pi.generation")
	results := make(chan error, 3)
	go func() { _, err := input.Wait(waitCtx); results <- err }()
	go func() { results <- env.root.WaitForIdle(waitCtx) }()
	go func() { _, err := env.h.WaitForTask(waitCtx, generation.Id); results <- err }()
	gate.Arrivals(t, 1)
	cancel(errors.New("gone"))
	for range 3 {
		if err := <-results; err == nil || !strings.Contains(err.Error(), "gone") {
			t.Fatalf("waiter err = %v", err)
		}
	}
	dead, deadCancel := context.WithCancelCause(bg)
	deadCancel(errors.New("pre"))
	if _, err := input.Wait(dead); err == nil || !strings.Contains(err.Error(), "pre") {
		t.Fatalf("pre-aborted err = %v", err)
	}
	gate.Open()
	equal(t, env.wait(input).Status, "done", "turn finished")
}

func TestHarnessWaitForIdleWaitsForAJustCreatedTask(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	input := env.send(env.root, "A")
	var idleResolved atomic.Bool
	idle := make(chan error, 1)
	go func() {
		err := env.h.WaitForIdle(bg)
		idleResolved.Store(true)
		idle <- err
	}()
	gate.Arrivals(t, 1)
	time.Sleep(10 * time.Millisecond)
	if idleResolved.Load() {
		t.Fatal("waitForIdle resolved while the generation was live")
	}
	gate.Open()
	env.wait(input)
	check(t, <-idle)
	equal(t, len(env.liveTasks()), 0, "live tasks")
}
