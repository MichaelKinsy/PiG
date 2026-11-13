//go:build windows

package daemon

// pi: packages/env/daemon/src/sys/windows.rs

// Ports the Windows functions of packages/env/daemon/src/sys/windows.rs that Pi has no test for (tmpdir, home, drive_cwds, identity, the libuv-style
// open of fs__open), with the values Node's os.tmpdir(), uv_os_homedir and libuv's fs__open give. They run on Windows only, where the files compile.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestTmpdirIsNodesOsTmpdir(t *testing.T) {
	tests := []struct {
		name                                  string
		temp, tmp, systemRoot, windir, expect string
	}{
		{name: "TEMP wins", temp: `C:\Users\x\Temp`, tmp: `D:\other`, systemRoot: `C:\Windows`, expect: `C:\Users\x\Temp`},
		{name: "a trailing backslash is dropped", temp: `C:\Users\x\Temp\`, expect: `C:\Users\x\Temp`},
		{name: "only one trailing backslash is dropped", temp: `C:\a\\`, expect: `C:\a\`},
		{name: "a drive root keeps its backslash", temp: `C:\`, expect: `C:\`},
		{name: "a lone backslash is kept", temp: `\`, expect: `\`},
		{name: "a forward slash is not a separator here", temp: `C:/Temp/`, expect: `C:/Temp/`},
		{name: "TMP when TEMP is empty", tmp: `D:\tmp\`, systemRoot: `C:\Windows`, expect: `D:\tmp`},
		{name: "SystemRoot when both are empty", systemRoot: `C:\Windows`, windir: `D:\W`, expect: `C:\Windows\temp`},
		{name: "windir when SystemRoot is empty", windir: `D:\W`, expect: `D:\W\temp`},
		{name: "nothing set", expect: `\temp`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TEMP", tt.temp)
			t.Setenv("TMP", tt.tmp)
			t.Setenv("SystemRoot", tt.systemRoot)
			t.Setenv("windir", tt.windir)
			if got := tmpdir(); got != tt.expect {
				t.Fatalf("tmpdir() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestHomeIsUSERPROFILE(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\someone`)
	t.Setenv("HOME", `D:\not-this`)
	if got := home(); got != `C:\Users\someone` {
		t.Fatalf("home() = %q", got)
	}
	t.Setenv("USERPROFILE", "")
	if got := home(); got != "" {
		t.Fatalf("home() = %q without USERPROFILE", got)
	}
}

func TestDriveCwdsCollectsTheDriveVariables(t *testing.T) {
	got := driveCwdsOf([]string{
		`=C:=C:\work`,
		`=d:=D:\x\y`,
		`=E:=`,
		`=F:=a=b`,
		`=1:=digit`,
		`=CC:=two letters`,
		`=C=short`,
		`C:=no leading equals`,
		`=C:`,
		`=`,
		``,
		`PATH=C:\bin`,
		`=::=punctuation`,
	})
	want := Object{"C:": `C:\work`, "D:": `D:\x\y`, "E:": "", "F:": "a=b"}
	if len(got) != len(want) {
		t.Fatalf("driveCwdsOf = %v, want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("driveCwdsOf = %v, want %v", got, want)
		}
	}
	if len(driveCwdsOf(nil)) != 0 {
		t.Fatal("no environment, some drives")
	}
}

func TestDriveCwdsReadsTheProcessEnvironment(t *testing.T) {
	want := map[string]string{}
	for _, entry := range os.Environ() {
		if len(entry) >= 4 && entry[0] == '=' && (entry[1]|0x20 >= 'a' && entry[1]|0x20 <= 'z') && entry[2] == ':' && entry[3] == '=' {
			want[string([]byte{entry[1] &^ 0x20, ':'})] = entry[4:]
		}
	}
	got := driveCwds()
	if len(got) != len(want) {
		t.Fatalf("driveCwds() = %v, want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("driveCwds() = %v, want %v", got, want)
		}
	}
}

// openForRead shares read, write and delete access like libuv, so a file open for reading can still be renamed and removed, and it opens a directory.
func TestOpenForReadSharesDeleteAndOpensDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openForRead(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	data := make([]byte, 16)
	if n, err := file.Read(data); err != nil || string(data[:n]) != "content" {
		t.Fatalf("read %q, %v", data[:n], err)
	}
	renamed := filepath.Join(dir, "renamed")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatalf("a file open through openForRead cannot be renamed: %v", err)
	}
	if err := os.Remove(renamed); err != nil {
		t.Fatalf("a file open through openForRead cannot be removed: %v", err)
	}

	directory, err := openForRead(dir)
	if err != nil {
		t.Fatalf("openForRead of a directory: %v", err)
	}
	_ = directory.Close()
	viaWrapper, err := openReadOnly(dir, true)
	if err != nil {
		t.Fatalf("openReadOnly of a directory: %v", err)
	}
	_ = viaWrapper.Close()
}

func TestOpenForReadReportsPathErrors(t *testing.T) {
	for name, path := range map[string]string{
		"missing":      filepath.Join(t.TempDir(), "missing"),
		"embedded NUL": "a\x00b",
	} {
		_, err := openForRead(path)
		var pathError *fs.PathError
		if !errors.As(err, &pathError) || pathError.Op != "open" || pathError.Path != path {
			t.Errorf("%s: err = %#v, want an open *fs.PathError for %q", name, err, path)
		}
	}
	if _, err := openForRead(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file: err = %v", err)
	}
}

func TestOpenDirectoryRefusesAFile(t *testing.T) {
	dir := t.TempDir()
	directory, err := openDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = directory.Close()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if file, err := openDirectory(path); err == nil || errorCode(err) != "ENOTDIR" {
		if file != nil {
			_ = file.Close()
		}
		t.Fatalf("openDirectory of a file: %v, code %q", err, errorCode(err))
	}
	if _, err := openDirectory(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("openDirectory of a missing path: %v", err)
	}
}

// identity is the creation time as Go reads it (nanoseconds since 1970), with no device number, and nothing for a file without system data.
func TestIdentityIsTheCreationTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		t.Fatalf("Sys() is %T", info.Sys())
	}
	dev, ino := identity(path, info, false)
	if dev != 0 || ino != uint64(attributes.CreationTime.Nanoseconds()) || ino == 0 {
		t.Fatalf("identity = %d, %d; creation time %d", dev, ino, attributes.CreationTime.Nanoseconds())
	}
	if dev, ino := identity(path, noSysInfo{info}, false); dev != 0 || ino != 0 {
		t.Fatalf("identity without system data = %d, %d", dev, ino)
	}
}

type noSysInfo struct{ os.FileInfo }

func (noSysInfo) Sys() any { return nil }

func TestWindowsSystemConstants(t *testing.T) {
	if pathSeparator != `\` || pathSeparators != `\/` || !windowsOS {
		t.Fatalf("separator %q separators %q windows %v", pathSeparator, pathSeparators, windowsOS)
	}
	if isLinkRefusal(syscall.EINVAL) {
		t.Fatal("Windows has no O_NOFOLLOW refusal")
	}
	if text, ok := rustIOError(errors.New("x")); ok || text != "" {
		t.Fatalf("rustIOError = %q, %v", text, ok)
	}
	if libuvName("ENOENT") != "ENOENT" || libuvName("anything") != "anything" {
		t.Fatal("libuvName changes a code")
	}
	if rawName("x") != nil {
		t.Fatal("rawName is not nil")
	}
	defaultChildSignals()
}
