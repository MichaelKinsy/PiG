package outputfiles

// pi: packages/coding-agent/src/utils/output-files.ts

import (
	"path/filepath"
	"runtime"
	"testing"
)

// output-files.ts:19 joins Node's os.tmpdir(). On POSIX that is the first non-empty TMPDIR, TMP or TEMP, else /tmp; on Windows the first
// non-empty TEMP or TMP, else %SystemRoot%\temp (Node 24 lib/os.js tmpdir and src/node_os.cc GetTempDir). Go's os.TempDir reads only
// TMPDIR on POSIX and prefers TMP on Windows.
func TestOutputFilesUseNodeTmpdir(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("TEMP", first)
		t.Setenv("TMP", second)
	} else {
		t.Setenv("TMPDIR", "")
		t.Setenv("TMP", first)
		t.Setenv("TEMP", second)
	}
	path, err := WriteFile("pi-test", ".txt", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != first {
		t.Fatalf("output file %s, want it in %s", path, first)
	}
	streamPath, f, err := CreateStream("pi-test", ".txt")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if filepath.Dir(streamPath) != first {
		t.Fatalf("stream file %s, want it in %s", streamPath, first)
	}
}

func TestNodeTmpdir(t *testing.T) {
	for _, tc := range []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{"posix TMPDIR", "linux", map[string]string{"TMPDIR": "/a", "TMP": "/b", "TEMP": "/c"}, "/a"},
		{"posix empty TMPDIR falls through", "linux", map[string]string{"TMPDIR": "", "TMP": "/b", "TEMP": "/c"}, "/b"},
		{"posix TEMP", "darwin", map[string]string{"TEMP": "/c/"}, "/c"},
		{"posix default", "linux", nil, "/tmp"},
		{"posix root keeps its slash", "linux", map[string]string{"TMPDIR": "/"}, "/"},
		{"posix strips one trailing slash", "linux", map[string]string{"TMPDIR": "/a//"}, "/a/"},
		{"windows TEMP before TMP", "windows", map[string]string{"TEMP": `C:\t`, "TMP": `C:\u`}, `C:\t`},
		{"windows TMP", "windows", map[string]string{"TMP": `C:\u\`}, `C:\u`},
		{"windows drive root keeps its backslash", "windows", map[string]string{"TEMP": `C:\`}, `C:\`},
		{"windows SystemRoot", "windows", map[string]string{"SystemRoot": `C:\Windows`, "windir": `D:\W`}, `C:\Windows\temp`},
		{"windows windir", "windows", map[string]string{"windir": `D:\W`}, `D:\W\temp`},
		{"windows without a root", "windows", nil, `undefined\temp`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeTmpdir(tc.goos, func(name string) string { return tc.env[name] }); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
