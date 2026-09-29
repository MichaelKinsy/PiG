//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func TestInteractiveSignalsRetainHandlersThroughDisposalAndDrain(t *testing.T) {
	binary := os.Getenv("PIG_SIGNAL_REFERENCE_BIN")
	if binary == "" {
		binary = buildPigBinaryForSignalTest(t)
	} else {
		out, err := exec.CommandContext(t.Context(), binary, "--version").CombinedOutput()
		if err != nil || string(out) != "0.87.1\n" {
			t.Fatalf("reference pin=%q err=%v", out, err)
		}
	}
	for _, tc := range []struct {
		name          string
		first, second syscall.Signal
		dead          bool
	}{{"TERM_TERM", syscall.SIGTERM, syscall.SIGTERM, false}, {"TERM_HUP", syscall.SIGTERM, syscall.SIGHUP, false}, {"HUP_TERM", syscall.SIGHUP, syscall.SIGTERM, false}, {"DEAD_HUP", syscall.SIGHUP, syscall.SIGHUP, true}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			agent, project := filepath.Join(root, "agent"), filepath.Join(root, "project")
			if err := os.MkdirAll(agent, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(project, 0o755); err != nil {
				t.Fatal(err)
			}
			ready, started, release, complete, boot := filepath.Join(root, "ready"), filepath.Join(root, "dispose"), filepath.Join(root, "release"), filepath.Join(root, "complete"), filepath.Join(root, "boot")
			paths, _ := json.Marshal(map[string]string{"ready": ready, "started": started, "release": release, "complete": complete, "boot": boot})
			script := `import fs from 'node:fs';const paths=` + string(paths) + `;export default pi=>{pi.registerProvider('signal-faux',{api:'openai-completions',baseUrl:'https://faux.invalid/v1',apiKey:'faux-key',models:[{id:'signal-test-model',name:'signal-test-model',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:8192,maxTokens:128}]});pi.on('session_start',()=>{fs.writeFileSync(paths.boot,'boot')});pi.registerCommand('ready',{handler:async()=>{fs.writeFileSync(paths.ready,'ready')}});pi.on('session_shutdown',async()=>{fs.appendFileSync(paths.started,'dispose\n');await new Promise(resolve=>{const timer=setInterval(()=>{if(fs.existsSync(paths.release)){clearInterval(timer);resolve()}},10)});fs.appendFileSync(paths.complete,'complete\n')});};`
			extension := filepath.Join(root, "signal.mjs")
			if err := os.WriteFile(extension, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			master, slave := openPTY(t, 40, 160)
			initial, err := unix.IoctlGetTermios(int(master.Fd()), unix.TCGETS)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "--no-session", "--no-extensions", "-e", extension, "--approve", "--model", "signal-faux/signal-test-model")
			cmd.Dir = project
			cmd.Env = append(os.Environ(), "PIG_HOME="+root, "PIG_CODING_AGENT_DIR="+agent, "PI_CODING_AGENT_DIR="+agent, "PIG_OFFLINE=1", "PI_OFFLINE=1", "TERM=xterm-256color")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
			output := &ptyOutput{}
			readDone := make(chan struct{})
			captureCtx, cancelCapture := context.WithCancel(ctx)
			captureFD := int(master.Fd())
			go func() {
				defer close(readDone)
				buffer := make([]byte, 4096)
				for captureCtx.Err() == nil {
					ready, err := unix.Poll([]unix.PollFd{{Fd: int32(captureFD), Events: unix.POLLIN}}, 20)
					if errors.Is(err, syscall.EINTR) {
						continue
					}
					if err != nil {
						return
					}
					if ready == 0 {
						continue
					}
					n, err := unix.Read(captureFD, buffer)
					if n > 0 {
						_, _ = output.Write(buffer[:n])
					}
					if err != nil || n == 0 {
						return
					}
				}
			}()
			if err := cmd.Start(); err != nil {
				cancelCapture()
				<-readDone
				_ = slave.Close()
				_ = master.Close()
				t.Fatal(err)
			}
			_ = slave.Close()
			done := make(chan error, 1)
			go func() { defer close(done); done <- cmd.Wait() }()
			defer func() { cancel(); <-done; cancelCapture(); <-readDone; _ = master.Close() }()
			output.waitQuiet(0, []byte("signal-test-model"), 50*time.Millisecond, testbudget.Wait(t))
			if !bytes.Contains(output.since(0), []byte("signal-test-model")) {
				t.Fatalf("interactive model was not ready: %s", output.since(0))
			}
			waitFile := func(path string) {
				t.Helper()
				tick := time.NewTicker(10 * time.Millisecond)
				defer tick.Stop()
				for {
					if _, err := os.Stat(path); err == nil {
						return
					}
					select {
					case err := <-done:
						t.Fatalf("process exited before %s: %v\n%s", filepath.Base(path), err, output.since(0))
					case <-ctx.Done():
						t.Fatalf("missing %s\n%s", filepath.Base(path), output.since(0))
					case <-tick.C:
					}
				}
			}
			waitFile(boot)
			if _, err := master.Write([]byte("/ready\r")); err != nil {
				t.Fatal(err)
			}
			waitFile(ready)
			if tc.dead {
				if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
					t.Fatal(err)
				}
				cancelCapture()
				<-readDone
				if err := master.Close(); err != nil {
					t.Fatal(err)
				}
				var result error
				select {
				case result = <-done:
				case <-ctx.Done():
					t.Fatalf("dead terminal did not terminate: %v", ctx.Err())
				}
				var exit *exec.ExitError
				if !errors.As(result, &exit) || exit.ExitCode() != 129 {
					t.Fatalf("dead terminal exit=%v, want129\n%s", result, output.since(0))
				}
				fmt.Println("INTERACTIVE_SIGNAL DEAD_HUP exit=129")
				return
			}
			if err := cmd.Process.Signal(tc.first); err != nil {
				t.Fatal(err)
			}
			waitFile(started)
			if err := cmd.Process.Signal(tc.second); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				t.Fatalf("signal interrupted pending disposal: %v\n%s", err, output.since(0))
			case <-time.After(1500 * time.Millisecond):
			}
			// Keep late key releases arriving while cleanup finishes, so a re-sent HUP lands during the terminal drain rather than after process exit.
			stopKeys, keysDone, keyStarted := make(chan struct{}), make(chan struct{}), make(chan struct{})
			keyErrors := make(chan error, 1)
			var once sync.Once
			go func() {
				defer close(keysDone)
				tick := time.NewTicker(5 * time.Millisecond)
				defer tick.Stop()
				first := true
				for {
					select {
					case <-stopKeys:
						return
					case <-ctx.Done():
						return
					case <-tick.C:
						if _, err := master.Write([]byte("\x1b[97;1:3u")); err != nil {
							keyErrors <- err
							return
						}
						if first {
							close(keyStarted)
							first = false
						}
					}
				}
			}()
			stopWriting := func() { once.Do(func() { close(stopKeys) }); <-keysDone }
			defer stopWriting()
			select {
			case <-keyStarted:
			case err := <-keyErrors:
				t.Fatal(err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
				t.Fatal(err)
			}
			waitFile(complete)
			time.Sleep(100 * time.Millisecond)
			if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
				t.Fatalf("process left before drain signal: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
			stopWriting()
			select {
			case err := <-keyErrors:
				t.Fatal(err)
			default:
			}
			var exitErr error
			select {
			case exitErr = <-done:
			case <-ctx.Done():
				t.Fatalf("shutdown did not drain: %v\n%s", ctx.Err(), output.since(0))
			}
			if exitErr != nil {
				t.Fatalf("signal shutdown=%v\n%s", exitErr, output.since(0))
			}
			contents, err := os.ReadFile(started)
			if err != nil || string(contents) != "dispose\n" {
				t.Fatalf("disposals=%q err=%v", contents, err)
			}
			contents, err = os.ReadFile(complete)
			if err != nil || string(contents) != "complete\n" {
				t.Fatalf("completions=%q err=%v", contents, err)
			}
			restored, err := unix.IoctlGetTermios(int(master.Fd()), unix.TCGETS)
			if err != nil {
				t.Fatal(err)
			}
			if restored.Lflag&unix.ISIG != initial.Lflag&unix.ISIG {
				t.Fatal("signal shutdown did not restore cooked input")
			}
			fmt.Printf("INTERACTIVE_SIGNAL %s disposal=1 completion=1 exit=0 handlers=retained\n", tc.name)
		})
	}
}
