package sdk

import (
	"errors"
	"testing"
)

func TestHostInfoReadsTheHostsEnvironmentEntry(t *testing.T) {
	t.Setenv(hostInfoEnv, `{"agentDir":"/home/u/.pig/agent","configDirName":".pig","version":"1.1.0"}`)
	if dir, err := AgentDir(); err != nil || dir != "/home/u/.pig/agent" {
		t.Fatalf("AgentDir = %q, %v", dir, err)
	}
	if name, err := ConfigDirName(); err != nil || name != ".pig" {
		t.Fatalf("ConfigDirName = %q, %v", name, err)
	}
	if version, err := Version(); err != nil || version != "1.1.0" {
		t.Fatalf("Version = %q, %v", version, err)
	}
}

func TestHostInfoIsUnavailableWithoutAHostThatProvidesIt(t *testing.T) {
	for name, value := range map[string]string{"unset": "", "not JSON": "{", "no agent directory": `{"configDirName":".pig"}`} {
		t.Setenv(hostInfoEnv, value)
		if _, err := AgentDir(); !errors.Is(err, ErrHostInfoUnavailable) {
			t.Errorf("%s: AgentDir error = %v, want ErrHostInfoUnavailable", name, err)
		}
	}
}
