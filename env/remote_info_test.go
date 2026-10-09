package env

import (
	"reflect"
	"testing"
)

// Pi packages/env/src/connection.ts RemoteInfo: the daemon's hello fields land in the same-named RemoteInfo members; absent or mistyped fields stay zero.
// mutation-checked: dropping the reads and writes of RemoteInfo.Arch, RemoteInfo.Pid, RemoteInfo.Protocol, RemoteInfo.Separator, RemoteInfo.Tmpdir, RemoteInfo.Version fails it
// Pi packages/env/src/connection.ts:37 RemoteInfo: the daemon's hello fields land in the same-named RemoteInfo members; absent or mistyped fields stay zero.
func TestParseInfoReadsEveryHelloField(t *testing.T) {
	info := parseInfo(Json{
		"protocol": float64(1), "version": "0.9.2", "os": "windows", "arch": "x64", "home": `C:\Users\u`, "tmpdir": `C:\Temp`,
		"separator": `\`, "cwd": `C:\work`, "pid": float64(4242), "driveCwds": Json{"C:": `C:\work`, "D:": float64(3)},
	})
	want := RemoteInfo{Protocol: 1, Version: "0.9.2", OS: "windows", Arch: "x64", Home: `C:\Users\u`, Tmpdir: `C:\Temp`, Separator: `\`, Cwd: `C:\work`, Pid: 4242, DriveCwds: map[string]string{"C:": `C:\work`}}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("got %+v, want %+v", info, want)
	}
	if empty := parseInfo(Json{"arch": 1, "tmpdir": nil}); empty.Arch != "" || empty.Tmpdir != "" || empty.Version != "" || empty.Separator != "" || len(empty.DriveCwds) != 0 {
		t.Fatalf("mistyped fields leaked: %+v", empty)
	}
}
