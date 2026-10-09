package env

import (
	"errors"
	"reflect"
	"testing"
)

// connection.ts:22-34 RemoteError: super(message or "Remote operation failed"), name "RemoteError", code (default "unknown"), path only when a string,
// and fields kept as given. The inherited stack and cause have no Go member (D103).
func TestRemoteErrorMembers(t *testing.T) {
	fields := Json{"code": "ENOENT", "path": "/tmp/x", "message": "no such file", "extra": 1.0}
	e := NewRemoteError(fields)
	if e.Code != "ENOENT" || e.Path != "/tmp/x" || e.Message != "no such file" || e.Error() != "no such file" || !reflect.DeepEqual(e.Fields, fields) {
		t.Fatalf("code=%q path=%q message=%q fields=%v", e.Code, e.Path, e.Message, e.Fields)
	}
	if e.Name() != "RemoteError" || errors.Unwrap(e) != nil {
		t.Fatalf("name=%q unwrap=%v", e.Name(), errors.Unwrap(e))
	}
	bare := NewRemoteError(Json{"path": 7.0})
	if bare.Code != "unknown" || bare.Path != "" || bare.Message != "Remote operation failed" {
		t.Fatalf("defaults: code=%q path=%q message=%q", bare.Code, bare.Path, bare.Message)
	}
}

// ssh.ts:40-55: HostKeyUnknownError and HostKeyChangedError are bare Error subclasses (name "Error"), and SshError adds exitCode and stderr; none
// sets a name of its own.
func TestSshErrorClassMembers(t *testing.T) {
	unknown := NewHostKeyUnknownError("not trusted")
	changed := NewHostKeyChangedError("changed")
	for name, e := range map[string]interface {
		Error() string
		Name() string
	}{"unknown": unknown, "changed": changed} {
		if e.Name() != "Error" {
			t.Errorf("%s name = %q, want Error", name, e.Name())
		}
	}
	if unknown.Error() != "not trusted" || changed.Error() != "changed" {
		t.Fatalf("messages: %q %q", unknown.Error(), changed.Error())
	}
	code := 255
	ssh := NewSshError("ssh failed", &code, "denied")
	if ssh.ExitCode == nil || *ssh.ExitCode != 255 || ssh.Stderr != "denied" || ssh.Name() != "Error" || ssh.Error() != "ssh failed" {
		t.Fatalf("ssh: %+v", ssh)
	}
}

// connection.ts:36-48: `hello` reports every RemoteInfo member; parseInfo keeps each one and an absent driveCwds is an empty record.
func TestParseInfoReadsEveryHelloMember(t *testing.T) {
	info := parseInfo(Json{
		"protocol": 1.0, "version": "0.4.2", "os": "windows", "arch": "x86_64", "home": `C:\Users\a`, "tmpdir": `C:\Temp`,
		"separator": `\`, "cwd": `C:\work`, "pid": 42.0, "driveCwds": Json{"C:": `C:\work`, "D:": 5.0},
	})
	want := RemoteInfo{Protocol: 1, Version: "0.4.2", OS: "windows", Arch: "x86_64", Home: `C:\Users\a`, Tmpdir: `C:\Temp`, Separator: `\`, Cwd: `C:\work`, Pid: 42, DriveCwds: map[string]string{"C:": `C:\work`}}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("parseInfo = %+v, want %+v", info, want)
	}
	if empty := parseInfo(Json{}); empty.DriveCwds == nil || len(empty.DriveCwds) != 0 {
		t.Fatalf("absent driveCwds = %#v", empty.DriveCwds)
	}
}
