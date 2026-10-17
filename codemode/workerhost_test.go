//go:build codemodeworker

package codemode_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The behavior tests of sandbox.test.ts on the Cloudflare Worker host: a RemoteSandbox whose remote is the real Worker module
// (durable/core/host/cf/src/codemode/serve.ts: the TinyGo build of this package and QuickJS on V8) in a Node process, reached over
// JSON lines. Run by durable/core/host/cf/test/codemode-go-behavior.test.ts with CODEMODE_WORKER_WASM set:
//
//	go test -tags codemodeworker ./codemode -run TestSandboxBehaviorOnTheWorkerHostBackend

// workerHost is a Remote that serves each execution in its own Node process.
type workerHost struct {
	helper string
	wasm   string
	serve  codemode.ServeOptions
}

type hostLine struct {
	ID       int                      `json:"id"`
	Messages []codemode.RemoteMessage `json:"messages"`
	Done     bool                     `json:"done"`
}

func (h workerHost) Execute(ctx context.Context, request codemode.RemoteRequest, exchange codemode.RemoteExchange) error {
	cmd := exec.CommandContext(ctx, "node", h.helper)
	cmd.Env = append(os.Environ(), "CODEMODE_WORKER_WASM="+h.wasm)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { _ = cmd.Wait() }()

	var writeMu sync.Mutex
	write := func(value any) {
		line, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = stdin.Write(append(line, '\n')) // a closed pipe is a process that is gone: the execution ended
	}
	write(map[string]any{"request": request, "options": map[string]any{"maxInterruptBudget": h.serve.MaxInterruptBudget, "maxMemoryLimitBytes": h.serve.MaxMemoryLimitBytes}})

	reader := bufio.NewReaderSize(stdout, 1<<20)
	for {
		raw, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil
			}
			return err
		}
		var line hostLine
		if err := json.Unmarshal(raw, &line); err != nil {
			return err
		}
		if line.Done {
			_ = stdin.Close()
			return nil
		}
		id := line.ID
		exchange(ctx, line.Messages, func(reply *codemode.RemoteReply, err error) {
			switch {
			case err != nil:
				write(map[string]any{"id": id, "error": err.Error()})
			case reply == nil:
				write(map[string]any{"id": id, "reply": nil})
			default:
				write(map[string]any{"id": id, "reply": reply})
			}
		})
	}
}

func workerHostBackend(t *testing.T) sandboxBackend {
	t.Helper()
	wasm := os.Getenv("CODEMODE_WORKER_WASM")
	if wasm == "" {
		t.Fatal("CODEMODE_WORKER_WASM is not set: run durable/core/host/cf/test/codemode-go-behavior.test.ts")
	}
	helper := filepath.Join(testenv.ModuleRoot(t), "durable", "core", "host", "cf", "test", "worker", "codemode-stdio.ts")
	return sandboxBackend{"workerhost", func(options codemode.SandboxOptions) (*codemode.Sandbox, error) {
		sandbox, err := codemode.NewRemoteSandbox(codemode.RemoteSandboxOptions{
			Tools: options.Tools, Globals: options.Globals, TimeoutMs: options.TimeoutMs, MemoryLimitBytes: options.MemoryLimitBytes,
			Remote: func() (codemode.Remote, error) {
				return workerHost{helper: helper, wasm: wasm, serve: codemode.ServeOptions{MaxInterruptBudget: inlineInterruptBudget}}, nil
			},
		})
		if err != nil {
			return nil, err
		}
		openRemotes.mu.Lock()
		openRemotes.sandboxes = append(openRemotes.sandboxes, sandbox)
		openRemotes.mu.Unlock()
		return sandbox.Sandbox, nil
	}}
}

func TestSandboxBehaviorOnTheWorkerHostBackend(t *testing.T) {
	onBackend(t, workerHostBackend(t), behaviorTests)
}
