//go:build unix

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A quit that a native command in flight holds (stdin ended, a session_shutdown handler that never settles beside it) ends on the signals Pi's rpc-mode ends on
// (Pi 1.5.2, RUN beside a Node interval): a single SIGTERM during the shutdown does not interrupt it, a second one exits 143 after 415 ms, SIGINT, which rpc-mode
// leaves to the default action, ends the process by the signal at once (10 ms), and the hold itself lasts as long as the command does.
func TestRPCHeldQuitOfANativeCommandEndsOnTheSignalsPiEnds(t *testing.T) {
	goExt := buildGoSDKFixture(t, "rpc-settle-wait-go")
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-shutdown.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		first []syscall.Signal
		then  []syscall.Signal
		want  func(syscall.WaitStatus) bool
		text  string
	}{
		{"SIGTERM twice", []syscall.Signal{syscall.SIGTERM}, []syscall.Signal{syscall.SIGTERM}, func(s syscall.WaitStatus) bool { return s.Exited() && s.ExitStatus() == 143 }, "exit 143"},
		{"SIGINT", nil, []syscall.Signal{syscall.SIGINT}, func(s syscall.WaitStatus) bool { return s.Signaled() && s.Signal() == syscall.SIGINT }, "death by SIGINT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			peer := newRPCSettlePeer(t)
			home := t.TempDir()
			report := filepath.Join(t.TempDir(), "report.txt")
			env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic",
				"RPC_SHUTDOWN_REPORT=" + report, "RPC_SHUTDOWN_BLOCK_QUIT=1", "RPC_SETTLE_ADDR=" + peer.addr()}
			p := startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1", "-e", fixture, "-e", goExt)
			awaitReady(p)
			p.send(`{"id":"c","type":"prompt","message":"/hold"}`)
			held := peer.accept()
			p.closeInput()
			if len(tc.first) > 0 {
				signalRPC(t, p, tc.first...)
			}
			// The command's connection stays silent while pig holds the quit.
			if err := held.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if n, err := held.Read(make([]byte, 16)); !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("the connection of the command in flight ended or spoke within a second of the end of input (%d bytes, %v): pig did not hold the quit\n%s", n, err, p.stderr.String())
			}
			ended := time.Now()
			signalRPC(t, p, tc.then...)
			status := waitForRPCTermination(t, p)
			if !tc.want(status) {
				t.Fatalf("wait status = %v, want %s", status, tc.text)
			}
			if took := time.Since(ended); took > 1500*time.Millisecond {
				t.Fatalf("the process took %s to end after the signal, want at most 1.5 s (Pi: 10 to 415 ms)", took)
			}
		})
	}
}
