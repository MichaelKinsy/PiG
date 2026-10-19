package daemon

// pi: packages/env/daemon/src/exec.rs

// Ports the exec rules of packages/env/daemon/src/exec.rs that packages/env/test exercises only through the client:
// cancel and kill, timeout with its spill file, and the codes of a missing shell, working directory and command.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExecCancelAbortsAndKillSettlesWithTheKilledStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh and signals")
	}
	p := startServe(t, Options{})
	cwd := t.TempDir()
	p.request(1, Object{"op": "exec", "cwd": cwd, "argv": []any{"sleep", "30"}}, nil)
	p.send(FrameCancel, 1, Object{}, nil)
	if reply := p.reply(1); reply.Kind != FrameError || object(t, reply)["code"] != "aborted" {
		t.Fatalf("abort: %+v", reply)
	}
	// A cancel right behind its request is never lost: the daemon registers the request first.
	for id := uint32(10); id < 30; id++ {
		p.request(id, Object{"op": "exec", "cwd": cwd, "argv": []any{"sleep", "30"}}, nil)
		p.send(FrameCancel, id, Object{}, nil)
		if reply := p.reply(id); reply.Kind != FrameError || object(t, reply)["code"] != "aborted" {
			t.Fatalf("request %d: %+v", id, reply)
		}
	}
	p.request(2, Object{"op": "exec", "cwd": cwd, "command": "sleep 30"}, nil)
	p.send(FrameCancel, 2, Object{"mode": "kill"}, nil)
	reply := p.reply(2)
	if reply.Kind != FrameResult || object(t, reply)["exitCode"] != float64(128+9) {
		t.Fatalf("kill: %+v", reply)
	}
}

func TestExecReportsTimeoutSpillAndShellErrorsWithTheirCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	p := startServe(t, Options{})
	cwd := t.TempDir()
	p.request(1, Object{"op": "exec", "cwd": cwd, "command": "yes | head -c 5000; exec sleep 30", "timeoutMs": float64(500), "spill": Object{"afterBytes": float64(10), "afterLines": float64(1000)}}, nil)
	reply := p.reply(1)
	failure := object(t, reply)
	if reply.Kind != FrameError || failure["code"] != "timeout" {
		t.Fatalf("timeout: %+v", failure)
	}
	spill, ok := failure["spillPath"].(string)
	if !ok {
		t.Fatalf("the timeout error has no spillPath: %v", failure)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(spill)) })
	if content, err := os.ReadFile(spill); err != nil || len(content) != 5000 {
		t.Fatalf("spill file: %d bytes, %v", len(content), err)
	}
	p.request(2, Object{"op": "exec", "cwd": cwd, "command": "true", "shellPath": filepath.Join(cwd, "no-such-shell")}, nil)
	if reply := p.reply(2); reply.Kind != FrameError || object(t, reply)["code"] != "shell_unavailable" {
		t.Fatalf("missing shell: %+v", reply)
	}
	p.request(3, Object{"op": "exec", "cwd": filepath.Join(cwd, "missing"), "argv": []any{"true"}}, nil)
	if reply := p.reply(3); reply.Kind != FrameError || object(t, reply)["code"] != "spawn_error" {
		t.Fatalf("missing working directory: %+v", reply)
	}
	p.request(4, Object{"op": "exec", "cwd": cwd}, nil)
	if reply := p.reply(4); reply.Kind != FrameError || object(t, reply)["code"] != "EINVAL" {
		t.Fatalf("exec without a command: %+v", reply)
	}
}
