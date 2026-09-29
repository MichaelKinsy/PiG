package main

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"slices"
	"testing"
)

// A resources_discover path failure occurs before the RPC event forwarder starts. Disposal cannot wait for an event acknowledgment that no consumer can produce.
func TestRPCStartupResourceErrorDoesNotWaitForEventForwarder(t *testing.T) {
	p, _ := startRPCShutdownFixture(t, "RPC_SHUTDOWN_BAD_RESOURCE=1")
	drainRPCOutput(p)
	err := p.cmd.Wait()
	p.exited = true
	if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 {
		t.Fatalf("startup exit = %v, stderr=%s", err, p.stderr.String())
	}
}

// Pi rpc-mode.ts:352-355 checks ctx.shutdown after publishing agent_settled. The forwarder must remain runnable until queued event publication finishes.
func TestRPCSettledShutdownDoesNotBlockEventFlush(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t, "RPC_SHUTDOWN_ON_SETTLED=1")
			p.sendJSON(map[string]any{"id": "prompt", "type": "prompt", "message": "What is 20+22?"})
			p.await("agent_settled", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
			records := drainRPCOutput(p)
			p.waitForExit("after agent_settled requests shutdown")
			if !reflect.DeepEqual(records, []rpcRecord{rpcShutdownStartedNotify}) {
				t.Fatalf("stdout after agent_settled = %v", records)
			}
			events := readRPCShutdownReport(t, report)
			if len(events) < 2 || !slices.Equal(events[len(events)-2:], []string{"agent_settled", "session_shutdown"}) {
				t.Fatalf("events = %v", events)
			}
		})
	}
}

// Synchronous fire-and-forget effects cannot release command admission before a later ctx.shutdown in the same handler prefix.
func TestRPCShutdownAfterSynchronousUIEffectKeepsPiOrder(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, _ := impl.start(t)
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "quit", "type": "prompt", "message": "/quit title"})
			records := drainRPCOutput(p)
			p.waitForExit("after a synchronous UI effect and shutdown")
			want := []rpcRecord{
				{"type": "extension_ui_request", "method": "setTitle", "title": "quitting"},
				rpcShutdownStartedNotify,
				{"id": "quit", "type": "response", "command": "prompt", "success": true},
			}
			if !reflect.DeepEqual(records, want) {
				t.Fatalf("stdout=%v, want %v", records, want)
			}
		})
	}
}

func TestRPCPendingDialogIsPublishedBeforeShutdownCanObserveIt(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	u := newRPCUIContext(func(any) { close(entered); <-release })
	defer u.Close()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = u.Select(ctx, "dialog", []string{"yes"}, nil) }()
	<-entered
	pending := u.PendingRequest()
	select {
	case <-pending:
		t.Error("shutdown observed a dialog before its request reached stdout")
	default:
	}
	close(release)
	<-pending
	cancel()
	<-done
}
