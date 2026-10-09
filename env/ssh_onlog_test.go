package env

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

// packages/env/src/ssh.ts:462 `onLog?: ConnectionOptions["onLog"]` on SshConnectOptions: the platform warnings found while detecting
// the host are reported through it, one per call, each ending in a newline, before the daemon is deployed.
func TestSshConnectOptionsOnLogReceivesThePlatformWarnings(t *testing.T) {
	target, _ := fakeHost(t, fakeRule{Match: probeCommand, Stdout: posixAnswer("Linux", "aarch64", "Android", "/data/data/com.termux/files/home", "-", "-")})
	var mu sync.Mutex
	var logs []string
	connection, _ := sshConnectionPair(SshConnectOptions{SshTarget: target, OnLog: func(text string) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, text)
	}})
	t.Cleanup(connection.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, _ = connection.Info(ctx) // deployment against the scripted host fails; detection has already logged

	want := []string{
		"TMPDIR is not set; Termux's sshd normally sets it to $PREFIX/tmp.\n",
		"termux-exec is not loaded (LD_PRELOAD); scripts with #!/usr/bin/env shebangs will fail.\n",
		"Android may suspend Termux; run termux-wake-lock on the device to keep the connection alive.\n",
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) < len(want) || !slices.Equal(logs[:len(want)], want) {
		t.Fatalf("OnLog calls = %q, want the three warnings first", logs)
	}
}
