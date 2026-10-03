package toolchain

import (
	"errors"
	"strings"
	"testing"
)

func lookPathTable(found map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path, ok := found[name]; ok {
			return path, nil
		}
		return "", errors.New("not found")
	}
}

func TestPythonExecutablePrefersRealInterpreterOverWindowsStoreAlias(t *testing.T) {
	const alias = `C:\Users\dev\AppData\Local\Microsoft\WindowsApps\python.exe`
	const alias3 = `C:\Users\dev\AppData\Local\Microsoft\WindowsApps\python3.exe`
	tests := []struct {
		name  string
		found map[string]string
		want  string
	}{
		{"alias then real python3", map[string]string{"python": alias, "python3": `C:\Python312\python3.exe`}, `C:\Python312\python3.exe`},
		{"real python then alias python3", map[string]string{"python": `C:\Python312\python.exe`, "python3": strings.ToUpper(alias3)}, `C:\Python312\python.exe`},
		{"short-name forward-slash alias then real python3", map[string]string{"python": `C:/Users/DEVELO~1/AppData/Local/Microsoft/WindowsApps/python.exe`, "python3": `D:\tools\python3.exe`}, `D:\tools\python3.exe`},
		{"only alias is the last resort", map[string]string{"python": alias}, alias},
		{"first alias wins when both are aliases", map[string]string{"python": alias, "python3": alias3}, alias},
		{"only python3 alias is the last resort", map[string]string{"python3": alias3}, alias3},
		{"Program Files WindowsApps is a real interpreter", map[string]string{"python": `C:\Program Files\WindowsApps\PythonSoftwareFoundation.Python.3.12_qbz5n2kfra8p0\python.exe`, "python3": `C:\Python312\python3.exe`}, `C:\Program Files\WindowsApps\PythonSoftwareFoundation.Python.3.12_qbz5n2kfra8p0\python.exe`},
		{"nothing found keeps python", map[string]string{}, "python"},
	}
	for _, test := range tests {
		if got := PythonExecutable("windows", lookPathTable(test.found)); got != test.want {
			t.Errorf("%s: PythonExecutable = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestPythonExecutableNonWindows(t *testing.T) {
	const path = "/opt/Microsoft/WindowsApps/python3"
	if got := PythonExecutable("linux", lookPathTable(map[string]string{"python3": path})); got != path {
		t.Fatalf("PythonExecutable = %q, want %q", got, path)
	}
	if got := PythonExecutable("darwin", lookPathTable(map[string]string{"python": "/usr/bin/python"})); got != "python3" {
		t.Fatalf("PythonExecutable without python3 = %q, want bare python3", got)
	}
}
