package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeStubScript writes a POSIX shell stub at path and returns the path that
// starts it. Windows starts only files with an executable extension, so there
// the script is written beside a path.cmd wrapper that runs it with Git for
// Windows' sh.exe; PATH lookup also finds the wrapper by the bare name.
func writeStubScript(t *testing.T, path, script string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var sh string
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if candidate := filepath.Join(base, "Git", "bin", "sh.exe"); base != "" && fileExistsForTest(candidate) {
			sh = candidate
			break
		}
	}
	if sh == "" {
		t.Fatal("Git for Windows sh.exe not found; the stub needs a POSIX shell")
	}
	if err := os.WriteFile(path+".sh", []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	wrapper := path + ".cmd"
	if err := os.WriteFile(wrapper, []byte("@\""+sh+"\" \""+path+".sh\" %*\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return wrapper
}

func fileExistsForTest(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
