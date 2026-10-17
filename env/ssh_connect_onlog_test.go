package env

import (
	"context"
	"slices"
	"testing"
)

// packages/env/src/ssh.ts:528 connectSsh reports each warning of the detected system through options.onLog, one line per
// warning, once the daemon is verified, before any connection starts.
func TestConnectSshReportsPlatformWarningsThroughOnLog(t *testing.T) {
	target, _ := fakeHost(t,
		fakeRule{Match: probeCommand, Stdout: posixAnswer("Linux", "aarch64", "Android", "/data/data/com.termux/files/home", "-", "-")},
		fakeRule{Match: "echo present", Stdout: "present\n"},
	)
	var logs []string
	connection, remote, err := ConnectSsh(context.Background(), SshConnectOptions{SshTarget: target, Binary: new(daemonBinary(t)), OnLog: func(text string) { logs = append(logs, text) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	if len(remote.Warnings) != 3 {
		t.Fatalf("precondition: warnings %q", remote.Warnings)
	}
	var want []string
	for _, warning := range remote.Warnings {
		want = append(want, warning+"\n")
	}
	if !slices.Equal(logs, want) {
		t.Fatalf("onLog calls %q, want %q", logs, want)
	}
}
