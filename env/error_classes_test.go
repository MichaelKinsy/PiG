package env

import "testing"

// packages/env connection.ts:22-34 RemoteError sets name "RemoteError"; ssh.ts:40-55 HostKeyUnknownError, HostKeyChangedError and SshError set no
// name, so `name` is the inherited "Error".
func TestEnvErrorClassesCarryTheirNames(t *testing.T) {
	exit := 255
	cases := []struct {
		err           interface{ Error() string }
		name, message string
	}{
		{NewRemoteError(Json{"message": "denied", "code": "EACCES"}), "RemoteError", "denied"},
		{NewHostKeyUnknownError("unknown key"), "Error", "unknown key"},
		{NewHostKeyChangedError("changed key"), "Error", "changed key"},
		{NewSshError("ssh failed", &exit, "stderr"), "Error", "ssh failed"},
	}
	for _, c := range cases {
		named := c.err.(interface{ Name() string })
		if named.Name() != c.name || c.err.Error() != c.message {
			t.Errorf("%T: name=%q message=%q, want %q %q", c.err, named.Name(), c.err.Error(), c.name, c.message)
		}
	}
}
