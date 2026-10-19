package cli

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// RPC mode reads the extension host from the Session it serves (coding.Session.ExtensionHost), not from the factory that built it: each Session reaches its own build's bridge, and the process controls of any Session reach the hosts of the whole process, including the hosts of Sessions created after input ended.
func TestSessionExtensionHostIsItsBuildsHostWithProcessWideControls(t *testing.T) {
	builder, cwd, agentDir := benchReplacementBuilder(t, 1)
	ctx := context.Background()
	build, err := builder.buildResources(ctx, cliBuildInput{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := coding.NewInMemorySessionManager(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.buildSession(ctx, build, cliBuildInput{CWD: cwd, Manager: manager}); err != nil {
		t.Fatal(err)
	}
	if build.Bridge == nil {
		t.Fatal("the startup build has no extension bridge; the fixture extension did not load")
	}
	host := builder.printHost(ctx, build, &printStartup{Manager: manager})
	var rebuilt *cliBuild
	factory := newCLISessionFactory(ctx, host.inputs(), struct{}{}, func(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (cliSessionInputs, struct{}, error) {
		next, err := builder.rebuild(ctx, options)
		if err != nil {
			return cliSessionInputs{}, struct{}{}, err
		}
		rebuilt = next
		replacement := builder.printHost(ctx, next, nil)
		return replacement.inputs(), struct{}{}, nil
	})
	defer factory.Close()
	rt, err := coding.CreateAgentSessionRuntime(ctx, factory.Factory(), coding.CreateAgentSessionRuntimeOptions{CWD: cwd, AgentDir: agentDir, SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	first := rt.Session()
	firstHost, firstBridge := rpcExtensionHost(first)
	if firstHost == nil || firstBridge != build.Bridge {
		t.Fatalf("first Session: extension host %v with bridge %p, want the startup build's bridge %p", firstHost, firstBridge, build.Bridge)
	}
	firstHost.EndInput()
	if !factory.inputEnded {
		t.Fatal("EndInput through the Session's extension host did not reach the process's hosts")
	}

	if result, err := rt.NewSession(ctx, &extension.NewSessionOptions{}); err != nil || result.Cancelled {
		t.Fatalf("new session: cancelled=%v err=%v", result.Cancelled, err)
	}
	second := rt.Session()
	secondHost, secondBridge := rpcExtensionHost(second)
	if rebuilt == nil || secondHost == nil || secondBridge != rebuilt.Bridge || secondBridge == firstBridge {
		t.Fatalf("replacement Session: bridge %p, want the replacement build's bridge (startup %p)", secondBridge, firstBridge)
	}
	secondHost.HoldRetirements()
	if !factory.holding {
		t.Fatal("HoldRetirements through the replacement Session's extension host did not reach the factory")
	}
	// HoldRetirements keeps a retired host's Node process, and the cell usage lease it holds, until TerminateProcesses, so a path
	// that holds must end in it; Windows cannot remove a held lock file, and TempDir cleanup failed on it.
	factory.TerminateProcesses()
	// TerminateProcesses only kills, and the held release skipped the host's Shutdown, which reaps the process and releases its
	// cell usage lease. Windows keeps the lock file open until the killed process is gone, so TempDir cleanup raced it; the
	// test reaps the replacement build's host itself. The startup build's release ran before the hold.
	t.Cleanup(func() {
		rebuilt.Host.Shutdown("test end")
		rebuilt.Services.Close()
	})
}
