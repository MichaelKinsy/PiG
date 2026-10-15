//go:build unix || windows

package host

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// The real child processes of manager.test.ts (Unix and Windows): ForkDriver forks testdata/manager_relay.mjs under
// Node, which runs this test binary as the child host (TestManagerWorkerProcess, Pi's test/fixtures/worker.ts).

const managerWorkerEnv = "CHORD_MANAGER_WORKER"

// managerDriver is manager.test.ts driver(env, lock): forkDriver({ entry, env: { HOST_NAME: "w1", ...env }, init:
// { greeting: "hi" }, lock }).
func managerDriver(t *testing.T, env map[string]string, lock *string) Driver {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
	entry, err := filepath.Abs(filepath.Join("testdata", "manager_relay.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	merged := map[string]string{"HOST_NAME": "w1", managerWorkerEnv: "1"}
	maps.Copy(merged, env)
	return ForkDriver(ForkDriverOptions{
		Entry:   entry,
		Args:    []string{executable, "-test.run=^TestManagerWorkerProcess$"},
		Env:     merged,
		Init:    chordjson.ObjectOf("greeting", "hi"),
		HasInit: true,
		Lock:    lock,
	})
}

// TestManagerWorkerProcess is the child host of the real-process cases (Pi test/fixtures/worker.ts); it only runs as
// the relay's worker. FIXTURE_MODE: normal | ignore-shutdown | fatal.
func TestManagerWorkerProcess(t *testing.T) {
	if os.Getenv(managerWorkerEnv) != "1" {
		t.Skip("child host process of the manager.test.ts real-process cases")
	}
	os.Exit(runManagerWorker())
}

func runManagerWorker() int {
	conn, err := workerPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	channel := newNodeChannel(conn)
	mode := os.Getenv("FIXTURE_MODE")
	if mode == "" {
		mode = "normal"
	}
	control := ChildControl(channel)
	if mode == "fatal" {
		control.Fatal(errors.New("fixture refuses to start"))
		time.Sleep(200 * time.Millisecond)
		return 1
	}
	// Before anything that owns data: the kernel lock (if the manager configured one).
	if err := control.Lock(); err != nil {
		time.Sleep(200 * time.Millisecond)
		return 1
	}
	name := os.Getenv("HOST_NAME")
	if name == "" {
		name = "worker"
	}
	host := CreateHost(HostOptions{Self: kindHost(), Name: name, Upstream: &UpstreamOptions{Link: NewIpcLink(channel), ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(error) {}})
	init, err := control.Init(context.Background())
	if err != nil {
		return 1
	}
	greeting, _ := init.(*chordjson.Object).Value("greeting").(string)
	// The forked child (the manager's instance) is the relay, this process's parent.
	pid := os.Getppid()
	if err := host.Serve(managerEcho, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf(
			"variant", "main",
			"echo", methodFunc(func(args any, _ Ctx) (any, error) {
				return fmt.Sprintf("%s %v from %d", greeting, args, pid), nil
			}),
			"pid", methodFunc(func(any, Ctx) (any, error) { return float64(pid), nil }),
		), nil
	}, ServeOptions{}); err != nil {
		return 1
	}
	if file := os.Getenv("GRANDCHILD_FILE"); file != "" {
		node, err := exec.LookPath("node")
		if err != nil {
			return 1
		}
		grandchild := exec.Command(node, "-e", "setInterval(() => {}, 1000)")
		if err := grandchild.Start(); err != nil {
			return 1
		}
		if err := os.WriteFile(file, []byte(strconv.Itoa(grandchild.Process.Pid)), 0o600); err != nil {
			return 1
		}
	}
	done := make(chan int, 1)
	control.OnShutdown(func() {
		if mode == "ignore-shutdown" {
			return
		}
		_ = host.Close()
		done <- 0
	})
	control.Ready()
	return <-done
}
