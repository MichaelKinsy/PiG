package subprocess

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func nodeCellProcessSnapshot(platform string, output func(string, ...string) ([]byte, error)) ([]byte, error) {
	if platform == "windows" {
		// CIM supplies the same PID/command-line rows as ps. No test path enters the PowerShell program, and UTF-8 preserves non-ASCII path markers.
		return output("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"$ErrorActionPreference = 'Stop'; [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false); Get-CimInstance Win32_Process -Filter \"Name = 'node.exe'\" | ForEach-Object { '{0} {1}' -f $_.ProcessId, $_.CommandLine }")
	}
	return output("ps", "-axo", "pid=,command=")
}

func TestNodeCellProcessSnapshotUsesNativeCommand(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows"} {
		t.Run(platform, func(t *testing.T) {
			want := "123 node /tmp/extension.mjs\n"
			got, err := nodeCellProcessSnapshot(platform, func(name string, args ...string) ([]byte, error) {
				if platform == "windows" {
					// A native Windows installation has no Unix ps. The process list must retain command lines, not just process names or host bookkeeping.
					if name != "powershell.exe" {
						return nil, exec.ErrNotFound
					}
					if len(args) != 4 || !slices.Equal(args[:3], []string{"-NoProfile", "-NonInteractive", "-Command"}) {
						t.Fatalf("PowerShell arguments = %q", args)
					}
					for _, required := range []string{"UTF8Encoding", "$ErrorActionPreference = 'Stop'", "Get-CimInstance Win32_Process", "Name = 'node.exe'", "ProcessId", "CommandLine"} {
						if !strings.Contains(args[3], required) {
							t.Errorf("process query missing %q: %s", required, args[3])
						}
					}
				} else if name != "ps" || !slices.Equal(args, []string{"-axo", "pid=,command="}) {
					t.Fatalf("Unix process command = %s %q", name, args)
				}
				return []byte(want), nil
			})
			if err != nil || string(got) != want {
				t.Fatalf("process snapshot = %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestNodeCellProcessSnapshotPropagatesFailure(t *testing.T) {
	want := errors.New("process enumeration denied")
	for _, platform := range []string{"linux", "darwin", "windows"} {
		_, err := nodeCellProcessSnapshot(platform, func(string, ...string) ([]byte, error) { return nil, want })
		if !errors.Is(err, want) {
			t.Errorf("%s enumeration error = %v, want %v", platform, err, want)
		}
	}
}
