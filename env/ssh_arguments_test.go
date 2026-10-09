package env

// Ports packages/env/src/ssh.ts sshArguments, checked against the cases testdata/generate_ssh_arguments.mjs records
// from Pi's own function.

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"
)

// mutation-checked: dropping the reads and writes of SshTarget.ConfigFile, SshTarget.IdentityFile, SshTarget.Port, SshTarget.User fails it
func TestSshArgumentsMatchPi(t *testing.T) {
	checkSshArgumentCases(t, func(target SshTarget, strict bool, knownHosts string) ([]string, error) {
		return SshArguments(target, SshArgumentsOptions{StrictHostKeys: &strict, KnownHostsFile: &knownHosts})
	})
}

// Pi ssh.ts:77-81: sshArguments(target, strictHostKeys, knownHostsFile) takes its three parameters positionally; SshArgumentsWith is that call.
func TestSshArgumentsWithMatchesPiPositionally(t *testing.T) {
	checkSshArgumentCases(t, SshArgumentsWith)
}

// checkSshArgumentCases runs every recorded Pi case through call, one Go spelling of sshArguments(target, strictHostKeys, knownHostsFile).
func checkSshArgumentCases(t *testing.T, call func(target SshTarget, strict bool, knownHosts string) ([]string, error)) {
	t.Helper()
	var file struct {
		Cases []struct {
			Target struct {
				Host           string   `json:"host"`
				User           *string  `json:"user"`
				Port           *float64 `json:"port"`
				IdentityFile   *string  `json:"identityFile"`
				KnownHostsFile string   `json:"knownHostsFile"`
				HostKeyAlias   string   `json:"hostKeyAlias"`
				ConfigFile     *string  `json:"configFile"`
			} `json:"target"`
			StrictHostKeys bool     `json:"strictHostKeys"`
			KnownHostsFile *string  `json:"knownHostsFile"`
			Args           []string `json:"args"`
			Error          string   `json:"error"`
		} `json:"cases"`
	}
	data, err := os.ReadFile("testdata/ssh_arguments_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) < 40 {
		t.Fatalf("case table is too small: %d", len(file.Cases))
	}
	checked, refused, fractional := 0, 0, 0
	for _, c := range file.Cases {
		var port *int
		if c.Target.Port != nil {
			// A Go int cannot hold Pi's fractional port (`Invalid port: 22.5`), so only those cases are not stated.
			if *c.Target.Port != float64(int(*c.Target.Port)) {
				fractional++
				continue
			}
			port = new(int(*c.Target.Port))
		}
		target := SshTarget{
			Host: c.Target.Host, User: c.Target.User, Port: port, IdentityFile: c.Target.IdentityFile,
			KnownHostsFile: c.Target.KnownHostsFile, HostKeyAlias: c.Target.HostKeyAlias, ConfigFile: c.Target.ConfigFile,
		}
		knownHosts := target.KnownHostsFile
		if c.KnownHostsFile != nil {
			knownHosts = *c.KnownHostsFile
		}
		got, err := call(target, c.StrictHostKeys, knownHosts)
		if c.Error != "" {
			refused++
			if err == nil || err.Error() != c.Error {
				t.Errorf("target %+v: error %v, Pi gives %q", c.Target, err, c.Error)
			}
			continue
		}
		checked++
		if err != nil || !slices.Equal(got, c.Args) {
			t.Errorf("target %+v strict %v: %q, %v\nPi gives %q", c.Target, c.StrictHostKeys, got, err, c.Args)
		}
	}
	if checked < 10 || refused < 15 || fractional != 2 {
		t.Fatalf("checked %d accepted and %d refused targets, skipped %d fractional ports", checked, refused, fractional)
	}
}

// Pi ssh.ts:77-81: sshArguments(target) defaults strictHostKeys to true and knownHostsFile to target.knownHostsFile.
func TestSshArgumentsDefaultsToStrictHostKeysAndTheTargetKnownHostsFile(t *testing.T) {
	target := SshTarget{Host: "example.test", HostKeyAlias: "pi-env-example", KnownHostsFile: "/tmp/known_hosts"}
	got, err := SshArguments(target)
	if err != nil {
		t.Fatal(err)
	}
	want, err := SshArguments(target, SshArgumentsOptions{StrictHostKeys: new(true), KnownHostsFile: &target.KnownHostsFile})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("SshArguments = %v, want the strict default %v (err %v)", got, want, err)
	}
	relaxed, err := SshArguments(target, SshArgumentsOptions{StrictHostKeys: new(false), KnownHostsFile: &target.KnownHostsFile})
	if err != nil || reflect.DeepEqual(got, relaxed) {
		t.Fatalf("strict and relaxed host-key checking must differ: %v (err %v)", relaxed, err)
	}
	if _, err := SshArguments(SshTarget{HostKeyAlias: "pi-env-example"}); err == nil {
		t.Fatal("a target without a host must be refused")
	}
}

// Pi ssh.ts HostKeyChangedError extends Error: its message is the one it was constructed with.
// mutation-checked: dropping the reads and writes of HostKeyChangedError.Message fails it
// Pi: packages/env/src/ssh.ts:50 (message)
// Pi ssh.ts HostKeyChangedError extends Error (packages/env/src/ssh.ts:43): its message is the one it was constructed with (ssh.ts:163).
func TestHostKeyChangedErrorCarriesItsMessage(t *testing.T) {
	var err error = &HostKeyChangedError{Message: "Host key for pi-env-x changed"}
	var changed *HostKeyChangedError
	if !errors.As(err, &changed) || changed.Message != "Host key for pi-env-x changed" || err.Error() != changed.Message {
		t.Fatalf("err=%v changed=%+v", err, changed)
	}
}

// ssh.ts:40: HostKeyUnknownError is built from its message alone; it is not a HostKeyChangedError.
func TestNewHostKeyUnknownErrorKeepsItsMessage(t *testing.T) {
	err := NewHostKeyUnknownError("The host key of example.test is not trusted yet")
	var unknown *HostKeyUnknownError
	var changed *HostKeyChangedError
	if err.Message != "The host key of example.test is not trusted yet" || err.Error() != err.Message || !errors.As(error(err), &unknown) || errors.As(error(err), &changed) {
		t.Fatalf("NewHostKeyUnknownError = %+v", err)
	}
}

// Pi ssh.ts:134 spawns `target.ssh ?? "ssh"`: only an unset program defaults to ssh, and a set one, even empty, is used as given.
func TestSshProgramDefaultsOnlyWhenUnset(t *testing.T) {
	if got := sshProgram(SshTarget{}); got != "ssh" {
		t.Fatalf("unset program = %q, want ssh", got)
	}
	for _, program := range []string{"", "/opt/ssh/bin/ssh"} {
		if got := sshProgram(SshTarget{Ssh: &program}); got != program {
			t.Fatalf("set program %q = %q", program, got)
		}
	}
}
