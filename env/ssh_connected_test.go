package env

import "testing"

// upstream: packages/env/src/ssh.ts:510-517 sshConnection returns `{ connection, remote: () => state.remote }`: remote() is undefined until a start detected the system and then the detected system.
func TestSshConnectedRemoteReadsTheDetectedSystem(t *testing.T) {
	state := &sshState{}
	connected := sshConnected(SshConnectOptions{SshTarget: SshTarget{Host: "h", HostKeyAlias: "a", KnownHostsFile: "/tmp/known"}}, state)
	if connected.Connection == nil {
		t.Fatal("SshConnection returned no connection")
	}
	t.Cleanup(connected.Connection.Close)
	if connected.Remote() != nil {
		t.Fatal("remote() before detection must be nil")
	}
	detected := &RemotePlatform{Platform: "linux", Arch: "x64", Home: "/home/pi"}
	state.mu.Lock()
	state.remote = detected
	state.mu.Unlock()
	if got := connected.Remote(); got != detected {
		t.Fatalf("remote() = %v, want the detected system %v", got, detected)
	}
}
