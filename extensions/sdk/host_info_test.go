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

func TestPackageDirAndShellConfigReadTheHostsEntry(t *testing.T) {
	t.Setenv(hostInfoEnv, `{"agentDir":"/a","configDirName":".pig","version":"1.1.0","packageDir":"/opt/pig","shell":{"shell":"/bin/bash","args":["-c"]}}`)
	if dir, err := PackageDir(); err != nil || dir != "/opt/pig" {
		t.Fatalf("PackageDir = %q, %v", dir, err)
	}
	config, err := GetShellConfig()
	if err != nil || config.Shell != "/bin/bash" || len(config.Args) != 1 || config.Args[0] != "-c" || config.CommandTransport != "" {
		t.Fatalf("GetShellConfig = %+v, %v", config, err)
	}
	t.Setenv(hostInfoEnv, `{"agentDir":"/a","shell":{"error":"No bash shell found."}}`)
	if _, err := GetShellConfig(); err == nil || err.Error() != "No bash shell found." {
		t.Fatalf("GetShellConfig error = %v", err)
	}
	if _, err := PackageDir(); !errors.Is(err, ErrHostInfoUnavailable) {
		t.Fatalf("PackageDir without one = %v", err)
	}
	t.Setenv(hostInfoEnv, "")
	if _, err := GetShellConfig(); !errors.Is(err, ErrHostInfoUnavailable) {
		t.Fatalf("GetShellConfig without a host = %v", err)
	}
}

// getShellConfig(customShellPath) checks the path itself, as Pi does: it must exist, and the legacy WSL bash.exe reads its command from standard input.
func TestGetShellConfigCustomPath(t *testing.T) {
	t.Setenv(hostInfoEnv, "")
	existing := t.TempDir()
	config, err := GetShellConfig(existing)
	if err != nil || config.Shell != existing || len(config.Args) != 1 || config.Args[0] != "-c" {
		t.Fatalf("existing path: %+v, %v", config, err)
	}
	if _, err := GetShellConfig(existing + "/missing"); err == nil || err.Error() != "Custom shell path not found: "+existing+"/missing" {
		t.Fatalf("missing path error = %v", err)
	}
	for shell, legacy := range map[string]bool{
		`C:\Windows\System32\bash.exe`: true, `c:/windows/sysnative/bash.exe`: true, `D:\WINDOWS\SYSTEM32\BASH.EXE`: true, "\u212a:\\windows\\system32\\bash.exe": true,
		`C:\Windows\System32\bash.exe `: false, `C:\Windows\bash.exe`: false, `C:\Program Files\Git\bin\bash.exe`: false, `1:\windows\system32\bash.exe`: false,
		"\u0130:\\windows\\system32\\bash.exe": false, `C:\Wİndows\System32\bash.exe`: false,
	} {
		config := bashShellConfig(shell)
		if got := config.CommandTransport == "stdin"; got != legacy || (legacy && config.Args[0] != "-s") || (!legacy && config.Args[0] != "-c") {
			t.Errorf("%q: %+v, legacy %t", shell, config, legacy)
		}
	}
}
